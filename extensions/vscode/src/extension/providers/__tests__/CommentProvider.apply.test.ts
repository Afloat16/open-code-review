// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

import { mkdtempSync, readFileSync, rmSync, writeFileSync } from 'fs';
import { tmpdir } from 'os';
import { join } from 'path';
import * as vscode from 'vscode';
import { ReviewComment, ReviewMode } from '@shared/types';
import { CommentProvider } from '../CommentProvider';
import { GitService } from '../../services/GitService';

jest.mock('vscode', () => {
  const base = jest.requireActual('../../../../__mocks__/vscode.js');
  return {
    ...base,
    comments: { createCommentController: () => new base.CommentController() },
    workspace: { ...base.workspace, applyEdit: jest.fn() },
    window: {
      ...base.window, showErrorMessage: jest.fn(), showTextDocument: jest.fn(),
    },
    WorkspaceEdit: class {
      edits: Array<{ range: vscode.Range; text: string }> = [];
      replace(_uri: vscode.Uri, range: vscode.Range, text: string) { this.edits.push({ range, text }); }
      delete(_uri: vscode.Uri, range: vscode.Range) { this.edits.push({ range, text: '' }); }
    },
  };
});

interface EditRecord { range: vscode.Range; text: string }

describe('applying suggestions to the current document', () => {
  let directory: string;
  let file: string;
  let text: string;
  let provider: CommentProvider;
  let save: jest.Mock;
  let states: Array<{ status: string }>;

  function offset(position: vscode.Position): number {
    const lines = text.split('\n');
    return lines.slice(0, position.line).reduce((sum, line) => sum + line.length + 1, 0) + position.character;
  }

  beforeEach(() => {
    directory = mkdtempSync(join(tmpdir(), 'ocr-apply-'));
    file = join(directory, 'code.ts');
    states = [];
    save = jest.fn(async () => { writeFileSync(file, text); return true; });
    const document = {
      get lineCount() { return text.split('\n').length; },
      lineAt: (line: number) => ({ text: text.split('\n')[line].replace(/\r$/, '') }),
      getText: (range?: vscode.Range) => range ? text.slice(offset(range.start), offset(range.end)) : text,
      save,
    };
    jest.spyOn(vscode.workspace, 'openTextDocument').mockResolvedValue(document as unknown as vscode.TextDocument);
    (vscode.workspace as any).workspaceFolders = [{ uri: vscode.Uri.file(directory) }];
    jest.mocked(vscode.workspace.applyEdit).mockImplementation(async edit => {
      for (const change of (edit as unknown as { edits: EditRecord[] }).edits) {
        text = text.slice(0, offset(change.range.start)) + change.text + text.slice(offset(change.range.end));
      }
      return true;
    });
    const git = {
      prepareReviewFileStatus: async () => {},
      getReviewFileStatus: async () => 'modified',
      readWorkspaceFile: async () => readFileSync(file, 'utf8'),
    } as unknown as GitService;
    provider = new CommentProvider(vscode.Uri.file(directory), git);
    provider.onSync(next => { states = next; });
  });

  afterEach(() => {
    provider.clear();
    jest.restoreAllMocks();
    jest.clearAllMocks();
    rmSync(directory, { recursive: true, force: true });
  });

  async function show(source: string, comments: Partial<ReviewComment>[]) {
    text = source;
    writeFileSync(file, source);
    await provider.show(comments.map(comment => ({
      path: 'code.ts', content: 'Finding', startLine: 2, endLine: 2,
      existingCode: 'bad();', suggestionCode: 'fixed();', ...comment,
    })), { mode: ReviewMode.Workspace });
  }

  it.each([
    {
      name: 'inserted unsaved lines', source: 'header\nbad();\nfooter',
      current: '// unsaved\nheader\nbad();\nfooter', expected: '// unsaved\nheader\nfixed();\nfooter', comment: {},
    },
    {
      name: 'deleted lines before the quoted source', source: 'one\ntwo\nbad();\nfooter',
      current: 'bad();\nfooter', expected: 'fixed();\nfooter', comment: { startLine: 3, endLine: 3 },
    },
    {
      name: 'a comment mounted from source with zero line hints', source: 'header\nbad();\nfooter',
      current: 'header\nbad();\nfooter', expected: 'header\nfixed();\nfooter', comment: { startLine: 0, endLine: 0 },
    },
    {
      name: 'literal leading operators', source: '-value;\nvalue;\nfooter',
      current: '-value;\nvalue;\nfooter', expected: 'fixed();\nvalue;\nfooter', comment: { existingCode: '-value;' },
    },
    {
      name: 'blank lines inside a moved source block', source: 'header\nbad();\n\nmoreBad();\nfooter',
      current: '// unsaved\nheader\nbad();\n\nmoreBad();\nfooter', expected: '// unsaved\nheader\nfixed();\nfooter',
      comment: { existingCode: 'bad();\n\nmoreBad();', endLine: 4 },
    },
    {
      name: 'CRLF source and a terminal quote newline', source: 'header\r\nbad();\r\nfooter',
      current: '// unsaved\r\nheader\r\nbad();\r\nfooter', expected: '// unsaved\r\nheader\r\nfixed();\r\nfooter',
      comment: { existingCode: 'bad();\r\n' },
    },
  ])('replaces only the quoted code after $name', async ({ source, current, expected, comment }) => {
    await show(source, [comment]);
    text = current;
    await provider.apply(0);
    expect(text).toBe(expected);
    expect(readFileSync(file, 'utf8')).toBe(expected);
    expect(save).toHaveBeenCalledTimes(1);
    expect(states[0].status).toBe('applied');
  });

  it.each([
    { name: 'modified source at a valid line', current: 'header\nalreadyFixed();\nfooter', comment: {} },
    { name: 'two possible relocated blocks', current: 'bad();\nmarker\nbad();', comment: {} },
    { name: 'a substring of a different line', current: 'header\nreallybad();\nfooter', comment: {} },
    { name: 'missing source evidence', current: 'header\nbad();\nfooter', comment: { existingCode: undefined } },
  ])('leaves the document unchanged for $name', async ({ current, comment }) => {
    const source = 'header\nbad();\nfooter';
    await show(source, [comment]);
    text = current;
    await provider.apply(0);
    expect(text).toBe(current);
    expect(readFileSync(file, 'utf8')).toBe(source);
    expect(vscode.workspace.applyEdit).not.toHaveBeenCalled();
    expect(save).not.toHaveBeenCalled();
    expect(vscode.window.showErrorMessage).toHaveBeenCalled();
    expect(states[0].status).toBe('pending');
  });

  it('keeps a matching line hint usable when the same code appears elsewhere', async () => {
    await show('bad();\nseparator\nbad();', [{ startLine: 3, endLine: 3 }]);
    await provider.apply(0);
    expect(text).toBe('bad();\nseparator\nfixed();');
    expect(states[0].status).toBe('applied');
  });

  it('continues applying later findings after an earlier suggestion changes the line count', async () => {
    await show('header\nbad();\nseparator\nsecondBad();\nfooter', [
      { suggestionCode: 'fixed();\nextra();' },
      { startLine: 4, endLine: 4, existingCode: 'secondBad();', suggestionCode: 'secondFixed();' },
    ]);
    await provider.apply(0);
    await provider.apply(1);
    expect(text).toBe('header\nfixed();\nextra();\nseparator\nsecondFixed();\nfooter');
    expect(readFileSync(file, 'utf8')).toBe(text);
    expect(states.map(state => state.status)).toEqual(['applied', 'applied']);
  });
});
