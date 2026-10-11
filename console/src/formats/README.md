# Format registry

Everything the Console knows about one artifact format is declared here,
so pages ask the registry instead of branching on format names (issue 272).
The server still decides which operations a repository allows
(`FormatProfile`, `RepositoryCapabilities`). The registry describes how the
Console presents a format and where its features appear.

## Layers

| Module                                | Holds                                                                     | Imported by                                      |
| ------------------------------------- | ------------------------------------------------------------------------- | ------------------------------------------------ |
| `descriptors.ts` / `types.ts`         | Plain traits and text per format (`FormatDescriptor`)                     | Any page or library; no React                    |
| `index.ts`                            | `formatDescriptor`, trait helpers, coordinate display and search encoding | Pages, `lib`                                     |
| `listers.ts`, `<format>/artifacts.ts` | How a repository's artifacts are listed, one page at a time               | The repository artifacts tab                     |
| `views.tsx`, `<format>/views.tsx`     | Detail panels and publish guides                                          | Pages; each format loads lazily in its own chunk |

`FORMAT_DESCRIPTORS` is a `Record<Format, FormatDescriptor>`. When the server
adds a format to the API, the generated `Format` type grows and this table
stops compiling until the format is described. A format string the Console
does not know (a newer server) gets `undefined`, and pages fall back to
their generic presentation.

## Adding a format

1. Add its descriptor to `descriptors.ts`. Start from `base`, and set only
   the traits that differ.
2. Add `<format>/views.tsx` with its `ArtifactDetail` (and `PublishGuide` when
   `publishGuide` is true), and register the loader in `views.tsx`.
3. If its artifacts are not package- or path-listed, add
   `<format>/artifacts.ts` and register it in `listers.ts`.
4. Extend `formats.test.ts`: each trait test lists the formats that have the
   trait.

No page under `src/features` needs a new branch.

## Deliberate exceptions

- `RepositoryArtifactsTab` handles proxy Maven itself. The proxy cache view
  (asset filter, running totals, cache detail and usage) is Maven-only UI
  with its own state, not a listing other formats share.
- Per-format text tables still `switch` on the format: retention copy,
  scanning hints, the resource-prefix grammar, access model labels, and
  `lib/usage` snippets. They are candidates for the descriptor text next.
