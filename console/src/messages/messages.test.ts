import { readdirSync, readFileSync } from "node:fs";
import { join, relative, resolve } from "node:path";
import ts from "typescript";
import { describe, expect, it } from "vitest";
import { enUS, zhCN } from ".";
import { glossary } from "./glossary";
import { createTranslator, errorText, LocalizedError } from "./translate";

const root = resolve(process.cwd(), "src");
const messagesDir = join(root, "messages");

function placeholders(message: string): string[] {
  return [...message.matchAll(/\{([^{}]+)\}/g)].map((match) => match[1]).sort();
}

async function featureDictionaries() {
  const names = readdirSync(messagesDir)
    .filter((name) => name.endsWith(".zh-CN.ts"))
    .map((name) => name.slice(0, -".zh-CN.ts".length));
  return Promise.all(
    names.map(async (name) => {
      const [zh, en] = await Promise.all([
        import(`./${name}.zh-CN.ts`),
        import(`./${name}.en-US.ts`),
      ]);
      const pick = (module: Record<string, unknown>) =>
        Object.values(module).find(
          (value): value is Record<string, string> =>
            typeof value === "object" && value !== null,
        ) ?? {};
      return { name, zh: pick(zh), en: pick(en) };
    }),
  );
}

function sourceFiles(directory: string): string[] {
  return readdirSync(directory, { withFileTypes: true }).flatMap((entry) => {
    const path = join(directory, entry.name);
    if (entry.isDirectory()) {
      return path === messagesDir || entry.name === "client"
        ? []
        : sourceFiles(path);
    }
    return /\.tsx?$/.test(entry.name) && !/\.test\.tsx?$/.test(entry.name)
      ? [path]
      : [];
  });
}

const han = /\p{Script=Han}/u;

// Text that is the same in every locale: a language's own name, and the
// built-in theme packages, whose descriptions mirror the server's packages.
const languageNames = new Set(["中", "中文", "简体中文", "切换到中文"]);
const exemptFiles = new Set(["lib/theme/defaultConsoleThemes.ts"]);

/** Every string, template and JSX text in a file that carries Han script. */
function hanLiterals(path: string): string[] {
  const source = readFileSync(path, "utf8");
  const found: string[] = [];
  const file = ts.createSourceFile(
    path,
    source,
    ts.ScriptTarget.Latest,
    true,
    path.endsWith(".tsx") ? ts.ScriptKind.TSX : ts.ScriptKind.TS,
  );
  file.forEachChild(function visit(node) {
    if (
      (ts.isStringLiteralLike(node) ||
        ts.isTemplateHead(node) ||
        ts.isTemplateMiddle(node) ||
        ts.isTemplateTail(node) ||
        ts.isJsxText(node)) &&
      han.test(node.text) &&
      !languageNames.has(node.text.trim())
    ) {
      const { line } = file.getLineAndCharacterOfPosition(node.getStart());
      found.push(`${relative(root, path)}:${line + 1} ${node.text.trim()}`);
    }
    node.forEachChild(visit);
  });
  return found;
}

describe("message dictionaries", () => {
  it("define every key in both locales with the same placeholders", async () => {
    for (const { name, zh, en } of await featureDictionaries()) {
      expect(Object.keys(en).sort(), name).toEqual(Object.keys(zh).sort());
      for (const [key, message] of Object.entries(zh)) {
        expect(message.trim(), key).not.toBe("");
        expect(en[key].trim(), key).not.toBe("");
        expect(placeholders(en[key]), key).toEqual(placeholders(message));
      }
    }
  });

  it("never define one key in two dictionaries", async () => {
    const owners = new Map<string, string>();
    const duplicates: string[] = [];
    for (const { name, zh } of await featureDictionaries()) {
      for (const key of Object.keys(zh)) {
        const owner = owners.get(key);
        if (owner) duplicates.push(`${key} (${owner}, ${name})`);
        owners.set(key, name);
      }
    }
    expect(duplicates).toEqual([]);
    expect(Object.keys(enUS).length).toBe(Object.keys(zhCN).length);
    expect(owners.size).toBe(Object.keys(zhCN).length);
  });

  it("name each domain term the way the glossary does", () => {
    const misnamed = Object.entries(zhCN).flatMap(([key, message]) =>
      glossary.flatMap(({ zh, avoid = [] }) =>
        avoid
          .filter((synonym) => message.includes(synonym))
          .map((synonym) => `${key}: ${synonym} → ${zh}`),
      ),
    );
    expect(misnamed).toEqual([]);
  });

  it("only keep messages some source file uses", () => {
    const source = sourceFiles(root)
      .map((path) => readFileSync(path, "utf8"))
      .join("\n");
    // Keys built at run time, such as `account.kind.${kind}`.
    const prefixes = [...source.matchAll(/`([a-zA-Z]+\.[A-Za-z.]*)\$\{/g)].map(
      (match) => match[1],
    );
    const unused = Object.keys(zhCN).filter(
      (key) =>
        !source.includes(`"${key}"`) &&
        !prefixes.some((prefix) => key.startsWith(prefix)),
    );
    expect(unused).toEqual([]);
  });

  it("keeps display text out of components", () => {
    const literals = sourceFiles(root)
      .filter((path) => !exemptFiles.has(relative(root, path)))
      .flatMap(hanLiterals);
    expect(literals).toEqual([]);
  });
});

describe("createTranslator", () => {
  it("fills named placeholders once and leaves unknown ones literal", () => {
    const t = createTranslator("en-US");
    expect(t("ui.retry")).toBe("Retry");
    expect(createTranslator("zh-CN")("ui.retry")).toBe("重试");
    expect(t("system.disableTarget", { name: "{name} ops" })).toBe(
      "Disable {name} ops",
    );
    expect(t("system.disableTarget", {})).toBe("Disable {name}");
  });

  it("translates a LocalizedError where it is shown", () => {
    const t = createTranslator("en-US");
    const error = new LocalizedError(
      "publicBrowse.failedToReadOciTagsStatus",
      { status: 503 },
      { status: 503 },
    );
    expect(error.status).toBe(503);
    expect(errorText(error, t, "ui.requestFailed")).toBe(
      "Failed to read OCI tags (503)",
    );
    expect(errorText(new Error("plain"), t, "ui.requestFailed")).toBe("plain");
    expect(errorText(undefined, t, "ui.requestFailed")).toBe("Request failed");
  });
});
