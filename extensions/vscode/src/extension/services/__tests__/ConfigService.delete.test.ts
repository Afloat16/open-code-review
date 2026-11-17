// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

import fs from 'fs';
import os from 'os';
import path from 'path';
import { CliService } from '../CliService';
import { ConfigService } from '../ConfigService';

describe('saved configuration during provider deletion', () => {
  let home: string;
  let configPath: string;
  let config: ConfigService;

  beforeEach(() => {
    home = fs.mkdtempSync(path.join(os.tmpdir(), 'ocr-provider-delete-'));
    configPath = path.join(home, '.opencodereview', 'config.json');
    fs.mkdirSync(path.dirname(configPath));
    jest.spyOn(os, 'homedir').mockReturnValue(home);
    config = new ConfigService({} as CliService);
  });

  afterEach(() => {
    jest.restoreAllMocks();
    fs.rmSync(home, { recursive: true, force: true });
  });

  function save(extra: Record<string, unknown> = {}): string {
    const raw = { provider: 'local', model: 'old-model', custom_providers: { local: { model: 'old-model' } }, ...extra };
    const bytes = JSON.stringify(raw);
    fs.writeFileSync(configPath, bytes, { mode: 0o600 });
    return bytes;
  }

  it.each([
    { language: 'en-US' },
    { effort: 'high', future_option: { enabled: false, count: 0 } },
  ])('preserves unrelated settings when deleting the last provider: %j', (remaining) => {
    save(remaining);

    config.deleteCustomProvider('local');

    expect(JSON.parse(fs.readFileSync(configPath, 'utf8'))).toEqual(remaining);
    expect(fs.readdirSync(path.dirname(configPath))).toEqual(['config.json']);
  });

  it('keeps the original file intact when a replacement is only partially written', () => {
    const before = save({ provider: 'openai', providers: { openai: { model: 'kept', api_key: 'fixture-key' } } });
    const write = fs.writeFileSync;
    const failure = Object.assign(new Error('disk full'), { code: 'ENOSPC' });
    jest.spyOn(fs, 'writeFileSync').mockImplementation((target, data, options) => {
      write(target, String(data).slice(0, 32), options);
      throw failure;
    });

    expect(() => config.deleteCustomProvider('local')).toThrow(failure);

    expect(fs.readFileSync(configPath, 'utf8')).toBe(before);
    expect(fs.readdirSync(path.dirname(configPath))).toEqual(['config.json']);
  });

  it('keeps the original file and removes temporary content when replacement fails', () => {
    const before = save({ provider: 'openai', providers: { openai: { model: 'kept' } } });
    const failure = Object.assign(new Error('replacement denied'), { code: 'EACCES' });
    jest.spyOn(fs, 'renameSync').mockImplementation(() => { throw failure; });

    expect(() => config.deleteCustomProvider('local')).toThrow(failure);

    expect(fs.readFileSync(configPath, 'utf8')).toBe(before);
    expect(fs.readdirSync(path.dirname(configPath))).toEqual(['config.json']);
  });

  (process.platform === 'win32' ? it.skip : it)('replaces an existing readable config with a private file', () => {
    save({ provider: 'openai', providers: { openai: { model: 'kept', api_key: 'fixture-key' } } });
    fs.chmodSync(configPath, 0o644);

    config.deleteCustomProvider('local');

    expect(fs.statSync(configPath).mode & 0o777).toBe(0o600);
    const raw = JSON.parse(fs.readFileSync(configPath, 'utf8'));
    expect(raw.providers.openai.api_key).toBe('fixture-key');
    expect(raw.custom_providers).toBeUndefined();
    expect(fs.readdirSync(path.dirname(configPath))).toEqual(['config.json']);
  });

  it('still removes a configuration that has no settings left', () => {
    save();

    expect(config.deleteCustomProvider('local')).toBeNull();

    expect(fs.existsSync(configPath)).toBe(false);
    expect(fs.readdirSync(path.dirname(configPath))).toEqual([]);
  });

  it('does not rewrite malformed configuration when the provider cannot be read', () => {
    const bytes = '{"custom_providers":';
    fs.writeFileSync(configPath, bytes);

    expect(config.deleteCustomProvider('local')).toBeNull();

    expect(fs.readFileSync(configPath, 'utf8')).toBe(bytes);
  });
});
