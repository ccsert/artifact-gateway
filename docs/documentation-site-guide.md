# Documentation Site Integration Guide

[简体中文](documentation-site-guide.zh-CN.md) | [Documentation index](README.md)

Artifact Gateway keeps its Markdown portable so the repository can later feed
Docusaurus, VitePress, MkDocs, or another static documentation system without
rewriting document ownership and language rules.

## Information architecture

`docs/site-map.json` is the navigation contract. It defines six ordered
sections, localized section and page titles, stable page IDs, and one English
and Simplified Chinese route for every document.

The sections are: Start here; Architecture and design; Protocols and formats;
Operations and security; Quality, performance, and release; and Strategy and
reference.

The repository README remains the project landing page. `docs/README.md` and
`docs/README.zh-CN.md` are the documentation landing pages and preserve the
same section order as the site map.

## Locale and route convention

- English is the default locale and uses the unsuffixed `.md` path.
- Simplified Chinese uses the matching `.zh-CN.md` path.
- Root documents follow the same rule, for example `SECURITY.md` and
  `SECURITY.zh-CN.md`.
- Every pair links to the other locale and to the localized documentation index.
- `.en.md` is rejected to avoid two English route conventions.

A site generator may mount English pages at `/` and Chinese pages under
`/zh-CN/`, but source paths and IDs should remain unchanged. Route aliases for
renamed public pages belong in site configuration, not a second source naming scheme.

## Content ownership

The locales are behaviorally equivalent, not necessarily sentence-for-sentence
literal translations. Both preserve commands, configuration keys, API routes,
status codes, compatibility limits, security boundaries, evidence scope, and
whether a capability is released, previewed, planned, or historical.

Do not publish a locale stub that only links to the other language. When one
locale changes behavior, update its companion in the same change.

## Generator adapter

A future adapter should read `docs/site-map.json` and generate its sidebar or
navigation. It may add generated frontmatter in a build directory, but must not
rewrite source Markdown or commit a second handwritten navigation tree.

Assets remain under `docs/assets/`. Relative links stay valid in the repository
and may be rewritten only in generated output. Root-level pages require the
adapter to preserve their relationship to the documentation tree.

The build should fail when a route is missing, duplicated, not paired, or lacks
a localized title. Search indexes should carry stable page ID, locale, section
ID, title, and public route so language switching does not compare translated titles.

## Local validation

```sh
make docs-check
```

The gate checks tracked and untracked Markdown that exists in the working tree.
It validates local Markdown/HTML links, language-pair naming, reciprocal links,
substantive Chinese bodies, localized titles, unique IDs and paths, and complete
navigation coverage.

When a page is removed or renamed, update both locale files, inbound links, and
`docs/site-map.json` in the same change. Keep release records under
`docs/release-records/` and ADRs under `docs/adr/`.

## Initial site acceptance

- Both locale homes render all six sections in site-map order.
- Language switching stays on the same stable page ID.
- Root pages, ADRs, release records, diagrams, and images resolve.
- Code blocks, Mermaid, tables, headings, and anchors render correctly.
- Search returns the active locale and records the source locale.
- Redirects cover previously shared documentation URLs.
- CI runs `make docs-check` and the site generator's link/build checks.
