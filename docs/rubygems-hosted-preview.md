# RubyGems Hosted preview

[简体中文](rubygems-hosted-preview.zh-CN.md) · [Documentation index](README.md)

RubyGems Hosted is a default-disabled preview under
`/api/v2/previews/rubygems`. It publishes original `.gem` files and serves native
RubyGems/Bundler compact indexes, legacy Marshal 4.8 indexes and zlib gemspecs.
It is absent from the admitted format profiles, formal repository catalog,
Console creation selector and installation presets. There is no preview `agctl`
command. The Hosted slice is tracked by [#459](https://github.com/ccsert/artifact-gateway/issues/459);
Proxy/Group, yank, full lifecycle governance, Console, scanning, distribution,
backup admission and Nexus migration remain in
[#293](https://github.com/ccsert/artifact-gateway/issues/293).

## Enable and provision

Apply the normal append-only migrations, including
`000185_native_rubygems_hosted_preview.sql`. Use the existing PostgreSQL and
verified S3/RustFS runtime ports and set
`GATEWAY_RUBYGEMS_PREVIEW_ENABLED=true`. The default is false; an invalid boolean
fails startup. Publication persistence is an explicitly injected domain store,
with a separate PostgreSQL adapter and advisory-lock pool.

An API-only process serves the preview without starting its cleanup worker.
Enable the same preview on a worker/standalone process with the `rubygems`
format and `retention` work kind enabled to collect expired uploads. A format
or work-kind filter that excludes either also excludes that collector.
Diagnostics report `rubygems` under `previewWorkerFormats`; the formal
`workerFormats` enum remains unchanged.

An administrator creates a repository with:

```text
POST /api/v2/previews/rubygems/repositories
Authorization: Bearer <local administrator credential>
Idempotency-Key: <stable request identifier>
Content-Type: application/json

{"name":"ruby-internal","anonymousRead":false}
```

The receipt includes `id`, `preview:true`, `sourceUrl`, `pushHost` and
`publicationUrl`. Locations are Gateway-relative; prepend the Gateway origin.
Keep the receipt because the formal repository list does not expose this
preview. Read it back with
`GET /api/v2/previews/rubygems/repositories/{id}`. Replaying the same actor/key
and body returns the same repository; a changed body conflicts.

`sourceUrl` ends in `/` for consumer configuration. `pushHost` deliberately has
no trailing slash: the official `gem push` client appends `/api/v1/gems` to its
host. `publicationUrl` is the complete endpoint for raw HTTP uploads, and must
not be passed as a `gem push --host` value.

## Native authentication and clients

The native `Authorization` header accepts a local API key, Bearer credential,
or HTTP Basic whose password is that credential. Basic's username does not
select the actor. Repository/name-prefix grants and current account state use
the existing Gateway authorizer. Collection indexes filter each visible gem by
its exact name; a prefix grant permits only its matching entries. Push checks
the actual parsed name and rechecks authentication, actor and write permission
immediately before commit. Explicit invalid, expired or revoked credentials
never fall back to anonymous access. Anonymous reads require both the global
policy and repository opt-in.

For an existing private credential and a source rooted at
`https://gateway.example.com/rubygems/ruby-internal/`:

```sh
export GEM_HOST_API_KEY='<local publication credential>'
gem push owned_top-1.2.0.gem \
  --host https://gateway.example.com/rubygems/ruby-internal

gem install owned_top --version 1.2.0 --remote --clear-sources \
  --source 'https://gateway:<local read credential>@gateway.example.com/rubygems/ruby-internal/' \
  --no-document
```

Use an owned credential file/configuration for unattended clients; avoid
recording credential-bearing source URLs in shared logs. A Bundler Gemfile uses
this repository's `sourceUrl` as its only `source`. Standard Bundler credential
configuration may supply Basic authentication. Neither client needs a public
fallback source for packages and transitive dependencies published here.

## Publication, integrity and capacity

The immutable identity is the case-preserved gem name, RubyGems-canonical
version identity and platform. Original version/platform strings remain in
native indexes. The parser treats tar, gzip and YAML as bounded data: it never
runs Ruby, creates YAML objects or reads an uploaded Marshal payload. Required
metadata, data and SHA-256/SHA-512 checksum entries must agree; declared files
must exactly match safe regular-file payload paths. Duplicate archive/YAML
entries, aliases, unsafe paths, unsupported Ruby tags and inconsistent
requirements fail explicitly. Ordinary bounded trailing YAML data does not
erase recognized runtime/development dependencies or their constraints.

Limits are 64 MiB original gem bytes, 1 MiB expanded metadata, 256 MiB expanded
payload, 10,000 regular payload files, 256 dependencies, 32 constraints per
requirement and 4,096 active reserved/committed identities per repository.
Both outer and payload tar archives must use USTAR, including valid USTAR prefix
paths; PAX/GNU extensions, signed archives and symlink payloads are rejected.
Platform must be a canonical RubyGems string; missing, null or empty platform
means `ruby`, matching the fixed official parser. Sequence/object platforms,
noncanonical aliases and host-dependent `current` are unsupported. YAML
dependency types must be the actual plain `:runtime`/`:development` symbols,
preserving the original gem semantics; strings that only spell the same word are
rejected.
Specification dates may be absent/null, valid ISO calendar dates, or timestamps
understood by the fixed official YAML parser; quoted date strings follow the
official `date=` string grammar, which rejects a quoted ISO `T` timestamp.
Other date coercions are unsupported. `require_paths` may be absent/null, a
string array, or the official legacy nested string-array form. Scalars and
non-string entries are rejected. Known Ruby object tags are checked throughout
the metadata tree, including otherwise unused tail fields, because the official
YAML loader initializes those nodes too. Ordinary bounded tail fields remain
supported and cannot hide malformed active date/version initializers.
Platform packages are indexed and downloadable; server-side extension execution
and verification of every target operating system are outside this slice.

A native push reserves capacity and a unique object key, stages the exact
original bytes, reopens and hashes the entire object, revalidates identity and
metadata, rechecks publication authority, then atomically commits visibility
and its audit. Pending or failed uploads never enter any index. Same identity
and identical bytes replay idempotently without a second publication audit;
different bytes or an ambiguous native filename return 409. All original
package reads recheck complete size and SHA-256 before writing a response.

GET/PUT `/api/v2/previews/rubygems/repositories/{id}/capacity` is administrator
only. `usedBytes` and `objectCount` describe committed originals; `pendingBytes`
reserves unexpired uploads. `quotaBytes:0` means no configured byte quota.
Quota reduction below committed plus reserved usage is refused, and quota
updates retain their audit transaction. This independent preview capacity does
not pretend that the format has general retention/backup admission.

Reservations expire after one hour. Retry the same original native push after
a dropped response or process restart. Cleanup only removes expired,
never-visible objects and shares the identity lock with publication; replacement
sessions have distinct object keys. It does not delete published gems. Failed
deletes/collection markers remain eligible for retry.

## Native contract and verification

The server serves `names`, `versions`, `info/{name}`, `specs.4.8.gz`,
`latest_specs.4.8.gz`, `prerelease_specs.4.8.gz`,
`quick/Marshal.4.8/{fullName}.gemspec.rz`, and `gems/{fullName}.gem` beneath
`/rubygems/{repository}/`. GET and HEAD have the same authorization. Conditional
ETags are supported; a Range request receives the complete current 200
representation. Compact MD5 checksums are protocol cache checksums; immutable
package identity and integrity use SHA-256.

Run `scripts/native-rubygems-e2e.sh` for the owned native gate. It fixes the
[official Ruby image](https://hub.docker.com/_/ruby) to
`ruby:3.4.4-alpine@sha256:81096866ac15f906adc79867da3ed97a2aa271d6149363e216a174701345c53b`
and requires its actual **Ruby 3.4.4 / RubyGems 3.6.7 / Bundler 2.6.7** tuple.
The gate verifies pinned
[specification serialization](https://github.com/rubygems/rubygems/blob/v3.6.7/lib/rubygems/specification.rb),
[push host construction](https://github.com/rubygems/rubygems/blob/v3.6.7/lib/rubygems/gemcutter_utilities.rb),
and [compact-index parsing](https://github.com/rubygems/rubygems/blob/v3.6.7/bundler/lib/bundler/compact_index_client/parser.rb)
from official sources against recorded SHA-256 values. Protocol behavior also
follows the official [push API](https://guides.rubygems.org/rubygems-org-api/).

The gate builds the real Gateway binary, starts owned PostgreSQL/RustFS,
performs network-isolated `gem build`, native push, empty-cache gem and Bundler
three-level installs through compact and full indexes, explicit prerelease
installation and platform fetch, and repeats cold consumers after physically
restarting Gateway, PostgreSQL and object storage. A transparent forwarding
observer records HTTP responses without mocking authentication, protocol
responses, bytes or persistence. Build/push alone mount the original package
directory. Each consumer has a separate initially empty working directory,
HOME and cache tree; gem installs explicitly require remote resolution. Each
phase separately proves full target `.gem` GETs with matching original size,
SHA-256, parsed identity and dependencies, including both cold clients after
restart; requests from another phase cannot satisfy its assertions.
Five original size/SHA/identity closures, per-phase response proofs, raw client
outputs, JSON test results, source/tool provenance and cleanup receipts are
written under `output/native-rubygems-e2e/<run>/`. Its mandatory client test
cannot pass by skipping unavailable tools. These are fixture/local or CI
acceptance results; they do not constitute production deployment or every
RubyGems platform's extension validation. Cancellation by SIGTERM/SIGINT keeps
a nonzero result, reaps owned command groups and performs bounded cleanup with
a receipt. The offline cancellation regression runs the unchanged producer in
a temporary repository with a Docker substitute; it never uses a real daemon
or adds deliberate failed runs to the native evidence directory.
