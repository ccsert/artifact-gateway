# Conan 2 native publication

[简体中文](conan-native-publication.zh-CN.md) · [Protocol compatibility](protocol-compatibility.md)

Register the canonical remote root `/conan/<repository>`. Conan appends
`/v1/ping` or `/v2/...` itself. Hosted, Proxy and Group use this same layout;
`/conan/v2/<repository>` has no alias and returns 404. Hosted repositories
accept upload; Proxy and Group reject writes. Existing management session,
revision browse, lifecycle and audit APIs remain available. Management and
native publication both require a complete validated revision.

The name `v2` is reserved for Conan repositories and Groups to keep the retired layout unreachable.

```sh
conan remote add releases https://gateway.example.com/conan/releases
conan remote login releases ci -p "$SERVICE_ACCOUNT_CREDENTIAL"
conan create . --user=team --channel=stable -nr
conan upload 'widget/1.0@team/stable#*:*#*' -r releases --confirm
conan list 'widget/1.0@team/stable#*:*#*' -r releases
conan download widget/1.0@team/stable -r releases
conan install --requires=widget/1.0@team/stable -r releases --build=never
```

Use the recipe's real name/version. `agctl repo connect` and Console usage
snippets emit the same canonical root. `agctl` does not reimplement Conan's
upload protocol. For a Group, publish to its Hosted member and install from
the Group's canonical remote instead.

## Authentication and permissions

Set `GATEWAY_CONAN_TOKEN_SECRET` to an independently generated secret of at least
32 bytes, for example the output of `openssl rand -hex 32`. It must differ from
the administrator and resolver login credentials. Keep it private and consistent
across Gateway replicas. Empty configuration leaves other formats available but
returns `501/not_supported` for the official authenticated Conan handshake;
short or reused login secrets fail configuration validation. Connection examples
require this server configuration; a declared publish operation does not supply
missing authentication material.

Conan Basic login accepts a local service-account credential or the configured
resolver credential. It exchanges that credential for a five-minute token bound
to the exact repository or Group ID. The token contains a credential hash, never
the credential itself. Each use checks the source credential's revocation and
service-account state again; credential and signing-key rotation invalidate old
tokens. A token for one remote cannot authorize another. Interactive user
passwords, OIDC browser sessions and standalone API keys are not supported for
this exchange. Upstream credentials and client tokens are never interchanged or
forwarded to a Proxy. Invalid explicit authentication cannot fall back to
anonymous access.

Artifact reads need repository read permission; PUT needs write permission on
the exact recipe/package resource. Discovery, session status and read-back also
need read access. A successful login is not an artifact permission grant.

## Visibility, retry and lifecycle

Each native file PUT declares `X-Checksum-Sha1`; the Gateway spools a bounded body and
verifies its length, SHA-1 and independent storage SHA-256. It opens or resumes
the publisher's durable one-hour recipe/package session using the exact
reference, RREV, package ID and PREV. Every attempt uses a fresh immutable object
key and stages a versioned pointer in the existing session/object-intent tables.
No object is visible through protocol metadata while the revision is incomplete.

The official client sends `conanmanifest.txt` last. The Gateway reads back and
checks the bytes, size and SHA-256 of every staged asset, streams the uncompressed
manifest tree, checks every declared MD5, derives RREV/PREV and binds the package
ID to `conaninfo.txt`. It then commits the immutable revision, session state and
publication audit for the actual commit caller in one metadata transaction.
The session version fences any
concurrent pointer change. Identical retries keep one visible identity; changed
bytes at an existing identity return 409. Missing or wrong assets/manifest and
identity mismatch return 422. Permission denial does not expose a revision.

Management publication retains its HTTP request and response shapes:
`POST /api/v2/repositories/{id}/conan-publish-sessions` declares the complete
recipe or package file set with exact SHA-256 and size;
`PUT /api/v2/conan-publish-sessions/{id}/objects/{name}` verifies and stages
each declared object under a fresh immutable key; and
`POST /api/v2/conan-publish-sessions/{id}:commit` validates the full staged
snapshot before visibility. Both entry points use the same manifest, asset
and identity checks and the same version-fenced atomic revision/session/audit
commit. The audit actor is the authenticated commit caller, even when an
authorized caller differs from the session creator. A successful create or
object PUT does not publish a partial revision. Legacy declarations with
non-hash identities, missing manifest or incomplete assets are rejected.

