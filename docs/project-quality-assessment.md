# Project quality assessment

[简体中文](project-quality-assessment.zh-CN.md) · [Documentation index](README.md)

Snapshot: 2026-08-24. This assessment describes engineering quality at the
0.1.0 early-release boundary. It is not a production certification or support
commitment.

## Executive assessment

Artifact Gateway has a strong protocol and persistence foundation, explicit
contracts, broad automated gates, and unusually detailed lifecycle semantics
for a project at this stage. Its primary risk is no longer missing basic
repository behavior; it is the cost of changing several large application and
Console modules safely while the feature surface continues to grow.

| Dimension | Assessment | Evidence and interpretation |
| --- | --- | --- |
| Architecture | Strong | PostgreSQL and object-storage responsibilities are explicit; runtime roles and durable-job semantics are documented. |
| Protocol correctness | Strong | Compatibility limits are explicit and native-client fixtures exist for the advertised formats. |
| Verification | Strong with coverage headroom | CI, contract, integration, E2E, recovery, and readiness gates exist; some global coverage floors remain intentionally conservative. |
| Maintainability | Needs focused work | Several route, model, and native-protocol files are large enough to slow review and increase change coupling. |
| Documentation | Complete bilingual source set | Every site document has an English canonical route and a substantive Simplified Chinese companion; a tested site map owns navigation and pair coverage. |
| Operability | Good early-release baseline | Health, metrics, diagnostics, recovery, Kubernetes, and distributed-role guidance exist; production-environment evidence remains separate. |
| Performance | Reproducible local baseline | Binary/image size, quiet memory, authenticated PostgreSQL/RustFS reads, warm 64 MiB reads, and a controlled HTTPS Proxy cold miss are measured; controlled production-like load and soak remain open. |
| Public-project readiness | Private early release | Versioned distribution exists; licensing, public visibility, a public security-reporting channel, and public support commitments remain deliberately deferred. |

## What is already high quality

- The architecture does not hide storage boundaries: PostgreSQL owns metadata
  and coordination, while verified immutable bytes use S3-compatible storage.
- Background work uses durable leases, fencing, idempotency, and recoverable
  polling instead of treating notifications as the source of truth.
- Protocol claims are constrained by an explicit compatibility matrix and
  executable client gates.
- OpenAPI sources, generated Console clients, and generated Go contracts move
  through one drift check.
- Migration, backup/restore, readiness, dependency, and coverage checks are
  versioned with the repository.
- The isolated-Docker [performance baseline](performance-baseline.md) quantifies
  deliverable size, quiet memory, concurrent throughput, and observed peaks
  without presenting a laptop result as a production SLA.
- Preview work such as APT Hosted, Cargo, and NuGet is kept distinguishable
  from advertised support.

## Maintainability work in progress

The quality program has started, but it is not complete:

- `console/src/lib/publicBrowseModel.ts` already owns pure Maven/Conan grouping
  and deep-link state, with tests through that module interface.
- `console/src/components/PublicBrowsePrimitives.tsx` already owns shared
  version-selection, metadata, and usage-snippet presentation.
- The current slice moves recursive OCI index/manifest/config reading and tag
  pagination into `console/src/lib/publicOci.ts`. Its small interface accepts a
  production or test HTTP adapter, and focused tests cover recursion, digest
  retention, size aggregation, optional config failure, and error propagation.
- Protocol-specific repository setup snippets now come from the pure
  `console/src/lib/publicRepositoryUsage.ts` boundary. Tests cover all eight
  formats without coupling them to browser state or the page component.
- Together these slices reduce `PublicBrowse.tsx` from 2,705 to 2,534 lines
  without changing its route or visible behavior. The page is still a hotspot;
  Maven/Conan display sections and remaining format projections are the next
  seams.

No equivalent extraction has yet reduced `internal/repository/model.go` or the
large native Maven/npm/OCI application modules. They remain planned work, not
completed quality improvements.

The Raw byte-path review produced concrete backend improvements in this slice:

- Proxy misses now hash into a temporary file with a fixed 128 KiB buffer;
  cache publication and reads use `PutVerifiedReader`, `Stat`, `Open`, and
  `OpenRange` instead of an artifact-sized `[]byte`.
- Positive HEAD misses remain HEAD requests and do not download or cache the
  upstream body.
- Anonymous member filtering no longer aliases the MemoryStore slice; request
  locks release per member and renew while slow upstream work is active.
- Raw and OCI resumable PATCH writes persist immutable offset chunks. Earlier
  bytes are not downloaded and rewritten for every chunk; completion performs
  one ordered digest pass and remains compatible with old cumulative sessions.
  Completed, cancelled, and expired Raw sessions retain a PostgreSQL trace
  while durable reclaim removes any remaining prefix and chunk objects.
- Each Gateway admits at most four concurrent Raw Proxy staging files by
  default. The positive configurable limit is enforced before upstream access;
  saturation returns `503` with `Retry-After: 1` and increments
  `artifact_gateway_raw_spool_rejections_total`.
