// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

import { execFile } from 'child_process';
import { mkdtemp, rm, writeFile } from 'fs/promises';
import { tmpdir } from 'os';
import path from 'path';
import { promisify } from 'util';
import * as vscode from 'vscode';
import { ReviewContext, ReviewMode } from '@shared/types';
import { GitService } from '../GitService';
import { resolveCommentAnchor } from '../../providers/commentAnchor';

jest.mock('vscode', () => {
  const base = jest.requireActual('../../../../__mocks__/vscode.js');
  return {
    ...base,
    extensions: { getExtension: jest.fn() },
    commands: { ...base.commands, executeCommand: jest.fn(async () => undefined) },
    workspace: { ...base.workspace, openTextDocument: jest.fn(async () => ({})) },
    window: { ...base.window, showTextDocument: jest.fn(async () => undefined) },
  };
});

const execFileAsync = promisify(execFile);
const contexts: ReviewContext[] = [
  { mode: ReviewMode.Workspace },
  { mode: ReviewMode.Commit, commit: 'HEAD' },
  { mode: ReviewMode.Branch, from: 'HEAD^', to: 'HEAD' },
];

describe('GitService renamed file diffs', () => {
  let repoRoot: string;
  const originalFolders = vscode.workspace.workspaceFolders;
  const git = (args: string[]) => execFileAsync('git', args, { cwd: repoRoot });

  beforeEach(async () => {
    jest.clearAllMocks();
    repoRoot = await mkdtemp(path.join(tmpdir(), 'ocr-vscode-rename-'));
    await git(['init', '-q']);
    await git(['config', 'user.name', 'Test User']);
    await git(['config', 'user.email', 'test@example.com']);
    const original = 'const one = 1;\nconst two = 2;\nconst value = "old";\nconst four = 4;\nconst five = 5;\n';
    await writeFile(path.join(repoRoot, 'old name.ts'), original);
    await git(['add', '--all']);
    await git(['commit', '-q', '-m', 'base']);
    await git(['mv', 'old name.ts', 'new name.ts']);
    await writeFile(path.join(repoRoot, 'new name.ts'), original.replace('"old"', '"new"'));
    await git(['add', '--all']);
    (vscode.workspace as any).workspaceFolders = [{ uri: { fsPath: repoRoot } }];
    (vscode.extensions.getExtension as jest.Mock).mockReturnValue({
      isActive: true,
      exports: { getAPI: () => ({
        repositories: [{ rootUri: { fsPath: repoRoot } }],
        toGitUri: (uri: vscode.Uri, ref: string) => ({ ...uri, scheme: 'git', ref }),
      }) },
    });
  });

  afterEach(async () => {
    (vscode.workspace as any).workspaceFolders = originalFolders;
    await rm(repoRoot, { recursive: true, force: true });
  });

  it.each(contexts)('opens the old and new paths in $mode mode', async (ctx) => {
    if (ctx.mode !== ReviewMode.Workspace) await git(['commit', '-q', '-m', 'rename']);

    await new GitService().openDiff({ path: 'new name.ts', status: 'renamed', ...ctx });

    const [command, left, right] = (vscode.commands.executeCommand as jest.Mock).mock.calls[0];
    expect(command).toBe('vscode.diff');
    expect(path.normalize(left.fsPath)).toBe(path.join(repoRoot, 'old name.ts'));
    expect(path.normalize(right.fsPath)).toBe(path.join(repoRoot, 'new name.ts'));
    expect(left.scheme).toBe('git');
  });

  it.each(contexts.slice(1))('uses the old path for comment diff previews in $mode mode', async (ctx) => {
    await git(['commit', '-q', '-m', 'rename']);

    const diff = await new GitService().buildCommentDiffUris('new name.ts', 'renamed', ctx);

    expect(path.normalize(diff!.left.fsPath)).toBe(path.join(repoRoot, 'old name.ts'));
    expect(path.normalize(diff!.right.fsPath)).toBe(path.join(repoRoot, 'new name.ts'));
  });

  it('locates an old-side comment at the source path of a rename', async () => {
    await git(['commit', '-q', '-m', 'rename']);
    const service = new GitService();
    const ctx = { mode: ReviewMode.Commit, commit: 'HEAD' };

    const anchor = await resolveCommentAnchor({
      path: 'new name.ts', content: 'Check the old value.', startLine: 99, endLine: 99,
      existingCode: 'const value = "old";',
    }, ctx, {
      repoRoot,
      fileStatus: async () => 'renamed',
      readAtRef: (ref, filename) => service.readFileAtRef(ref, filename),
      readWorkspace: (filename) => service.readWorkspaceFile(filename),
      buildDiffUris: (filename, status) => service.buildCommentDiffUris(filename, status, ctx),
      toGitUri: async (filename, ref) => (await service.createGitFileUri(filename, ref))!,
    });

    expect(anchor.kind).toBe('mountable');
    if (anchor.kind === 'mountable') {
      expect(anchor.side).toBe('left');
      expect(path.normalize(anchor.uri.fsPath)).toBe(path.join(repoRoot, 'old name.ts'));
      expect(anchor.range.start.line).toBe(2);
    }
  });

  it('keeps both rename paths when the native diff falls back to patch text', async () => {
    await git(['commit', '-q', '-m', 'rename']);
    (vscode.commands.executeCommand as jest.Mock).mockRejectedValueOnce(new Error('editor unavailable'));

    await new GitService().openDiff({
      path: 'new name.ts', status: 'renamed', mode: ReviewMode.Commit, commit: 'HEAD',
    });

    const [{ content }] = (vscode.workspace.openTextDocument as jest.Mock).mock.calls[0];
    expect(content).toContain('rename from old name.ts');
    expect(content).toContain('rename to new name.ts');
    expect(content).toContain('-const value = "old";');
    expect(content).toContain('+const value = "new";');
  });
});
