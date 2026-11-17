// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

process.env.OCR_SKIP_SHELL_RESOLVE = '1';
import { spawn } from 'child_process';
import { EventEmitter } from 'events';
import { PassThrough } from 'stream';
import { CliService } from '../CliService';
import { ConfigService } from '../ConfigService';

jest.mock('child_process', () => ({
  ...jest.requireActual('child_process'),
  spawn: jest.fn(),
}));

const originalPlatform = Object.getOwnPropertyDescriptor(process, 'platform');

function processStreams(pid: number) {
  return Object.assign(new EventEmitter(), {
    pid,
    stdout: new PassThrough(),
    stderr: new PassThrough(),
    exitCode: null as number | null,
    signalCode: null,
    kill: jest.fn(() => true),
  });
}

function close(proc: ReturnType<typeof processStreams>) {
  if (proc.exitCode !== null) return;
  proc.exitCode = 0;
  proc.stdout.end();
  proc.stderr.end();
  proc.emit('close', 0);
}

describe('CliService cancellation ownership', () => {
  let processes: Array<ReturnType<typeof processStreams>>;

  beforeEach(() => {
    Object.defineProperty(process, 'platform', { value: 'linux' });
    jest.clearAllMocks();
    processes = [];
    jest.mocked(spawn).mockImplementation(() => {
      const proc = processStreams(100 + processes.length);
      processes.push(proc);
      return proc as unknown as ReturnType<typeof spawn>;
    });
  });

  afterEach(() => {
    for (const proc of processes) close(proc);
    jest.restoreAllMocks();
    if (originalPlatform) Object.defineProperty(process, 'platform', originalPlatform);
  });

  it.each([
    ['connection', false], ['connection', true],
    ['single setting', false], ['single setting', true],
    ['settings batch', false], ['settings batch', true],
  ] as const)('keeps the review cancellable during %s (finished=%s)', async (kind, finished) => {
    const cli = new CliService('ocr');
    const config = new ConfigService(cli);
    jest.spyOn(config, 'read').mockReturnValue(null);
    const review = cli.runRaw(['review'], '.', () => {});
    const setting = kind === 'connection'
      ? cli.testConnection()
      : kind === 'single setting'
        ? config.set('model', 'example')
        : config.setMany([{ key: 'model', value: 'example' }]);
    try {
      if (finished) {
        close(processes[1]);
        await setting;
      }

      cli.cancel();

      expect(processes[0].kill).toHaveBeenCalledWith('SIGTERM');
      expect(processes[1].kill).not.toHaveBeenCalled();
    } finally {
      for (const proc of processes) close(proc);
      await Promise.all([review, setting]);
    }
  });

  it.each(['close', 'error'])('does not let an older process %s clear the current review', async (event) => {
    const cli = new CliService('ocr');
    const first = cli.runRaw(['review'], '.', () => {}).catch(() => '');
    const second = cli.runRaw(['review'], '.', () => {});
    try {
      if (event === 'error') processes[0].emit('error', new Error('old process failed'));
      else close(processes[0]);
      await first;

      cli.cancel();

      expect(processes[1].kill).toHaveBeenCalledWith('SIGTERM');
      expect(processes[0].kill).not.toHaveBeenCalled();
    } finally {
      for (const proc of processes) close(proc);
      await second;
    }
  });

  it('targets the review process tree on Windows while a connection test runs', async () => {
    Object.defineProperty(process, 'platform', { value: 'win32' });
    const cli = new CliService('ocr');
    const review = cli.runRaw(['review'], '.', () => {});
    const connection = cli.testConnection();
    try {
      cli.cancel();

      expect(spawn).toHaveBeenNthCalledWith(3, 'taskkill', ['/pid', '100', '/t', '/f'], {
        stdio: 'ignore', windowsHide: true,
      });
    } finally {
      for (const proc of processes) close(proc);
      await Promise.all([review, connection]);
    }
  });
});
