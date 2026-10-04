# Administrator test email delivery

[简体中文](email-notification-delivery.zh-CN.md) | [Documentation index](README.md)

The approved small-deployment direction is a finite built-in channel: Gateway owns
versioned email templates and a bounded SMTP sender; PostgreSQL owns durable
recipient snapshots, test delivery state, idempotency and worker fencing.
Prometheus/Alertmanager remains useful for external availability monitoring, but
adds services and routing configuration. A stopped Gateway or database cannot
report its own outage through this channel. See [#211](https://github.com/ccsert/artifact-gateway/issues/211)
and [#212](https://github.com/ccsert/artifact-gateway/issues/212).

This slice implements **explicit administrator synthetic test mail**. Capacity
and 5xx observations do not enqueue mail. Alert evaluation, routing and Console
pages remain future work. [#229](https://github.com/ccsert/artifact-gateway/issues/229)
and [#230](https://github.com/ccsert/artifact-gateway/issues/230) remain broader than
this slice; browser screenshots are not Gmail, Outlook or Apple Mail acceptance.

## Deployment contract

No config file means disabled. `GATEWAY_EMAIL_CONFIG_FILE` is an absolute path to
an operator-managed JSON file. It is read offline at startup, with unknown keys
rejected. A synthetic documentation example:

```json
{
  "enabled": true,
  "host": "smtp.example.test",
  "port": 465,
  "mode": "implicit_tls",
  "from": "gateway@example.test",
  "approvedIPs": ["192.0.2.10"],
  "consoleOrigin": "https://console.example.invalid"
}
```

These addresses are examples, not a usable relay. Enabling a real relay and
recipients requires a separate deployment decision. The management API cannot
supply relay addresses, authentication, headers or message bodies.

- `mode` is `implicit_tls` or `starttls_required`. TLS 1.2 or later, certificate
  chain and hostname verification are required. There is no plaintext fallback.
- Every resolved address must match `approvedIPs`; dialing uses the approved IP
  directly while TLS verifies `host`. Explicit private relay IPs are allowed.
  Loopback, link-local/metadata, multicast and unspecified addresses are denied.
  DNS changes require an operator update and restart.
- Optional `caFile` is an absolute path to private PEM trust roots. Optional
  `authFile` is an absolute path to a regular JSON file with `username` and
  `password`, readable only by its owner (mode 0600 or stricter). Authentication
  is sent only after verified TLS. Secret contents are never API fields or logs.
  Rotate the file atomically; workers reread it per attempt.
- Configure `GATEWAY_SETTINGS_ENCRYPTION_KEY` through existing deployment secret
  management. Recipients are AES-GCM encrypted with target-specific associated
  data. Keep the encryption key stable while queued snapshots exist.
- Optional `consoleOrigin` must be an explicit HTTPS origin, without credentials,
  query or fragment. The server appends `/system?tab=diagnostics`. No request Host
  or arbitrary event URL is used; absent configuration omits the link.
- Workers need the same relay, encryption key and trust configuration as API
  nodes. A dedicated worker uses `GATEWAY_NODE_ROLES=worker` and
  `GATEWAY_WORKER_KINDS=email`; empty kind filters include email workers.

## Administrator API

All endpoints require a platform administrator and reject forced-password-change
sessions. Responses use `Cache-Control: no-store`.

| Endpoint | Behavior |
| --- | --- |
| `POST /api/v2/email-notifications:preview` | Offline fixed `scenario` and `locale`; returns subject, HTML, text and template version; never sends |
| `GET /api/v2/email-notifications` | Safe readiness (`ready`, `email_disabled`, `encryption_key_unavailable`) |
| `GET/POST /api/v2/email-targets` | List/create one-recipient targets; default disabled; recipient is write-only |
| `GET/PUT /api/v2/email-targets/{targetId}` | Read/update; `If-Match` protects updates; omitted recipient retains ciphertext |
| `POST /api/v2/email-notifications:test` | `targetId`, `scenario`, target `If-Match` and UUID `Idempotency-Key`; queues a synthetic delivery and returns 202 |
| `GET /api/v2/email-deliveries` | Newest safe statuses, `limit` 1–100 (default 50) |
| `GET /api/v2/email-deliveries/{deliveryId}` | Safe status and version |
| `POST /api/v2/email-deliveries/{deliveryId}:replay` | Dead deliveries only, `If-Match`, enabled unchanged target and relay |

Locales are `en` and `zh-CN`; scenarios are `warning`, `critical`, `resolved`.
Unknown fields and header/address injection are rejected. Targets expose only
`recipientConfigured`, never the address/ciphertext. Statuses expose fixed error
codes, attempts and times, never SMTP replies, hosts, credentials or bodies.
Target changes and explicit tests/replays are audited using IDs only.

Same idempotency key and descriptor return the original record. Reusing a key
with another target/version/scenario returns 409. PostgreSQL atomically applies
a global five new tests/replays per rolling minute and 1,000 live-delivery cap;
duplicates do not consume another slot. Version conflicts return 412, disabled
targets or non-dead replay 409, rate limits 429, disabled relay/missing key or full
queue 503. Replaying preserves event ID, encrypted recipient, content descriptor
and possible-duplicate warning. A changed target requires a new explicit test.

## Delivery semantics

States are `pending`, `delivering`, `retrying`, `accepted`, `dead`. Each claim
uses database time, `FOR UPDATE SKIP LOCKED`, a new fencing token and a 30-second
lease. Worker session identity changes on restart. An expired claim can be
reclaimed; an old token cannot complete it. One attempt has a total 10-second
SMTP deadline within a conservative 25-second budget measured from the local
monotonic start of claiming; database wall-clock time is not a local deadline.

There are eight attempts, with 5-second exponential backoff capped at one hour.
Network failures and SMTP 4xx retry; SMTP 5xx and invalid content are terminal.
TLS/authentication errors use fixed codes and bounded attempts. Target disable
or version change observed before sending makes the delivery dead. Already
in-flight mail may finish; disabling cannot recall it.

A positive final DATA reply means **SMTP accepted**, not inbox delivery/read.
Losing that reply after sending DATA is `outcome_unknown`: retry may duplicate
mail. Expired in-flight claims are also conservatively marked
`possibleDuplicate`. The warning is sticky through retries and replay. MIME
uses a stable Message-ID, timestamp and deterministic boundary for the immutable
version-1 synthetic descriptor, including persisted From and Console-origin
snapshots. Operator transport reconfiguration does not change its content. This
is not exactly-once delivery.

The HTML uses escaped semantic values, inline critical styles, presentation
tables, a 600px maximum width and narrow-screen/dark-mode enhancements. Each
severity has text and color; multipart/alternative contains equivalent UTF-8
plain text and HTML. There are no external images, fonts, tracking pixels or
attachments. Template version 1 must remain available for queued replay.
Terminal history remains in PostgreSQL; retention/archival policy is follow-up
work. Queries and active backlog are bounded; this slice is not a general mail
or monitoring platform.

## Validation and next slice

Tests exercise the public HTTP, store and sender boundaries using isolated
PostgreSQL and owned TLS SMTP sinks. Fixtures never relay mail. Restart recovery,
concurrent global rate limits, worker fencing, retry exhaustion, safe replay,
TLS trust/downgrade failures, actual localized multipart MIME and administrator
permissions are covered. `make integration-test` includes these tests.

Next, add a separate finite capacity evaluator and immutable real events in one
transaction with delivery creation. Repository quotas use logical bytes and a
configured quota; explicit local mounts use process-visible available bytes.
S3/NAS pool capacity stays unknown. Sustained breach, recovery hysteresis,
fresh/unknown data, cooldown and shared evaluator ownership must be validated
before enabling real alerts. The existing [operational signals](operational-signals.md)
and [#211](https://github.com/ccsert/artifact-gateway/issues/211) own that work.
