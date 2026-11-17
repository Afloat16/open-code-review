// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

import { execFile } from 'child_process';
import { mkdtemp, rm, writeFile } from 'fs/promises';
import { tmpdir } from 'os';
import path from 'path';
import { promisify } from 'util';
import * as vscode from 'vscode';
import { ReviewMode } from '@shared/types';
import { GitService } from '../GitService';

const execFileAsync = promisify(execFile);

describe('GitService workspace files', () => {
  let repoRoot: string;
  const originalWorkspaceFolders = vscode.workspace.workspaceFolders;

  beforeEach(async () => {
    repoRoot = await mkdtemp(path.join(tmpdir(), 'ocr-vscode-git-'));
    await execFileAsync('git', ['init', '-q'], { cwd: repoRoot });
    (vscode.workspace as any).workspaceFolders = [{ uri: { fsPath: repoRoot } }];
  });

  afterEach(async () => {
    (vscode.workspace as any).workspaceFolders = originalWorkspaceFolders;
    await rm(repoRoot, { recursive: true, force: true });
  });

  it('includes staged and untracked files before the first commit', async () => {
    await writeFile(path.join(repoRoot, 'staged.ts'), 'export const staged = true;\n');
    await execFileAsync('git', ['add', 'staged.ts'], { cwd: repoRoot });
    await writeFile(path.join(repoRoot, 'untracked.ts'), 'export const untracked = true;\n');

    const state = await new GitService().getState(ReviewMode.Workspace);

    expect(state.workspaceFiles).toEqual([
      { path: 'staged.ts', status: 'added' },
      { path: 'untracked.ts', status: 'added' },
    ]);
  });

  it.each([false, true])('omits staged changes canceled by the working tree (untracked=%s)', async (untracked) => {
    const filename = path.join(repoRoot, 'tracked.ts');
    await writeFile(filename, 'export const value = 1;\n');
    await execFileAsync('git', ['add', 'tracked.ts'], { cwd: repoRoot });
    await execFileAsync('git', ['-c', 'user.name=Test User', '-c', 'user.email=test@example.com', 'commit', '-q', '-m', 'base'], { cwd: repoRoot });
    await writeFile(filename, 'export const value = 2;\n');
    await execFileAsync('git', ['add', 'tracked.ts'], { cwd: repoRoot });
    await writeFile(filename, 'export const value = 1;\n');
    if (untracked) {
      await writeFile(path.join(repoRoot, 'new.ts'), 'export const added = true;\n');
    }

    const state = await new GitService().getState(ReviewMode.Workspace);

    expect(state.workspaceFiles).toEqual(untracked ? [{ path: 'new.ts', status: 'added' }] : []);
  });

  it('uses the HEAD revision when a file is also named HEAD', async () => {
    await writeFile(path.join(repoRoot, 'HEAD'), 'before\n');
    await execFileAsync('git', ['add', 'HEAD'], { cwd: repoRoot });
    await execFileAsync('git', ['-c', 'user.name=Test User', '-c', 'user.email=test@example.com', 'commit', '-q', '-m', 'base'], { cwd: repoRoot });
    await writeFile(path.join(repoRoot, 'HEAD'), 'after\n');

    const state = await new GitService().getState(ReviewMode.Workspace);

    expect(state.workspaceFiles).toEqual([{ path: 'HEAD', status: 'modified' }]);
  });

  it.each(['sha1', 'sha256'])('omits removed staged additions before the first %s commit', async (format) => {
    await rm(path.join(repoRoot, '.git'), { recursive: true, force: true });
    await execFileAsync('git', ['init', '-q', '--object-format=' + format], { cwd: repoRoot });
    await writeFile(path.join(repoRoot, 'removed.ts'), 'export const removed = true;\n');
    await execFileAsync('git', ['add', 'removed.ts'], { cwd: repoRoot });
    await rm(path.join(repoRoot, 'removed.ts'));
    await writeFile(path.join(repoRoot, 'new.ts'), 'export const added = true;\n');

    const state = await new GitService().getState(ReviewMode.Workspace);

    expect(state.workspaceFiles).toEqual([{ path: 'new.ts', status: 'added' }]);
  });

  it('lists merge commit files relative to the first parent', async () => {
    await execFileAsync('git', ['config', 'user.email', 'test@example.com'], { cwd: repoRoot });
    await execFileAsync('git', ['config', 'user.name', 'Test User'], { cwd: repoRoot });

    await writeFile(path.join(repoRoot, 'base.ts'), 'export const base = true;\n');
    await execFileAsync('git', ['add', 'base.ts'], { cwd: repoRoot });
    await execFileAsync('git', ['commit', '-q', '-m', 'base'], { cwd: repoRoot });
    await execFileAsync('git', ['branch', '-M', 'main'], { cwd: repoRoot });

    await execFileAsync('git', ['checkout', '-q', '-b', 'feature'], { cwd: repoRoot });
    await writeFile(path.join(repoRoot, 'feature.ts'), 'export const feature = true;\n');
    await execFileAsync('git', ['add', 'feature.ts'], { cwd: repoRoot });
    await execFileAsync('git', ['commit', '-q', '-m', 'feature'], { cwd: repoRoot });

    await execFileAsync('git', ['checkout', '-q', 'main'], { cwd: repoRoot });
    await writeFile(path.join(repoRoot, 'main.ts'), 'export const main = true;\n');
    await execFileAsync('git', ['add', 'main.ts'], { cwd: repoRoot });
    await execFileAsync('git', ['commit', '-q', '-m', 'main'], { cwd: repoRoot });
    await execFileAsync('git', ['merge', '--no-ff', '-q', 'feature', '-m', 'merge'], { cwd: repoRoot });

    const mergeSha = (await execFileAsync('git', ['rev-parse', 'HEAD'], { cwd: repoRoot })).stdout.trim();
    const files = await new GitService().getCommitFiles(mergeSha);

    expect(files).toEqual([{ path: 'feature.ts', status: 'added' }]);
  });
});
