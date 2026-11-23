// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

import { execFile } from 'child_process';
import { mkdtemp, readFile, rm, writeFile } from 'fs/promises';
import { tmpdir } from 'os';
import path from 'path';
import { promisify } from 'util';
import * as vscode from 'vscode';
import { ReviewContext, ReviewMode } from '@shared/types';
import { GitService } from '../GitService';
import { parseNameStatus } from '../gitMap';
import { resolveCommentAnchor } from '../../providers/commentAnchor';
import { SidebarProvider } from '../../providers/SidebarProvider';

const execFileAsync = promisify(execFile);
const oldPath = 'before.ts';
const newPath = 'after.ts';
const oldContent = Array.from({ length: 40 }, (_, index) => `export const value${index} = ${index};`).join('\n') + '\n';
const newContent = oldContent.replace('export const value3 = 3;', 'export const value3 = 300;');

describe('rename source paths', () => {
  it('retains the decoded old path from a rename record', () => {
    expect(parseNameStatus('R098\t"old\\tname.ts"\tnew.ts\n')).toEqual([
      { path: 'new.ts', oldPath: 'old\tname.ts', status: 'renamed' },
    ]);
  });

  it('does not attach an old path to ordinary status records', () => {
    expect(parseNameStatus('M\tsame.ts\nA\tnew.ts\nD\tgone.ts\n')).toEqual([
      { path: 'same.ts', status: 'modified' },
      { path: 'new.ts', status: 'added' },
      { path: 'gone.ts', status: 'deleted' },
    ]);
  });
});

