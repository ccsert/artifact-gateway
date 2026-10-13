import assert from 'node:assert/strict';
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import test from 'node:test';

import {
  enumerateContractOperations,
  findContractDrift,
  loadContractFile,
  normalizedCommand,
  parseCommandMap,
} from './cli-contract-check.mjs';

const repoRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');

function classification(rows) {
  return parseCommandMap(
    ['| API / operationId | command | issue | phase | permission | acceptance |', ...rows].join('\n'),
  );
}

function mapRow(method, route, operationId, command, owner, phase, permission, acceptance) {
  return `| \`${method} ${route}\`<br>\`${operationId}\` | ${command} | ${owner} | ${phase} | ${permission} | ${acceptance} |`;
}

function widgetRow(operationId = 'listWidgets', command = '`widget list`') {
  return mapRow('GET', '/widgets', operationId, command, '#999', 'M2', 'A', 'Q');
}

test('enumerates path/method/operationId triples from a contract bundle', () => {
  const operations = enumerateContractOperations({
    openapi: '3.1.0',
    paths: {
      '/widgets': { get: { operationId: 'listWidgets' }, parameters: [] },
      '/widgets/{widgetId}': { put: { operationId: 'replaceWidget' }, head: {} },
    },
  });
  assert.deepEqual(operations, [
    { operationId: 'listWidgets', method: 'GET', path: '/widgets' },
    { operationId: 'replaceWidget', method: 'PUT', path: '/widgets/{widgetId}' },
    { operationId: '', method: 'HEAD', path: '/widgets/{widgetId}' },
  ]);
});

test('parses classified operations from the command map tables', () => {
  const classified = classification([widgetRow()]);
  assert.equal(classified.size, 1);
  assert.deepEqual(classified.get('listWidgets'), {
    operationId: 'listWidgets',
    method: 'GET',
    path: '/widgets',
    command: '`widget list`',
    owner: '#999',
    phase: 'M2',
    permission: 'A',
    acceptance: 'Q',
  });
});

test('reports an injected contract operation with the classification hint', () => {
  const managementOperations = enumerateContractOperations({
    paths: {
      '/widgets': { get: { operationId: 'listWidgets' } },
      '/gadgets': { post: { operationId: 'createGadget' } },
    },
  });
  const issues = findContractDrift({
    managementOperations,
    nativeOperations: [],
    englishMap: classification([widgetRow()]),
  });
  assert.equal(issues.length, 1);
  assert.equal(issues[0].kind, 'unclassified');
  assert.match(issues[0].message, /new operation needs explicit classification/);
  assert.match(issues[0].message, /createGadget/);
});

