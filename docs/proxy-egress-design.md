# Proxy Repository Egress Proxy Design

[简体中文](proxy-egress-design.zh-CN.md) | [Documentation index](README.md)

Status: implemented (2026-08-06). Migration `000070_egress_proxy.sql`, the
`internal/egress` transport factory, the V2 management API (including the
`:test` endpoint), and the Console settings form are in place. Audit
`egress_mode` fields from the original proposal were deferred; existing
`upstream_error` outcomes cover the failure signal for now.

## Problem

At the time of this proposal, only the Raw proxy path had explicit egress handling
(`internal/app/raw.go`): it honors `http.ProxyFromEnvironment`, and when no
environment proxy applies it pins the dial to DNS answers that passed the
private-address check. OCI, Maven, and Conan upstream fetches fall back to
the default transport. Consequences:

- Egress proxy policy is process-wide (env vars), not per repository.
  One gateway instance cannot route `maven-central` through a corporate
  HTTP proxy while reaching an internal OCI registry directly.
- SOCKS5 is unsupported: `http.ProxyFromEnvironment` only handles HTTP
  proxies, and `ALL_PROXY` socks5 URLs are not honored by the standard
  library transport.
- SSRF hardening is asymmetric. The private-address check and DNS pinning
  exist only for Raw; the other formats rely on host allowlists alone.
- Operators cannot see which egress path a Proxy Repository uses, and
  upstream health/circuit status does not distinguish proxy failures from
  upstream failures.

## Goals

- Per-Proxy-Repository egress configuration managed through the V2
  management API and the Console Settings tab.
- Support `direct`, `environment` (current behavior), and `custom` modes;
  `custom` supports `http` (CONNECT) and `socks5` (remote DNS, a.k.a.
  socks5h) protocols with optional username/password authentication.
- One shared egress transport factory used by all four formats.
- Preserve existing security invariants: upstream host allowlists still
  apply to the *target* host, and the proxy address itself must not
  resolve to a private address unless explicitly allowed for testing.
- Credentials never appear in API responses, audit records, metric labels,
  or logs.

## Non-Goals

- Per-request client-supplied proxy routing.
- PAC file evaluation.
- Proxying Hosted Repository reads (they serve local bytes) or replication
  destinations (covered by a separate design if needed).

## Data Model

`HostedRepository` (and the legacy Group `Member` proxy shape) gains an
optional `egressProxy` object:

```json
{
  "egressProxy": {
    "mode": "custom",
    "protocol": "socks5",
    "host": "proxy.corp.example",
    "port": 1080,
    "username": "gateway",
    "password": "AES-GCM ciphertext, base64",
    "remoteDns": true,
    "noProxy": ["*.internal.example", "10.0.0.0/8"]
  }
}
```

- `mode`: `direct` | `environment` | `custom`. Default `environment`
  preserves current behavior.
- `protocol`: `http` | `socks5` (custom mode only).
- `password`: decided — **encrypted at rest**. The API accepts a plaintext
  password over TLS, the server encrypts it with AES-256-GCM using a key
  from `GATEWAY_EGRESS_PROXY_KEY` (32-byte, env-injected; a KMS envelope
  can replace it later without a schema change), and stores only the
  base64 ciphertext. The management API never returns plaintext or
  ciphertext; responses carry `credentialsConfigured: true` instead.
- `remoteDns`: decided — **optional, default `false`**. `false` resolves
  the upstream hostname locally (and applies the private-address check)
  before dialing through the SOCKS5 proxy with an IP literal; `true` sends
  the hostname to the proxy (socks5h semantics) for networks where local
  DNS cannot resolve the upstream. HTTP CONNECT always sends the hostname
  and is unaffected by this flag.
- `noProxy`: suffix/CIDR list evaluated against the *upstream* host, not
  the proxy host.

Storage: new JSONB column `egress_proxy` on the repository table via a
forward migration; `NULL` means `environment`. Optimistic concurrency via
the existing `version` field covers updates.

## Transport Factory

New package `internal/egress` (or a section of `internal/app`):

```go
type Config struct {
    Mode     Mode // direct | environment | custom
    Protocol Protocol // http | socks5
    Host     string
    Port     int
    Username string
    Password string // decrypted in memory with GATEWAY_EGRESS_PROXY_KEY
    RemoteDNS bool
    NoProxy  []string
}

func (c Config) WrapTransport(base *http.Transport) (*http.Transport, error)
```

Behavior:

- `direct`: clone base, set `Proxy = nil`, keep the Raw-style DNS
  resolution + private-address check + pinned dial for the upstream host.
- `environment`: current behavior — `Proxy = http.ProxyFromEnvironment`,
  clear local dial hooks, delegate DNS to the proxy.
