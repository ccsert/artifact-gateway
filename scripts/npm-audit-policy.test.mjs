import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { spawnSync } from 'node:child_process';
import test from 'node:test';
import { acceptsAdvisory, auditFailures, bracesAdvisory, consoleGuard, parseAuditResult, verifyConsoleGlobScope, verifyConsoleSource } from './npm-audit-policy.mjs';

const root = new URL('../', import.meta.url).pathname;
const report = nodes => ({ vulnerabilities: nodes, metadata: { vulnerabilities: { info: 0, low: 0, moderate: 0, high: Object.keys(nodes ?? {}).length, critical: 0, total: Object.keys(nodes ?? {}).length } } });
const advisory = { url: bracesAdvisory };
const accepted = (item, name) => Boolean(acceptsAdvisory('console', item, name, new Date('2026-10-03'), () => consoleGuard('console', root)));

test('accepts only the verified unpatched dev chain before expiry', () => {
  const nodes = { braces: { via: [advisory] }, micromatch: { via: ['braces'] }, 'fast-glob': { via: ['micromatch'] }, '@ant-design/cli': { via: ['fast-glob'] } };
  assert.deepEqual(auditFailures(report(nodes), accepted), []);
  assert.equal(acceptsAdvisory('tools/openapi', advisory, 'braces', new Date('2026-10-03'), () => {}), false);
  assert.throws(() => acceptsAdvisory('console', advisory, 'braces', new Date('2026-10-17'), () => {}), /expired/);
  nodes.braces.via.push({ url: 'https://example.test/new-advisory' });
  assert.equal(auditFailures(report(nodes), accepted).length, 4);
});

test('execution errors, signals, invalid JSON and inconsistent npm status fail closed', () => {
  const clean = JSON.stringify(report({}));
  assert.deepEqual(parseAuditResult({status:0,stdout:clean}), report({}));
  for (const result of [{status:1,stdout:clean}, {status:0,stdout:'{bad'}, {status:0,stdout:JSON.stringify({error:'network'})}, {status:2,stdout:clean}, {status:0,stdout:clean,signal:'SIGTERM'}, {status:0,stdout:clean,error:new Error('timeout')}]) assert.throws(() => parseAuditResult(result));
});

test('invalid reports, unknown leaves, and cyclic chains fail closed', () => {
  for (const invalid of [{ error: 'registry failed' }, {}, report(null), report([])]) assert.throws(() => auditFailures(invalid, () => true), /invalid/);
  assert.deepEqual(auditFailures(report({ a: { via: ['unknown'] } }), () => true), ['a']);
  assert.deepEqual(auditFailures(report({ a: { via: ['b'] }, b: { via: ['a'] } }), () => true).sort(), ['a', 'b']);
});

test('CLI bytes, lock versions, production use, or additional consumers invalidate scope', () => {
  const lock = JSON.parse(readFileSync(new URL('../console/package-lock.json', import.meta.url)));
  const cli = readFileSync(new URL('../console/node_modules/@ant-design/cli/dist/index.js', import.meta.url));
  verifyConsoleGlobScope(root, lock, cli);
  assert.throws(() => verifyConsoleGlobScope(root, lock, Buffer.concat([cli, Buffer.from('changed')])), /bytes changed/);
  for (const change of [value => value.packages['node_modules/braces'].dev = false, value => value.packages['node_modules/braces'].version = '3.0.4', value => value.packages['node_modules/new-consumer'] = { dependencies: { braces: '^3.0.3' } }, value => value.packages['node_modules/new-peer'] = { peerDependencies: { braces: '^3.0.3' } }]) {
    const altered = structuredClone(lock); change(altered);
    assert.throws(() => verifyConsoleGlobScope(root, altered, cli), /scope changed|consumer changed/);
  }
});

test('parsed module sources reject relative, escaped, computed and direct product imports', () => {
  for (const source of ["import x from 'braces'", "import x from '../node_modules/braces/index.js'", "import x from '../node_modules/./braces/index.js'", "import x from '../node_modules/foo/../braces/index.js'", "import x from 'br\\u0061ces'", "export * from 'fast-glob'", "import('micromatch')", "require('@ant-design/cli')", "import(`braces`)", "import('brace' + 's')"]) assert.throws(() => verifyConsoleSource('guard.ts',source), /braces|Computed/);
  verifyConsoleSource('guard.ts', "import x from 'react'; import fs from 'node:fs'; import path from 'path'; export * from './client'");
});

test('CLI wrapper rejects arbitrary scope and arguments without running CLI', () => {
  for (const args of [['lint', '../'], ['lint', 'src'], ['unknown'], ['doctor', '--help']]) {
    const result = spawnSync(process.execPath, ['scripts/console-antd.mjs', ...args], { cwd: root, encoding: 'utf8' });
    assert.notEqual(result.status, 0);
    assert.match(result.stderr, /fixed-scope/);
  }
});
