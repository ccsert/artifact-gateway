#!/usr/bin/env node

import { execFileSync } from 'node:child_process';
import { readFileSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const contractBundlePaths = {
  management: 'api/openapi/management-runtime-v1.json',
  native: 'api/openapi/native-hosted-v1.json',
};
const commandMapPath = 'docs/cli-command-map.md';
const chineseCommandMapPath = 'docs/cli-command-map.zh-CN.md';

const httpMethods = new Set(['get', 'put', 'post', 'delete', 'patch', 'head', 'options', 'trace']);

// A classified row is a Markdown table line whose first cell is
// `METHOD /path`<br>`operationId`; the remaining cells carry the classification
// columns (planned command, owning issue, phase, permission, acceptance).
const classifiedRowPattern = /^\|\s*`([A-Z]+) ([^`]+)`<br>`([A-Za-z0-9_-]+)`\s*\|(.*)\|\s*$/u;
const justifiedNotApplicablePattern = /^N(?![A-Za-z0-9_])/u;

export function enumerateContractOperations(contract) {
  const operations = [];
  const paths =
    contract && typeof contract === 'object' && contract.paths && typeof contract.paths === 'object'
      ? contract.paths
      : {};
  for (const [route, pathItem] of Object.entries(paths)) {
    if (pathItem === null || typeof pathItem !== 'object') continue;
    for (const [method, operation] of Object.entries(pathItem)) {
      if (!httpMethods.has(method.toLowerCase())) continue;
      operations.push({
        operationId:
          operation && typeof operation === 'object' && typeof operation.operationId === 'string'
            ? operation.operationId
            : '',
        method: method.toUpperCase(),
        path: route,
      });
    }
  }
  return operations;
}

export function loadContractFile(filePath) {
  return JSON.parse(readFileSync(filePath, 'utf8'));
}

export function parseCommandMap(markdown) {
  const classified = new Map();
  for (const line of markdown.split('\n')) {
    const match = line.match(classifiedRowPattern);
    if (!match) continue;
    const [command = '', owner = '', phase = '', permission = '', acceptance = ''] = match[4]
      .split('|')
      .map((cell) => cell.trim());
    const operationId = match[3];
    classified.set(operationId, {
      operationId,
      method: match[1],
      path: match[2],
      command,
      owner,
      phase,
      permission,
      acceptance,
    });
  }
  return classified;
}

function contractIndex(operations) {
  const index = new Map();
  for (const operation of operations) {
    if (!operation.operationId) continue;
    const variants = index.get(operation.operationId) ?? new Set();
    variants.add(`${operation.method} ${operation.path}`);
    index.set(operation.operationId, variants);
  }
  return index;
}

// normalizedCommand extracts the registry name from a planned-command cell:
// backticks and an `agctl ` prefix are stripped, and argument placeholders or
// flags end the name. `config profile add NAME --server URL` normalizes to
// `config profile add`.
export function normalizedCommand(cell) {
  const cleaned = String(cell ?? '')
    .replace(/`/gu, '')
    .trim()
    .replace(/^agctl\s+/iu, '');
  if (!cleaned) return '';
  const leading = [];
  for (const part of cleaned.split(/\s+/u)) {
    // Registry names are lowercase words; placeholders (NAME, <id>), flags,
    // and prose end the name.
    if (!/^[a-z][a-z0-9-]*$/u.test(part)) break;
    leading.push(part);
  }
  return leading.join(' ').toLowerCase();
}

// A row claims implementation by carrying `implemented` in its Phase cell
// (for example `M1 implemented`). The claim is only legal when the installed
// command registry actually exposes the command. The registry is either a
// set of command names, or a map from each name to the endpoints its
// `agctl commands --json` metadata declares. With endpoints, a claimed
// management row must be one of the command's declared endpoints, and one
// command may serve several rows (such as `artifact search --global` and
// `--repo`) because each claim is checked against what it really sends.
// Without endpoints two rows may not claim one command.
export function managementEndpoint(row) {
  return `${row.method} /api/v2${row.path}`;
}

