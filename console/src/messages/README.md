# Console messages

Every piece of display text lives here, one dictionary pair per feature:
`<feature>.zh-CN.ts` and `<feature>.en-US.ts`. `index.ts` merges them into
the `zhCN` and `enUS` tables, and `MessageKey` is the union of every key.

## Writing a message

Components read the translator from `usePreferences()`:

```tsx
const { t } = usePreferences();
t("repository.deleteRepository", { name: repository.name });
```

- Keys are `<featurePrefix>.<camelCasedEnglish>`; the prefix names the
  dictionary that owns the key (`repository.`, `operations.`, `ui.` for
  `src/components`, `shell.` for `src/app`, `lib.` for `src/lib`).
- The Chinese dictionary defines the key type; the English one is typed as
  `Record<FeatureMessageKey, string>`, so a missing or unknown key fails type
  checking.
- `{name}` placeholders take the matching variable. Pass whole sentences,
  one key per branch, rather than gluing fragments together, and one key per
  plural form where English needs it.
- Helpers outside React take `t: Translate` as a parameter. Tests build a real
  translator with `createTranslator("zh-CN" | "en-US")` from `translate.ts`.
- Code that has no translator (query functions, readers) throws
  `LocalizedError(key, variables)`. `ErrorBanner` and `errorText()` translate
  it where it is shown; its `message` holds the Chinese text for logs.
- Lookup tables store keys (`Record<Code, MessageKey>`), not `[zh, en]` pairs.

## Terms

`glossary.ts` names each domain term from `CONTEXT.md` the way the Console
shows it (仓库, 制品, 晋升, 墓碑, 隔离, …) and lists the synonyms to avoid.

## Checks

`messages.test.ts` runs with the unit tests and fails when:

- a key is missing from either locale, is empty, or its placeholders differ;
- one key is defined in two dictionaries (the later spread would win);
- no source file uses a key (keys built at run time from a template prefix,
  such as `account.kind.${kind}`, count as used);
- a Chinese message uses a synonym the glossary avoids;
- a string, template or JSX text outside `src/messages` contains Chinese,
  that is, display text that bypassed the dictionaries. Language names
  (`简体中文`) and the built-in theme package descriptions are exempt.

## Migrating inline text

`scripts/i18n-migrate.mjs` moved the former `text("中文", "English")` calls and
`[中文, English]` tables into these dictionaries (issue 276). It is kept for
branches that still carry inline calls:

```sh
node scripts/i18n-migrate.mjs            # report only
node scripts/i18n-migrate.mjs --write    # rewrite, then run prettier and tsc
```
