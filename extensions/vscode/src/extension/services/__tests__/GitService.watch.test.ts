// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

import * as vscode from 'vscode';
import { GitService } from '../GitService';

jest.mock('vscode', () => ({
  workspace: { workspaceFolders: [{ uri: { fsPath: '/mock' } }] },
  extensions: { getExtension: jest.fn() },
  Disposable: class {
    constructor(private cleanup: () => void) {}
    dispose() { this.cleanup(); }
  },
}));

const flush = async () => { for (let i = 0; i < 8; i++) await Promise.resolve(); };

describe('workspace watcher disposal during Git initialization', () => {
  let listeners: Set<() => void>;
  let repositories: unknown[];
  let stateDispose: jest.Mock;
  let onStateChange: jest.Mock;
  let repository: unknown;

  beforeEach(() => {
    jest.useFakeTimers();
    listeners = new Set();
    repositories = [];
    stateDispose = jest.fn();
    onStateChange = jest.fn(() => ({ dispose: stateDispose }));
    repository = { rootUri: { fsPath: '/mock' }, state: { onDidChange: onStateChange } };
    const api = {
      repositories,
      onDidOpenRepository: (listener: () => void) => {
        listeners.add(listener);
        return { dispose: () => listeners.delete(listener) };
      },
    };
    jest.spyOn(vscode.extensions, 'getExtension').mockReturnValue({
      isActive: true, exports: { getAPI: () => api },
    } as unknown as vscode.Extension<unknown>);
  });

  afterEach(() => {
    jest.useRealTimers();
    jest.restoreAllMocks();
  });

  it.each(['timeout', 'repository'])('leaves no listener after disposal followed by %s initialization', async (completion) => {
    const onUpdate = jest.fn();
    const watch = new GitService().watchWorkspaceChanges(onUpdate);
    await flush();
    expect(listeners.size).toBe(1);

    watch.dispose();
    if (completion === 'timeout') jest.advanceTimersByTime(5000);
    else {
      repositories.push(repository);
      for (const listener of [...listeners]) listener();
    }
    await flush();

    expect(listeners.size).toBe(0);
    expect(onStateChange).not.toHaveBeenCalled();
    expect(onUpdate).not.toHaveBeenCalled();
    expect(jest.getTimerCount()).toBe(0);
  });

  it('keeps a live watcher subscribed and disposes both subscriptions later', async () => {
    const watch = new GitService().watchWorkspaceChanges(jest.fn());
    await flush();
    repositories.push(repository);
    for (const listener of [...listeners]) listener();
    await flush();

    expect(onStateChange).toHaveBeenCalledTimes(1);
    expect(listeners.size).toBe(1);
    watch.dispose();

    expect(stateDispose).toHaveBeenCalledTimes(1);
    expect(listeners.size).toBe(0);
    expect(jest.getTimerCount()).toBe(0);
  });
});
