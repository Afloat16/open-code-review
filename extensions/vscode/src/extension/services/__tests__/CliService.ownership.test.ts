// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

process.env.OCR_SKIP_SHELL_RESOLVE = '1';
import { ChildProcess } from 'child_process';
import { once } from 'events';
import { mkdtempSync, rmSync, writeFileSync } from 'fs';
import { tmpdir } from 'os';
import { join } from 'path';
import { ReviewMode } from '@shared/types';
import { CliService } from '../CliService';
import { ReviewSession } from '../ReviewSession';

const childProcess = jest.requireActual<typeof import('child_process')>('child_process');
const realSpawn = childProcess.spawn;

const describePosix = process.platform === 'win32' ? describe.skip : describe;

describePosix('review cancellation while other CLI commands run', () => {
  let cwd: string;
  let service: CliService;
  let children: Array<{ process: ChildProcess; closed: Promise<unknown> }>;

  beforeEach(() => {
    cwd = mkdtempSync(join(tmpdir(), 'ocr-cancel-owner-'));
    children = [];
    writeFileSync(join(cwd, 'review'), [
      'const timer = setInterval(() => {}, 1000);',
      'function finish() {',
      '  clearInterval(timer);',
      '  process.stdout.end(JSON.stringify({ status: "success", comments: [] }));',
      '  process.stdin.pause();',
      '}',
      'if (process.argv.includes("defer")) {',
      '  process.on("SIGTERM", () => {});',
      '  process.stdin.once("data", finish);',
      '} else { process.on("SIGTERM", finish); }',
      'process.stderr.write("review ready\\n");',
    ].join('\n'));
    writeFileSync(join(cwd, 'config'), 'process.stdout.write("configured");');
    writeFileSync(join(cwd, 'llm'), 'process.stdout.write("connected");');
    jest.spyOn(process, 'cwd').mockReturnValue(cwd);
    jest.spyOn(childProcess, 'spawn').mockImplementation((...args: Parameters<typeof realSpawn>) => {
      const proc = realSpawn(...args);
      jest.spyOn(proc, 'kill');
      children.push({ process: proc, closed: new Promise(resolve => proc.once('close', resolve)) });
      return proc;
    });
    service = new CliService(process.execPath);
  });

  afterEach(async () => {
    for (const child of children) {
      if (child.process.pid && child.process.exitCode === null && child.process.signalCode === null) {
        child.process.kill('SIGKILL');
      }
    }
    await Promise.all(children.map(child => child.closed));
    jest.restoreAllMocks();
    rmSync(cwd, { recursive: true, force: true });
  });

  function startReview(background = '') {
    let notifyReady!: () => void;
    const ready = new Promise<void>(resolve => { notifyReady = resolve; });
    const callbacks = {
      onState: jest.fn(), onDone: jest.fn(),
      onLog: (line: { text: string }) => { if (line.text.includes('review ready')) notifyReady(); },
    };
    const session = new ReviewSession(service, cwd);
    const done = session.run({ mode: ReviewMode.Workspace, customPrompt: background }, callbacks);
    const child = children[children.length - 1].process;
    return { ready, done, session, callbacks, child };
  }

  it.each(['config', 'connection'])('can cancel after an overlapping %s command finishes', async command => {
    const review = startReview();
    await review.ready;
    if (command === 'config') {
      await expect(service.runRaw(['config', 'set', 'model', 'example'], cwd, () => {})).resolves.toBe('configured');
    } else {
      await expect(service.testConnection()).resolves.toEqual({ ok: true });
    }

    review.session.cancel(review.callbacks);
    expect(review.child.kill).toHaveBeenCalledWith('SIGTERM');
    await review.done;
    expect(review.callbacks.onState).toHaveBeenLastCalledWith('cancelled');
    expect(review.callbacks.onDone).not.toHaveBeenCalled();
  });

  it('cancels the review while an overlapping connection test remains alive', async () => {
    const review = startReview();
    await review.ready;
    writeFileSync(join(cwd, 'llm'), [
      'process.stdin.once("data", () => process.stdout.end("connected"));',
      'process.stderr.write("connection ready\\n");',
    ].join('\n'));
    const connection = service.testConnection();
    const probe = children[children.length - 1].process;
    await once(probe.stderr!, 'data');

    review.session.cancel(review.callbacks);
    expect(review.child.kill).toHaveBeenCalledWith('SIGTERM');
    expect(probe.kill).not.toHaveBeenCalled();
    await review.done;
    expect(probe.exitCode).toBeNull();
    expect(probe.signalCode).toBeNull();
    probe.stdin!.end('continue');
    await expect(connection).resolves.toEqual({ ok: true });
  });

  it('keeps the current review cancellable when the previous review closes late', async () => {
    const previous = startReview('defer');
    await previous.ready;
    previous.session.cancel(previous.callbacks);
    const current = startReview();
    await current.ready;
    previous.child.stdin!.end('continue');
    await previous.done;

    current.session.cancel(current.callbacks);
    expect(current.child.kill).toHaveBeenCalledWith('SIGTERM');
    await current.done;
    expect(current.callbacks.onState).toHaveBeenLastCalledWith('cancelled');
  });

  it('keeps the review cancellable after a different command fails to spawn', async () => {
    const failed = service.runRaw(['config'], join(cwd, 'missing'), () => {}).catch(error => error);
    const review = startReview();
    await review.ready;
    expect(await failed).toMatchObject({ code: 'ENOENT' });

    review.session.cancel(review.callbacks);
    expect(review.child.kill).toHaveBeenCalledWith('SIGTERM');
    await review.done;
    expect(review.callbacks.onState).toHaveBeenLastCalledWith('cancelled');
  });
});
