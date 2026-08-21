#!/usr/bin/env node

import { execFileSync } from 'node:child_process';
import { existsSync, readFileSync, statSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const remoteOrRuntimeTarget = /^(?:[a-z][a-z0-9+.-]*:|\/)/i;

export function extractDocumentTargets(markdown) {
  const targets = [];
  const patterns = [
    /!?\[[^\]]*\]\(\s*(<[^>]+>|[^\s)]+)(?:\s+['"][^'"]*['"])?\s*\)/g,
    /<(?:a|img)\b[^>]*?\b(?:href|src)\s*=\s*["']([^"']+)["'][^>]*>/gi,
    /^\s*\[[^\]]+\]:\s*(<[^>]+>|\S+)/gm,
  ];
  for (const pattern of patterns) {
    for (const match of markdown.matchAll(pattern)) targets.push(match[1]);
  }
  return targets;
}

export function normalizeLocalTarget(rawTarget) {
  let target = rawTarget.trim();
  if (target.startsWith('<') && target.endsWith('>')) target = target.slice(1, -1);
  if (!target || target.startsWith('#') || remoteOrRuntimeTarget.test(target)) return null;
  target = target.split('#', 1)[0].split('?', 1)[0];
  if (!target) return null;
  try {
    return decodeURIComponent(target);
  } catch {
    return target;
  }
}

export function findBrokenDocumentLinks(repoRoot, markdownFiles, readFile) {
  const broken = [];
  for (const relativeFile of markdownFiles) {
    const source = readFile(path.join(repoRoot, relativeFile));
    for (const rawTarget of extractDocumentTargets(source)) {
      const target = normalizeLocalTarget(rawTarget);
      if (!target) continue;
      const resolved = path.resolve(repoRoot, path.dirname(relativeFile), target);
      if (!resolved.startsWith(`${path.resolve(repoRoot)}${path.sep}`) && resolved !== path.resolve(repoRoot)) {
        broken.push({ file: relativeFile, target: rawTarget, reason: 'outside repository' });
        continue;
      }
      if (!existsSync(resolved)) {
        broken.push({ file: relativeFile, target: rawTarget, reason: 'missing target' });
        continue;
      }
      if (statSync(resolved).isDirectory() && !existsSync(path.join(resolved, 'README.md'))) {
        broken.push({ file: relativeFile, target: rawTarget, reason: 'directory has no README.md' });
      }
    }
  }
  return broken;
}

function main() {
  const scriptPath = fileURLToPath(import.meta.url);
  const repoRoot = path.resolve(path.dirname(scriptPath), '..');
  const markdownFiles = execFileSync(
    'git',
    ['ls-files', '--cached', '--others', '--exclude-standard', '--', '*.md'],
    { cwd: repoRoot, encoding: 'utf8' },
  )
    .split('\n')
    .filter(
      (file) => file && !file.startsWith('.') && existsSync(path.join(repoRoot, file)),
    );
  const broken = findBrokenDocumentLinks(repoRoot, markdownFiles, (file) => readFileSync(file, 'utf8'));
  if (broken.length > 0) {
    for (const item of broken) {
      process.stderr.write(`${item.file}: ${item.target} (${item.reason})\n`);
    }
    process.exitCode = 1;
    return;
  }
  process.stdout.write(`documentation link check passed (${markdownFiles.length} files)\n`);
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) main();
