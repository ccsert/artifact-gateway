# Explicit Format Modules and Raw Tracer

[简体中文](0011-format-modules.zh-CN.md) · [Documentation index](../README.md)

Status: accepted; Raw tracer implemented (#393), remaining formats tracked by #281

## Context and alternatives

Raw's Hosted and Proxy handler, Group byte resolution, scanning, retention,
and replication publication used private symbols in `internal/app`. Splitting
files would preserve those dependencies. A single `FormatModule` interface
containing every operation would instead force each future format to implement
unsupported methods and receive unrelated stores.

We choose a small module identity plus independently queried capabilities.
This implements ADR 0009 incrementally; historical management interfaces and
URL aliases do not constrain the design. Standard package protocols, immutable
identities, complete byte verification, authorization and durable fencing do.

## Module and registry

`internal/format.Module` supplies only `Format()`. `format.New` takes explicit
instances, rejects nil (including typed nil), empty identities and duplicate
formats, then owns an immutable lookup map. It has no global registration or
hidden storage construction. `format.Capability[T]` queries a capability only
on the requested registered module; missing modules/capabilities remain absent.

`NativeRoutes` supplies protocol routes. The registry validates the entire set
before mounting, including duplicate paths, typed nil handlers and invalid HTTP
patterns. Application composition retains request observation, credentials,
shared authorization/member ordering and metrics adapters. The module owns the
format's response decisions and Hosted/Proxy/Group execution.

The Raw tracer implements these capabilities in `internal/format/raw`:

| Capability | Ownership |
| --- | --- |
| Native routes | `/raw/`, canonical path parsing, Hosted PUT and resumable completion, CAS, checksums, Proxy cache/fallthrough and Group reads |
| Profile and browse | Existing Raw capability profile, directory parent validation, display names, canonical search paths, bounded search projections and usage identity |
| Scanner | Immutable path/digest validation and read-only scanner Asset streams |
| Retention and governance | Raw age/pattern selection, tombstoning and restore; shared usage protection stays in the retention coordinator |
| Promotion and replication | Raw promotion payload, native checkpoint snapshot and target publication through the existing durable workers |
| Immutable publication | Native Raw path validation, CAS object lock, staging, create-only visibility and read-back used by migration orchestration |
| Cache and background | Existing Raw cache collection and reclaim/promotion/replication scheduler/worker startup |

The optional interfaces carry domain records, never generated management DTOs.
Management handlers keep authentication, response/error mapping, signed cursors,
permission checks and auditing around the capabilities. Unmigrated formats keep
their existing dispatch branches until their own independently mergeable slices.
Raw registration is explicitly bound at HTTP/process composition and at the
narrow read or lifecycle service seam; it does not synthesize missing ports.
An admitted format profile describes protocol capability, not deployment
readiness of optional workers, caches or scanners.

## Narrow ports and durable invariants

Raw native reads/writes depend on `repository.NativeRawStore`, small repository
and Group lookup ports, object storage, read-policy/quarantine readers and shared
authorization. Scanner/publication callbacks and metric/diagnostic adapters do
not expose the composition root. Migration publication has a separate four-method
Raw store port and does not receive the migration ledger or SQL implementation.
`StageVerifiedPublication` accepts bytes whose full size and hash the migration
coordinator has already checked. Object-store `PutVerifiedReader` records that
verified metadata; it does not perform digest verification. Visibility and final
complete read-back remain separate coordinator steps.

The Job/Replication/Publication interface fields retain the original concrete
store as their dynamic value. Do not wrap or reconstruct stores out of embedded
interfaces: that can erase PostgreSQL transaction, lease/advisory-lock,
notification and quarantine-admission ports. Reclaim coordination, publication
CAS, quota checks, migration transaction fencing and complete read-back keep
using the same native store operations and request context. No SQL migration
or persistence fallback is introduced.

`internal/artifacthttp` owns the previously shared immutable object HTTP response
used by Raw and npm; it accepts format-neutral object metadata. `internal/uploadspool`
owns bounded staging/chunk assembly shared by unmigrated format handlers too.
Raw never imports npm/OCI for an incidental helper. Raw upstream URL assembly
and transport policy also live in its module, while transport tracing/test hooks
are supplied explicitly. Format modules do not import app, another format or a
PostgreSQL implementation; `make architecture-check` enforces that direction.

## Verification and remaining work

The registry is exercised through real route mounting and invalid registration.
The Raw module can publish, conditionally replace, resumably complete, read,
scan, plan retention, prepare distribution, tombstone/restore and search without
constructing a Gateway. Existing whole-Gateway Raw/Proxy/Group, authorization,
quarantine, failure diagnostics and scan scheduling tests remain in place.
PostgreSQL/S3 and shared Memory/PostgreSQL contracts prove the unchanged native
storage/publication boundaries; the standard curl gate exercises the mounted route.

This tracer does not complete #281. Cargo, Go, npm, PyPI, Maven, Conan, OCI and
APT remain separate format slices. Static admission profiles still live in
`repository`; cache-control SQL projections remain persistence implementation
work alongside #282. Shared member authorization, HTTP contracts and migration
manifest/capacity/lease orchestration remain their own domains. The app switch
count is a progress metric, not a claim that the final ≤10 target is met.
No production migration, cutover or source deletion is performed.
