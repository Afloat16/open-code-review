// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

import { execFile } from 'child_process';
import fs from 'fs';
import os from 'os';
import path from 'path';
import { promisify } from 'util';
import { CliService } from '../CliService';
import { ConfigService } from '../ConfigService';

const execFileAsync = promisify(execFile);
const nativeTest = process.env.OCR_TEST_CLI ? it : it.skip;

describe('configuration batch publication', () => {
  let home: string;
  let savedPath: string;
  let before: string;
  let cli: { runRaw: jest.Mock };
  let config: ConfigService;

  beforeEach(() => {
    home = fs.mkdtempSync(path.join(os.tmpdir(), 'ocr-config-batch-'));
    savedPath = path.join(home, '.opencodereview', 'config.json');
    fs.mkdirSync(path.dirname(savedPath));
    before = '{"language":"Chinese","effort":"low","future_option":{"enabled":false}}';
    fs.writeFileSync(savedPath, before);
    jest.spyOn(os, 'homedir').mockReturnValue(home);
    cli = { runRaw: jest.fn(runSet) };
    config = new ConfigService(cli as unknown as CliService);
  });

  afterEach(() => {
    jest.restoreAllMocks();
    fs.rmSync(home, { recursive: true, force: true });
  });

  async function runSet(args: string[], _cwd: string, _log: unknown, env?: NodeJS.ProcessEnv) {
    if (args[2] === 'invalid.key') throw new Error('invalid config key');
    const target = path.join(env?.HOME ?? home, '.opencodereview', 'config.json');
    const raw = fs.existsSync(target) ? JSON.parse(fs.readFileSync(target, 'utf8')) : {};
    raw[args[2]] = args[3];
    fs.mkdirSync(path.dirname(target), { recursive: true });
    fs.writeFileSync(target, JSON.stringify(raw));
    return '';
  }

  function expectNoTemporaryFiles(saved = true) {
    expect(fs.readdirSync(path.dirname(savedPath))).toEqual(saved ? ['config.json'] : []);
  }

  const invalidBatch = [{ key: 'language', value: 'English' }, { key: 'invalid.key', value: 'bad' }];
  const validBatch = [{ key: 'language', value: 'English' }, { key: 'effort', value: 'high' }];

  it('preserves exact saved bytes when a later CLI validation fails', async () => {
    await expect(config.setMany(invalidBatch)).rejects.toThrow('invalid config key');

    expect(fs.readFileSync(savedPath, 'utf8')).toBe(before);
    expectNoTemporaryFiles();
  });

  it('leaves no saved configuration after a failed first-time setup', async () => {
    fs.unlinkSync(savedPath);

    await expect(config.setMany(invalidBatch)).rejects.toThrow('invalid config key');

    expect(fs.existsSync(savedPath)).toBe(false);
    expectNoTemporaryFiles(false);
  });

  it('publishes all settings only after every CLI command succeeds', async () => {
    cli.runRaw.mockImplementation(async (...args) => {
      expect(fs.readFileSync(savedPath, 'utf8')).toBe(before);
      return runSet(...args as Parameters<typeof runSet>);
    });

    await config.setMany(validBatch);

    expect(JSON.parse(fs.readFileSync(savedPath, 'utf8'))).toEqual({
      language: 'English', effort: 'high', future_option: { enabled: false },
    });
    expectNoTemporaryFiles();
  });

  it('preserves a configuration changed while the batch is running', async () => {
    const newer = '{"language":"newer setting"}';
    cli.runRaw.mockImplementation(async (...args) => {
      await runSet(...args as Parameters<typeof runSet>);
      fs.writeFileSync(savedPath, newer);
      return '';
    });

    await expect(config.setMany(validBatch)).rejects.toThrow(/configuration changed/i);

    expect(fs.readFileSync(savedPath, 'utf8')).toBe(newer);
    expectNoTemporaryFiles();
  });

  it('keeps the original configuration if the final rename fails', async () => {
    const error = Object.assign(new Error('replacement denied'), { code: 'EACCES' });
    jest.spyOn(fs, 'renameSync').mockImplementation(() => { throw error; });

    await expect(config.setMany(validBatch)).rejects.toThrow(error);

    expect(fs.readFileSync(savedPath, 'utf8')).toBe(before);
    expectNoTemporaryFiles();
  });

  it('leaves empty batches untouched without invoking the CLI', async () => {
    await config.setMany([]);

    expect(cli.runRaw).not.toHaveBeenCalled();
    expect(fs.readFileSync(savedPath, 'utf8')).toBe(before);
    expectNoTemporaryFiles();
  });

  nativeTest.each(['existing-failure', 'missing-failure', 'success'])('uses the real CLI for %s', async (mode) => {
    cli.runRaw.mockImplementation(async (args, cwd, _log, env) => {
      const result = await execFileAsync(process.env.OCR_TEST_CLI!, args, {
        cwd, env: { ...process.env, HOME: home, USERPROFILE: home, ...env },
      });
      return result.stdout;
    });
    if (mode === 'missing-failure') fs.unlinkSync(savedPath);

    if (mode === 'success') {
      await config.setMany(validBatch);
      expect(JSON.parse(fs.readFileSync(savedPath, 'utf8'))).toMatchObject({
        language: 'English', effort: 'high', future_option: { enabled: false },
      });
    } else {
      await expect(config.setMany(invalidBatch)).rejects.toThrow();
      if (mode === 'existing-failure') expect(fs.readFileSync(savedPath, 'utf8')).toBe(before);
      else expect(fs.existsSync(savedPath)).toBe(false);
    }
    expectNoTemporaryFiles(mode !== 'missing-failure');
  });
});
