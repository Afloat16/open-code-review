// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

import * as vscode from 'vscode';
import { ReviewComment, ReviewMode } from '@shared/types';
import { CommentProvider } from '../CommentProvider';
import { GitService } from '../../services/GitService';

jest.mock('vscode', () => {
  const base = jest.requireActual('../../../../__mocks__/vscode.js');
  return {
    ...base,
    comments: {
      createCommentController: () => ({
        createCommentThread: (_uri: unknown, _range: unknown, comments: unknown[]) => ({ comments, dispose: jest.fn() }),
        dispose: jest.fn(),
      }),
    },
    workspace: { ...base.workspace, openTextDocument: jest.fn(), applyEdit: jest.fn() },
    window: { ...base.window, showTextDocument: jest.fn(), showErrorMessage: jest.fn() },
    WorkspaceEdit: class {
      edits: Array<{ range: vscode.Range; text: string }> = [];
      replace(_uri: unknown, range: vscode.Range, text: string) { this.edits.push({ range, text }); }
      delete(_uri: unknown, range: vscode.Range) { this.edits.push({ range, text: '' }); }
    },
  };
});

describe('CommentProvider suggestion targets', () => {
  let content: string;
  let provider: CommentProvider;

  beforeEach(() => {
    jest.clearAllMocks();
    content = 'first\nkeep\ntarget\nlast';
    const doc = {
      get lineCount() { return content.split('\n').length; },
      lineAt: (line: number) => ({ text: content.split('\n')[line] }),
      save: jest.fn(async () => true),
    };
    (vscode.workspace.openTextDocument as jest.Mock).mockResolvedValue(doc);
    (vscode.workspace.applyEdit as jest.Mock).mockImplementation(async (edit) => {
      for (const { range, text } of edit.edits) {
        const lines = content.split('\n');
        lines.splice(range.start.line, range.end.line - range.start.line + 1, ...text.split('\n'));
        content = lines.join('\n');
      }
      return true;
    });
    const git = {
      prepareReviewFileStatus: jest.fn(async () => undefined),
      getReviewFileStatus: jest.fn(async () => null),
      readWorkspaceFile: jest.fn(async () => content),
    } as unknown as GitService;
    provider = new CommentProvider(vscode.Uri.file('/mock'), git);
  });

  afterEach(() => provider.dispose());

  const comment = (startLine: number): ReviewComment => ({
    path: 'file.ts', content: 'Use the corrected value.', startLine, endLine: startLine,
    existingCode: 'target', suggestionCode: 'fixed',
  });

  it.each([0, 99])('applies at the displayed anchor when line %s was relocated', async (line) => {
    await provider.show([comment(line)], { mode: ReviewMode.Workspace });

    await provider.apply(0);

    expect(content).toBe('first\nkeep\nfixed\nlast');
    expect(vscode.window.showErrorMessage).not.toHaveBeenCalled();
  });

  it('tracks earlier edits relative to relocated anchors', async () => {
    const first: ReviewComment = {
      path: 'file.ts', content: 'Add a line.', startLine: 1, endLine: 1,
      existingCode: 'first', suggestionCode: 'first\ninserted',
    };
    await provider.show([first, comment(99)], { mode: ReviewMode.Workspace });

    await provider.apply(0);
    await provider.apply(1);

    expect(content).toBe('first\ninserted\nkeep\nfixed\nlast');
    expect(vscode.window.showErrorMessage).not.toHaveBeenCalled();
  });

  it('keeps applying an unchanged numeric anchor', async () => {
    await provider.show([comment(3)], { mode: ReviewMode.Workspace });

    await provider.apply(0);

    expect(content).toBe('first\nkeep\nfixed\nlast');
  });

  it('does not edit a comment that could not be mounted', async () => {
    await provider.show([{ ...comment(0), existingCode: 'missing' }], { mode: ReviewMode.Workspace });

    await provider.apply(0);

    expect(content).toBe('first\nkeep\ntarget\nlast');
    expect(vscode.workspace.applyEdit).not.toHaveBeenCalled();
    expect(vscode.window.showErrorMessage).toHaveBeenCalled();
  });
});
