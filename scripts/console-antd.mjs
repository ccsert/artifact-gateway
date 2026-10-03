import { spawnSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import { join } from 'node:path';
import { consoleGuard } from './npm-audit-policy.mjs';

const root = fileURLToPath(new URL('../', import.meta.url));
const [command, ...extra] = process.argv.slice(2);
if (!['lint', 'usage', 'doctor'].includes(command) || extra.length) throw new Error('Only fixed-scope Ant Design lint, usage, or doctor is supported');
consoleGuard(join(root, 'console'), root);
const args = [join(root, 'console/node_modules/@ant-design/cli/dist/index.js'), command];
if (command !== 'doctor') args.push('src');
args.push('--format', 'json');
const result = spawnSync(process.execPath, args, { cwd: join(root, 'console'), stdio: 'inherit', shell: false });
if (result.error || result.signal) throw result.error ?? new Error('Ant Design CLI terminated unexpectedly');
process.exitCode = result.status ?? 1;
