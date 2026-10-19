# npm version governance

[简体中文](npm-version-governance.zh-CN.md) · [Documentation index](README.md)

Hosted npm supports the native npm 11.6.2 deprecation and unpublish protocol.
These operations change registry metadata, never the published tarball,
manifest, digest, size, publisher or publication time. Proxy and Group are
read-only. There is no Nexus endpoint or alternate publication database.

## Client operations and permissions

Use a path-scoped npm registry and credential, with no fallback registry:

```sh
npm deprecate 'widget@1.0.0' 'Use 1.1.0'
npm deprecate '@scope/widget@>=1.0.0 <2.0.0' 'Use 2.0.0'
npm deprecate 'widget@1.0.0' ''
npm unpublish 'widget@1.0.0' --force
npm unpublish '@scope/widget' --force
```

Deprecation requires Repository `write`; unpublish and management restore
require Repository `admin`. Write inherits read but does not grant deletion.
Native package mutations evaluate the package resource prefix. Restore uses
the existing Repository administration boundary. Pending, reset-required,
ungranted and revoked identities remain denied before mutation. Anonymous
reads retain global and local policy; anonymous writes remain denied.

npm selects exact versions and SemVer ranges itself, including prereleases
according to that client's rules. The registry validates the resulting full
document against its committed snapshot; it does not reinterpret ranges.
Warnings are plain text, limited to 4096 UTF-8 bytes. An empty string clears a
warning. A metadata document or whole-package removal is bounded to 1000
versions; invalid or unknown targets reject the entire transaction.

Console shows the selected version's warning to readers. Hosted writers can
edit or clear it; administrators can unpublish that version. Failed mutations
retain the package and draft. An incomplete successful response is an unknown
outcome: read current state before deciding whether to submit again. Proxy and
Group details show warnings without mutation controls.

## Revisions, concurrency and audit

`GET /npm/<repository>/<package>` includes the Hosted package `_rev`. Metadata
and versions come from one database snapshot. Publication, dist-tags,
deprecation, tombstone and restore serialize through the same package
coordination and advance its revision when state changes.

| Native request | Effect |
| --- | --- |
| `PUT /npm/<repository>/<package>` without attachments | Deprecate or clear warnings; all other visible version fields and tags stay unchanged |
| `PUT /npm/<repository>/<package>/-rev/<revision>` | Tombstone removed versions and atomically update retained tags |
| `DELETE /npm/<repository>/<package>/-rev/<revision>` | Tombstone every remaining version and remove tags |
| `DELETE /npm/<repository>/<package>/-/<tarball>/-rev/<revision>` | Acknowledge npm's cleanup of an already-tombstoned identity; retain bytes |

Path/body revisions must agree. A stale request returns `409`; it cannot
overwrite a concurrent publish, tag move, delete, restore or warning update.
After a lost response, a stale replay also returns `409`. Re-read the packument
to learn whether the first request committed. A fresh no-op warning update is
audited without advancing the revision. npm itself treats a registry `404` on
unpublish as already absent; authorization failures remain `401`/`403`.

Successful `npm.deprecate`, `npm.unpublish` and `artifact.restore` audit records
commit with their metadata. They preserve the actual actor, version/digest
scope, standardized request reason and revision evidence. A failed audit
insert rolls back the mutation. The post-unpublish acknowledgement is audited
as `npm.unpublish.cleanup`; it is not a physical-delete event. Authentication
and grant refusals carry their authorization source and reason without tokens.

## Visibility, recovery and reclaim

Packument, exact-version, tarball, dist-tag and search reads hide tombstones.
A higher-priority Hosted tombstone remains an authority barrier even after its
last visible version disappears: lower Hosted or Proxy members cannot revive
that version through a Group. Restoring the same identity removes its barrier.
Quarantine decisions remain attached to the same immutable identity.

Restore through the existing Console Tombstones task or
`POST /api/v2/repositories/<id>/restore` with `{"coordinate":"widget@1.0.0"}`.
Restore verifies retained object size/digest, takes the object lease, and
rechecks the collection fence before returning visibility. It preserves the
warning and immutable identity. If `latest` is absent it points to the restored
version; removed custom tags are not recreated. Use `npm dist-tag` explicitly
when a different pointer is needed.

Unpublish is logical removal. Existing retention and the minimum 24-hour
reclaim grace remain in force. A visible shared reference prevents byte
collection. Before deleting S3 bytes the collector persists a `collecting`
fence under the object lease. Restore fails closed from that point, including
when byte deletion succeeds but its final metadata update fails. Retrying the
lifecycle job finishes collection; no missing-byte version can be restored.
Collected identities remain reserved and cannot be republished.

## Persistence and verification

Forward migration `000149_native_npm_metadata_governance.sql` adds package
revisions, mutable warning metadata and the collection fence. It backfills
warnings from existing manifests without rewriting publication identity or
tarballs. Metadata-only changes neither upload bytes nor consume new byte
quota. Restore retains the existing quota and lease admission. No new secret,
upstream credential forwarding or signing key is introduced.

Gateway composition injects native metadata mutation and management restore
through separate narrow ports. The collector receives its own collection fence
port. New operations do not expand the frozen GatewayStore aggregate; its old
restore signature is retired without an alias.

- Shared Memory/PostgreSQL contracts cover atomic selection, no-op/stale
  replay, immutable identity, tombstone/tag consistency, restore and shared
  references. Two PostgreSQL instances test concurrent CAS and audit rollback.
- The upgrade test applies the complete pre-149 schema, seeds a publication,
  applies 149 and rebuilds the store to verify its warning and unchanged
  manifest/time/digest/object identity.
- `make native-npm-e2e` verifies the fixed SHA-512 of official npm 11.6.2. Its
  PostgreSQL/RustFS gate exercises scoped/unscoped exact/range warnings,
  empty-cache install and clearing, writer refusal, single/whole unpublish,
  Group authority, fresh instances and management restore. All four complete
  tarball size/SHA-256 identities are checked before and after governance.
- HTTP, lifecycle and PostgreSQL/S3 tests cover unavailable bytes, cancellation,
  stale responses, permission revocation and partial reclaim recovery. Console
  tests cover retained drafts, unknown outcomes, escaped warnings, native
  writes and desktop/narrow light/dark geometry; the full Playwright suite is
  the Console gate.

The reference comparison is kkRepo's fixed
[`2d0ce092` npm Hosted service](https://github.com/klboke/kkRepo/blob/2d0ce09290dbd9a2f47a5d4df7a373337a503c36/server/src/main/java/com/github/klboke/kkrepo/server/npm/NpmHostedService.java).
Its revision-based metadata/delete paths informed protocol coverage; Gateway
retains its own tombstone, immutable identity, quota and lease contracts.
These tests are isolated local/CI evidence, not a production cutover or a
promise to reproduce npmjs.com's account and time-window policy.
