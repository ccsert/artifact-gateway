# Webhook Delivery Contract

Status: normative contract for durable operational webhook delivery.

## First Slice

The first slice publishes the two security-governance transitions that already
have a versioned Repository-local identity:

- `artifact.quarantined`
- `artifact.released`

The Artifact Quarantine state transition, immutable event, and one delivery
per matching enabled subscription commit in the same database transaction.
No audit-log poller is allowed to reconstruct these events after the fact.

Each event carries a stable UUID and this immutable data:

- Repository ID and format
- canonical Artifact coordinate and digest
- quarantine state, reason, actor, and version
- event occurrence time

Delivery is **at least once**. Consumers must deduplicate by event ID. Ordering
between different Artifacts or subscriptions is not guaranteed.

## Subscriptions

Webhook subscriptions are global administrator-managed resources. A
subscription has a unique name, HTTPS endpoint, non-empty event-type filter,
enabled flag, and optimistic-concurrency version. The signing secret is
accepted on write, encrypted with `GATEWAY_SETTINGS_ENCRYPTION_KEY`, and never
returned. Responses expose only `secretConfigured`.

The endpoint must be an HTTPS URL without user information or a fragment.
Production delivery rejects private, loopback, link-local, unspecified, and
multicast destinations and pins the validated address for the connection.
Redirects are never followed. Tests may inject a local TLS client without
weakening production validation.

Disabling a subscription stops creation of new deliveries. Already-created
deliveries remain inspectable and retryable; administrators may explicitly
replay a dead delivery after correcting the endpoint or subscription.

## Envelope And Signature

Webhook requests use `POST application/json` with this envelope:

```json
{
  "id": "event UUID",
  "type": "artifact.quarantined",
  "occurredAt": "RFC3339 timestamp",
  "data": {}
}
```

Requests include:

- `X-Artifact-Gateway-Event-ID`
- `X-Artifact-Gateway-Event-Type`
- `X-Artifact-Gateway-Timestamp` (Unix seconds)
- `X-Artifact-Gateway-Signature: v1=<hex HMAC-SHA256>`

The signature input is `<timestamp>.<exact request body>` using the decrypted
subscription secret. A 2xx response completes the delivery. Every other
status or transport error is retried with bounded exponential backoff. Each
claim owns a lease so another Worker can resume after process failure.

## Retry And Replay

- Maximum attempts: 8
- Initial retry delay: 5 seconds
- Maximum retry delay: 1 hour
- Default delivery lease: 30 seconds
- Response bodies are ignored and never persisted
- Persisted errors are bounded and must not contain secrets or response bodies
- Attempt 8 transitions the delivery to `dead`
- Explicit replay changes only `dead` deliveries back to `pending`, clears the
  attempt count/error/status, and preserves the event ID

## Management Surface

The management API exposes administrator-only subscription list/create/get/CAS
update and delivery list/get/replay operations. Delivery responses include
event identity, state, attempts, next attempt, HTTP status, bounded error, and
timestamps, but never the signing secret or event payload credentials.

Console Operations renders subscriptions and recent deliveries, including an
explicit replay action for dead deliveries.

Dedicated worker nodes may select only this workload with
`GATEWAY_NODE_ROLES=worker GATEWAY_WORKER_KINDS=webhook`. Webhook delivery is a
global job kind and ignores `GATEWAY_WORKER_FORMATS`.
