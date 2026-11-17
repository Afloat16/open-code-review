// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

process.env.OCR_SKIP_SHELL_RESOLVE = '1';
import { spawn } from 'child_process';
import { EventEmitter } from 'events';
import { PassThrough } from 'stream';
import { ReviewMode } from '@shared/types';
import { CliService } from '../CliService';

jest.mock('child_process', () => ({
  ...jest.requireActual('child_process'),
  spawn: jest.fn(),
}));

function processStreams() {
  return Object.assign(new EventEmitter(), {
    stdout: new PassThrough(),
    stderr: new PassThrough(),
  });
}

function writeBytes(stream: PassThrough, text: string) {
  for (const byte of Buffer.from(text, 'utf8')) stream.write(Buffer.from([byte]));
}

async function close(proc: ReturnType<typeof processStreams>, code = 0) {
  proc.stdout.end();
  proc.stderr.end();
  await new Promise<void>((resolve) => setImmediate(resolve));
  proc.emit('close', code);
}

describe('CliService stream framing', () => {
  let proc: ReturnType<typeof processStreams>;

  beforeEach(() => {
    jest.clearAllMocks();
    proc = processStreams();
    jest.mocked(spawn).mockReturnValue(proc as unknown as ReturnType<typeof spawn>);
  });

  it('preserves review content and paths split inside UTF-8 characters', async () => {
    const result = new CliService('ocr').review({ mode: ReviewMode.Workspace }, '.', () => {});
    const finding = { path: '\u8def\u5f84.ts', content: '\u4fee\u590d \u{1f680}', start_line: 1, end_line: 1 };
    writeBytes(proc.stdout, JSON.stringify({ status: 'success', comments: [finding] }));
    await close(proc);

    expect((await result).comments).toEqual([expect.objectContaining({
      path: finding.path, content: finding.content,
    })]);
  });

  it('emits complete stderr lines and flushes an unterminated final line', async () => {
    const onLog = jest.fn();
    const result = new CliService('ocr').runRaw([], '.', onLog);
    writeBytes(proc.stderr, 'warning: \u91cd\u8bd5 \u{1f680}\r\n');
    writeBytes(proc.stderr, 'finished\nlast message');
    await close(proc);
    await result;

    expect(onLog.mock.calls.map(([line]) => line)).toEqual([
      { text: 'warning: \u91cd\u8bd5 \u{1f680}', level: 'warn' },
      { text: 'finished', level: 'info' },
      { text: 'last message', level: 'info' },
    ]);
  });

  it('preserves a fragmented non-ASCII CLI error', async () => {
    const result = new CliService('ocr').runRaw([], '.', () => {});
    const rejected = expect(result).rejects.toThrow('\u8fde\u63a5\u5931\u8d25 \u{1f680}');
    writeBytes(proc.stderr, 'Error: \u8fde\u63a5\u5931\u8d25 \u{1f680}\n');
    await close(proc, 1);
    await rejected;
  });

  it('keeps interleaved install streams and their final fragments separate', async () => {
    const onLog = jest.fn();
    const result = new CliService('ocr').install(onLog);
    writeBytes(proc.stdout, 'download \u5305');
    writeBytes(proc.stderr, 'warning: ');
    writeBytes(proc.stdout, ' complete\nstdout tail');
    writeBytes(proc.stderr, '\u4f9d\u8d56\u{1f680}\nstderr tail');
    await close(proc);

    expect(await result).toBe(true);
    const lines = onLog.mock.calls.map(([line]) => line.text);
    expect(lines).toEqual(expect.arrayContaining([
      'download \u5305 complete', 'warning: \u4f9d\u8d56\u{1f680}', 'stdout tail', 'stderr tail',
    ]));
    expect(lines).toHaveLength(6);
  });
});
