import { spawnSync } from 'node:child_process';

const exceptions = new Map([
  [
    'https://github.com/advisories/GHSA-qwww-vcr4-c8h2',
    {
      package: 'react-router',
      expiresAt: '2026-11-01',
      reason: 'The Console is a client-only Vite SPA and does not enable React Router RSC actions.',
    },
  ],
]);

function activeException(advisory, packageName) {
  const exception = exceptions.get(advisory.url);
  if (!exception || exception.package !== packageName) return false;
  if (new Date(`${exception.expiresAt}T00:00:00Z`) <= new Date()) {
    throw new Error(`npm audit exception expired for ${advisory.url}`);
  }
  process.stdout.write(`Accepted ${advisory.url} for ${packageName} until ${exception.expiresAt}: ${exception.reason}\n`);
  return true;
}

function audit(project) {
  const result = spawnSync('npm', ['--prefix', project, 'audit', '--json'], { encoding: 'utf8' });
  if (result.error) throw result.error;
  let report;
  try {
    report = JSON.parse(result.stdout);
  } catch {
    throw new Error(`npm audit returned invalid JSON for ${project}: ${result.stderr}`);
  }

  const acceptedPackages = new Set();
  const failures = [];
  for (const [packageName, vulnerability] of Object.entries(report.vulnerabilities ?? {})) {
    const advisories = vulnerability.via.filter((item) => typeof item === 'object');
    if (advisories.length > 0 && advisories.every((item) => activeException(item, packageName))) {
      acceptedPackages.add(packageName);
      continue;
    }
    if (advisories.length > 0) failures.push(`${packageName}: ${advisories.map((item) => item.url).join(', ')}`);
  }

  for (const [packageName, vulnerability] of Object.entries(report.vulnerabilities ?? {})) {
    const parents = vulnerability.via.filter((item) => typeof item === 'string');
    if (parents.length > 0 && !parents.every((item) => acceptedPackages.has(item))) {
      failures.push(`${packageName}: affected through ${parents.join(', ')}`);
    }
  }

  if (failures.length > 0) throw new Error(`npm audit failed for ${project}:\n${failures.join('\n')}`);
  process.stdout.write(`npm audit gate passed for ${project}.\n`);
}

const projects = process.argv.slice(2);
if (projects.length === 0) throw new Error('provide at least one npm project directory');
for (const project of projects) audit(project);