// declaredEndpoint is how a command names a row's operation: management
// operations live under /api/v2, native protocol operations at their own
// paths.
function declaredEndpoint(row, nativeOperationIds) {
  return nativeOperationIds.has(row.operationId) ? `${row.method} ${row.path}` : managementEndpoint(row);
}

function findImplementationIssues(englishMap, registeredCommands, nativeOperationIds = new Set()) {
  const issues = [];
  const byCommand = new Map();
  const endpointsKnown = registeredCommands instanceof Map;
  for (const [operationId, row] of englishMap) {
    if (!/implemented/iu.test(row.phase)) continue;
    const command = normalizedCommand(row.command);
    if (!command || !registeredCommands.has(command)) {
      issues.push({
        kind: 'implemented-without-command',
        message: `${commandMapPath} marks ${operationId} implemented, but ${command ? `command "${command}" is` : 'its planned command is'} not registered in cmd/agctl; register it or drop the implemented claim`,
      });
      continue;
    }
    if (endpointsKnown) {
      const endpoint = declaredEndpoint(row, nativeOperationIds);
      if (!registeredCommands.get(command).has(endpoint)) {
        issues.push({
          kind: 'implemented-endpoint-undeclared',
          message: `${commandMapPath} marks ${operationId} implemented by "${command}", but that command does not declare ${endpoint} in its endpoints; declare it or drop the implemented claim`,
        });
      }
      continue;
    }
    const claimants = byCommand.get(command) ?? [];
    claimants.push(operationId);
    byCommand.set(command, claimants);
  }
  for (const [command, claimants] of byCommand) {
    if (claimants.length > 1) {
      issues.push({
        kind: 'duplicate-implementation',
        message: `command "${command}" is claimed implemented by ${claimants.join(' and ')}; one registration serves exactly one implementation claim`,
      });
    }
  }
  return issues;
}

export function findContractDrift({ managementOperations, nativeOperations, englishMap, chineseMap = null, registeredCommands = new Set() }) {
  const issues = [];
  const bundles = [
    { source: contractBundlePaths.management, operations: managementOperations },
    { source: contractBundlePaths.native, operations: nativeOperations },
  ];

  const required = new Map();
  for (const bundle of bundles) {
    for (const operation of bundle.operations) {
      if (!operation.operationId) {
        issues.push({
          kind: 'contract-without-operation-id',
          message: `${bundle.source} operation ${operation.method} ${operation.path} has no operationId; it cannot be classified until one is declared`,
        });
        continue;
      }
      const entry = required.get(operation.operationId) ?? { method: operation.method, path: operation.path, sources: new Set() };
      entry.sources.add(bundle.source);
      required.set(operation.operationId, entry);
    }
  }

  for (const [operationId, entry] of required) {
    if (englishMap.has(operationId)) continue;
    const sources = [...entry.sources].join(' and ');
    issues.push({
      kind: 'unclassified',
      message: `new operation needs explicit classification in ${commandMapPath}: ${entry.method} ${entry.path} (${operationId}) is declared by ${sources}`,
    });
  }

  const declared = contractIndex([...managementOperations, ...nativeOperations]);
  for (const [operationId, row] of englishMap) {
    const variants = declared.get(operationId);
    if (!variants) {
      issues.push({
        kind: 'stale-classification',
        message: `${commandMapPath} classifies ${operationId} as ${row.method} ${row.path}, but no contract operation declares it; a rename or removal left this row behind`,
      });
      continue;
    }
    if (!variants.has(`${row.method} ${row.path}`)) {
      issues.push({
        kind: 'classification-mismatch',
        message: `${commandMapPath} maps ${operationId} to ${row.method} ${row.path}, but the contract declares ${[...variants].join(', ')}; update the row`,
      });
      continue;
    }
    if (/^n\/a$/iu.test(row.phase) && (!row.command || !justifiedNotApplicablePattern.test(row.acceptance))) {
      issues.push({
        kind: 'invalid-not-applicable-row',
        message: `${commandMapPath} marks ${operationId} N/A without a stated reason and acceptance code N; the not-applicable exception must be an explicit row, not an ignore list`,
      });
    }
  }

  if (chineseMap) {
    for (const [operationId, row] of englishMap) {
      const translated = chineseMap.get(operationId);
      if (!translated) {
        issues.push({
          kind: 'translation-missing',
          message: `${chineseCommandMapPath} does not classify ${operationId}; both language maps must carry the same operations`,
        });
        continue;
      }
      if (translated.method !== row.method || translated.path !== row.path) {
        issues.push({
          kind: 'translation-mismatch',
          message: `${chineseCommandMapPath} classifies ${operationId} as ${translated.method} ${translated.path}, but ${commandMapPath} says ${row.method} ${row.path}; keep the pair aligned`,
        });
      }
    }
    for (const operationId of chineseMap.keys()) {
      if (!englishMap.has(operationId)) {
        issues.push({
          kind: 'translation-extra',
          message: `${chineseCommandMapPath} classifies ${operationId}, but ${commandMapPath} does not; keep the pair aligned`,
        });
      }
    }
  }

  // The native bundle repeats the management operations it shares; only the
  // ones the management bundle lacks are protocol paths outside /api/v2.
  const managementIds = new Set(managementOperations.map((operation) => operation.operationId));
  const nativeOperationIds = new Set(
    nativeOperations.map((operation) => operation.operationId).filter((id) => id && !managementIds.has(id)),
  );
  issues.push(...findImplementationIssues(englishMap, registeredCommands, nativeOperationIds));

  return issues;
}

