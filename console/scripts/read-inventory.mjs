#!/usr/bin/env node
// Lists every Gateway read in console/src and where it runs (issue 284):
// inside a query's queryFn, in a helper a queryFn calls, or elsewhere.
//
//   node scripts/read-inventory.mjs [--json]
//
// Reads are generated SDK operations that issue GET or HEAD, and `fetch`
// calls without a method other than GET or HEAD.
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import ts from "typescript";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const src = path.join(root, "src");

const sdk = ts.createSourceFile(
  "sdk.gen.ts",
  fs.readFileSync(path.join(src, "client", "sdk.gen.ts"), "utf8"),
  ts.ScriptTarget.Latest,
  true,
);
const readOperations = new Set();
sdk.forEachChild((node) => {
  if (!ts.isVariableStatement(node)) return;
  for (const declaration of node.declarationList.declarations) {
    const text = declaration.initializer?.getText(sdk) ?? "";
    if (/\)\.(get|head)</.test(text))
      readOperations.add(declaration.name.getText(sdk));
  }
});

function files(directory) {
  return fs.readdirSync(directory, { withFileTypes: true }).flatMap((entry) => {
    const full = path.join(directory, entry.name);
    if (entry.isDirectory()) return entry.name === "client" ? [] : files(full);
    return /\.tsx?$/.test(entry.name) && !/\.test\.tsx?$/.test(entry.name)
      ? [full]
      : [];
  });
}

function fetchIsRead(call, source) {
  const init = call.arguments[1];
  if (!init || !ts.isObjectLiteralExpression(init)) return true;
  const method = init.properties.find(
    (property) =>
      ts.isPropertyAssignment(property) &&
      property.name.getText(source) === "method",
  );
  if (!method) return true;
  return /^["'`](GET|HEAD)["'`]$/.test(method.initializer.getText(source));
}

function enclosingFunctionName(node) {
  for (let current = node.parent; current; current = current.parent) {
    if (ts.isFunctionDeclaration(current) && current.name)
      return current.name.text;
    if (
      (ts.isArrowFunction(current) || ts.isFunctionExpression(current)) &&
      ts.isVariableDeclaration(current.parent)
    )
      return current.parent.name.getText();
  }
  return undefined;
}

// The type checker resolves each name to its declaration, so reachability
// follows what the code actually calls rather than names that look alike.
const config = ts.getParsedCommandLineOfConfigFile(
  path.join(root, "tsconfig.app.json"),
  {},
  { ...ts.sys, onUnRecoverableConfigFileDiagnostic: () => {} },
);
const program = ts.createProgram(config.fileNames, config.options);
const checker = program.getTypeChecker();
const wanted = new Set(files(src));
const sources = program
  .getSourceFiles()
  .filter((source) => wanted.has(path.resolve(source.fileName)));

function walk(node, visit) {
  visit(node);
  node.forEachChild((child) => walk(child, visit));
}

function isQueryFn(node, source) {
  return (
    ts.isPropertyAssignment(node) && node.name.getText(source) === "queryFn"
  );
}

// Hooks that read through one of their own parameters, such as
// useQuotaSnapshot(read, …): the argument in that position runs as a query.
const readerHooks = new Map();
for (const source of sources) {
  walk(source, (node) => {
    if (!ts.isFunctionDeclaration(node) || !node.name) return;
    const params = node.parameters.map((param) => param.name.getText(source));
    walk(node, (inner) => {
      if (!isQueryFn(inner, source)) return;
      walk(inner.initializer, (use) => {
        if (ts.isIdentifier(use) && params.includes(use.text)) {
          const indexes = readerHooks.get(node.name.text) ?? new Set();
          indexes.add(params.indexOf(use.text));
          readerHooks.set(node.name.text, indexes);
        }
      });
    });
  });
}

// Code that runs as a query: queryFn bodies and reader-hook arguments.
const contexts = [];
for (const source of sources) {
  walk(source, (node) => {
    if (isQueryFn(node, source)) contexts.push(node.initializer);
    if (ts.isCallExpression(node) && ts.isIdentifier(node.expression)) {
      for (const index of readerHooks.get(node.expression.text) ?? [])
        if (node.arguments[index]) contexts.push(node.arguments[index]);
    }
  });
}
const contextNodes = new Set(contexts.filter(Boolean));

// Follow every name a context uses to its declaration: functions, function
// values, registries of them (`LISTERS[format]`), and object entries.
function followed(declaration) {
  if (
    ts.isFunctionDeclaration(declaration) ||
    ts.isMethodDeclaration(declaration) ||
    ts.isArrowFunction(declaration) ||
    ts.isFunctionExpression(declaration)
  )
    return declaration;
  if (
    (ts.isVariableDeclaration(declaration) ||
      ts.isPropertyAssignment(declaration)) &&
    declaration.initializer
  )
    return declaration.initializer;
  if (ts.isShorthandPropertyAssignment(declaration)) {
    const value = checker.getShorthandAssignmentValueSymbol(declaration);
    return value?.valueDeclaration;
  }
  return undefined;
}

const reachedNodes = new Set();
const queue = [...contexts];
while (queue.length) {
  const node = queue.pop();
  if (!node || reachedNodes.has(node)) continue;
  reachedNodes.add(node);
  walk(node, (inner) => {
    if (!ts.isIdentifier(inner)) return;
    let symbol = checker.getSymbolAtLocation(inner);
    if (symbol && symbol.flags & ts.SymbolFlags.Alias)
      symbol = checker.getAliasedSymbol(symbol);
    for (const declaration of symbol?.declarations ?? []) {
      const target = followed(declaration);
      if (
        target &&
        wanted.has(path.resolve(declaration.getSourceFile().fileName))
      )
        queue.push(target);
    }
  });
}

function insideContext(node) {
  for (let current = node; current; current = current.parent)
    if (contextNodes.has(current) || reachedNodes.has(current)) return true;
  return false;
}

const rows = [];
for (const source of sources) {
  walk(source, (node) => {
    if (!ts.isCallExpression(node) || !ts.isIdentifier(node.expression)) return;
    const name = node.expression.text;
    const isRead =
      readOperations.has(name) ||
      (name === "fetch" && fetchIsRead(node, source));
    if (!isRead) return;
    const helper = enclosingFunctionName(node);
    const where = insideContext(node) ? "query" : "other";
    const { line } = source.getLineAndCharacterOfPosition(
      node.getStart(source),
    );
    rows.push({
      file: path.relative(src, source.fileName),
      line: line + 1,
      call: name,
      where,
      function: helper ?? "",
    });
  });
}

if (process.argv.includes("--json")) {
  console.log(JSON.stringify(rows, null, 2));
} else {
  const other = rows.filter((row) => row.where === "other");
  console.log(
    `reads: ${rows.length}, in queries: ${rows.length - other.length}, elsewhere: ${other.length}`,
  );
  for (const row of other)
    console.log(
      `  ${row.file}:${row.line} ${row.call}${row.function ? ` (in ${row.function})` : ""}`,
    );
}
