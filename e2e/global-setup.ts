import { execFileSync } from 'node:child_process';
import { mkdtempSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

export default function setup() {
  const dir = process.env.E2E_DIR ?? mkdtempSync(join(tmpdir(), 'remote-agent-e2e-'));
  process.env.E2E_DIR = dir;
  execFileSync(join(__dirname, 'start.sh'), { env: process.env, stdio: 'inherit' });
}
