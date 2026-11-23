// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

import { h } from 'preact';
import { renderToString } from 'preact-render-to-string';
import { FileChange, ReviewMode } from '../../shared/types';
import { App } from '../App';
import { bridge } from '../bridge';
import { IdleView } from '../views/IdleView';

jest.mock('../styles/global.css', () => ({}));
jest.mock('../bridge', () => ({ bridge: { post: jest.fn(), onMessage: jest.fn(() => () => {}) } }));
jest.mock('../views/IdleView', () => ({ IdleView: jest.fn(() => null) }));

describe('App rename diff messages', () => {
  beforeEach(() => jest.clearAllMocks());

  it.each([ReviewMode.Workspace, ReviewMode.Commit, ReviewMode.Branch])(
    'keeps the source path when opening a %s file', (mode) => {
      renderToString(h(App, {}));
      const props = jest.mocked(IdleView).mock.calls[0][0];
      const file: FileChange = { path: 'after.ts', oldPath: 'before.ts', status: 'renamed' };
      props.onOpenFile(file, mode, 'main', 'feature', 'abc123');
      expect(bridge.post).toHaveBeenCalledWith({
        type: 'openFileDiff', path: 'after.ts', oldPath: 'before.ts', status: 'renamed',
        mode, from: 'main', to: 'feature', commit: 'abc123',
      });
    },
  );
});
