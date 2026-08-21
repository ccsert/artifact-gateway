# APT Hosted Signing And H3 Rotation Preview

[简体中文](apt-hosted-signing.zh-CN.md) | [Documentation index](README.md)

APT Hosted H2 is an operator preview for validating the complete signed
publication path. It is installable by Debian clients, but it is not a
production signing service. H3 must add managed key custody, rotation,
revocation, recovery drills, metrics, and alerts before the format is
advertised. The external HTTPS and client-rotation drill is now executable;
managed KMS/HSM custody and key recovery remain open.

## Local deployment

Generate a random bearer token of 32 to 256 bytes and set these values in the
local `.env` without committing them:

```text
COMPOSE_PROFILES=apt-signer
GATEWAY_APT_SIGNER_ENDPOINT=http://127.0.0.1:18083/v1/sign-release
GATEWAY_APT_SIGNER_TOKEN=<random-token>
GATEWAY_APT_SIGNER_TIMEOUT=15s
```

Then start the normal local stack. The signer shares Gateway's network
namespace, listens only on loopback, and stores its RSA private key in the
`gateway-apt-signer` volume mounted only into the signer container. Gateway
receives signed bytes and public identity only; it never reads the private key.

The signer public key is available inside the shared network at
`http://127.0.0.1:18083/v1/public-key`. Export it with the health-check binary
or copy it from a trusted operator channel before configuring an APT client.
Automatic trust installation is intentionally absent.

## Publication sequence

1. Provision `format: apt, type: hosted` with `POST /api/v2/repositories`.
2. Reserve one immutable package with
   `POST /api/v2/repositories/{repositoryId}/apt/publication-sessions` and an
   `Idempotency-Key`.
3. Upload the exact `.deb` to the returned session's `/package` endpoint using
   `Content-Type: application/vnd.debian.binary-package`.
4. Publish one suite snapshot with
   `POST /api/v2/repositories/{repositoryId}/apt/snapshots`, another stable
   `Idempotency-Key`, and this body:

```json
{
  "suite": "stable",
  "sequence": 1,
  "publicationSessionIds": ["<staged-session-id>"]
}
```

The package remains invisible until step 4 atomically commits `Release`,
`InRelease`, `Release.gpg`, direct and by-hash indices, and every referenced
`pool/` package. Exact retries return or resume the same snapshot. A changed
body under the same key returns `idempotency_conflict`; signer outages return
`signer_unavailable`; capacity failures return `quota_exceeded` and can be
retried with the same key after the operator raises the quota.

## Verification

Run `make native-apt-e2e`. The gate builds a real `.deb`, publishes it through
the generated management contract, imports the reference public key in a clean
Debian container, runs `apt-get update`, and installs the package. It then
captures the complete immutable evidence for `Release`, `InRelease`,
`Release.gpg`, direct and by-hash package indices, and the referenced `.deb`;
backs up PostgreSQL and RustFS; publishes a different snapshot; and restores
the backup. The restored signing-state object and every captured byte digest
must exactly match the original snapshot. Finally, the gate stops the signer
and repeats update and installation from the restored immutable snapshot.

This recovery gate proves repository metadata and object restoration. The
reference signer's private-key volume is deliberately outside the
PostgreSQL/RustFS backup, so managed key custody, key backup, and key recovery
remain production H3 responsibilities.

Run `make apt-signer-rotation-e2e` for the external boundary. The gate
pre-provisions two private keys into signer-owned volumes, starts two separate
signer services with those volumes mounted read-only, and connects Gateway over
TLS using a signer-specific CA bundle. A separate init container validates the
certificate/key pair and copies it into a signer-owned volume; the serving
containers receive that volume read-only, so the drill has the same UID and
mount semantics on Linux and macOS. It then publishes with the old key,
enters an old/new trust overlap, switches the external signer, verifies that an
old-key-only Debian client rejects the new snapshot, retires the old key, and
installs again with only the new public key. Gateway never mounts either
private-key volume.

This is a production-shaped local rotation and client-trust drill, not a claim
that the reference signer is a managed KMS or HSM. The generated keys and
short-lived CA are test fixtures and are deleted with the isolated Compose
project.

The Console artifact tab and repository search show only the current visible
snapshot. Capacity includes staged package revisions and deduplicated generated
metadata for the current visible snapshot; direct and by-hash names pointing to
the same object are counted once.

