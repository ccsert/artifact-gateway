# Protocol-Only Formats May Be Admitted Without Invented Publication APIs

Status: accepted

Artifact Gateway normally admits a format only after Hosted, Proxy, Group, and
artifact lifecycle behavior are executable. Some ecosystems intentionally
standardize distribution but have no repository publication protocol. Go
Modules are the first such case: `GOPROXY` defines immutable reads, while module
authors publish through version-control systems rather than uploading to a
module proxy.

For these ecosystems, a format may declare only Proxy and Group repository
types when all of the following are true:

1. The native client protocol has no standard publication operation.
2. The capability profile omits Hosted and every lifecycle operation that is
   not executable.
3. Proxy caching, integrity validation, authorization, anonymous access,
   audit, search, capacity accounting, recovery, Console workflows, and a real
   client fixture ship together.
4. Adding a future Hosted workflow requires a separate accepted contract; a
   product-specific upload API is not presented as an ecosystem standard.

This exception preserves truthful capability discovery. It does not relax the
quality requirements for the repository types that the format does declare.
