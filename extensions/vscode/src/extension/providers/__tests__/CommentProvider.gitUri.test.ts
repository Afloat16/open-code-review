// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

import * as vscode from 'vscode';
import { ReviewMode } from '@shared/types';
import { GitService } from '../../services/GitService';
import { CommentProvider } from '../CommentProvider';

jest.mock('vscode', () => {
  class Range {
    start: { line: number; character: number };
    end: { line: number; character: number };
    constructor(start: number, startCharacter: number, end: number, endCharacter: number) {
      this.start = { line: start, character: startCharacter };
      this.end = { line: end, character: endCharacter };
    }
  }
  return {
    env: { language: 'en' },
    workspace: {
      workspaceFolders: [{ uri: { fsPath: '/repo' } }],
      openTextDocument: jest.fn().mockResolvedValue({}),
    },
    window: {
      visibleTextEditors: [],
      tabGroups: { activeTabGroup: { activeTab: undefined } },
      showTextDocument: jest.fn().mockResolvedValue({}),
      showWarningMessage: jest.fn(),
    },
    comments: {
      createCommentController: jest.fn(() => ({
        createCommentThread: jest.fn((_uri, _range, comments) => ({ comments, dispose: jest.fn() })),
        dispose: jest.fn(),
      })),
    },
    commands: { executeCommand: jest.fn().mockResolvedValue(undefined) },
    Range,
    Selection: class {
      constructor(public start: unknown, public end: unknown) {}
    },
    TabInputTextDiff: class {},
    MarkdownString: class { constructor(public value: string) {} },
    CommentMode: { Preview: 1 },
    CommentThreadCollapsibleState: { Expanded: 1, Collapsed: 0 },
    TextEditorRevealType: { InCenter: 0 },
  };
});

function gitUri(ref: string, overrides: { query?: string; authority?: string } = {}): vscode.Uri {
  const query = overrides.query ?? JSON.stringify({ path: '/repo/app.ts', ref });
  const authority = overrides.authority ?? '';
  return {
    scheme: 'git', authority, path: '/repo/app.ts', fsPath: '/repo/app.ts', query,
    toString: () => `git://${authority}/repo/app.ts?${encodeURIComponent(query)}`,
  } as vscode.Uri;
}

function editor(uri: vscode.Uri): vscode.TextEditor {
  return { document: { uri }, revealRange: jest.fn() } as unknown as vscode.TextEditor;
}

describe('comment navigation distinguishes Git snapshots', () => {
  let provider: CommentProvider | undefined;

  afterEach(() => {
    provider?.dispose();
    provider = undefined;
    (vscode.window as any).visibleTextEditors = [];
    jest.clearAllMocks();
  });

  async function showAt(target: vscode.Uri, visible: vscode.TextEditor[], deleted = false): Promise<void> {
    (vscode.window as any).visibleTextEditors = visible;
    const ref = JSON.parse(target.query).ref;
    const git = {
      prepareReviewFileStatus: jest.fn().mockResolvedValue(undefined),
      getReviewFileStatus: jest.fn().mockResolvedValue(deleted ? 'deleted' : 'modified'),
      readFileAtRef: jest.fn().mockResolvedValue('first\nreviewed line\nlast\n'),
      buildCommentDiffUris: jest.fn().mockResolvedValue({
        left: gitUri('base'), right: gitUri('head'), title: 'app.ts',
        mountRef: ref, mountSide: deleted ? 'left' : 'right', leftRef: 'base', rightRef: 'head',
      }),
      createGitFileUri: jest.fn().mockResolvedValue(target),
    } as unknown as GitService;
    provider = new CommentProvider({} as vscode.Uri, git);
    await provider.show([{
      path: 'app.ts', content: 'Inspect this line.', startLine: 2, endLine: 2,
    }], { mode: ReviewMode.Commit, commit: 'head' });
  }

  test.each([
    ['right snapshot', 'head', 'base', false],
    ['deleted left snapshot', 'base', 'head', true],
    ['index snapshot with an empty ref', '', 'HEAD', false],
    ['merge stage', ':3', ':2', false],
  ])('reveals the %s when a different revision is visible first', async (_name, ref, otherRef, deleted) => {
    const target = gitUri(ref as string);
    const other = editor(gitUri(otherRef as string));
    const selected = editor(target);

    await showAt(target, [other, selected], deleted as boolean);

    expect(other.revealRange).not.toHaveBeenCalled();
    expect(selected.revealRange).toHaveBeenCalledWith(
      expect.objectContaining({ start: { line: 1, character: 0 }, end: { line: 1, character: 0 } }),
      vscode.TextEditorRevealType.InCenter,
    );
    await provider!.jumpTo(0);
    expect(other.revealRange).not.toHaveBeenCalled();
    expect(selected.revealRange).toHaveBeenCalledTimes(2);
  });

  test.each([
    ['malformed JSON', '{broken'],
    ['missing ref', JSON.stringify({ path: '/repo/app.ts' })],
    ['different source path', JSON.stringify({ path: '/other/app.ts', ref: 'head' })],
    ['different submodule', JSON.stringify({ path: '/repo/app.ts', ref: 'head', submoduleOf: '/parent' })],
  ])('does not treat %s as the mounted snapshot', async (_name, query) => {
    const target = gitUri('head');
    const other = editor(gitUri('head', { query }));
    const selected = editor(target);

    await showAt(target, [other, selected]);

    expect(other.revealRange).not.toHaveBeenCalled();
    expect(selected.revealRange).toHaveBeenCalledTimes(1);
  });

  test('does not select the same path and ref from another URI authority', async () => {
    const target = gitUri('head', { authority: 'target' });
    const other = editor(gitUri('head', { authority: 'other' }));
    const selected = editor(target);
    await showAt(target, [other, selected]);
    expect(other.revealRange).not.toHaveBeenCalled();
    expect(selected.revealRange).toHaveBeenCalledTimes(1);
  });

  test('accepts equivalent Git metadata with a different JSON property order', async () => {
    const target = gitUri('head');
    const selected = editor(gitUri('head', { query: JSON.stringify({ ref: 'head', path: '/repo/app.ts' }) }));
    await showAt(target, [selected]);
    expect(selected.revealRange).toHaveBeenCalledTimes(1);
  });

  test('keeps exact URI matches working', async () => {
    const target = gitUri('head');
    const selected = editor(gitUri('head'));
    await showAt(target, [selected]);
    expect(selected.revealRange).toHaveBeenCalledTimes(1);
  });
});