## Production boundary

Do not reuse the reference signer's locally generated private key as a
production root of trust. Do not publish its token or volume. A production H3
adapter must expose the same narrow digest-bound signing protocol over HTTPS
while keeping private-key operations in a managed KMS, HSM, or equivalent
audited signer.

Remote HTTPS signers are fail-closed unless
`GATEWAY_APT_SIGNER_TRUSTED_FINGERPRINTS` contains their OpenPGP fingerprint
and `GATEWAY_APT_SIGNER_TRUSTED_PUBLIC_KEYS_FILE` points to a bounded, armored,
public-only keyring containing exactly the same one or two keys. Gateway verifies
both `InRelease` and `Release.gpg` against this keyring and requires the verified
signing entity to match the reported fingerprint. The persisted signer identity,
signing-key algorithm, and primary-key fingerprint are derived from the verified
OpenPGP packets and trusted entity rather than accepted from the HTTP response.
The parser accepts exactly one public-key armor block and rejects private keys,
additional armor blocks, and trailing data. Remote signatures must use RSA with
SHA-256 and a 2048- to 4096-bit signing key. The fingerprint setting
accepts one active 40- or 64-hex-character fingerprint and one optional next
fingerprint for a controlled rotation overlap. It never changes client trust
automatically.

For a signer whose server certificate chains to a private CA, mount the bounded
PEM bundle read-only and set `GATEWAY_APT_SIGNER_TLS_CA_FILE`. Gateway builds a
dedicated TLS trust pool for only the signer client; it does not change
process-wide roots. Public-CA signers leave this setting empty.

A production rotation uses this order:

1. distribute and verify the next public key through the operator-owned trust
   channel;
2. mount a public keyring containing `old,new`, configure the matching trusted
   fingerprints, and restart Gateway;
3. rotate the external signer and confirm a newly published snapshot reports
   the new fingerprint in the immutable publication response and audit;
4. remove the old fingerprint only after the client overlap window closes.

The `reference-apt-signer-keyring` helper merges one or two independently
exported public keys into the single public-only armor block required by the
Gateway trust policy and can print their canonical comma-separated
fingerprints. It never accepts or emits a private key.

An unlisted signer key is rejected before snapshot visibility changes. The
loopback reference signer may omit this setting because it remains an H2 test
fixture. Configuration loading reads and validates the complete public-only
keyring against the configured fingerprint set before preflight can pass;
preflight diagnostics and startup logs expose only the validated key count so
an unpinned deployment is not mistaken for H3 production readiness.

## Operator state and metrics

Repository administrators can inspect
`GET /api/v2/repositories/{repositoryId}/apt/signing-state` or open the
repository Security tab. The response contains only the signer mode, the one or
two public fingerprints, and evidence copied from the latest visible immutable
snapshot. It never returns the signer endpoint, bearer token, public-key file
path, or private-key material.

The readiness values are operationally significant:

- `unconfigured` means no signer can publish a new snapshot;
- `fixture` means the loopback H2 reference signer is active and must not be
  treated as production trust;
- `ready` means one remote fingerprint is pinned as the active trust key;
- `rotation_overlap` means the old/next two-key window is active;
- `policy_mismatch` means the trust policy is incomplete or the latest visible
  snapshot was signed by a key outside the current pins. Publication is already
  fail-closed for an untrusted new signature, but operators must resolve this
  state before the next release.

`/metrics` exports bounded process-wide counters and a signing latency
histogram:

```text
artifact_gateway_apt_signing_requests_total{outcome="success|untrusted_signer|invalid_signature|unavailable"}
artifact_gateway_apt_signing_duration_seconds_bucket
artifact_gateway_apt_signing_duration_seconds_sum
artifact_gateway_apt_signing_duration_seconds_count
```

The labels deliberately exclude repository, actor, endpoint, and fingerprint.
A deployment may start with alerts equivalent to these PromQL expressions,
tuning the window and threshold to its release frequency:

```promql
sum(increase(artifact_gateway_apt_signing_requests_total{outcome!="success"}[15m])) > 0
histogram_quantile(0.95, sum by (le) (rate(artifact_gateway_apt_signing_duration_seconds_bucket[15m]))) > 10
```

The first alert should page during an active release window; the second is a
warning for signer or HSM latency. Alert installation remains deployment-owned
until the project ships a supported monitoring stack.
