# APT Proxy and Group

Artifact Gateway exposes Debian repositories at:

```text
https://gateway.example.com/apt/<repository>/
```

APT is currently a protocol-only format. Proxy repositories and ordered Groups
are supported; Hosted publication and lifecycle operations are intentionally
not advertised because generating and signing `Release`, `InRelease`, and
`Packages` metadata requires a separate trusted publication workflow.

## Configure a source

Create an APT Proxy with an upstream such as
`https://deb.debian.org/debian` and allow every hostname that the upstream may
redirect to. Then add a source using the Gateway URL:

```text
deb https://gateway.example.com/apt/debian bookworm main
```

For a Group, replace `debian` with the Group name. Group members are tried in
configured order. Only active APT Proxy members participate.

## Cached paths

The proxy accepts canonical Debian repository paths under `dists/` and
`pool/`, including:

```text
dists/bookworm/InRelease
dists/bookworm/Release
dists/bookworm/Release.gpg
dists/bookworm/main/binary-amd64/Packages.xz
pool/main/h/hello/hello_2.10-3_amd64.deb
```

Metadata, signatures, and packages are cached byte-for-byte. The Gateway does
not rewrite signed metadata. Requests containing empty, dot, parent, escaped
parent, backslash, query, or fragment path segments are rejected before an
upstream request is made.

Cached assets support `GET`, `HEAD`, conditional requests with `ETag` or
`Last-Modified`, and one HTTP byte range per request. This allows package
downloads to resume without loading the complete object from storage. Mutable
`dists/` metadata is conditionally revalidated; immutable `pool/` and
`dists/*/by-hash/` objects are served directly from verified cache. If mutable
metadata cannot be revalidated temporarily, the last cached copy is returned
with `Warning: 110`.

## Authentication

APT reads support bearer credentials, HTTP Basic credentials, and anonymous
access. Anonymous reads require both the global anonymous policy and the
repository or Group policy to be enabled. Repository grants use the APT path
as the resource prefix, for example `dists/bookworm` or `pool/main`.
