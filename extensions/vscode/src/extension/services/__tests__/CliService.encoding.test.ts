// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

process.env.OCR_SKIP_SHELL_RESOLVE = '1';
import { ChildProcess } from 'child_process';
import { mkdtempSync, rmSync, writeFileSync } from 'fs';
import { tmpdir } from 'os';
import { join } from 'path';
import { ReviewMode } from '@shared/types';
import { CliService } from '../CliService';

const childProcess = jest.requireActual<typeof import('child_process')>('child_process');
const realSpawn = childProcess.spawn;
const boundaries = [
  ['\u00e9', 1],
  ['\u4e2d', 1], ['\u4e2d', 2],
  ['\u{1f642}', 1], ['\u{1f642}', 2], ['\u{1f642}', 3],
] as const;

async function withFragmentedOutput<T>(
  bytes: Buffer,
  split: number,
  stream: 'stdout' | 'stderr',
  exitCode: number,
  run: (service: CliService, cwd: string) => Promise<T>,
): Promise<T> {
  const cwd = mkdtempSync(join(tmpdir(), 'ocr-encoding-'));
  let child: ChildProcess | undefined;
  writeFileSync(join(cwd, 'review'), [
    `const output = process.${stream};`,
    `process.stdin.once('data', () => {`,
    `  output.end(Buffer.from('${bytes.subarray(split).toString('base64')}', 'base64'));`,
    `  process.exitCode = ${exitCode};`,
    `});`,
    `output.write(Buffer.from('${bytes.subarray(0, split).toString('base64')}', 'base64'));`,
  ].join('\n'));
  // A pipe acknowledgement guarantees that the two byte fragments arrive separately.
  const spawnSpy = jest.spyOn(childProcess, 'spawn').mockImplementation((...args: Parameters<typeof realSpawn>) => {
    child = realSpawn(...args);
    child[stream]!.once('data', () => child!.stdin!.end('continue'));
    return child;
  });
  try {
    return await run(new CliService(process.execPath), cwd);
  } finally {
    spawnSpy.mockRestore();
    if (child && child.exitCode === null && child.signalCode === null) child.kill();
    rmSync(cwd, { recursive: true, force: true });
  }
}

describe('CLI output decoding across pipe chunks', () => {
  const fields = ['path', 'content', 'existing_code', 'suggestion_code', 'thinking', 'message'] as const;

  const reviewCases = boundaries.map(([character, split], index) => ({ character, split, field: fields[index] }));

  it.each(reviewCases)('preserves $field split at UTF-8 byte $split', async ({ character, split, field }) => {
    const comment = {
      path: 'main.ts', content: 'Finding', existing_code: 'const x = 1;',
      suggestion_code: 'const x = 2;', thinking: 'Reason', start_line: 1, end_line: 1,
    };
    const result = { status: 'success', message: 'Completed', comments: [comment] };
    const value = `before ${character} after`;
    if (field === 'message') result.message = value;
    else comment[field] = value;
    const bytes = Buffer.from(JSON.stringify(result));
    const offset = bytes.indexOf(Buffer.from(character)) + split;

    const actual = await withFragmentedOutput(bytes, offset, 'stdout', 0, (service, cwd) =>
      service.review({ mode: ReviewMode.Workspace }, cwd, () => {}));

    expect(actual).toMatchObject({
      status: 'success', message: result.message,
      comments: [{
        path: comment.path, content: comment.content, existingCode: comment.existing_code,
        suggestionCode: comment.suggestion_code, thinking: comment.thinking,
      }],
    });
  });

  it.each(boundaries)('preserves an error containing %s split at byte %i', async (character, split) => {
    const message = `cannot read ${character}.ts`;
    const bytes = Buffer.from(`Error: ${message}\n`);
    const offset = bytes.indexOf(Buffer.from(character)) + split;
    await expect(withFragmentedOutput(bytes, offset, 'stderr', 1, (service, cwd) =>
      service.runRaw(['review'], cwd, () => {}))).rejects.toThrow(message);
  });

  it('flushes a trailing incomplete sequence when stdout ends', async () => {
    const bytes = Buffer.concat([Buffer.from('prefix '), Buffer.from([0xe4, 0xb8])]);
    const actual = await withFragmentedOutput(bytes, 7, 'stdout', 0, (service, cwd) =>
      service.runRaw(['review'], cwd, () => {}));
    expect(actual).toBe(bytes.toString('utf8'));
  });
});