// loadRegisteredCommands reads the installed command registry through its
// stable machine contract (`agctl commands --json`): each command name with
// the endpoints it declares.
export function loadRegisteredCommands(repoRoot) {
  const output = execFileSync('go', ['run', './cmd/agctl', 'commands', '--json'], {
    cwd: repoRoot,
    encoding: 'utf8',
  });
  const items = JSON.parse(output)?.data?.items;
  if (!Array.isArray(items)) {
    throw new Error('agctl commands --json did not return a command list');
  }
  return new Map(
    items.map((item) => [
      String(item.name).toLowerCase(),
      new Set(Array.isArray(item.meta?.endpoints) ? item.meta.endpoints.map(String) : []),
    ]),
  );
}

function main() {
  const scriptPath = fileURLToPath(import.meta.url);
  const repoRoot = path.resolve(path.dirname(scriptPath), '..');
  const managementOperations = enumerateContractOperations(
    loadContractFile(path.join(repoRoot, contractBundlePaths.management)),
  );
  const nativeOperations = enumerateContractOperations(
    loadContractFile(path.join(repoRoot, contractBundlePaths.native)),
  );
  const englishMap = parseCommandMap(readFileSync(path.join(repoRoot, commandMapPath), 'utf8'));
  const chineseMap = parseCommandMap(readFileSync(path.join(repoRoot, chineseCommandMapPath), 'utf8'));
  const registeredCommands = loadRegisteredCommands(repoRoot);

  const issues = findContractDrift({ managementOperations, nativeOperations, englishMap, chineseMap, registeredCommands });
  if (issues.length > 0) {
    for (const issue of issues) process.stderr.write(`${issue.message}\n`);
    process.exitCode = 1;
    return;
  }

  const managementIds = new Set(
    managementOperations.map((operation) => operation.operationId).filter(Boolean),
  );
  const nativeExtras = new Set(
    nativeOperations
      .map((operation) => operation.operationId)
      .filter((operationId) => operationId && !managementIds.has(operationId)),
  );
  const implementedRows = [...englishMap.values()].filter((row) => /implemented/iu.test(row.phase)).length;
  process.stdout.write(
    `CLI command map contract check passed (${managementIds.size} management operations + ${nativeExtras.size} native protocol operations classified; ${implementedRows} implemented rows checked against ${registeredCommands.size} registered commands; planned rows are classified, not verified)\n`,
  );
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) main();