After an interrupted upload, rerun the original `conan upload` with the same
cache, identity, target and credential. An open session resumes across Gateway,
PostgreSQL and S3 service restarts. An expired session is fenced and a retry
receives fresh staging keys. A failed commit leaves the revision invisible and
retains the open session; quota 507 can be retried after capacity is available.
Group/Proxy writes return 405 for an authorized caller. Malformed upload paths
or unsupported upload files return 400; unsupported handshake methods return 404.

Existing capacity accounting, asynchronous scan-on-publish and recipe-revision
quarantine closure apply. Quarantining a recipe also prevents a package append;
release permits a valid retry. Retention works only on visible revisions;
unexpired open-session objects are excluded from collection, abandoned intents
become reclaimable after the collector's grace period, and committed visible
references retain bytes. No new storage backend or second publication system is
introduced. Existing promotion, replication, tombstone/restore and collection
contracts still apply to complete revisions.

Successful PUTs return `X-Artifact-Gateway-Publish-Session` and
`X-Artifact-Gateway-Publish-State` (`open` or `committed`). The management session
GET can inspect that same session. Management object PUT/commit cannot mutate a
native session or bypass its manifest validation. Console artifact views show
only committed revisions. Expanding a Conan `publish` audit shows its persisted
publication session UUID and localized `committed` state alongside the existing
coordinate and machine actor. Other evidence, including the digest, remains
available through the audit API. Missing or malformed publication evidence is
shown as unavailable. No new Console upload form is claimed.

## Supported boundary and verification

The pinned official client is Conan **2.21.0**. This slice supports its default
hash revision workflow: lowercase 32-hex RREV/PREV, 40-hex package ID, recipe
`conanfile.py` or `conan_export.tgz`, optional `conan_sources.tgz`, package
`conaninfo.txt` plus `conan_package.tgz`, and the required manifest. Limits are
1 GiB per upload and combined expanded tree (including tar framing), 1 MiB per
manifest/conaninfo, and 4096 manifest entries and archive entries. Archives are
validated without extracting or executing content. Unsafe links, traversal,
duplicates, truncated gzip/tar, cancellation and unsupported entry types fail
closed. Conan 1, SCM revisions, optional metadata uploads, alternative archive
compression and remote index federation are not implemented.

`make conan-e2e` provisions a private pinned client and unique PostgreSQL/RustFS
Compose project with per-run loopback ports. It runs real `create/upload/list/
download/install` against Memory and PostgreSQL/S3, cuts an upload connection,
restarts both persistence services before retry and again before read, verifies
the producer's local-cache RREV/package ID/PREV and captures its exact seven upload inputs before the Gateway. It compares each complete HTTP/S3 object and metadata size/SHA-256/identity to those independent producer inputs, checks
publish audits and scan scheduling, then performs an empty-cache target-only
install through a Group. The script requires the named fixtures to pass (skips
cannot produce green) and removes its processes, credentials, containers, volumes
and network. No production or external Nexus is accessed.

Unit/storage contracts cover authentication, quotas, immutable conflict, concurrent
publishers, snapshot fencing, expiry and open-session GC retention. The existing
management publication/read and lifecycle tests remain regression coverage.
Public HTTP and Memory/PostgreSQL contracts also cover ordinary management
sessions racing with native sessions: invalid complete byte declarations
cannot publish a mismatched manifest, expose metadata or replace staged bytes;
audit actor and session snapshot checks apply to both paths.
Evidence is local to the tested source tree; CI and a merged/released head are
separate gates. kkRepo commit `2d0ce09290dbd9a2f47a5d4df7a373337a503c36`
`ConanService`/`ConanLeaseManager`/manifest policy and its black-box tests informed
the lifecycle review; source/fixture inspection is not a claim of running kkRepo.
