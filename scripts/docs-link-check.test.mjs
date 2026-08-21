import assert from 'node:assert/strict';
import {
  mkdirSync,
  mkdtempSync,
  readFileSync,
  rmSync,
  writeFileSync,
} from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import test from 'node:test';

import {
  extractDocumentTargets,
  findBrokenDocumentLinks,
  findUnmarkedEnglishOnlyLinks,
  normalizeLocalTarget,
} from './docs-link-check.mjs';

test('extracts Markdown, HTML, image, and reference targets', () => {
  const document = '[guide](docs/guide.md) ![hero](assets/hero.png)\n<a href="README.md">read</a>\n[id]: other.md';
  assert.deepEqual(extractDocumentTargets(document), [
    'docs/guide.md',
    'assets/hero.png',
    'README.md',
    'other.md',
  ]);
});

test('normalizes local targets and skips remote or runtime routes', () => {
  assert.equal(normalizeLocalTarget('guide.md#setup'), 'guide.md');
  assert.equal(normalizeLocalTarget('<folder%20name/file.md>'), 'folder name/file.md');
  assert.equal(normalizeLocalTarget('https://example.test/guide'), null);
  assert.equal(normalizeLocalTarget('/api/v2/repositories'), null);
  assert.equal(normalizeLocalTarget('#setup'), null);
});

test('reports missing and escaping links while accepting valid files', () => {
  const root = mkdtempSync(path.join(tmpdir(), 'artifact-gateway-docs-'));
  try {
    mkdirSync(path.join(root, 'docs'));
    writeFileSync(path.join(root, 'README.md'), '[ok](docs/guide.md) [missing](docs/missing.md) [escape](../outside.md)');
    writeFileSync(path.join(root, 'docs/guide.md'), '# Guide\n');
    const files = ['README.md', 'docs/guide.md'];
    const broken = findBrokenDocumentLinks(root, files, (file) =>
      file.endsWith('README.md')
        ? '[ok](docs/guide.md) [missing](docs/missing.md) [escape](../outside.md)'
        : '# Guide\n',
    );
    assert.deepEqual(broken, [
      { file: 'README.md', target: 'docs/missing.md', reason: 'missing target' },
      { file: 'README.md', target: '../outside.md', reason: 'outside repository' },
    ]);
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});

test('requires English-only entries in the Chinese index to be marked', () => {
  const root = mkdtempSync(path.join(tmpdir(), 'artifact-gateway-docs-language-'));
  try {
    mkdirSync(path.join(root, 'docs'));
    writeFileSync(
      path.join(root, 'docs/chinese.md'),
      '# 中文正文\n这是一份具有足够中文内容的文档，不会被一个导航标签冒充。\n',
    );
    writeFileSync(
      path.join(root, 'docs/english.md'),
      '# English body\n[简体中文](english.zh-CN.md)\n',
    );
    const index = [
      '- [中文](chinese.md)',
      '- [已披露](english.md)（仅英文）',
      '- [遗漏](english.md)',
    ].join('\n');
    writeFileSync(path.join(root, 'docs/README.zh-CN.md'), index);

    assert.deepEqual(
      findUnmarkedEnglishOnlyLinks(
        root,
        'docs/README.zh-CN.md',
        (file) => readFileSync(file, 'utf8'),
      ),
      [{ file: 'docs/README.zh-CN.md', target: 'english.md' }],
    );
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});
