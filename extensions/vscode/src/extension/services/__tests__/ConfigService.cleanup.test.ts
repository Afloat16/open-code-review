// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

import fs from 'fs';
import os from 'os';
import path from 'path';
import { CliService } from '../CliService';
import { ConfigService } from '../ConfigService';

describe('ConfigService draft cleanup', () => {
  let root: string;
  let temporaryRoot: string;
  let savedConfig: string;
  let savedBytes: string;
  let cli: { testConnection: jest.Mock };
  let config: ConfigService;

  beforeEach(() => {
    root = fs.mkdtempSync(path.join(os.tmpdir(), 'ocr-config-cleanup-'));
    const home = path.join(root, 'home');
    temporaryRoot = path.join(root, 'temporary');
    savedConfig = path.join(home, '.opencodereview', 'config.json');
    fs.mkdirSync(path.dirname(savedConfig), { recursive: true });
    fs.mkdirSync(temporaryRoot);
    savedBytes = '{"provider":"openai","providers":{"openai":{"api_key":"fixture-key","model":"saved-model"}}}';
    fs.writeFileSync(savedConfig, savedBytes);
    jest.spyOn(os, 'homedir').mockReturnValue(home);
    jest.spyOn(os, 'tmpdir').mockReturnValue(temporaryRoot);
    cli = { testConnection: jest.fn(async () => ({ ok: true })) };
    config = new ConfigService(cli as unknown as CliService);
  });

  afterEach(() => {
    jest.restoreAllMocks();
    fs.rmSync(root, { recursive: true, force: true });
  });

  function expectClean() {
    expect(fs.readdirSync(temporaryRoot)).toEqual([]);
    expect(fs.readFileSync(savedConfig, 'utf8')).toBe(savedBytes);
  }

  it('removes the temporary home if creating the draft directory fails', async () => {
    const mkdir = fs.mkdirSync;
    const failure = Object.assign(new Error('draft directory denied'), { code: 'EACCES' });
    jest.spyOn(fs, 'mkdirSync').mockImplementation((target, options) => {
      if (String(target).startsWith(temporaryRoot + path.sep)) throw failure;
      return mkdir(target, options);
    });

    await expect(config.testWithEntries([{ key: 'model', value: 'draft-model' }])).rejects.toBe(failure);

    expectClean();
    expect(cli.testConnection).not.toHaveBeenCalled();
  });

  it('removes a partially written draft after a filesystem write failure', async () => {
    const write = fs.writeFileSync;
    const failure = Object.assign(new Error('draft write failed'), { code: 'ENOSPC' });
    jest.spyOn(fs, 'writeFileSync').mockImplementation((target, data, options) => {
      if (String(target).startsWith(temporaryRoot + path.sep)) {
        write(target, String(data).slice(0, 64), options);
        throw failure;
      }
      return write(target, data, options);
    });

    await expect(config.testWithEntries([{ key: 'model', value: 'draft-model' }])).rejects.toBe(failure);

    expectClean();
    expect(cli.testConnection).not.toHaveBeenCalled();
  });

  it('still exposes the draft while testing and removes it after success', async () => {
    cli.testConnection.mockImplementation(async ({ home }) => {
      const draft = JSON.parse(fs.readFileSync(path.join(home, '.opencodereview', 'config.json'), 'utf8'));
      expect(draft.providers.openai.model).toBe('draft-model');
      return { ok: true };
    });

    expect(await config.testWithEntries([{ key: 'model', value: 'draft-model' }])).toEqual({ ok: true });

    expectClean();
  });

  it('preserves connection failures while removing the draft', async () => {
    const failure = new Error('connection failed');
    cli.testConnection.mockRejectedValue(failure);

    await expect(config.testWithEntries([])).rejects.toBe(failure);

    expectClean();
  });
});
