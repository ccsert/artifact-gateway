import assert from 'node:assert/strict';
import test from 'node:test';
import { acceptsAdvisory, auditFailures, parseAuditResult } from './npm-audit-policy.mjs';

const report = nodes => ({ vulnerabilities: nodes, metadata: { vulnerabilities: { info: 0, low: 0, moderate: 0, high: Object.keys(nodes ?? {}).length, critical: 0, total: Object.keys(nodes ?? {}).length } } });
const advisory = { url: 'https://github.com/advisories/GHSA-vfj7-8cjw-p6xm' };
const accepted = (item, name) => Boolean(acceptsAdvisory(item, name, new Date('2026-10-03')));

test('reintroduced braces chains fail without a date or project exception', () => {
  const nodes = { braces: { via: [advisory] }, micromatch: { via: ['braces'] }, 'fast-glob': { via: ['micromatch'] }, '@ant-design/cli': { via: ['fast-glob'] } };
  assert.deepEqual(auditFailures(report(nodes), accepted).sort(), Object.keys(nodes).sort());
  for (const date of ['2026-10-03', '2026-10-17', '2026-11-02']) {
    assert.equal(acceptsAdvisory(advisory, 'braces', new Date(date)), false);
  }
  nodes.braces.via.push({ url: 'https://example.test/new-advisory' });
  assert.equal(auditFailures(report(nodes), accepted).length, 4);
});

test('preserves the separate React Router decision and expiry', () => {
  const router = { url: 'https://github.com/advisories/GHSA-qwww-vcr4-c8h2' };
  const date = new Date('2026-10-31T23:59:59Z');
  assert.ok(acceptsAdvisory(router, 'react-router', date));
  assert.equal(acceptsAdvisory(router, 'braces', date), false);
  assert.equal(acceptsAdvisory({ url: 'https://example.test/new-advisory' }, 'react-router', date), false);
  assert.throws(() => acceptsAdvisory(router, 'react-router', new Date('2026-11-01T00:00:00Z')), /expired/);
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
