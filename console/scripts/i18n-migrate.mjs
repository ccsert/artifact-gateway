#!/usr/bin/env node
// Moves inline `text("中文", "English")` calls into the feature message
// dictionaries under src/messages (issue 276).
//
//   node scripts/i18n-migrate.mjs [--write] [path-prefix ...]
//
// Without --write the script only reports what it would change. A call is
// migrated when both arguments are string literals, or template literals that
// interpolate the same expressions; the expressions become `{placeholders}`
// and are passed as `t()` variables. Every other shape is listed for a manual
// move. Bindings of the old translator (the `usePreferences()` destructuring,
// `(chinese, english) => string` parameters and props) are renamed to `t` and
// retyped as `Translate`, so helpers that received `text` keep compiling.
// Run prettier and the type checker afterwards.
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import ts from "typescript";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const srcDir = path.join(root, "src");
const messagesDir = path.join(srcDir, "messages");
const args = process.argv.slice(2);
const write = args.includes("--write");
const prefixes = args
  .filter((arg) => !arg.startsWith("--"))
  .map((arg) => path.resolve(root, arg));

// Feature directories whose dictionaries predate the script keep their names.
const namespaceOverrides = {
  "features/access-control": { file: "access", prefix: "access" },
  "features/public-browse": { file: "public-browse", prefix: "publicBrowse" },
  components: { file: "ui", prefix: "ui" },
  app: { file: "shell", prefix: "shell" },
  lib: { file: "lib", prefix: "lib" },
  formats: { file: "formats", prefix: "formats" },
};

function camel(value) {
  return value.replace(/-([a-z])/g, (_, letter) => letter.toUpperCase());
}

function namespaceFor(file) {
  const relative = path.relative(srcDir, file).split(path.sep);
  const scope =
    relative[0] === "features" ? `features/${relative[1]}` : relative[0];
  if (namespaceOverrides[scope]) return namespaceOverrides[scope];
  const name = relative[0] === "features" ? relative[1] : relative[0];
  return { file: name, prefix: camel(name) };
}

// ---------------------------------------------------------------- dictionaries

/** Every key already in a dictionary, so new keys never shadow one. */
const takenKeys = new Map();
/** prefix -> Map(zh \0 en -> key), so repeated copy shares one key. */
const pairKeys = new Map();
/** file -> { zh: Map(key -> zh), en: Map(key -> en), prefix, existing } */
const dictionaries = new Map();

function readDictionary(file) {
  const source = ts.createSourceFile(
    file,
    fs.readFileSync(file, "utf8"),
    ts.ScriptTarget.Latest,
    true,
  );
  const entries = new Map();
  source.forEachChild(function visit(node) {
    if (
      ts.isPropertyAssignment(node) &&
      ts.isStringLiteral(node.name) &&
      ts.isStringLiteralLike(node.initializer)
    ) {
      entries.set(node.name.text, node.initializer.text);
    }
    node.forEachChild(visit);
  });
  return entries;
}

for (const name of fs.readdirSync(messagesDir)) {
  const match = /^(.*)\.zh-CN\.ts$/.exec(name);
  if (!match) continue;
  const zh = readDictionary(path.join(messagesDir, name));
  const en = readDictionary(path.join(messagesDir, `${match[1]}.en-US.ts`));
  for (const [key, value] of zh) {
    takenKeys.set(key, match[1]);
    const prefix = key.slice(0, key.indexOf("."));
    if (!pairKeys.has(prefix)) pairKeys.set(prefix, new Map());
    pairKeys.get(prefix).set(`${value}\0${en.get(key)}`, key);
  }
  dictionaries.set(match[1], {
    zh: new Map(),
    en: new Map(),
    existing: true,
  });
}

function keyWords(english) {
  const words = english
    .replace(/\{[^{}]+\}/g, " ")
    .split(/[^A-Za-z0-9]+/)
    .filter(Boolean)
    .slice(0, 8);
  if (words.length === 0) return "message";
  return words
    .map((word, index) =>
      index === 0
        ? word.charAt(0).toLowerCase() + word.slice(1)
        : word.charAt(0).toUpperCase() + word.slice(1),
    )
    .join("");
}