- `custom` + `http`: `Proxy = http.ProxyURL(...)` with optional
  `Proxy-Authorization` basic auth; CONNECT targets remain the allowlisted
  upstream host, so allowlist enforcement is unchanged.
- `custom` + `socks5`: `golang.org/x/net/proxy.SOCKS5` dialer (already an
  indirect dependency at v0.57.0; promote to direct) installed as
  `DialContext`. With `remoteDns: false` the upstream host is resolved
  locally first (private-address check applies) and the dialer receives an
  IP literal; with `remoteDns: true` the dialer sends the hostname to the
  proxy (socks5h semantics) for networks where local DNS is unusable.
- `noProxy` matching bypasses the custom proxy per upstream host.

The proxy address itself is validated at save time and at dial time: it
must parse, the port must be valid, and (outside explicit test overrides)
must not resolve to a private/loopback address — same rule as
`privateAddress` in `raw.go`.

## API And Contract

- `api/openapi/native-hosted.yaml`: add `EgressProxy` schema component;
  `POST /api/v2/repositories` accepts it for `proxy` type; `PATCH` on the
  repository settings endpoint updates it with the existing optimistic
  concurrency. Hosted repositories reject the field.
- Responses include the object with `credentialsConfigured` but never the
  password in any form; submitting a new password overwrites, and an
  explicit `clearCredentials: true` removes stored credentials.
- `POST /api/v2/repositories/{name}/egress-proxy:test` (admin only):
  dials the proxy and issues a lightweight upstream request (e.g. OCI
  `/v2/` probe or upstream root HEAD), reporting reachability, auth
  result, and latency. Result is ephemeral, not persisted.
- Regenerate with `make openapi-bundle && make openapi-generate-admin`;
  `make openapi-check` keeps generated artifacts honest.

## Format Integration

- Raw: replace the bespoke logic in `raw.go` with the shared factory
  (keeping its pinning and TLS-override rejection semantics for
  `direct`/`environment` modes).
- OCI: route `UpstreamClient.Fetch` through the factory using the
  resolved member's egress config.
- Maven: same, including metadata and negative-cache fetches; circuit
  breaker gains a `proxy_error` outcome distinct from `upstream_error`.
- Conan: same via its bound Group member config.

## Console

- Repository Settings tab (Proxy type): egress proxy form — mode radio
  (直连 / 跟随环境变量 / 自定义), protocol select, host/port, optional
  auth, noProxy list editor, and a "测试连接" button wired to the
  `:test` endpoint showing reachability/latency.
- Repository Overview: show effective egress mode next to the upstream
  endpoint; upstream health card distinguishes proxy failures.
- Audit/Operations surfaces: new `proxy_error` outcome renders like
  existing upstream errors.

## Security And Audit

- Passwords are stored only as AES-256-GCM ciphertext, decrypted lazily in
  memory when a transport is constructed; the data key comes from
  `GATEWAY_EGRESS_PROXY_KEY` and key rotation re-encrypts via a forward
  migration tool.
- Audit records gain `egress_mode` (`direct|environment|custom`) and
  `egress_proxy_host` (host only, no credentials) fields.
- Metric labels stay low-cardinality: mode and protocol only, never host.
- The `:test` endpoint requires admin scope and is rate-limited.

## Rollout

1. Migration + model + validation (direct/environment/custom accepted,
   custom behaves as environment until step 2).
2. Shared transport factory; Raw migrates first (parity tests exist in
   `raw_cache_test.go`), then OCI/Maven/Conan.
3. OpenAPI contract + generated clients + Console Settings UI with
   connection test.
4. Audit/metrics fields and documentation updates (README env-var table,
   `docs/protocol-compatibility.md` notes).

## Maven/OCI Per-Hop Downloads

Maven and OCI Proxy content reads apply the effective egress policy separately
to the initial request and every redirect. The limit is five redirects; loops,
missing or invalid locations, user information in URLs, unapproved origins and
HTTPS downgrades fail before the next request. Production Proxy endpoints must
use HTTPS. This tightening may reject an old HTTP-only or previously unchecked
redirect chain; do not broaden policy to hide the failure.

The configured endpoint's exact origin, including its port, is allowed. Extra
`allowedHosts` entries authorize the HTTPS host on port 443; the management API
continues to accept bare hostnames. An in-process member with an explicit port
entry is matched exactly, never by hostname alone. A content host permission
does not establish trust in an OCI token issuer. Anonymous Bearer exchange
requires the separate explicit issuer/audience configuration described below.

