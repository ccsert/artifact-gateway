import { readFileSync } from 'node:fs';

// These public toolchains must install on GitHub-hosted runners without access
// to a developer's private registry. Run before npm ci, which follows locked URLs.
for (const file of ['console/package-lock.json', 'tools/openapi/package-lock.json']) {
  const lock = JSON.parse(readFileSync(file, 'utf8'));
  for (const [name, entry] of Object.entries(lock.packages)) {
    if (!entry.resolved) continue; // Root and bundled dependencies have no URL.
    const url = new URL(entry.resolved);
    if (url.origin !== 'https://registry.npmjs.org' || url.username || url.password) {
      throw new Error(`${file}: ${name} must resolve from the public npm registry`);
    }
  }
  process.stdout.write(`Public npm registry check passed for ${file}.\n`);
}
