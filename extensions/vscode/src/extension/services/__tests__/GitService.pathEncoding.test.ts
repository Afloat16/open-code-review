// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

import { execFile } from 'child_process';
import { mkdtemp, readFile, rm, writeFile } from 'fs/promises';
import { tmpdir } from 'os';
import path from 'path';
import { promisify } from 'util';
import * as vscode from 'vscode';
import { ReviewMode } from '@shared/types';
import { GitService } from '../GitService';
import { unquoteGitPath } from '../gitMap';

const execFileAsync = promisify(execFile);
const quotedNames = ['caf\u00e9 "draft".ts', '\u4e2d\tvalue.ts', '\u{1f642}\\value.ts'];

describe('Git paths containing raw Unicode and escapes', () => {
  it.each(quotedNames)('keeps UTF-8 characters in a quoted path: %s', (name) => {
    // These quote, tab and backslash escapes are shared by JSON and Git.
    expect(unquoteGitPath(JSON.stringify(name))).toBe(name);
  });

  it('combines octal UTF-8 bytes with raw multibyte characters', () => {
    expect(unquoteGitPath('"\\303\\251-\u4e2d-\\360\\237\\231\\202\\t.ts"'))
      .toBe('\u00e9-\u4e2d-\u{1f642}\t.ts');
  });

  it('preserves the existing ASCII escape decoding', () => {
    expect(unquoteGitPath(String.raw`"a\t\n\r\\\".ts"`)).toBe('a\t\n\r\\".ts');
  });

  it('leaves unquoted Unicode paths unchanged', () => {
    expect(unquoteGitPath('src/\u4e2d-\u{1f642}.ts')).toBe('src/\u4e2d-\u{1f642}.ts');
  });
});

// Quotes, tabs and literal backslashes cannot all be filenames on Windows.
const describePosix = process.platform === 'win32' ? describe.skip : describe;
describePosix('GitService with actual Git-quoted Unicode filenames', () => {
  let repoRoot: string;
  const originalWorkspaceFolders = vscode.workspace.workspaceFolders;
  const git = (args: string[]) => execFileAsync('git', args, { cwd: repoRoot });

  beforeEach(async () => {
    repoRoot = await mkdtemp(path.join(tmpdir(), 'ocr-vscode-quoted-paths-'));
    await git(['init', '-q']);
    await git(['config', 'user.name', 'Test User']);
    await git(['config', 'user.email', 'test@example.com']);
    await writeFile(path.join(repoRoot, 'base.ts'), 'export const base = true;\n');
    await git(['add', '--', 'base.ts']);
    await git(['commit', '-q', '-m', 'base']);
    (vscode.workspace as any).workspaceFolders = [{ uri: { fsPath: repoRoot } }];
  });

  afterEach(async () => {
    (vscode.workspace as any).workspaceFolders = originalWorkspaceFolders;
    await rm(repoRoot, { recursive: true, force: true });
  });

  it.each(['untracked', 'modified', 'commit', 'branch'] as const)(
    'returns readable original paths from the %s file list', async (mode) => {
      for (const name of quotedNames) await writeFile(path.join(repoRoot, name), `// ${name}\n`);
      if (mode !== 'untracked') {
        await git(['add', '--', ...quotedNames]);
        await git(['commit', '-q', '-m', 'quoted paths']);
      }
      if (mode === 'modified') {
        for (const name of quotedNames) await writeFile(path.join(repoRoot, name), `// ${name}\n// changed\n`);
      }

      const service = new GitService();
      const files = mode === 'commit' ? await service.getCommitFiles('HEAD')
        : mode === 'branch' ? await service.getBranchDiff('HEAD^', 'HEAD')
          : (await service.getState(ReviewMode.Workspace)).workspaceFiles;

      expect(files.map(file => file.path).sort()).toEqual([...quotedNames].sort());
      expect(files.every(file => file.status === (mode === 'modified' ? 'modified' : 'added'))).toBe(true);
      for (const file of files) {
        expect(await readFile(path.join(repoRoot, file.path), 'utf8')).toContain(`// ${file.path}\n`);
      }
    },
  );
});