test('detects drift against an injected contract file', () => {
  const root = mkdtempSync(path.join(tmpdir(), 'artifact-gateway-cli-contract-'));
  try {
    const contractPath = path.join(root, 'management-runtime-v1.json');
    writeFileSync(
      contractPath,
      JSON.stringify({
        paths: {
          '/widgets': {
            get: { operationId: 'listWidgets' },
            delete: { operationId: 'deleteAllWidgets' },
          },
        },
      }),
    );
    const managementOperations = enumerateContractOperations(loadContractFile(contractPath));
    const issues = findContractDrift({
      managementOperations,
      nativeOperations: [],
      englishMap: classification([widgetRow()]),
    });
    assert.equal(issues.length, 1);
    assert.match(issues[0].message, /deleteAllWidgets/);
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});

test('treats an explicit N/A row as classified and rejects an unjustified one', () => {
  const managementOperations = enumerateContractOperations({
    paths: { '/grants': { put: { operationId: 'replaceGrants' } } },
  });
  const justified = classification([
    mapRow('PUT', '/grants', 'replaceGrants', 'not exposed as a whole-table PUT', '#134', 'N/A', 'RA', 'N'),
  ]);
  assert.deepEqual(
    findContractDrift({ managementOperations, nativeOperations: [], englishMap: justified }),
    [],
  );

  const unjustified = classification([
    mapRow('PUT', '/grants', 'replaceGrants', '', '#134', 'N/A', 'RA', ''),
  ]);
  const issues = findContractDrift({ managementOperations, nativeOperations: [], englishMap: unjustified });
  assert.equal(issues.length, 1);
  assert.equal(issues[0].kind, 'invalid-not-applicable-row');
  assert.match(issues[0].message, /N\/A without a stated reason/);
});

test('reports rows left behind by a rename or removal', () => {
  const managementOperations = enumerateContractOperations({
    paths: { '/widgets': { get: { operationId: 'listWidgets' } } },
  });
  const issues = findContractDrift({
    managementOperations,
    nativeOperations: [],
    englishMap: classification([widgetRow(), widgetRow('listGizmos', '`gizmo list`')]),
  });
  assert.equal(issues.length, 1);
  assert.equal(issues[0].kind, 'stale-classification');
  assert.match(issues[0].message, /listGizmos/);
});

test('reports a method or path change instead of silently accepting it', () => {
  const managementOperations = enumerateContractOperations({
    paths: { '/widgets/v2': { get: { operationId: 'listWidgets' } } },
  });
  const issues = findContractDrift({
    managementOperations,
    nativeOperations: [],
    englishMap: classification([widgetRow()]),
  });
  assert.equal(issues.length, 1);
  assert.equal(issues[0].kind, 'classification-mismatch');
  assert.match(issues[0].message, /\/widgets\/v2/);
});

test('requires the Chinese map to classify the same operations', () => {
  const managementOperations = enumerateContractOperations({
    paths: { '/widgets': { get: { operationId: 'listWidgets' } } },
  });
  const missing = findContractDrift({
    managementOperations,
    nativeOperations: [],
    englishMap: classification([widgetRow()]),
    chineseMap: classification([]),
  });
  assert.equal(missing.length, 1);
  assert.equal(missing[0].kind, 'translation-missing');

  const extra = findContractDrift({
    managementOperations,
    nativeOperations: [],
    englishMap: classification([widgetRow()]),
    chineseMap: classification([widgetRow(), widgetRow('listGizmos', '`gizmo list`')]),
  });
  assert.equal(extra.length, 1);
  assert.equal(extra[0].kind, 'translation-extra');
});

test('the repository command map classifies every contract operation', () => {
  const managementOperations = enumerateContractOperations(
    loadContractFile(path.join(repoRoot, 'api/openapi/management-runtime-v1.json')),
  );
  const nativeOperations = enumerateContractOperations(
    loadContractFile(path.join(repoRoot, 'api/openapi/native-hosted-v1.json')),
  );
  const englishMap = parseCommandMap(readFileSync(path.join(repoRoot, 'docs/cli-command-map.md'), 'utf8'));
  const chineseMap = parseCommandMap(
    readFileSync(path.join(repoRoot, 'docs/cli-command-map.zh-CN.md'), 'utf8'),
  );
  assert.deepEqual(findContractDrift({ managementOperations, nativeOperations, englishMap, chineseMap }), []);
});

test('fails an implemented row whose command is not registered', () => {
  const managementOperations = enumerateContractOperations({
    paths: { '/widgets': { get: { operationId: 'listWidgets' } } },
  });
  const englishMap = classification([
    mapRow('GET', '/widgets', 'listWidgets', '`widget list`', '#999', 'M1 implemented', 'A', 'Q'),
  ]);
  const issues = findContractDrift({
    managementOperations,
    nativeOperations: [],
    englishMap,
    registeredCommands: new Set(['info', 'version']),
  });
  const finding = issues.find((issue) => issue.kind === 'implemented-without-command');
  assert.ok(finding, `expected implemented-without-command in ${JSON.stringify(issues)}`);
  assert.match(finding.message, /listWidgets/);
  assert.match(finding.message, /widget list/);
  assert.match(finding.message, /not registered/);
});

test('accepts an implemented row whose command is registered', () => {
  const managementOperations = enumerateContractOperations({
    paths: { '/widgets': { get: { operationId: 'listWidgets' } } },
  });
  const englishMap = classification([
    mapRow('GET', '/widgets', 'listWidgets', '`agctl widget list`', '#999', 'M1 implemented', 'A', 'Q'),
  ]);
  assert.deepEqual(
    findContractDrift({
      managementOperations,
      nativeOperations: [],
      englishMap,
      registeredCommands: new Set(['widget list']),
    }),
    [],
  );
});

test('fails two implemented rows that claim the same registered command', () => {
  const managementOperations = enumerateContractOperations({
    paths: {
      '/widgets': { get: { operationId: 'listWidgets' } },
      '/widgets/{widgetId}': { get: { operationId: 'getWidget' } },
    },
  });
  const englishMap = classification([
    mapRow('GET', '/widgets', 'listWidgets', '`widget list`', '#999', 'M1 implemented', 'A', 'Q'),
    mapRow('GET', '/widgets/{widgetId}', 'getWidget', '`widget list <id>`', '#999', 'M1 implemented', 'A', 'Q'),
  ]);
  const issues = findContractDrift({
    managementOperations,
    nativeOperations: [],
    englishMap,
    registeredCommands: new Set(['widget list']),
  });
  const finding = issues.find((issue) => issue.kind === 'duplicate-implementation');
  assert.ok(finding, `expected duplicate-implementation in ${JSON.stringify(issues)}`);
  assert.match(finding.message, /listWidgets and getWidget/);
});

test('normalizes planned-command cells to registry names', () => {
  assert.equal(normalizedCommand('`config profile add NAME --server URL`'), 'config profile add');
  assert.equal(normalizedCommand('agctl auth whoami'), 'auth whoami');
  assert.equal(normalizedCommand('`publish upload <session>`'), 'publish upload');
  assert.equal(normalizedCommand(''), '');
});
