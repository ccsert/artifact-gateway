# APT target-signed promotion and replication

[简体中文](apt-hosted-distribution.zh-CN.md) | [APT roadmap](apt-hosted-roadmap.md)

APT Hosted distribution is an operator preview. It uses the existing promotion
jobs and replication plans, and requires active Hosted APT source and target
repositories, administration permission on both, and a configured signer on the
API and relevant Worker nodes. Public format capabilities and Console creation
choices still advertise Proxy/Group support; this gate does not establish
production key custody or APT Group aggregation.

## Request

Use either `POST /api/v2/repositories/{sourceId}/promotions` or
`POST /api/v2/repositories/{sourceId}/replications`, with an `Idempotency-Key`:

```json
{
  "targetRepositoryId": "00000000-0000-4000-8000-000000000002",
  "coordinate": "pool/main/w/widget/widget_1.0-1_amd64.deb",
  "digest": "sha256:0000000000000000000000000000000000000000000000000000000000000000",
  "aptTargetSuite": "candidate"
}
```

Replace the illustrative IDs, path and digest with actual visible package
identity. The coordinate is repository-global and includes its component; do
not prefix it with a suite. The package must remain visible in at least one
source suite. Staged or retired-only packages are not eligible. Changing the
target suite under the same idempotency key returns a conflict. Other formats
reject `aptTargetSuite`.

Both endpoints return `202`. Promotion is inspected through the target's
lifecycle jobs; replication through either repository's replication list/detail.
The replication response preserves `aptTargetSuite` across Worker restarts.
A successful request means queued work; inspect `completed` and the target's
`/apt/signing-state` before using its signed view. Promotion retains the existing
target security-policy check at enqueue time.

## Publication and recovery

- Promotion verifies package bytes and stages target-owned package metadata.
  Replication first copies/verifies the single immutable `.deb` checkpoint with
  the existing lease heartbeat and retry runtime. Neither path copies source
  `Release`, `InRelease`, `Release.gpg`, `Packages` or by-hash metadata.
- The Worker keeps the existing target suite membership and index scopes, adds
  the selected package, and requests a signature using the target repository ID.
  The configured signer selects its signing identity; different repository keys
  require operator signer routing and client trust configuration.
- Source visibility and quarantine are rechecked before signing and in the final
  transaction. That transaction also checks target quarantine, immutable pool
  paths, deletion barriers, quota, the reviewed target base and the live Worker
  lease. Concurrent target publication produces a conflict instead of losing
  another publication's members. No source/target database row lock spans the
  external signing request.
- A failed signer leaves the previous target view intact. Verified replication
  checkpoints survive retries; resumed bytes are hashed again before staging.
  A committed command receipt shares the APT lifecycle result store. Replaying
  that receipt does not republish an old snapshot or undo subsequent deletion.
- Publication audit evidence records the source repository, pool coordinate,
  digest, target base, command ID and target signer evidence. Source artifact
  intelligence is copied using the existing deferred-copy mechanism; configured
  publication scanning is scheduled for the target as for ordinary publication.

The production Worker configuration uses `apt` with the existing `promotion`
and `replication` worker kinds. Signer absence disables these workers. Migration
`000118_apt_target_signed_distribution.sql` adds APT to the replication format
constraint and persists the target suite. Downgrade requires restoration of a
pre-upgrade backup; old binaries cannot execute APT distribution plans safely.

## Verification

Memory and PostgreSQL/RustFS run the same distribution contract: preservation of
existing target members, target signer identity, replay after deletion, signer
outage/recovery, quarantine/deletion during signing, concurrent target changes,
lease loss, verified checkpoint resume and suite idempotency.

`make native-apt-e2e` installs the source package, promotes and replicates it into
two target repositories, and runs fresh Debian `apt-get update` and installation
against both target `candidate` suites using explicit public-key trust. It then
continues the quarantine, signed lifecycle and archive recovery gates. These are
isolated validation environments, not a production deployment.

## Console workflow

Open the source Hosted APT Repository's **Signed snapshots** tab, enter the source suite and choose **Promote / replicate**. Select a currently visible package from that suite, a different active Hosted APT target and an explicit target suite. The source identity uses the server-returned canonical `poolPath` and SHA-256. Advanced manual input remains subject to the same server admission checks. Both repositories require administrator access, and the API and Workers require signing configuration.

Promotion progress belongs to the target Repository's **Lifecycle jobs** tab. Replication progress appears in the plan list, including its persisted target suite. Failed or uncertain submissions retain the same idempotency key while retrying the unchanged operation in the current form. The Console describes this as an operator preview; public format capabilities and Hosted creation remain unchanged.
