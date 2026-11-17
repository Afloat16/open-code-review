// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

process.env.OCR_SKIP_SHELL_RESOLVE = '1';
import { spawn } from 'child_process';
import { EventEmitter } from 'events';
import { PassThrough } from 'stream';
import { CliService } from '../CliService';

jest.mock('child_process', () => ({
  ...jest.requireActual('child_process'),
  spawn: jest.fn(),
}));

const originalPlatform = Object.getOwnPropertyDescriptor(process, 'platform');

function processStreams() {
  return Object.assign(new EventEmitter(), {
    pid: 4001, exitCode: null as number | null, signalCode: null,
    stdout: new PassThrough(), stderr: new PassThrough(), kill: jest.fn(() => true),
  });
}

describe('bounded CLI availability probes', () => {
  let proc: ReturnType<typeof processStreams>;
  let cli: CliService;
  const probe = (service: CliService) =>
    (service as unknown as { probeCommand(bin: string): Promise<{ ok: boolean; version?: string }> }).probeCommand('ocr');

  beforeEach(() => {
    jest.useFakeTimers();
    Object.defineProperty(process, 'platform', { value: 'linux' });
    jest.spyOn(process, 'kill').mockImplementation(() => true);
    proc = processStreams();
    jest.mocked(spawn).mockReturnValue(proc as unknown as ReturnType<typeof spawn>);
    cli = new CliService('ocr');
  });

  afterEach(() => {
    proc.exitCode = 0;
    proc.stdout.end();
    proc.stderr.end();
    proc.emit('close', 0);
    jest.useRealTimers();
    jest.restoreAllMocks();
    jest.mocked(spawn).mockReset();
    if (originalPlatform) Object.defineProperty(process, 'platform', originalPlatform);
  });

  it('reports a hung probe as unavailable and terminates its POSIX process group', async () => {
    const results: unknown[] = [];
    void probe(cli).then((result) => results.push(result));
    jest.advanceTimersByTime(9999);
    await Promise.resolve();
    expect(results).toEqual([]);
    jest.advanceTimersByTime(1);
    await Promise.resolve();

    expect(results).toEqual([{ ok: false }]);
    expect(process.kill).toHaveBeenCalledWith(-4001, 'SIGKILL');
    expect(spawn).toHaveBeenCalledWith('ocr', ['--version'], expect.objectContaining({ detached: true }));
  });

  it('stops an oversized version response before the deadline', async () => {
    const results: unknown[] = [];
    void probe(cli).then((result) => results.push(result));
    proc.stdout.write(Buffer.alloc(64 * 1024 + 1, 'x'));
    await Promise.resolve();

    expect(results).toEqual([{ ok: false }]);
    expect(process.kill).toHaveBeenCalledWith(-4001, 'SIGKILL');
    jest.runAllTicks();
    expect(jest.getTimerCount()).toBe(0);
  });

  it('returns a successful first version line and clears the timer', async () => {
    const result = probe(cli);
    proc.stdout.write('ocr 1.0\nadditional output\n');
    proc.exitCode = 0;
    proc.emit('close', 0);

    expect(await result).toEqual({ ok: true, version: 'ocr 1.0' });
    jest.runAllTicks();
    expect(jest.getTimerCount()).toBe(0);
    jest.advanceTimersByTime(10000);
    expect(process.kill).not.toHaveBeenCalled();
  });

  it('decodes a bounded version response after all UTF-8 bytes arrive', async () => {
    const result = probe(cli);
    for (const byte of Buffer.from('ocr \u{1f680} 1.0\n')) proc.stdout.write(Buffer.from([byte]));
    proc.exitCode = 0;
    proc.emit('close', 0);

    expect(await result).toEqual({ ok: true, version: 'ocr \u{1f680} 1.0' });
  });

  it('settles spawn errors without leaving a deadline timer', async () => {
    const result = probe(cli);
    proc.emit('error', new Error('spawn failed'));

    expect(await result).toEqual({ ok: false });
    jest.runAllTicks();
    expect(jest.getTimerCount()).toBe(0);
    expect(process.kill).not.toHaveBeenCalled();
  });

  it('terminates a timed-out Windows probe tree with a launcher fallback', async () => {
    Object.defineProperty(process, 'platform', { value: 'win32' });
    const treeKill = processStreams();
    jest.mocked(spawn)
      .mockReturnValueOnce(proc as unknown as ReturnType<typeof spawn>)
      .mockReturnValueOnce(treeKill as unknown as ReturnType<typeof spawn>);
    const results: unknown[] = [];
    void probe(cli).then((result) => results.push(result));
    jest.advanceTimersByTime(10000);
    await Promise.resolve();

    expect(results).toEqual([{ ok: false }]);
    expect(spawn).toHaveBeenNthCalledWith(2, 'taskkill', ['/pid', '4001', '/t', '/f'], {
      stdio: 'ignore', windowsHide: true,
    });
    treeKill.emit('error', new Error('taskkill unavailable'));
    expect(proc.kill).toHaveBeenCalledWith('SIGKILL');
  });
});