function messageKey(namespace, zh, en) {
  if (!pairKeys.has(namespace.prefix))
    pairKeys.set(namespace.prefix, new Map());
  const pairs = pairKeys.get(namespace.prefix);
  const pair = `${zh}\0${en}`;
  if (pairs.has(pair)) return pairs.get(pair);
  const base = `${namespace.prefix}.${keyWords(en)}`;
  let key = base;
  for (let suffix = 2; takenKeys.has(key); suffix += 1)
    key = `${base}${suffix}`;
  takenKeys.set(key, namespace.file);
  pairs.set(pair, key);
  if (!dictionaries.has(namespace.file)) {
    dictionaries.set(namespace.file, {
      zh: new Map(),
      en: new Map(),
      existing: false,
    });
  }
  const dictionary = dictionaries.get(namespace.file);
  dictionary.zh.set(key, zh);
  dictionary.en.set(key, en);
  return key;
}

// --------------------------------------------------------------------- program

const config = ts.getParsedCommandLineOfConfigFile(
  path.join(root, "tsconfig.app.json"),
  {},
  { ...ts.sys, onUnRecoverableConfigFileDiagnostic: () => {} },
);
const program = ts.createProgram(config.fileNames, config.options);
const checker = program.getTypeChecker();
const stringOrNumber = checker.getUnionType([
  checker.getStringType(),
  checker.getNumberType(),
]);

function isLocalizeType(type) {
  const signatures = type.getCallSignatures();
  if (signatures.length !== 1) return false;
  const [signature] = signatures;
  if (signature.parameters.length !== 2) return false;
  const stringType = checker.getStringType();
  return (
    signature.parameters.every(
      (parameter) =>
        checker.getTypeOfSymbol(parameter) === stringType &&
        !(parameter.valueDeclaration?.dotDotDotToken ?? false),
    ) && checker.getReturnTypeOfSignature(signature) === stringType
  );
}

function placeholderName(expression) {
  let node = expression;
  while (ts.isParenthesizedExpression(node) || ts.isNonNullExpression(node))
    node = node.expression;
  if (ts.isIdentifier(node)) return node.text;
  if (ts.isPropertyAccessExpression(node)) return node.name.text;
  if (ts.isCallExpression(node)) {
    const callee = node.expression;
    if (ts.isIdentifier(callee)) {
      if (callee.text === "String" || callee.text === "Number")
        return node.arguments[0] ? placeholderName(node.arguments[0]) : "value";
      const stripped = callee.text.replace(/^(format|to)(?=[A-Z])/, "");
      return stripped.charAt(0).toLowerCase() + stripped.slice(1);
    }
    if (ts.isPropertyAccessExpression(callee))
      return placeholderName(callee.expression);
  }
  return "value";
}

/** A template or string argument as message text plus its interpolations. */
function readArgument(node) {
  if (ts.isStringLiteralLike(node)) return { parts: [node.text], values: [] };
  if (!ts.isTemplateExpression(node)) return null;
  const parts = [node.head.text];
  const values = [];
  for (const span of node.templateSpans) {
    values.push(span.expression);
    parts.push(span.literal.text);
  }
  return { parts, values };
}

const report = { migrated: 0, pairs: 0, manual: [], files: new Map() };

const hasHan = (value) => /\p{Script=Han}/u.test(value);

/** A literal `[zh, en]` or `{ zh, en }` copy pair, as [zh, en]. */
function pairLiteral(node) {
  if (ts.isArrayLiteralExpression(node) && node.elements.length === 2) {
    const [zh, en] = node.elements;
    if (
      ts.isStringLiteralLike(zh) &&
      ts.isStringLiteralLike(en) &&
      hasHan(zh.text) &&
      !hasHan(en.text)
    )
      return [zh.text, en.text];
  }
  if (ts.isObjectLiteralExpression(node) && node.properties.length === 2) {
    const values = {};
    for (const property of node.properties) {
      if (
        !ts.isPropertyAssignment(property) ||
        !ts.isIdentifier(property.name) ||
        !ts.isStringLiteralLike(property.initializer)
      )
        return null;
      values[property.name.text] = property.initializer.text;
    }
    if (values.zh !== undefined && values.en !== undefined && hasHan(values.zh))
      return [values.zh, values.en];
  }
  return null;
}

