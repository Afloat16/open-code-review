// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

import * as vscode from 'vscode';
import { ReviewComment, ReviewMode } from '@shared/types';
import { GitService } from '../../services/GitService';
import { CommentProvider } from '../CommentProvider';

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

describe('suggestions are applied once per review comment', () => {
  let content: string;
  let provider: CommentProvider;
  let doc: { readonly lineCount: number; lineAt: (line: number) => { text: string }; save: jest.Mock };
  const suggestion: ReviewComment = {
    path: 'file.ts', content: 'Insert a line.', startLine: 1, endLine: 1,
    existingCode: 'first', suggestionCode: 'first\ninserted',
  };
  const later: ReviewComment = {
    path: 'file.ts', content: 'Replace the target.', startLine: 3, endLine: 3,
    existingCode: 'target', suggestionCode: 'fixed',
  };
  const ctx = { mode: ReviewMode.Workspace };

  beforeEach(() => {
    jest.resetAllMocks();
    content = 'first\nkeep\ntarget\nlast';
    doc = {
      get lineCount() { return content.split('\n').length; },
      lineAt: (line: number) => ({ text: content.split('\n')[line] }),
      save: jest.fn().mockResolvedValue(true),
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
      prepareReviewFileStatus: jest.fn().mockResolvedValue(undefined),
      getReviewFileStatus: jest.fn().mockResolvedValue(null),
      readWorkspaceFile: jest.fn(async () => content),
    } as unknown as GitService;
    provider = new CommentProvider(vscode.Uri.file('/mock'), git);
  });

  afterEach(() => provider.dispose());

  test('ignores a repeated Apply after the edit completed', async () => {
    await provider.show([suggestion], ctx);
    await provider.apply(0);
    await provider.apply(0);
    expect(content).toBe('first\ninserted\nkeep\ntarget\nlast');
    expect(vscode.workspace.applyEdit).toHaveBeenCalledTimes(1);
  });

  test('coalesces simultaneous Apply requests for one comment', async () => {
    await provider.show([suggestion], ctx);
    await Promise.all([provider.apply(0), provider.apply(0)]);
    expect(content).toBe('first\ninserted\nkeep\ntarget\nlast');
    expect(vscode.workspace.applyEdit).toHaveBeenCalledTimes(1);
  });

  test.each(['discarded', 'falsePositive'] as const)('does not apply a %s comment', async (status) => {
    await provider.show([suggestion], ctx);
    if (status === 'discarded') provider.discard(0);
    else provider.falsePositive(0);
    await provider.apply(0);
    expect(content).toBe('first\nkeep\ntarget\nlast');
    expect(vscode.workspace.applyEdit).not.toHaveBeenCalled();
  });

  test('permits a retry after the editor rejected the edit', async () => {
    await provider.show([suggestion], ctx);
    (vscode.workspace.applyEdit as jest.Mock).mockResolvedValueOnce(false);
    await provider.apply(0);
    expect(content).toBe('first\nkeep\ntarget\nlast');
    await provider.apply(0);
    expect(content).toBe('first\ninserted\nkeep\ntarget\nlast');
    expect(vscode.workspace.applyEdit).toHaveBeenCalledTimes(2);
  });

  test('permits a retry after opening the document failed', async () => {
    await provider.show([suggestion], ctx);
    (vscode.workspace.openTextDocument as jest.Mock).mockRejectedValueOnce(new Error('open failed'));
    await expect(provider.apply(0)).rejects.toThrow('open failed');
    await provider.apply(0);
    expect(content).toBe('first\ninserted\nkeep\ntarget\nlast');
    expect(vscode.workspace.applyEdit).toHaveBeenCalledTimes(1);
  });

  test('does not repeat a successful edit when saving fails and preserves its line offset', async () => {
    await provider.show([suggestion, later], ctx);
    doc.save.mockRejectedValueOnce(new Error('save failed'));
    await expect(provider.apply(0)).rejects.toThrow('save failed');
    await provider.apply(0);
    await provider.apply(1);
    expect(content).toBe('first\ninserted\nkeep\nfixed\nlast');
    expect(vscode.workspace.applyEdit).toHaveBeenCalledTimes(2);
  });

  test('does not repeat a successful edit when revealing it fails', async () => {
    await provider.show([suggestion], ctx);
    (vscode.window.showTextDocument as jest.Mock).mockRejectedValueOnce(new Error('reveal failed'));
    await expect(provider.apply(0)).rejects.toThrow('reveal failed');
    await provider.apply(0);
    expect(content).toBe('first\ninserted\nkeep\ntarget\nlast');
    expect(vscode.workspace.applyEdit).toHaveBeenCalledTimes(1);
  });

  test('keeps independent pending suggestions available after an application', async () => {
    await provider.show([suggestion, later], ctx);
    await provider.apply(0);
    await provider.apply(1);
    expect(content).toBe('first\ninserted\nkeep\nfixed\nlast');
  });

  test('resets application eligibility when displaying a new review', async () => {
    await provider.show([suggestion], ctx);
    await provider.apply(0);
    content = 'first\nkeep\ntarget\nlast';
    await provider.show([suggestion], ctx);
    await provider.apply(0);
    expect(content).toBe('first\ninserted\nkeep\ntarget\nlast');
    expect(vscode.workspace.applyEdit).toHaveBeenCalledTimes(2);
  });

  test('does not apply a comment cleared while its document is opening', async () => {
    await provider.show([suggestion], ctx);
    let open!: (value: typeof doc) => void;
    (vscode.workspace.openTextDocument as jest.Mock).mockImplementationOnce(() => new Promise((resolve) => { open = resolve; }));
    const pending = provider.apply(0);
    provider.clear();
    open(doc);
    await pending;
    expect(content).toBe('first\nkeep\ntarget\nlast');
    expect(vscode.workspace.applyEdit).not.toHaveBeenCalled();
  });

  test('does not change a replacement review when an earlier edit resolves', async () => {
    const sync = jest.fn();
    provider.onSync(sync);
    await provider.show([suggestion], ctx);
    const applyEdit = vscode.workspace.applyEdit as jest.Mock;
    const editDocument = applyEdit.getMockImplementation()!;
    let started!: () => void;
    let finish!: (value: boolean) => void;
    const editStarted = new Promise<void>((resolve) => { started = resolve; });
    applyEdit.mockImplementationOnce((edit) => {
      void editDocument(edit);
      started();
      return new Promise<boolean>((resolve) => { finish = resolve; });
    });
    const pending = provider.apply(0);
    await editStarted;
    await provider.show([{ ...later, startLine: 4, endLine: 4 }], ctx);
    finish(true);
    await pending;

    expect(sync).toHaveBeenLastCalledWith([{ index: 0, status: 'pending', jumpable: true }]);
    await provider.apply(0);
    expect(content).toBe('first\ninserted\nkeep\nfixed\nlast');
    expect(applyEdit).toHaveBeenCalledTimes(2);
  });
});