For direct paths, environment `NO_PROXY`, custom `noProxy`, and SOCKS5 with local
DNS, each hop checks all DNS answers before a connection and pins the selected
connection to those answers. Checks and connections obey the request context.
Private, loopback, link-local, unspecified and multicast addresses are refused,
as are special-use IPv4 ranges `0/8`, `100.64/10`, `192.0.0/24`, `192.0.2/24`,
`192.88.99/24`, `198.18/15`, `198.51.100/24`, `203.0.113/24` and `240/4`.
IPv6 must be in `2000::/3`, excluding `2001::/23` and `2001:db8::/32`.
Literal URLs receive the same check even on proxy-owned DNS paths.

HTTP CONNECT and SOCKS5 remote DNS leave target DNS and connection address
enforcement to the selected proxy. Local URL/origin permission still applies
before every hop. The mode and `NO_PROXY` decision are reevaluated for each
target; a successful proxy hop does not grant a later direct connection.

Gateway client authentication is not copied upstream. Cross-origin redirects
remove Authorization, Cookie and Proxy-Authorization and do not restore them
when a chain returns to the original origin. The supplied client's cookie jar
is disabled for upstream requests, so it cannot add authentication back.
Maven conditional headers and
User-Agent, and Maven/OCI Accept and Range headers retain their existing
semantics. Existing client redirect restrictions remain effective, including
`http.ErrUseLastResponse` returning the readable final redirect response. A single
budget covers DNS, the chain and response-body consumption; cancellation and
timeout remain recognizable through `errors.Is`. Public errors are safe fixed
messages. Proxy client spans propagate trace context without recording target
URLs, signed query strings or original transport/callback error messages.

Trusted in-process test hooks can map synthetic public addresses and the
selected proxy hop to task-owned loopback fixtures. `ProxyDialContext` is never
inferred from a supplied upstream dialer. `AllowHTTPForTesting` exists only on
the Go client, defaults false and is unavailable through repository settings;
it keeps older HTTP fixtures usable without admitting production HTTP paths.
Raw keeps its independent refusal to follow redirects. Hosted bytes, digest
verification and cache ownership/authorization are unchanged. Production CDN
permissions, native client acceptance and rollout require separate approval.

## Native OCI Proxy Bearer Configuration

Administrators can opt a native OCI Proxy repository into anonymous upstream
Bearer challenge exchange through `POST /api/v2/repositories` or
`PATCH /api/v2/repositories/{repositoryId}`:

```json
{
  "ociBearer": {
    "realm": "https://auth.example.test/token",
    "service": "registry.example.test"
  }
}
```

`realm` is the exact complete HTTPS token-service URL. HTTP, userinfo, query
parameters (including an empty query), and fragments (including an empty
fragment) are rejected. `service` is the exact challenge audience: 1–256 UTF-8
bytes with no whitespace or control characters. Both fields are required, and
unknown properties are rejected; this object never accepts credentials or tokens.

Omission on creation leaves exchange disabled. On PATCH, omission preserves the
existing object, `null` clears it, and an object replaces both fields together;
updates retain the existing `If-Match` concurrency requirement. Responses expose
configured realm/service and omit the field when disabled. Hosted repositories
and other formats reject this field, including `null`. Legacy V1 Group members
also reject it: V2 Groups inherit their native Proxy member's configuration at
runtime without introducing a legacy member configuration field.

Migration `000144_oci_bearer_configuration.sql` adds nullable JSONB `oci_bearer`
to `hosted_repositories`; existing rows remain disabled. Only configuration is
persisted, never exchanged tokens. The API does not alter `allowedHosts`: a
cross-origin token realm still needs independent authorization under the
repository's existing egress policy. Private authenticated registries and
production identity, network-policy or deployment changes require separate work.
The generated Console client includes the field; this change adds no Console
configuration form.

The exchange accepts one unambiguous Bearer challenge bound to the configured
realm/service and the requested repository's `pull` scope. It performs at most
one anonymous token GET and one content retry. Inbound credentials never enter
either request; cross-origin redirects remove the exchanged token. Token GETs
use the same five-hop egress checks as content, including independently approved
issuer/CDN hosts. Invalid challenges, tokens and repeated 401s return stable
errors without authentication payloads; cancellation/deadline identity remains
available to callers.

Runtime caches are process-local, with at most 256 entries and 32 refresh keys
in flight. Keys bind repository identity, registry, issuer, service, scope and
egress/host policy. Concurrent callers share a refresh bounded to 30 seconds;
cancelling one waiter does not cancel other callers. A rejected cached token is
invalidated by version. Missing `expires_in` defaults to 60 seconds; positive
remaining lifetime is cached for at most 300 seconds with a five-second refresh
margin. Optional RFC3339 `issued_at` must not place issuance more than 30 seconds
in the future or make the token expired. Challenge/token/JSON response limits
are 8 KiB/8 KiB/64 KiB. No refresh tokens or durable identities are requested.