function migrateCall(call, source, namespace) {
  if (call.arguments.length !== 2) return null;
  return migratePair(call.arguments[0], call.arguments[1], source, namespace);
}

function unwrap(node) {
  while (ts.isParenthesizedExpression(node)) node = node.expression;
  return node;
}

/**
 * `text(c ? "甲" : "乙", c ? "A" : "B")` becomes `c ? t("…a") : t("…b")`, so
 * each branch is a complete message rather than a sentence fragment.
 */
function migratePair(firstNode, secondNode, source, namespace) {
  const first = unwrap(firstNode);
  const second = unwrap(secondNode);
  if (ts.isConditionalExpression(first) && ts.isConditionalExpression(second)) {
    const condition = first.condition.getText(source);
    if (condition !== second.condition.getText(source)) return null;
    const whenTrue = migratePair(
      first.whenTrue,
      second.whenTrue,
      source,
      namespace,
    );
    const whenFalse = migratePair(
      first.whenFalse,
      second.whenFalse,
      source,
      namespace,
    );
    if (!whenTrue || !whenFalse) return null;
    return `(${condition} ? ${whenTrue} : ${whenFalse})`;
  }
  // An empty pair is an empty string in both languages.
  if (
    ts.isStringLiteralLike(first) &&
    ts.isStringLiteralLike(second) &&
    first.text === "" &&
    second.text === ""
  )
    return '""';
  const zh = readArgument(first);
  const en = readArgument(second);
  if (!zh || !en) return null;
  const keyOf = (expression) => expression.getText(source).replace(/\s+/g, "");
  const zhKeys = zh.values.map(keyOf).sort();
  const enKeys = en.values.map(keyOf).sort();
  if (zhKeys.join("\n") !== enKeys.join("\n")) return null;
  if (new Set(zhKeys).size !== zhKeys.length) return null;
  const names = new Map();
  const used = new Set();
  for (const expression of zh.values) {
    let name = placeholderName(expression).replace(/[^A-Za-z0-9_]/g, "");
    if (!name || /^[0-9]/.test(name)) name = "value";
    let unique = name;
    for (let suffix = 2; used.has(unique); suffix += 1)
      unique = `${name}${suffix}`;
    used.add(unique);
    names.set(keyOf(expression), { name: unique, expression });
  }
  const render = (argument) =>
    argument.parts
      .map((part, index) =>
        index < argument.values.length
          ? part + `{${names.get(keyOf(argument.values[index])).name}}`
          : part,
      )
      .join("");
  const key = messageKey(namespace, render(zh), render(en));
  if (names.size === 0) return `t(${JSON.stringify(key)})`;
  const variables = [...names.values()].map(({ name, expression }) => {
    let value = expression.getText(source);
    const type = checker.getTypeAtLocation(expression);
    if (!checker.isTypeAssignableTo(type, stringOrNumber))
      value = `String(${value})`;
    return value === name ? name : `${name}: ${value}`;
  });
  return `t(${JSON.stringify(key)}, { ${variables.join(", ")} })`;
}

/** `(chinese: string, english: string) => string`, written out. */
function isLocalizeTypeNode(node) {
  return (
    ts.isFunctionTypeNode(node) &&
    node.parameters.length === 2 &&
    node.parameters.every(
      (parameter) =>
        !parameter.dotDotDotToken &&
        !parameter.questionToken &&
        parameter.type?.kind === ts.SyntaxKind.StringKeyword,
    ) &&
    node.type.kind === ts.SyntaxKind.StringKeyword
  );
}

function isLocalizeAlias(node) {
  return ts.isTypeAliasDeclaration(node) && isLocalizeTypeNode(node.type);
}

/** Aliases of `(chinese, english) => string`, which become `Translate`. */
const localizeAliases = new Set();
for (const source of program.getSourceFiles()) {
  if (!source.fileName.startsWith(srcDir)) continue;
  source.forEachChild((node) => {
    if (isLocalizeAlias(node)) localizeAliases.add(node.name.text);
  });
}

function importPath(file) {
  let relative = path
    .relative(path.dirname(file), path.join(srcDir, "lib", "preferences"))
    .split(path.sep)
    .join("/");
  if (!relative.startsWith(".")) relative = `./${relative}`;
  return relative;
}

