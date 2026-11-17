// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

import { execFile } from 'child_process';
import { ReviewMode } from '@shared/types';
import { GitService } from '../GitService';

jest.mock('child_process', () => ({ execFile: jest.fn() }));

type Command = { args: string[]; finish: (error: Error | null, stdout: string) => void };
const tick = () => new Promise<void>((resolve) => setImmediate(resolve));

describe('overlapping workspace refreshes', () => {
  let commands: Command[];
  let service: GitService;

  beforeEach(() => {
    commands = [];
    service = new GitService();
    (execFile as unknown as jest.Mock).mockImplementation((_file, args, _options, finish) => {
      commands.push({ args, finish });
      return {};
    });
  });

  afterEach(() => jest.resetAllMocks());

  async function start() {
    const result = service.getState(ReviewMode.Workspace);
    const root = commands.shift()!;
    expect(root.args).toContain('rev-parse');
    root.finish(null, '/mock\n');
    await tick();
    const diff = commands.shift()!;
    const untracked = commands.shift()!;
    expect(diff.args).toContain('diff');
    expect(untracked.args).toContain('ls-files');
    return { result, diff, untracked };
  }

  async function finish(refresh: Awaited<ReturnType<typeof start>>, filename: string, error?: Error) {
    refresh.diff.finish(null, `M\t${filename}\n`);
    refresh.untracked.finish(error ?? null, '');
    return refresh.result;
  }

  it('keeps the newer file list when an older Git command finishes later', async () => {
    const older = await start();
    const newer = await start();
    expect((await finish(newer, 'latest.ts')).workspaceFiles).toEqual([{ path: 'latest.ts', status: 'modified' }]);

    expect((await finish(older, 'stale.ts')).workspaceFiles).toEqual([{ path: 'latest.ts', status: 'modified' }]);
  });

  it('does not erase the newer file list when an older command fails', async () => {
    const older = await start();
    const newer = await start();
    await finish(newer, 'latest.ts');

    expect((await finish(older, 'stale.ts', new Error('old scan failed'))).workspaceFiles)
      .toEqual([{ path: 'latest.ts', status: 'modified' }]);
  });

  it('ignores an older repository lookup failure after the current refresh succeeds', async () => {
    const older = service.getState(ReviewMode.Workspace);
    const oldRoot = commands.shift()!;
    const newer = await start();
    await finish(newer, 'latest.ts');

    oldRoot.finish(new Error('old workspace removed'), '');

    expect((await older).workspaceFiles).toEqual([{ path: 'latest.ts', status: 'modified' }]);
  });

  it('still clears the list when the current refresh fails', async () => {
    await finish(await start(), 'previous.ts');

    const current = await start();

    expect((await finish(current, 'latest.ts', new Error('current scan failed'))).workspaceFiles).toEqual([]);
  });
});
