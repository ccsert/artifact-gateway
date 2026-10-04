export function auditFailures(report, accepts) {
  if (!report || report.error || !report.vulnerabilities || typeof report.vulnerabilities !== 'object' || Array.isArray(report.vulnerabilities) || !report.metadata?.vulnerabilities) throw new Error('npm audit returned an invalid or failed report');
  const nodes = report.vulnerabilities;
  const counts = report.metadata.vulnerabilities;
  const severities = ['info', 'low', 'moderate', 'high', 'critical'];
  if ([...severities, 'total'].some(key => !Number.isSafeInteger(counts[key]) || counts[key] < 0) || severities.reduce((sum, key) => sum + counts[key], 0) !== counts.total || counts.total !== Object.keys(nodes).length) throw new Error('npm audit returned inconsistent vulnerability counts');
  const accepted = new Map();
  function visit(name, active = new Set()) {
    if (accepted.has(name)) return accepted.get(name);
    const node = nodes[name];
    if (!node || active.has(name) || !Array.isArray(node.via) || node.via.length === 0) return false;
    const next = new Set(active).add(name);
    const ok = node.via.every(item => typeof item === 'string' ? visit(item, next) : item && typeof item.url === 'string' && accepts(item, name));
    accepted.set(name, ok);
    return ok;
  }
  return Object.keys(nodes).filter(name => !visit(name));
}

export function parseAuditResult(result) {
  if (result.error || result.signal || ![0, 1].includes(result.status)) throw result.error ?? new Error('npm audit failed to execute');
  let report;
  try { report = JSON.parse(result.stdout); }
  catch { throw new Error('npm audit returned invalid JSON'); }
  auditFailures(report, () => false);
  if ((result.status === 0) !== (report.metadata.vulnerabilities.total === 0)) throw new Error('npm audit exit status does not match report');
  return report;
}

export function acceptsAdvisory(advisory, name, now) {
  if (advisory.url === 'https://github.com/advisories/GHSA-qwww-vcr4-c8h2' && name === 'react-router') {
    if (now >= new Date('2026-11-01T00:00:00Z')) throw new Error('react-router audit exception expired');
    return 'Client-only Vite SPA does not enable React Router RSC actions; expires 2026-11-01.';
  }
  return false;
}
