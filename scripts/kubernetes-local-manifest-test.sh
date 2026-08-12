#!/usr/bin/env bash
set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
overlay="$root/deploy/kubernetes/overlays/local"
rendered=$(mktemp)
rendered_json=$(mktemp)
trap 'rm -f "$rendered" "$rendered_json"' EXIT

kubectl kustomize "$overlay" >"$rendered"
kubectl create --dry-run=client --validate=false -f "$rendered" -o json | jq -s . >"$rendered_json"
bash -n "$root/scripts/kubernetes-local.sh"
test -f "$root/Dockerfile.console"

assert_json() {
  local expression=$1
  local description=$2
  if ! jq -e "$expression" "$rendered_json" >/dev/null; then
    printf 'rendered Kubernetes manifest is missing %s\n' "$description" >&2
    exit 1
  fi
}

assert_json 'map(select(.kind == "Deployment" and .metadata.name == "artifact-gateway")) | length == 1' 'Gateway Deployment'
assert_json 'map(select(.kind == "Deployment" and .metadata.name == "artifact-gateway-console")) | length == 1' 'Console Deployment'
assert_json 'map(select(.kind == "StatefulSet" and .metadata.name == "postgres")) | length == 1' 'PostgreSQL StatefulSet'
assert_json 'map(select(.kind == "StatefulSet" and .metadata.name == "minio")) | length == 1' 'MinIO StatefulSet'
assert_json 'map(select(.kind == "StatefulSet" and .metadata.name == "postgres" and .spec.volumeClaimTemplates[0].spec.resources.requests.storage == "2Gi")) | length == 1' 'PostgreSQL 2Gi persistent volume claim'
assert_json 'map(select(.kind == "StatefulSet" and .metadata.name == "minio" and .spec.volumeClaimTemplates[0].spec.resources.requests.storage == "5Gi")) | length == 1' 'MinIO 5Gi persistent volume claim'
assert_json 'map(select(.kind == "Deployment" and .metadata.name == "artifact-gateway" and .spec.template.spec.securityContext.runAsNonRoot == true and .spec.template.spec.containers[0].securityContext.readOnlyRootFilesystem == true and .spec.template.spec.containers[0].readinessProbe.httpGet.path == "/readyz" and .spec.template.spec.containers[0].livenessProbe.httpGet.path == "/livez" and any(.spec.template.spec.containers[0].volumeMounts[]; .name == "temporary" and .mountPath == "/tmp") and any(.spec.template.spec.volumes[]; .name == "temporary" and .emptyDir.sizeLimit == "1Gi"))) | length == 1' 'hardened Gateway container, bounded upload spool, and health probes'
assert_json 'map(select(.kind == "Deployment" and .metadata.name == "artifact-gateway-console" and .spec.template.spec.securityContext.runAsNonRoot == true and .spec.template.spec.containers[0].securityContext.readOnlyRootFilesystem == true and .spec.template.spec.containers[0].readinessProbe.httpGet.path == "/console-healthz" and .spec.template.spec.containers[0].livenessProbe.httpGet.path == "/console-healthz")) | length == 1' 'hardened Console container and health probes'
assert_json 'map(select(.kind == "Deployment" and .metadata.name == "artifact-gateway" and .spec.template.spec.initContainers[0].name == "migrate" and .spec.template.spec.initContainers[0].securityContext.runAsNonRoot == true and .spec.template.spec.initContainers[0].securityContext.readOnlyRootFilesystem == true)) | length == 1' 'hardened database migration init container'
assert_json 'map(select(.kind == "Deployment" and .metadata.name == "artifact-gateway" and .spec.template.spec.containers[0].envFrom[1].secretRef.name == "artifact-gateway-secrets")) | length == 1' 'Gateway external Secret reference'
assert_json 'map(select(.kind == "Deployment" and (.metadata.name == "artifact-gateway" or .metadata.name == "artifact-gateway-console") and .spec.template.spec.automountServiceAccountToken == false)) | length == 2' 'disabled service-account token mounting for both Deployments'
assert_json 'map(select(.kind == "Service" and .metadata.name == "artifact-gateway-console" and .spec.type == "LoadBalancer" and .spec.ports[0].port == 18081)) | length == 1' 'fixed local Console service endpoint'

if grep -Eq 'local-(gateway|postgres|minio).*(password|token)' "$rendered"; then
  printf 'rendered Kubernetes manifest contains a local credential literal\n' >&2
  exit 1
fi

grep -Eq 'location .*\|apt\)' "$root/deploy/kubernetes/console/default.conf" || {
  printf 'Console reverse proxy does not expose the APT protocol route\n' >&2
  exit 1
}
grep -Fq 'proxy_connect_timeout 3s;' "$root/deploy/kubernetes/console/default.conf" || {
  printf 'Console reverse proxy does not bound unavailable Gateway connections\n' >&2
  exit 1
}
grep -Eq '"/apt": gateway' "$root/console/vite.config.ts" || {
  printf 'Vite development proxy does not expose the APT protocol route\n' >&2
  exit 1
}

grep -Fq 'COPY deploy/kubernetes/console/default.conf' "$root/Dockerfile.console" || {
  printf 'Console deployment image does not install the checked-in reverse proxy configuration\n' >&2
  exit 1
}

printf 'local Kubernetes manifest checks passed\n'
