# Project quality assessment

[简体中文](project-quality-assessment.zh-CN.md) · [Documentation index](README.md)

Snapshot: 2026-08-21. This assessment describes engineering quality during the
pre-public preparation stage. It is not a production certification or release
approval.

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
| Documentation | Good entry points, uneven depth | The README, quick start, and index are bilingual; most deep technical records remain single-language or mixed-language. |
| Operability | Good preparation baseline | Health, metrics, diagnostics, recovery, Kubernetes, and distributed-role guidance exist; production evidence is still preparation work. |
| Performance | Reproducible local baseline | Binary/image size, quiet memory, and authenticated PostgreSQL/RustFS reads are measured; controlled production-like load and soak remain open. |
| Public-project readiness | Deliberately deferred | Licensing, formal distribution, a public security-reporting channel, and public support commitments are outside the current scope. |

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

## Main quality gaps

### 1. Large modules increase change coupling

Current hotspots include:

| File | Approximate size | Recommended seam |
| --- | ---: | --- |
| `console/src/pages/PublicBrowse.tsx` | 2,705 lines | Route shell, query state, format projections, and presentational sections |
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

### 3. Deep documentation is not fully bilingual

The project now has equivalent English and Chinese README, documentation index,
and getting-started paths. Translating every ADR and research note would add a
large synchronization burden. Prioritize bilingual operator journeys,
authentication, recovery, and protocol client examples; keep implementation
research in one canonical language unless it becomes user-facing.

### 4. Preparation evidence is broader than one default test target

`make test` protects shared local behavior, while full protocol E2E,
integration, browser, performance, rotation, upgrade, and recovery gates are
separate targets. The preparation checklist must continue to point to exact CI
evidence rather than treating one green command as full release proof. The
current performance report is a reproducible arm64 local baseline; it still
needs a controlled Linux/amd64 runner, resource limits, TLS/ingress, mixed
traffic, and sustained soak before it can become a release threshold.

## Recommended improvement sequence

1. **Keep onboarding executable.** Treat `make dev-bootstrap`, the bilingual
   quick start, and documentation links as tested interfaces.
2. **Split `PublicBrowse.tsx` without redesigning behavior.** Extract pure
   format/view models first, then page sections; retain browser regression
   coverage for search, filters, deep links, and responsive states.
3. **Partition repository records by domain.** Move types mechanically before
   changing persistence or behavior; keep public repository interfaces stable.
4. **Deepen native protocol modules.** Separate HTTP parsing/response shaping
   from Hosted/Proxy/Group application services one format at a time.
5. **Raise focused coverage with each extraction.** Increase floors only after
   meaningful boundary tests exist; never lower a floor to pass CI.
6. **Promote performance evidence deliberately.** Repeat the baseline on a
   controlled Linux/amd64 runner, then add hard limits, mixed traffic, and soak
   thresholds without converting one laptop snapshot into a universal claim.
7. **Maintain a preparation matrix.** Record exact commit, clean worktree,
   immutable image, CI run, integration evidence, recovery evidence, target
   environment, and named approval when formal distribution work begins.

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
