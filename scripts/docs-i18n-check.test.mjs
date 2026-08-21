import assert from 'node:assert/strict';
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import test from 'node:test';

import {
  discoverEnglishDocuments,
  findPairIssues,
  findSiteMapIssues,
  hasLocalTarget,
} from './docs-i18n-check.mjs';

test('discovers site documents and excludes localized companions', () => {
  assert.deepEqual(
    discoverEnglishDocuments([
      'README.md',
      'README.zh-CN.md',
      'docs/guide.md',
      'docs/guide.zh-CN.md',
      'internal/note.md',
    ]),
    ['README.md', 'docs/guide.md'],
  );
});

test('accepts Markdown and HTML language links', () => {
  assert.equal(hasLocalTarget('[中文](README.zh-CN.md)', 'README.zh-CN.md'), true);
  assert.equal(hasLocalTarget('<a href="README.md">English</a>', 'README.md'), true);
  assert.equal(hasLocalTarget('README.md', 'README.md'), false);
});

test('reports missing bodies and reciprocal language links', () => {
  const root = mkdtempSync(path.join(tmpdir(), 'artifact-gateway-i18n-'));
  try {
    mkdirSync(path.join(root, 'docs'));
    writeFileSync(path.join(root, 'docs/guide.md'), '# Guide\n');
    writeFileSync(
      path.join(root, 'docs/guide.zh-CN.md'),
      '# 指南\n中文正文不应只靠标题冒充完整翻译，检查器需要确认页面确实包含足够的中文说明内容。\n',
    );
    assert.deepEqual(findPairIssues(root, ['docs/guide.md']), [
      'docs/guide.md: missing language link to guide.zh-CN.md',
      'docs/guide.zh-CN.md: missing language link to guide.md',
    ]);
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});

test('requires one conventional navigation entry per document pair', () => {
  const siteMap = {
    version: 1,
    defaultLocale: 'en',
    locales: ['en', 'zh-CN'],
    sections: [
      {
        id: 'start',
        title: { en: 'Start', 'zh-CN': '开始' },
        items: [
          {
            id: 'guide',
            title: { en: 'Guide', 'zh-CN': '指南' },
            en: 'docs/guide.md',
            'zh-CN': 'docs/guide.zh-CN.md',
          },
        ],
      },
    ],
  };
  assert.deepEqual(findSiteMapIssues(siteMap, ['docs/guide.md']), []);
  assert.deepEqual(findSiteMapIssues(siteMap, ['docs/guide.md', 'docs/extra.md']), [
    'site-map.json: missing docs/extra.md',
  ]);
});
