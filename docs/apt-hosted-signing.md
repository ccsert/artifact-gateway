# APT Hosted H2 Signing Preview

APT Hosted H2 is an operator preview for validating the complete signed
publication path. It is installable by Debian clients, but it is not a
production signing service. H3 must add managed key custody, rotation,
revocation, recovery drills, metrics, and alerts before the format is
advertised.

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
Debian container, runs `apt-get update` and installs the package, stops the
signer, and repeats update and installation from the already published
immutable snapshot.

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