describe('GitService rename diff workflows', () => {
  let repoRoot: string;
  let base: string;
  let service: GitService;
  let execute: jest.SpyInstance;
  const originalFolders = vscode.workspace.workspaceFolders;
  const originalShowDocument = vscode.window.showTextDocument;
  const git = (args: string[]) => execFileAsync('git', args, { cwd: repoRoot });
  const context = (mode: ReviewMode): ReviewContext => ({ mode, from: base, to: 'HEAD', commit: 'HEAD' });

  beforeEach(async () => {
    repoRoot = await mkdtemp(path.join(tmpdir(), 'ocr-vscode-rename-'));
    await git(['init', '-q']);
    await git(['config', 'user.name', 'Test User']);
    await git(['config', 'user.email', 'test@example.com']);
    await writeFile(path.join(repoRoot, oldPath), oldContent);
    await git(['add', '--', oldPath]);
    await git(['commit', '-q', '-m', 'original']);
    base = (await git(['rev-parse', 'HEAD'])).stdout.trim();
    (vscode.workspace as any).workspaceFolders = [{ uri: { fsPath: repoRoot } }];
    const api = {
      repositories: [{ rootUri: { fsPath: repoRoot } }],
      toGitUri: (uri: vscode.Uri, ref: string) => ({ ...uri, scheme: 'git', ref }),
    };
    jest.spyOn(vscode.extensions, 'getExtension').mockReturnValue({
      isActive: true, exports: { getAPI: () => api },
    } as any);
    execute = jest.spyOn(vscode.commands, 'executeCommand').mockResolvedValue(undefined);
    service = new GitService();
  });

  afterEach(async () => {
    jest.restoreAllMocks();
    (vscode.workspace as any).workspaceFolders = originalFolders;
    (vscode.window as any).showTextDocument = originalShowDocument;
    await rm(repoRoot, { recursive: true, force: true });
  });

  async function rename(commit: boolean): Promise<void> {
    await git(['mv', '--', oldPath, newPath]);
    await writeFile(path.join(repoRoot, newPath), newContent);
    if (commit) {
      await git(['add', '--', newPath]);
      await git(['commit', '-q', '-m', 'rename and edit']);
    }
  }

  async function readSide(uri: vscode.Uri & { ref?: string }): Promise<string> {
    if (uri.scheme === 'git') return (await git(['show', `${uri.ref}:${path.relative(repoRoot, uri.fsPath)}`])).stdout;
    return readFile(uri.fsPath, 'utf8');
  }

  async function filesFor(mode: ReviewMode) {
    if (mode === ReviewMode.Workspace) return (await service.getState(mode)).workspaceFiles;
    if (mode === ReviewMode.Commit) return service.getCommitFiles('HEAD');
    return service.getBranchDiff(base, 'HEAD');
  }

  it.each([ReviewMode.Workspace, ReviewMode.Commit, ReviewMode.Branch])(
    'opens the real old and new contents for a %s rename', async (mode) => {
      await rename(mode !== ReviewMode.Workspace);
      const files = await filesFor(mode);
      expect(files).toHaveLength(1);
      expect(files[0].status).toBe('renamed');
      const sidebar = new SidebarProvider({} as vscode.Uri, {} as any, {} as any, service, { onSync: jest.fn() } as any);
      await (sidebar as any).handle({ type: 'openFileDiff', ...files[0], ...context(mode) });
      const call = execute.mock.calls.find(args => args[0] === 'vscode.diff');
      expect(call).toBeDefined();
      expect(await readSide(call![1])).toBe(oldContent);
      expect(await readSide(call![2])).toBe(newContent);
      expect(files[0]).toHaveProperty('oldPath', oldPath);
    },
  );

  it.each([ReviewMode.Commit, ReviewMode.Branch])(
    'includes both paths in the %s patch fallback', async (mode) => {
      await rename(true);
      const files = await filesFor(mode);
      execute.mockRejectedValue(new Error('native diff unavailable'));
      const open = jest.spyOn(vscode.workspace, 'openTextDocument').mockResolvedValue({} as vscode.TextDocument);
      const show = jest.fn().mockResolvedValue({});
      (vscode.window as any).showTextDocument = show;
      await service.openDiff({ ...files[0], ...context(mode) });
      const options = open.mock.calls[0][0] as { content: string };
      expect(options.content).toContain('rename from before.ts');
      expect(options.content).toContain('rename to after.ts');
      expect(options.content).toContain('-export const value3 = 3;');
      expect(options.content).toContain('+export const value3 = 300;');
      expect(show).toHaveBeenCalledTimes(1);
    },
  );

  it.each([ReviewMode.Commit, ReviewMode.Branch])(
    'locates old-side source in a %s renamed file', async (mode) => {
      await rename(true);
      const ctx = context(mode);
      await service.prepareReviewFileStatus(ctx);
      const anchor = await resolveCommentAnchor({
        path: newPath, content: 'Check the original value', startLine: 0, endLine: 0,
        existingCode: 'export const value3 = 3;',
      }, ctx, {
        repoRoot,
        fileStatus: file => service.getReviewFileStatus(file),
        readAtRef: (ref, file) => service.readFileAtRef(ref, file),
        readWorkspace: file => service.readWorkspaceFile(file),
        buildDiffUris: (file, status) => service.buildCommentDiffUris(file, status, ctx),
        toGitUri: async (file, ref) => (await service.createGitFileUri(file, ref))!,
      });
      expect(anchor.kind).toBe('mountable');
      if (anchor.kind !== 'mountable') throw new Error('missing old-side anchor');
      expect(anchor.side).toBe('left');
      expect(anchor.range.start.line).toBe(3);
      expect(await readSide(anchor.uri)).toBe(oldContent);
      expect(await readSide(anchor.diff!.left)).toBe(oldContent);
      expect(await readSide(anchor.diff!.right)).toBe(newContent);
    },
  );

  it('drops rename metadata before a later review of the same path', async () => {
    await rename(true);
    await service.prepareReviewFileStatus(context(ReviewMode.Commit));
    await writeFile(path.join(repoRoot, newPath), newContent + '// later\n');
    await git(['add', '--', newPath]);
    await git(['commit', '-q', '-m', 'later edit']);
    await service.prepareReviewFileStatus(context(ReviewMode.Commit));
    const diff = await service.buildCommentDiffUris(newPath, 'modified', context(ReviewMode.Commit));
    expect(diff).not.toBeNull();
    expect(await readSide(diff!.left)).toBe(newContent);
    expect(await readSide(diff!.right)).toBe(newContent + '// later\n');
  });
});