- A controlled HTTPS Docker workload transferred one cold 64 MiB Proxy path to
  eight concurrent clients with one upstream body GET, then verified byte-exact
  cache replay after the upstream stopped.
- Startup signer/scanner failures include their cause and production `%v`
  error wrappers were replaced with error-chain-preserving `%w` wrappers.

The constant-heap proxy path deliberately trades heap pressure for temporary
disk. In-process admission now bounds simultaneous staging, but deployments
still need a dedicated temporary volume, free-space monitoring, and hard
ephemeral-storage requests/limits. Chunk completion is O(n), but a future S3
multipart-compose adapter could remove the whole-upload completion spool
without changing the HTTP chunk contract.

## Main quality gaps

### 1. Large modules increase change coupling

Current hotspots include:

| File | Approximate size | Recommended seam |
| --- | ---: | --- |
| `console/src/pages/PublicBrowse.tsx` | 2,534 lines | Route shell, query state, format projections, and presentational sections |
| `internal/repository/model.go` | 1,328 lines | Repository, identity, lifecycle, intelligence, and operations records |
| `internal/app/native_maven.go` | 1,173 lines | HTTP adapter versus Maven publication/application service |
| `internal/app/native_npm.go` | 1,160 lines | Registry HTTP shape versus Hosted/Proxy/Group orchestration |
| `internal/app/native_oci.go` | 1,033 lines | Distribution routing versus upload/manifest application services |

These files are not automatically defective, but their size makes independent
review, narrow tests, and future ownership harder. Refactors should preserve
public behavior and move one seam at a time; a one-shot package rewrite would
create more risk than it removes.

### 2. Coverage floors protect regression, not completion

The repository-wide Go floor is 40%. Stable packages have stronger focused
floors, but authorization begins at 38%. The Console tracks all hand-written
code with 40% line/statement, 53% function, and 65% branch floors plus stronger
per-module thresholds. These are useful non-regression guards, but they should
rise as the large modules are split and public-boundary tests become cheaper.

### 3. Bilingual documentation is now a maintained contract

Every site document now has an English canonical route and a substantive
Simplified Chinese companion, including ADRs, contracts, operations, release
evidence, and research notes. `docs/site-map.json` provides framework-neutral
navigation, while `make docs-check` rejects missing pairs, one-way language
links, untranslated Chinese stubs, duplicate routes, and incomplete coverage.

The remaining risk is synchronization rather than initial coverage. Behavior,
commands, compatibility limits, and preview status must change in both locales
within the same review.

### 4. Release evidence is broader than one default test target

`make test` protects shared local behavior, while full protocol E2E,
integration, browser, performance, rotation, upgrade, and recovery gates are
separate targets. The release checklist must continue to point to exact CI
evidence rather than treating one green command as full release proof. The
current performance report is a reproducible arm64 local baseline; it still
needs a controlled Linux/amd64 runner, resource limits, TLS/ingress, mixed
traffic, and sustained soak before it can become a release threshold.

## Recommended improvement sequence

1. **Keep onboarding executable.** Treat `make dev-bootstrap`, the bilingual
   quick start, and documentation links as tested interfaces.
2. **Continue splitting `PublicBrowse.tsx` without redesigning behavior.** The
   pure browse model, shared primitives, OCI reader, and repository usage
   generator now have focused seams; extract Maven/Conan display sections next
   while retaining browser regression coverage for search, filters, deep links,
   and responsive states.
3. **Partition repository records by domain.** Move types mechanically before
   changing persistence or behavior; keep public repository interfaces stable.
4. **Deepen native protocol modules.** Separate HTTP parsing/response shaping
   from Hosted/Proxy/Group application services one format at a time.
5. **Harden the large-object byte plane.** Add deployment-level temporary
   volume and ephemeral-storage limits, then test many unique concurrent misses
   through admission under those limits. Evaluate multipart compose behind the
   object-store port without requiring client chunk sizes.
6. **Raise focused coverage with each extraction.** Increase floors only after
   meaningful boundary tests exist; never lower a floor to pass CI.
7. **Promote performance evidence deliberately.** Repeat the baseline on a
   controlled Linux/amd64 runner, then add hard limits, mixed traffic, and soak
   thresholds without converting one laptop snapshot into a universal claim.
8. **Maintain a release matrix.** Record exact commit, clean worktree,
   immutable image, CI run, integration evidence, recovery evidence, target
   environment, and named approval for each controlled deployment decision.

## Quality rules for ongoing work

- Preserve protocol compatibility and immutable artifact identity before
  optimizing internal structure.
- Prefer small extractions with unchanged API and persistence contracts.
- Keep PostgreSQL as the sole control-plane coordination service; do not add a
  middleware dependency without measured evidence and an architecture decision.
- Never blur the separate S3-compatible byte-plane requirement in lightweight
  positioning.
- Add a focused test and documentation update whenever a refactor changes an
  operator-visible boundary.
- Distinguish local verification, CI candidates, and formal release evidence.
