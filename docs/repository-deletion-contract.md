# Repository Deletion Contract

[简体中文](repository-deletion-contract.zh-CN.md) · [Documentation index](README.md)

Repository deletion is an asynchronous management operation.

1. `DELETE /api/v2/repositories/{repositoryId}` changes an `active` repository
   to `deleting` and returns `202` with the repository representation.
2. The state change immediately blocks protocol reads, writes, proxy access,
   group resolution, search, and promotion for that repository. Repeating the
   delete request while it is `deleting` is idempotent.
3. `RepositoryDeletionWorker` scans once at process startup and then every
   minute. Each `deleting` repository is advanced to `deleted` in a guarded,
   idempotent transition. A failed scan leaves the repository in `deleting` so
   the next scan can retry it.
4. `deleted` is a terminal management state. The repository metadata row is
   retained as an audit and foreign-key anchor; it is not a usable repository.

The worker is intentionally separate from format object collectors. Artifact
bytes use content-addressed storage and may be shared by multiple repositories;
their physical reclamation must continue through the format-specific reference
checks and lifecycle jobs instead of deleting shared objects as part of the
repository state transition.
