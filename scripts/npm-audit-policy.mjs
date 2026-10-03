import { createHash } from 'node:crypto';
import { readFileSync, readdirSync, realpathSync } from 'node:fs';
import { join, resolve, posix } from 'node:path';
import { createRequire, isBuiltin } from 'node:module';
import { parseSync, Visitor } from '../console/node_modules/oxc-parser/src-js/index.js';

export const bracesAdvisory = 'https://github.com/advisories/GHSA-vfj7-8cjw-p6xm';
export const cliHash = 'e7c351b9ae24ac959475d48fc2a2c8c2a5a7b00da7407f31df895f9f1716add0';
const chain = new Map([['@ant-design/cli', '6.5.4'], ['fast-glob', '3.3.3'], ['micromatch', '4.0.8'], ['braces', '3.0.3']]);

export function verifyConsoleGlobScope(root, lock, installedCLI) {
  if (createHash('sha256').update(installedCLI).digest('hex') !== cliHash) throw new Error('Ant Design CLI bytes changed; re-evaluate the braces advisory');
  const packages = lock.packages;
  for (const [name, version] of chain) {
    const node = packages?.[`node_modules/${name}`];
    if (node?.version !== version || node.dev !== true) throw new Error(`braces scope changed: ${name}`);
    const expectedParent = name === '@ant-design/cli' ? '' : [...chain.keys()][[...chain.keys()].indexOf(name) - 1];
    const consumers = Object.entries(packages).filter(([, value]) => value.dependencies?.[name] || value.optionalDependencies?.[name] || value.devDependencies?.[name] || value.peerDependencies?.[name]).map(([path]) => path);
    const parentPath = expectedParent === '' ? '' : `node_modules/${expectedParent}`;
    if (consumers.length !== 1 || consumers[0] !== parentPath) throw new Error(`braces consumer changed: ${name}`);
    if (Object.keys(packages).some(path => path.endsWith(`/node_modules/${name}`))) throw new Error(`additional braces chain installation: ${name}`);
  }
  function inspect(path) {
    for (const item of readdirSync(path, { withFileTypes: true })) {
      const child = join(path, item.name);
      if (item.isDirectory()) inspect(child);
      else if (/\.[cm]?[jt]sx?$/.test(item.name)) verifyConsoleSource(child, readFileSync(child, 'utf8'));
    }
  }
  inspect(join(root, 'console/src'));
  for (const file of readdirSync(join(root, 'console'))) {
    if (/\.[cm]?[jt]s$/.test(file)) verifyConsoleSource(join(root, 'console', file), readFileSync(join(root, 'console', file), 'utf8'));
  }
}

export function verifyConsoleSource(filename, source) {
  const parsed = parseSync(filename, source);
  if (parsed.errors.length) throw new Error(`Cannot verify Console source: ${filename}`);
  const sourceRequire = createRequire(resolve(filename));
  function check(value, module = false) {
    if (typeof value !== 'string') return;
    const paths = [posix.normalize(value.replaceAll('\\', '/'))];
    if (module || value.includes('node_modules')) {
      let resolved;
      try { resolved = sourceRequire.resolve(value); } catch { /* TS/local non-Node module paths are checked lexically. */ }
      if (resolved && !isBuiltin(resolved)) { try { paths.push(realpathSync(resolved).replaceAll('\\', '/')); } catch { throw new Error(`Cannot resolve Console module identity: ${filename}`); } }
    }
    for (const path of paths) {
      for (const name of chain.keys()) {
        if (path === name || path.startsWith(`${name}/`) || path.includes(`/node_modules/${name}/`) || path.endsWith(`/node_modules/${name}`)) throw new Error(`braces chain imported by product or build: ${name}`);
      }
    }
  }
  function moduleSource(node) {
    if (node.type === 'Literal') { check(node.value, true); return; }
    if (node.type === 'TemplateLiteral' && node.expressions.length === 0) { check(node.quasis[0].value.cooked, true); return; }
    throw new Error(`Computed module source requires braces scope review: ${filename}`);
  }
  new Visitor({
    ImportDeclaration(node) { moduleSource(node.source); },
    ExportNamedDeclaration(node) { if (node.source) moduleSource(node.source); },
    ExportAllDeclaration(node) { moduleSource(node.source); },
    ImportExpression(node) { moduleSource(node.source); },
    CallExpression(node) { if (node.callee.type === 'Identifier' && node.callee.name === 'require') { if (node.arguments.length !== 1) throw new Error('Unsupported require source'); moduleSource(node.arguments[0]); } },
    Literal(node) { check(node.value); },
  }).visit(parsed.program);
}

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

export function acceptsAdvisory(project, advisory, name, now, guard) {
  if (advisory.url === 'https://github.com/advisories/GHSA-qwww-vcr4-c8h2' && name === 'react-router') {
    if (now >= new Date('2026-11-01T00:00:00Z')) throw new Error('react-router audit exception expired');
    return 'Client-only Vite SPA does not enable React Router RSC actions; expires 2026-11-01.';
  }
  if (advisory.url !== bracesAdvisory || name !== 'braces' || project !== 'console') return false;
  if (now >= new Date('2026-10-17T00:00:00Z')) throw new Error('braces audit exception expired; upstream remains unpatched');
  guard();
  return 'UNPATCHED dev-only braces: verified CLI 6.5.4 uses only fixed glob patterns; expires 2026-10-17.';
}

export function consoleGuard(project, repositoryRoot) {
  if (resolve(project) !== resolve(repositoryRoot, 'console')) throw new Error('braces exception is restricted to this Console');
  verifyConsoleGlobScope(repositoryRoot, JSON.parse(readFileSync(join(repositoryRoot, 'console/package-lock.json'), 'utf8')), readFileSync(join(repositoryRoot, 'console/node_modules/@ant-design/cli/dist/index.js')));
  const manifest = JSON.parse(readFileSync(join(repositoryRoot, 'console/package.json'), 'utf8'));
  for (const name of chain.keys()) if (manifest.dependencies?.[name] || manifest.optionalDependencies?.[name] || manifest.peerDependencies?.[name]) throw new Error('braces chain moved into production dependencies');
  for (const command of ['lint', 'usage', 'doctor']) if (manifest.scripts?.[`antd:${command}`] !== `node ../scripts/console-antd.mjs ${command}`) throw new Error('Ant Design helper no longer uses fixed scope');
  if (!readFileSync(join(repositoryRoot, 'Makefile'), 'utf8').includes('console-antd-lint:\n\t@npm --prefix console run antd:lint\n')) throw new Error('Ant Design make target no longer uses fixed scope');
}
