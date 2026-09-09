---
version: 1
slug: "e-src-pages-repository-detail-aptoperationstab-tsx"
primary_target: "console/src/pages/repository-detail/APTOperationsTab.tsx"
related_targets: ["console/src/pages/repository-detail/RepositoryDistributionTab.tsx","console/src/pages/repository-detail/RepositoryBrowseTree.tsx"]
---

# Hosted APT signed snapshots

## Scope and mode

**Operate.** The Hosted APT Repository's Signed snapshots tab serves repository administrators reviewing and changing suite membership. It inherits the Console's existing Ant Design controls, semantic themes, compact density, and parent-owned spacing. Related distribution and browse-tree guidance below is local to this repository workflow.

## Task and evidence

- Enter an exact suite name, then inspect its current visible snapshot before acting. Keep snapshot sequence, ID, signing fingerprint, signer identity, and publication time together above Packages, Recovery, Snapshots, and Promote / replicate.
- Package name, version, architecture, and component describe the package. The server-provided `poolPath` and SHA-256 identify the distributable artifact; never reconstruct the path from those display fields. Paths, digests, and snapshot identities use the established technical type role and remain readable at narrow widths.
- Deletion, recovery, and retention enter a review dialog before applying. Show the fixed source snapshot, affected members, remaining package count, and recovery window. Retention groups by component, package, and architecture and uses upload recency beyond both configured limits; it does not order Debian versions. Apply the exact previewed candidate set.
- The review's decisive action is “Apply and sign new snapshot.” Use an outlined semantic danger button for removal and retention, and outlined primary treatment for recovery. Preserve readable default, hover, active, focus, busy, and disabled states. A changed snapshot or candidate set invalidates review and requires a fresh preview; do not silently retry a conflict.
- Pruning is a separate, confirmed action on eligible retired snapshots. Explain that it removes old snapshot references, affects clients using those indices, and leaves shared objects protected by remaining references; background reclaim is separate.
- Promotion and replication require an explicit target Hosted Repository and target suite. The selected source supplies the canonical pool path and digest. Explain that the target creates its own signed snapshot and preserves existing members; a submitted job is not a completed distribution.

## Composition and states

The first viewport presents the operator-preview boundary, suite input, and current signature evidence. The memorable interaction is reviewing concrete package changes against one immutable snapshot before signing the successor. Initial loading, empty-suite guidance, read errors, action errors, and success feedback remain explicit. A refresh failure preserves previously loaded evidence, shows a retryable error above it, and disables dependent operations until refreshed successfully.

Signature evidence uses two columns on wider screens and one on narrow screens. Toolbars wrap and then stack; tables scroll inside their own surface, while long identities wrap without causing page overflow. Review member lists have bounded internal scrolling.

In the related artifact browser, give the tree more width than the inspector (1.45:1 on desktop). The root label shows the real repository name and format. The footer counts loaded nodes recursively, excluding pagination controls; it does not claim a repository total. Allow asset filenames two lines and preserve directory expansion and narrow-screen access to the inspector.

## Acceptance boundary

UI fixtures and local checks support interface behavior only. Preserve the visible operator-preview and signer requirement: production key custody, real deployed lifecycle behavior, and production release acceptance remain outside this surface's evidence. No new visual-world decision is open; continue the existing Console design system.
