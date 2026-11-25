import { execFileSync } from 'node:child_process';
import { join } from 'node:path';

export default function teardown() {
  execFileSync(join(__dirname, 'stop.sh'), { env: process.env, stdio: 'inherit' });
}