function processFile(source) {
  const file = source.fileName;
  const namespace = namespaceFor(file);
  const edits = [];
  let needsTranslate = false;

  const add = (start, end, replacement) =>
    edits.push({ start, end, replacement });

  source.forEachChild(function visit(node) {
    if (
      ts.isCallExpression(node) &&
      ts.isIdentifier(node.expression) &&
      node.expression.text === "text" &&
      isLocalizeType(checker.getTypeAtLocation(node.expression))
    ) {
      const replacement = migrateCall(node, source, namespace);
      if (replacement) {
        add(node.getStart(source), node.getEnd(), replacement);
        report.migrated += 1;
        // Inner calls would overlap this edit; the outer one wins.
        return;
      }
      const { line } = source.getLineAndCharacterOfPosition(node.getStart());
      report.manual.push(`${path.relative(root, file)}:${line + 1}`);
    }

    // `["中文", "English"]` and `{ zh: "中文", en: "English" }` table entries
    // become message keys; their readers then call `t(key)`.
    const pair = pairLiteral(node);
    if (pair) {
      const key = messageKey(namespace, pair[0], pair[1]);
      add(node.getStart(source), node.getEnd(), JSON.stringify(key));
      report.pairs += 1;
      return;
    }

    // The type alias itself goes; references become `Translate`.
    if (isLocalizeAlias(node)) {
      add(node.getFullStart(), node.getEnd(), "");
      needsTranslate = true;
      return;
    }
    if (isLocalizeTypeNode(node)) {
      add(node.getStart(source), node.getEnd(), "Translate");
      needsTranslate = true;
      return;
    }
    if (
      ts.isTypeReferenceNode(node) &&
      ts.isIdentifier(node.typeName) &&
      localizeAliases.has(node.typeName.text)
    ) {
      add(node.getStart(source), node.getEnd(), "Translate");
      needsTranslate = true;
      return;
    }
    if (
      ts.isImportSpecifier(node) &&
      localizeAliases.has(node.name.text) &&
      !node.propertyName
    ) {
      const list = node.parent;
      const index = list.elements.indexOf(node);
      if (list.elements.length === 1) {
        const declaration = list.parent.parent;
        add(declaration.getFullStart(), declaration.getEnd(), "");
      } else {
        const end =
          index < list.elements.length - 1
            ? list.elements[index + 1].getStart(source)
            : node.getEnd();
        const start =
          index < list.elements.length - 1
            ? node.getStart(source)
            : list.elements[index - 1].getEnd();
        add(start, end, "");
      }
      needsTranslate = true;
      return;
    }

    if (ts.isIdentifier(node) && node.text === "text") {
      renameIdentifier(node);
    }
    node.forEachChild(visit);
  });

  function renameIdentifier(node) {
    const parent = node.parent;
    if (ts.isJsxAttribute(parent) || ts.isPropertyAccessExpression(parent)) {
      // `props.text`, `<Child text={...}>`: only when it carries the translator.
      if (!isLocalizeType(checker.getTypeAtLocation(node))) return;
    } else if (!isLocalizeType(checker.getTypeAtLocation(node))) {
      return;
    }
    // `{ t, text } = usePreferences()` would become `{ t, t }`: drop text.
    if (
      ts.isBindingElement(parent) &&
      !parent.propertyName &&
      ts.isObjectBindingPattern(parent.parent) &&
      parent.parent.elements.some(
        (element) =>
          element !== parent &&
          ts.isIdentifier(element.name) &&
          element.name.text === "t",
      )
    ) {
      const elements = parent.parent.elements;
      const index = elements.indexOf(parent);
      if (index < elements.length - 1) {
        add(parent.getStart(source), elements[index + 1].getStart(source), "");
      } else {
        add(elements[index - 1].getEnd(), parent.getEnd(), "");
      }
      return;
    }
    add(node.getStart(source), node.getEnd(), "t");
  }

  if (edits.length === 0) return;
  edits.sort((left, right) => left.start - right.start || right.end - left.end);
  const kept = [];
  for (const edit of edits) {
    const previous = kept.at(-1);
    if (previous && edit.start < previous.end) continue;
    kept.push(edit);
  }
  let output = source.getFullText();
  for (const edit of kept.reverse()) {
    output =
      output.slice(0, edit.start) + edit.replacement + output.slice(edit.end);
  }
  if (
    needsTranslate &&
    !/import type \{[^}]*\bTranslate\b[^}]*\} from/.test(output)
  ) {
    output = `import type { Translate } from "${importPath(file)}";\n${output}`;
  }
  report.files.set(file, output);
}

