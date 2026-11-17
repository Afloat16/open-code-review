// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

process.env.OCR_SKIP_SHELL_RESOLVE = '1';
import fs from 'fs';
import os from 'os';
import path from 'path';
import { CliService } from '../CliService';

const posixTest = process.platform === 'win32' ? it.skip : it;

posixTest.each(['hang', 'stderr'])('bounds a real probe with %s behavior', async (mode) => {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'ocr-hung-probe-'));
  const executable = path.join(root, 'hung probe');
  const pidFile = path.join(root, 'pid');
  fs.writeFileSync(executable, '#!' + process.execPath + '\n' +
    'require("fs").writeFileSync(' + JSON.stringify(pidFile) + ', String(process.pid));\n' +
    (mode === 'hang' ? 'setInterval(() => {}, 1000);\n'
      : 'process.stderr.write("x".repeat(1024 * 1024)); process.stdout.write("ocr 1.0\\n");\n'),
  { mode: 0o755 });
  let cleanupRequired = true;
  let deadline: ReturnType<typeof setTimeout> | undefined;
  try {
    const service = new CliService(executable, 1000);
    const probe = (service as unknown as {
      probeCommand(bin: string): Promise<{ ok: boolean }>;
    }).probeCommand(executable);
    const result = await Promise.race([
      probe,
      new Promise<string>((resolve) => { deadline = setTimeout(() => resolve('deadline'), 2000); }),
    ]);
    expect(result).toEqual(mode === 'hang' ? { ok: false } : { ok: true, version: 'ocr 1.0' });
    const pid = Number(fs.readFileSync(pidFile, 'utf8'));
    let alive = true;
    for (let i = 0; i < 100 && alive; i++) {
      try { process.kill(pid, 0); } catch { alive = false; }
      if (alive) await new Promise((resolve) => setTimeout(resolve, 10));
    }
    expect(alive).toBe(false);
    cleanupRequired = false;
  } finally {
    if (deadline) clearTimeout(deadline);
    if (cleanupRequired && fs.existsSync(pidFile)) {
      try { process.kill(Number(fs.readFileSync(pidFile, 'utf8')), 'SIGKILL'); } catch { /* Already exited. */ }
    }
    fs.rmSync(root, { recursive: true, force: true });
  }
});

posixTest('terminates inherited probe pipes after their launcher exits', async () => {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'ocr-probe-child-'));
  const executable = path.join(root, 'probe launcher');
  const childPidFile = path.join(root, 'child-pid');
  const heartbeat = path.join(root, 'heartbeat');
  const childScript = 'require("fs").writeFileSync(' + JSON.stringify(childPidFile) + ', String(process.pid));' +
    'setInterval(() => require("fs").appendFileSync(' + JSON.stringify(heartbeat) + ', "."), 10);';
  fs.writeFileSync(executable, '#!' + process.execPath + '\n' +
    'require("child_process").spawn(process.execPath, ["-e", ' + JSON.stringify(childScript) + '], {stdio: "inherit"});' +
    'process.exit(0);\n', { mode: 0o755 });
  try {
    const service = new CliService(executable, 1000);
    const result = await (service as unknown as {
      probeCommand(bin: string): Promise<{ ok: boolean }>;
    }).probeCommand(executable);
    expect(result).toEqual({ ok: false });
    await new Promise((resolve) => setTimeout(resolve, 50));
    const stoppedAt = fs.statSync(heartbeat).size;
    expect(stoppedAt).toBeGreaterThan(0);
    await new Promise((resolve) => setTimeout(resolve, 100));
    expect(fs.statSync(heartbeat).size).toBe(stoppedAt);
  } finally {
    if (fs.existsSync(childPidFile)) {
      try { process.kill(Number(fs.readFileSync(childPidFile, 'utf8')), 'SIGKILL'); } catch { /* Already exited. */ }
    }
    fs.rmSync(root, { recursive: true, force: true });
  }
});
