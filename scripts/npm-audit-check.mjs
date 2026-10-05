import { spawnSync } from 'node:child_process';
import { acceptsAdvisory, auditFailures, parseAuditResult } from './npm-audit-policy.mjs';

function audit(project) {
  const result = spawnSync('npm', ['--prefix', project, 'audit', '--json'], { encoding: 'utf8', timeout: 120000 });
  const report = parseAuditResult(result);
  const printed = new Set();
  const failures = auditFailures(report, (advisory, name) => {
    const reason = acceptsAdvisory(advisory, name, new Date());
    if (reason && !printed.has(advisory.url)) { printed.add(advisory.url); process.stdout.write(`Accepted ${advisory.url}: ${reason}\n`); }
    return Boolean(reason);
  });
  if (failures.length) throw new Error(`npm audit failed for ${project}: ${failures.join(', ')}`);
  process.stdout.write(`npm audit gate passed for ${project}.\n`);
}

const projects = process.argv.slice(2);
if (projects.length === 0) throw new Error('provide at least one npm project directory');
for (const project of projects) audit(project);
