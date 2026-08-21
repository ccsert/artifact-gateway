#!/usr/bin/env node

import { execFileSync } from 'node:child_process';
import { existsSync, readFileSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

export const rootDocuments = [
  'README.md',
  'PRODUCT.md',
  'DESIGN.md',
  'CONTEXT.md',
  'ARCHITECTURE.md',
  'CONTRIBUTING.md',
  'SECURITY.md',
  'CHANGELOG.md',
];

export function chinesePathFor(englishPath) {
  return englishPath.replace(/\.md$/u, '.zh-CN.md');
}

export function discoverEnglishDocuments(markdownFiles) {
  return markdownFiles
    .filter(
      (file) =>
        !file.endsWith('.zh-CN.md') &&
        (file.startsWith('docs/') || rootDocuments.includes(file)),
    )
    .sort();
}

export function hasLocalTarget(document, target) {
  return (
    document.includes(`](${target})`) ||
    document.includes(`href="${target}"`) ||
    document.includes(`href='${target}'`)
  );
}

export function findPairIssues(repoRoot, englishDocuments, readFile = (file) => readFileSync(file, 'utf8')) {
  const issues = [];
  for (const englishPath of englishDocuments) {
    const chinesePath = chinesePathFor(englishPath);
    const absoluteEnglish = path.join(repoRoot, englishPath);
    const absoluteChinese = path.join(repoRoot, chinesePath);
    if (!existsSync(absoluteChinese)) {
      issues.push(`${englishPath}: missing ${chinesePath}`);
      continue;
    }

    const english = readFile(absoluteEnglish);
    const chinese = readFile(absoluteChinese);
    const chineseCharacters = chinese.match(/[\u3400-\u9fff]/gu) ?? [];
    if (chineseCharacters.length < 20) {
      issues.push(`${chinesePath}: Chinese body is missing or too short`);
    }

    const chineseTarget = path.basename(chinesePath);
    const englishTarget = path.basename(englishPath);
    if (!hasLocalTarget(english, chineseTarget)) {
      issues.push(`${englishPath}: missing language link to ${chineseTarget}`);
    }
    if (!hasLocalTarget(chinese, englishTarget)) {
      issues.push(`${chinesePath}: missing language link to ${englishTarget}`);
    }
  }
  return issues;
}

export function flattenSiteMap(siteMap) {
  return siteMap.sections.flatMap((section) => section.items);
}

export function findSiteMapIssues(siteMap, englishDocuments) {
  const issues = [];
  if (siteMap.version !== 1) issues.push('site-map.json: version must be 1');
  if (siteMap.defaultLocale !== 'en') issues.push('site-map.json: defaultLocale must be en');
  if (JSON.stringify(siteMap.locales) !== JSON.stringify(['en', 'zh-CN'])) {
    issues.push('site-map.json: locales must be [en, zh-CN]');
  }

  const items = flattenSiteMap(siteMap);
  const ids = new Set();
  const englishPaths = new Set();
  const chinesePaths = new Set();
  for (const item of items) {
    if (!item.id || ids.has(item.id)) issues.push(`site-map.json: duplicate or empty id ${item.id ?? ''}`);
    ids.add(item.id);
    if (!item.title?.en || !item.title?.['zh-CN']) {
      issues.push(`site-map.json: ${item.id} requires both localized titles`);
    }
    if (englishPaths.has(item.en)) issues.push(`site-map.json: duplicate English path ${item.en}`);
    if (chinesePaths.has(item['zh-CN'])) issues.push(`site-map.json: duplicate Chinese path ${item['zh-CN']}`);
    englishPaths.add(item.en);
    chinesePaths.add(item['zh-CN']);
    if (item['zh-CN'] !== chinesePathFor(item.en)) {
      issues.push(`site-map.json: ${item.id} has a non-conventional language pair`);
    }
  }

  for (const englishPath of englishDocuments) {
    if (!englishPaths.has(englishPath)) issues.push(`site-map.json: missing ${englishPath}`);
  }
  for (const englishPath of englishPaths) {
    if (!englishDocuments.includes(englishPath)) issues.push(`site-map.json: unknown ${englishPath}`);
  }
  return issues;
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
    .filter((file) => file && existsSync(path.join(repoRoot, file)));
  const englishDocuments = discoverEnglishDocuments(markdownFiles);
  const siteMap = JSON.parse(readFileSync(path.join(repoRoot, 'docs/site-map.json'), 'utf8'));
  const issues = [
    ...findPairIssues(repoRoot, englishDocuments),
    ...findSiteMapIssues(siteMap, englishDocuments),
  ];
  if (markdownFiles.some((file) => file.endsWith('.en.md'))) {
    issues.push('English documents must use the unsuffixed .md route');
  }
  if (issues.length > 0) {
    for (const issue of issues) process.stderr.write(`${issue}\n`);
    process.exitCode = 1;
    return;
  }
  process.stdout.write(
    `documentation i18n check passed (${englishDocuments.length} bilingual pairs, ${siteMap.sections.length} sections)\n`,
  );
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) main();
