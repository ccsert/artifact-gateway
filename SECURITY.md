# Security Policy

[简体中文](SECURITY.zh-CN.md) | [Documentation index](docs/README.md)

Artifact Gateway handles authentication credentials, repository permissions,
untrusted package metadata, and executable artifact bytes. Security reports
must therefore be handled privately until a fix and disclosure plan exist.

## Reporting a vulnerability

Do not open a public issue for a suspected vulnerability. Use the repository
host's private vulnerability-reporting channel when it becomes available, or
contact the maintainers through the private project channel used by the core
team.

Include, when possible:

- the affected revision or deployment version;
- the affected protocol, API route, or component;
- prerequisites and reproducible steps;
- the security impact and expected trust-boundary violation;
- logs or a minimal proof of concept with credentials and artifact contents
  removed.

Do not test against systems or data you do not own. Do not include live tokens,
passwords, private package contents, or personal data in a report.

## Response process

The maintainers will validate the report, identify affected revisions, prepare
tests and a fix, and coordinate disclosure after operators have a practical
upgrade path. Acknowledgement and remediation timelines are not published yet
because the project has not started a public support program.

## Current security baseline

- Production traffic must terminate TLS at the Gateway or a trusted reverse
  proxy. Plain HTTP examples in this repository are for local development.
- Replace every placeholder secret in `.env.example` and Compose examples.
- Keep `GATEWAY_ADMIN_TOKEN`, resolver tokens, API keys, database credentials,
  object-store credentials, and `GATEWAY_EGRESS_PROXY_KEY` in a secret manager.
- Use Service Accounts for CI and third-party applications. Bind Repository
  Grants to the stable account principal, rotate its one-time credentials with
  an overlap window, and disable the account to reject every credential during
  incident response. See `docs/service-account-operations.md`.
- Run database migrations as a separate deployment job and do not grant normal
  Gateway nodes schema-owner credentials.
- Restrict Proxy upstream hosts and outbound network access. Per-repository
  custom proxy passwords require `GATEWAY_EGRESS_PROXY_KEY`.
- Treat uploaded packages and metadata as untrusted input. Artifact Gateway
  does not provide malware scanning. Vulnerability, license, and SBOM analysis
  is available only when an operator explicitly enables the bundled reference
  scanner or a contract-compatible external scanner.
- Expose operational and management routes only to intended networks and
  identities. Worker-only and scheduler-only nodes intentionally expose only
  liveness, readiness, and metrics endpoints.
- Review audit records after Service Account or credential changes, permission,
  anonymous-access, retention, deletion, promotion, and replication changes.

Run the repository's dependency audit before a release candidate:

```sh
make dependency-audit
```
