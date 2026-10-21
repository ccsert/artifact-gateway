# OCI Catalog and Referrers discovery

[简体中文](oci-discovery.zh-CN.md) · [Documentation index](README.md)

Gateway supports authorized Catalog enumeration and the OCI 1.1 Referrers API
for Hosted, Proxy and Group read entry points. `/api/v2/formats` advertises
`ociDiscovery.catalog` and `ociDiscovery.referrers`. Those fields describe the
Gateway API; an individual upstream may still be incomplete or unsupported.
`tagFallback` and `signatureVerification` are false. Discovering a signature or
SBOM and verifying its original digests does not establish signer identity,
certificate validity or a trust policy.

## Enumerate authorized names

Authenticate to `GET /v2/_catalog?n=100`. Every returned name includes the
repository or Group prefix and is directly addressable as
`/v2/<returned-name>/manifests/<reference>`. Follow the opaque `Link` unchanged;
the `snapshot` identifies an immutable, principal-bound page set. `n` accepts
1–1000; names are deduplicated and lexically ordered. `gateway.entries` lists
the authorized source repository names in the same precedence used by reads.
Hosted and Proxy members retain the existing Group resolution order.

Catalog enumerates actual upstream Catalog pages, not the local cache-name
list. Within one scan, a source is enumerated once across repository and Group
views. The response header `OCI-Gateway-Discovery-Coverage` and
`gateway.coverage` distinguish `complete`, `partial` and `unsupported`.
`gateway.sources` reports authorized namespace coverage with finite reasons.
No private source names, endpoints, tokens or upstream error bodies are exposed.

An incomplete scan with some valid names returns 200 with `partial`; an
incomplete empty scan returns 502. If every eligible source lacks Catalog,
the result is 501 `UNSUPPORTED`, not a successful empty catalog. An authorized
view with no names is a valid complete empty result. Missing or foreign
pagination links, looping pages, bad names, authorization failures and safety
budgets cannot silently become a complete result.

The cursor binds principal claims, repository configuration, Group membership
and grant versions. Configuration or permission changes return 409
`DISCOVERY_CHANGED`; begin a new Catalog request without the old cursor.
Another principal cannot use the cursor. Revoked grants and quarantined Hosted
images are filtered again before returning a page. New discovery entry points
require explicit administrator, current role or repository-grant authority;
they do not use the older protocol authorization fallback.

## Discover and consume attached content

`GET /v2/<repository-or-group>/<image>/referrers/<sha256-digest>` returns a
verified OCI image index. The subject resolves from the first authorized member
that actually contains it. An absent root can fall through; a present root with
missing children or invalid content fails. A lower-priority member cannot add
its own signatures to an authoritative subject found elsewhere.

Gateway fetches and checks each advertised artifact manifest, its exact subject
descriptor, media type, original size and digest, and every config, manifest
and blob in the content closure. Referrer annotations are copied from the
verified artifact manifest/index; omitted descriptor annotations are filled in,
and conflicting or unverified descriptor values are rejected. `artifactType`
filtering is applied after full validation. Follow the returned `Link` for stable local pages; the filter and
subject cannot be substituted into another snapshot. Unsupported upstream
Referrers APIs return 501; there is no referrers-tag fallback.

The successful source binding is persisted per stable principal, scope, image
and subject. One restricted principal cannot prime another principal's source
choice. Role or downstream credential changes do not change that stable owner.
Reordering a Group preserves the existing authority. Removing its source,
revoking access or changing that repository's configuration/version rejects
the existing binding; it never switches sources. No implicit rebind operation
is provided for an existing subject. Use a new repository identity for an
intentional upstream replacement. These rules also govern later manifest and
blob reads through the same name. A supplied invalid or revoked credential
returns 401 even when that repository permits genuine anonymous reads.

For example, with credentials held in an owned ORAS registry configuration:

```sh
oras repo ls --registry-config "$OWNED_REGISTRY_CONFIG" registry.example.test
oras discover --distribution-spec v1.1-referrers-api --format json \
  --registry-config "$OWNED_REGISTRY_CONFIG" \
  registry.example.test/group/team/image@sha256:<subject>
oras pull --registry-config "$OWNED_REGISTRY_CONFIG" --output ./empty-output \
  registry.example.test/group/team/image@sha256:<artifact>
docker pull registry.example.test/group/team/image@sha256:<subject>
```

## Persistence, budgets and acceptance

Apply migration 000161 through the standard append-only migration runner.
Production explicitly supplies `OCIDiscoveryStore`; an absent port returns 503
instead of silently creating a Memory store. PostgreSQL snapshot and binding
publication checks configuration/grant versions under transaction locks and
commits its audit record atomically. A concurrent source update cannot seal an
old manifest. Stored receipts contain logical cache identities, not secrets.

Snapshots expire after 15 minutes, with at most 32 active snapshots per owner.
Catalog permits 128 namespaces/sources, 128 upstream pages and 10,000 names.
Referrers permit 128 upstream pages and 256 descriptors. Content traversal is
bounded to depth 8, 1024 objects, 16 MiB per manifest/index, 64 MiB of manifests,
64 MiB per blob and 256 MiB total closure. Persisted discovery payloads are
bounded to 8 MiB. Exceeding graph/storage budgets returns an explicit 429.
Existing cache capacity admission, egress policy and credential sealing apply.

Source bindings and snapshots survive a database/client restart. A fresh
verified binding can be consumed from the target cache while its source is
offline; after 15 minutes Referrers discovery requires a fresh source response.
A missing cached object can only be fetched from its bound source. Cache
reclamation or an offline source may therefore make content unavailable, but
cannot substitute another source or a successful empty list. Backup/restore
must preserve the database and the existing cache objects together.

Run `bash scripts/native-oci-discovery-e2e.sh` for the owned acceptance gate.
It uses isolated PostgreSQL/RustFS, applies all migrations twice, pins official
ORAS 1.3.3 and Docker 29.7.2 by archive/index/platform digests, and creates a
fresh labelled Docker daemon for each pull. Basic/Bearer sources, Proxy/Group,
Catalog, recursive discovery, original SBOM bytes, complete OCI-layout closure,
Docker image bytes, restart/offline consumption, source fencing and restricted
prefixes must actually run and pass. All mandatory tests reject skips.
`output/native-oci-discovery-e2e/<project>/` retains exact source hashes, tool
provenance, raw results and owned-resource cleanup. Local acceptance, merged CI
and production operation are separate facts.

Protocol references: [OCI Distribution 1.1.1](https://github.com/opencontainers/distribution-spec/blob/v1.1.1/spec.md),
[Registry Catalog](https://distribution.github.io/distribution/spec/api/#catalog),
[ORAS discover](https://oras.land/docs/commands/oras_discover/).
