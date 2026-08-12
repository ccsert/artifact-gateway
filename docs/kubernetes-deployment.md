# Kubernetes Deployment

Artifact Gateway ships a Kustomize base and a local-development overlay. The
local overlay is an executable deployment baseline validated on Docker Desktop.
It is not a production topology.

## Local quick start

The local stack requires Docker, `kubectl`, `jq`, `curl`, a current local Kubernetes
context, and a default `StorageClass`.

```sh
kubectl config current-context
make kubernetes-local-check
make kubernetes-local-up
make kubernetes-local-status
make kubernetes-local-verify
```

The Console and all same-origin API and artifact routes are exposed at
`http://127.0.0.1:18081`. The start command builds the Gateway and Console
images, creates runtime Secrets without checking credential values into the
rendered manifests, applies every database migration, waits for all workloads,
and verifies the Console and authenticated format API.

`make kubernetes-local-verify` creates a unique Raw Hosted Repository, writes an
object, restarts the Gateway Deployment, and reads the Repository record and
exact object bytes back. This proves PostgreSQL and MinIO persistence across a
real Pod replacement rather than only checking that PVC objects exist. When
an environment override is omitted on a later command, the helper reuses every
credential and encryption key already stored in the namespace Secret. Repeating
`up` or `verify` therefore does not silently rotate PostgreSQL, MinIO, Resolver,
administrator, or settings-encryption secrets back to local defaults.

The default administrator token is `local-gateway-admin-token`. These defaults
are for a disposable local cluster only. Override them before a shared local
installation:

```sh
K8S_LOCAL_POSTGRES_PASSWORD='replace-me' \
K8S_LOCAL_MINIO_USER='replace-me' \
K8S_LOCAL_MINIO_PASSWORD='replace-me' \
K8S_LOCAL_ADMIN_TOKEN='replace-me' \
K8S_LOCAL_RESOLVER_TOKEN='replace-me' \
K8S_LOCAL_SETTINGS_ENCRYPTION_KEY='0123456789abcdef0123456789abcdef' \
make kubernetes-local-up
```

Set `K8S_LOCAL_SKIP_BUILD=1` to reuse images already loaded into the local
cluster runtime. The helper refuses an unrecognized Kubernetes context. An
operator may set `ARTIFACT_GATEWAY_ALLOW_NONLOCAL_K8S=1` only after explicitly
checking the target and adapting its image-loading and service-exposure behavior,
but the local overlay remains unsuitable for production.

To remove the stack:

```sh
make kubernetes-local-down
```

This deletes the `artifact-gateway-local` namespace, including its PostgreSQL
and MinIO PersistentVolumeClaims and all local data.

## Local topology

```text
localhost:18081
       |
       v
Console nginx (SPA + same-origin reverse proxy)
       |
       v
Artifact Gateway (standalone API + scheduler + worker)
       |                         |
       v                         v
PostgreSQL PVC                 MinIO PVC
```

The Console and Gateway run as non-root users with read-only root filesystems,
dropped Linux capabilities, disabled service-account token mounting, resource
requests and limits, and HTTP health probes. PostgreSQL and MinIO are pinned
single-replica local dependencies. A bounded ephemeral `/tmp` volume supports
Gateway's streamed upload spool without making its root filesystem writable.
The Gateway migration init container uses
the same append-only migration files as Compose and safely skips migrations
whose recorded checksums already match.

The optional reference scanner is intentionally not part of this minimal
overlay. Use Compose for its current smoke test, or add a dedicated scanner
workload and configure `GATEWAY_ARTIFACT_SCANNER_URL` before assigning scan jobs
to Kubernetes workers.

## Manifest workflow

The reusable workloads live under `deploy/kubernetes/base`; the local services,
storage, configuration, and migration wiring live under
`deploy/kubernetes/overlays/local`.

```sh
kubectl kustomize deploy/kubernetes/overlays/local
make kubernetes-local-check
```

The check renders and parses the full overlay offline, verifies every named
workload's persistence, migration, probe, and container-hardening invariants,
and checks that both the production Console reverse proxy and Vite development
proxy expose the APT route. It also runs the local CLI against command fakes to
cover context rejection, credential validation, port conflicts, exact namespace
deletion, startup, status, and persistence-verification dispatch without
mutating a cluster.

## Production deployment path

Production should consume the base as an input, not deploy the local overlay.
Before declaring Kubernetes support production-ready, provide a separate
environment overlay or chart with all of the following:

- externally managed, backed-up, and highly available PostgreSQL and S3-compatible
  object storage;
- a single pre-deployment migration Job instead of one init migration per API
  replica;
- separate `api`, `scheduler`, and `worker` Deployments, with database connection
  budgets calculated across all replicas;
- TLS termination and an Ingress or Gateway resource that preserves streaming,
  ranges, authorization headers, and large uploads for every protocol route;
- an external secret manager, credential rotation, and no default local tokens;
- NetworkPolicies, namespace quotas, PodDisruptionBudgets, topology spread,
  autoscaling, and node-placement policy;
- metrics and logs collection, alerting, PostgreSQL/S3 backup and restore drills,
  and upgrade/rollback evidence;
- dedicated scanner workers and an explicitly network-restricted scanner service
  when automatic scanning is enabled.

The release-readiness checks remain authoritative even after these deployment
resources exist. A successful Pod rollout alone is not production evidence.
