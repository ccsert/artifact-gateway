# APT Hosted Roadmap

## Status and priority

APT Hosted is the next planned format expansion after the Kubernetes local
deployment baseline. APT Proxy and ordered Group reads are already supported;
this roadmap covers trusted publication owned by Artifact Gateway. It does not
change the current advertised capability until each milestone reaches its
acceptance gate.

The format cannot be admitted by adding a generic upload endpoint. Debian
clients resolve `Packages` indices through checksums in `Release` or
`InRelease`, and current APT clients reject unsigned repositories by default.
Artifact Gateway therefore needs one atomic, signed repository-snapshot model
before it can claim Hosted support. The normative upstream references are the
[Debian Repository Format](https://wiki.debian.org/DebianRepository/Format)
and [`apt-secure(8)`](https://manpages.debian.org/unstable/apt/apt-secure.8.en.html).

## APT-H1: publication contract and domain model

Current implementation status: H1 is complete. The streaming `.deb` parser,
quota-reserving idempotent session model, repository-write-scoped management
API, generated OpenAPI clients, content-addressed staged revision,
interrupted-upload recovery, durable reference-checked orphan collection,
publication audit evidence, explicit building-snapshot model,
Memory/PostgreSQL conformance, and narrow signer interface are implemented.
Staged revisions and building snapshots remain intentionally absent from APT
protocol reads. APT therefore continues to advertise Proxy-only capability
until the later signed-snapshot and production-signing gates are complete.

The H1 management sequence is deliberately pre-visibility:

1. An administrator explicitly provisions `format: apt, type: hosted` through
   `POST /api/v2/repositories`. This preview repository is management-visible,
   but remains absent from the format capability API and Console creation
   choices because it has no installable protocol surface yet.
2. `POST /api/v2/repositories/{repositoryId}/apt/publication-sessions`
   reserves quota for one suite, component, and declared `.deb` using an
   `Idempotency-Key`.
3. `PUT /api/v2/repositories/{repositoryId}/apt/publication-sessions/{sessionId}/package`
   streams, hashes, parses, and stages the exact package while deriving its
   canonical identity from Debian control metadata.
4. `GET /api/v2/repositories/{repositoryId}/apt/publication-sessions/{sessionId}`
   reports the durable state. A `staged` response is not an installable package;
   only H2 may publish it through an atomically visible signed snapshot.

- Define an APT publication session that accepts a `.deb` plus an explicit
  suite and component. Do not expose generic object PUT as package publication.
- Parse and validate the Debian control archive while streaming the object;
  derive the canonical package identity from `Package`, `Version`, and
  `Architecture`, and reject a client-supplied identity mismatch.
- Store immutable package bytes under a content-addressed object key and model
  suites, components, architectures, package revisions, and repository
  snapshots explicitly in PostgreSQL.
- Specify duplicate-version behavior, idempotency, quota reservation, audit
  evidence, and recovery after an interrupted upload.
- Establish the signing boundary: Gateway requests signatures from a narrow
  signer interface; application nodes do not expose or distribute private keys.

Acceptance gate: supported management provisioning, migrations, in-memory and PostgreSQL conformance, streaming
upload limits, malformed archive tests, canonical-identity tests, and a frozen
OpenAPI publication contract. This gate passed with the H1 management and
cleanup slice; it does not imply Hosted protocol availability.

## APT-H2: atomic Hosted repository

- Generate `Packages` plus supported compressed variants from committed package
  records, including `Filename`, `Size`, and SHA-256 fields that match the stored
  object.
- Generate `Release` checksums and Acquire-By-Hash objects, then publish a new
  immutable repository snapshot with a single visibility switch. Clients must
  never observe a package, index, and Release file from different snapshots.
- Use the H1 signer boundary to produce the minimum `InRelease` and detached
  `Release.gpg` signatures required for client acceptance. H3 hardens this
  boundary for production key custody and rotation; signing is not deferred
  until H3.
- Serve `dists/` metadata and `pool/` packages with native `GET`, `HEAD`, ranges,
  conditional responses, repository grants, anonymous policy, capacity,
  search, and Console browse behavior.
- Add a black-box Debian container gate that performs `apt-get update`, package
  download, and installation against the Gateway, then repeats the install with
  the object store or signer unavailable where cached immutable state permits.

Acceptance gate: an authenticated publication becomes installable only after
the signed snapshot is committed; injected failures at every publication stage
leave the previous snapshot completely readable.

## APT-H3: production signing, key rotation, and operations

- Harden the H2 signer behind an external service or KMS/HSM-backed adapter;
  record signer identity, key fingerprint, algorithm, snapshot digest, actor,
  and timestamps in immutable audit evidence.
- Support a controlled key-rotation overlap and an operator-visible repository
  signing state. Public-key distribution remains an explicit operator action in
  the first release rather than an automatic trust change.
- Add snapshot retention, rebuild, export, backup/restore, metrics, alerts, and
  a disaster-recovery drill that verifies signature and object checksums after
  restoration.

Acceptance gate: Debian client verification passes before, during, and after a
documented rotation; a restored repository reproduces the same signed snapshot
digests.

## APT-H4: lifecycle, scanning, and distribution

- Treat removal or retention as a new signed snapshot. Tombstoned packages stay
  restorable during the grace period, and object reclaim remains delayed and
  reference checked.
- Add a `.deb` resolver and scanner adapter before advertising manual or
  scan-on-publication support. The current reference scanner intentionally does
  not claim APT coverage.
- Extend quarantine-read and admission policy to the package identity and its
  signed snapshot. A blocked package must not remain reachable through an older
  visible index accidentally.
- Promotion and replication copy immutable package evidence, then regenerate
  and sign target-owned metadata. They must not copy a source repository's
  signed `Release` file as target authority.
- Keep APT Groups limited to ordered Proxy members until a separate Group
  snapshot owner can generate and sign a coherent aggregate. Never merge
  independently signed upstream metadata in place.

Acceptance gate: retention, restore, quarantine, promotion, and replication
each pass PostgreSQL, worker-retry, snapshot-atomicity, and real APT client
tests without partial visibility.

## Delivery order

Work proceeds H1 through H4. H1 and H2 establish the minimum truthful Hosted
capability; H3 is required before any production claim; H4 closes parity with
the existing lifecycle and security model. APT appears as `proxy`-only in the
format capability API and Console until H1-H3 have all passed their gates; the
explicit H1 Hosted provisioning request is an operator preview surface, not a
protocol compatibility claim.