for (const source of program.getSourceFiles()) {
  const file = source.fileName;
  if (!file.startsWith(srcDir)) continue;
  if (
    file.startsWith(messagesDir) ||
    file.includes(`${path.sep}client${path.sep}`)
  )
    continue;
  if (/\.test\.tsx?$/.test(file)) continue;
  // The translator's own definition stays until every caller has moved.
  if (file === path.join(srcDir, "lib", "preferences.tsx")) continue;
  if (prefixes.length && !prefixes.some((prefix) => file.startsWith(prefix)))
    continue;
  processFile(source);
}

// ------------------------------------------------------------------- emission

function typeName(file) {
  return file
    .split("-")
    .map((part) => part.charAt(0).toUpperCase() + part.slice(1))
    .join("");
}

function exportName(file, locale) {
  const name = typeName(file);
  return `${name.charAt(0).toLowerCase()}${name.slice(1)}${locale}`;
}

function entries(map) {
  return [...map]
    .map(
      ([key, value]) => `  ${JSON.stringify(key)}: ${JSON.stringify(value)},\n`,
    )
    .join("");
}

function appendEntries(file, map) {
  const text = fs.readFileSync(file, "utf8");
  const close = text.lastIndexOf("}");
  return `${text.slice(0, close)}${entries(map)}${text.slice(close)}`;
}

const outputs = new Map(report.files);
const newDictionaries = [];
for (const [name, dictionary] of dictionaries) {
  if (dictionary.zh.size === 0) continue;
  const zhFile = path.join(messagesDir, `${name}.zh-CN.ts`);
  const enFile = path.join(messagesDir, `${name}.en-US.ts`);
  if (dictionary.existing) {
    outputs.set(zhFile, appendEntries(zhFile, dictionary.zh));
    outputs.set(enFile, appendEntries(enFile, dictionary.en));
    continue;
  }
  const keyType = `${typeName(name)}MessageKey`;
  const zhName = exportName(name, "ZhCN");
  const enName = exportName(name, "EnUS");
  outputs.set(
    zhFile,
    `export const ${zhName} = {\n${entries(dictionary.zh)}};\n\nexport type ${keyType} = keyof typeof ${zhName};\n`,
  );
  outputs.set(
    enFile,
    `import type { ${keyType} } from "./${name}.zh-CN";\n\nexport const ${enName}: Record<${keyType}, string> = {\n${entries(dictionary.en)}};\n`,
  );
  newDictionaries.push({ name, zhName, enName });
}

if (newDictionaries.length) {
  const indexFile = path.join(messagesDir, "index.ts");
  let index = fs.readFileSync(indexFile, "utf8");
  const imports = newDictionaries
    .map(
      ({ name, zhName, enName }) =>
        `import { ${zhName} } from "./${name}.zh-CN";\nimport { ${enName} } from "./${name}.en-US";\n`,
    )
    .join("");
  index = imports + index;
  index = index.replace(
    /(export const zhCN = \{\n)/,
    `$1${newDictionaries.map(({ zhName }) => `  ...${zhName},\n`).join("")}`,
  );
  index = index.replace(
    /(export const enUS: Record<MessageKey, string> = \{\n)/,
    `$1${newDictionaries.map(({ enName }) => `  ...${enName},\n`).join("")}`,
  );
  outputs.set(indexFile, index);
}

console.log(`migrated calls: ${report.migrated}`);
console.log(`migrated pair literals: ${report.pairs}`);
console.log(`files changed: ${outputs.size}`);
console.log(`manual calls: ${report.manual.length}`);
for (const location of report.manual) console.log(`  ${location}`);
if (write) {
  for (const [file, output] of outputs) fs.writeFileSync(file, output);
} else {
  console.log("dry run; pass --write to apply");
}
