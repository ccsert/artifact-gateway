# Console messages

Feature dictionaries extend the existing `preferences.t(key, variables)`
translator. Public browse is the first page moved during the issue 275 split;
this uses the Console's existing locale selection and persistence without a
new translation library.

The access, identity, and repository grants dictionaries cover the pages and
dialogs split in issue 275. Their display text is unchanged, including preset
names, role explanations, interpolated values, and punctuation. Shared grant
and protocol helpers retain their existing localization interface.

Keep each feature's Chinese and English messages in `<feature>.zh-CN.ts` and
`<feature>.en-US.ts`. Derive the key type from the Chinese dictionary and type
the English dictionary as `Record<FeatureMessageKey, string>`, so missing or
unknown keys fail type checking. Merge both dictionaries into the corresponding
`preferences` dictionaries; the existing translator handles `{variables}`.

Message extraction preserves the displayed text, including punctuation and
spacing. Shared protocol snippets continue to use their existing localization
interface. Issue 276 still owns the terminology catalog, migration tooling,
shared component migration, and the remaining pages.
