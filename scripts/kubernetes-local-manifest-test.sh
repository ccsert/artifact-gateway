#!/usr/bin/env bash
set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
overlay="$root/deploy/kubernetes/overlays/local"
rendered=$(mktemp)
rendered_json=$(mktemp)
trap 'rm -f "$rendered" "$rendered_json"' EXIT

kubectl kustomize "$overlay" >"$rendered"
go run "$root/scripts/kubernetes-manifest-json.go" "$rendered" >"$rendered_json"
bash -n "$root/scripts/kubernetes-local.sh"
test -f "$root/Dockerfile.console"
test -f "$root/Dockerfile.apt-signer"

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
assert_json 'map(select(.kind == "Deployment" and .metadata.name == "artifact-gateway-ingress" and .spec.template.spec.serviceAccountName == "artifact-gateway-ingress" and .spec.template.spec.containers[0].image == "traefik:v3.7.10@sha256:9c3b91d5fb7770853ca5c1124a23c34bf2d9b47ffaebeab2614cbaf410dcb2ac" and .spec.template.spec.containers[0].securityContext.readOnlyRootFilesystem == true)) | length == 1' 'pinned and hardened local Ingress controller'
assert_json 'map(select(.kind == "Deployment" and .metadata.name == "artifact-gateway-ingress" and any(.spec.template.spec.containers[0].args[]; . == "--global.checknewversion=false") and any(.spec.template.spec.containers[0].args[]; . == "--global.sendanonymoususage=false"))) | length == 1' 'disabled Ingress update checks and telemetry'
assert_json 'map(select(.kind == "Deployment" and .metadata.name == "artifact-gateway-ingress" and any(.spec.template.spec.containers[0].args[]; . == "--entrypoints.web.http.encodedcharacters.allowencodedslash=true") and any(.spec.template.spec.containers[0].args[]; . == "--entrypoints.web.http.encodedcharacters.allowencodedbackslash=false") and any(.spec.template.spec.containers[0].args[]; . == "--entrypoints.web.http.encodedcharacters.allowencodednullcharacter=false") and any(.spec.template.spec.containers[0].args[]; . == "--entrypoints.web.http.encodedcharacters.allowencodedpercent=true") and any(.spec.template.spec.containers[0].args[]; . == "--entrypoints.web.http.encodedcharacters.allowencodedsemicolon=true") and any(.spec.template.spec.containers[0].args[]; . == "--entrypoints.web.http.encodedcharacters.allowencodedquestionmark=true") and any(.spec.template.spec.containers[0].args[]; . == "--entrypoints.web.http.encodedcharacters.allowencodedhash=true"))) | length == 1' 'explicit protocol-compatible Ingress encoded-path policy'
assert_json 'map(select(.kind == "StatefulSet" and .metadata.name == "postgres")) | length == 1' 'PostgreSQL StatefulSet'
assert_json 'map(select(.kind == "StatefulSet" and .metadata.name == "rustfs")) | length == 1' 'RustFS StatefulSet'
assert_json 'map(select(.kind == "StatefulSet" and .metadata.name == "postgres" and .spec.volumeClaimTemplates[0].spec.resources.requests.storage == "2Gi")) | length == 1' 'PostgreSQL 2Gi persistent volume claim'
assert_json 'map(select(.kind == "StatefulSet" and .metadata.name == "rustfs" and .spec.volumeClaimTemplates[0].spec.resources.requests.storage == "5Gi" and .spec.template.spec.containers[0].readinessProbe.httpGet.path == "/health" and .spec.template.spec.containers[0].securityContext.runAsUser == 10001)) | length == 1' 'hardened RustFS StatefulSet with 5Gi persistent volume claim'
assert_json 'map(select(.kind == "Deployment" and .metadata.name == "artifact-gateway" and .spec.template.spec.securityContext.runAsNonRoot == true and .spec.template.spec.containers[0].securityContext.readOnlyRootFilesystem == true and .spec.template.spec.containers[0].readinessProbe.httpGet.path == "/readyz" and .spec.template.spec.containers[0].livenessProbe.httpGet.path == "/livez" and any(.spec.template.spec.containers[0].volumeMounts[]; .name == "temporary" and .mountPath == "/tmp") and any(.spec.template.spec.volumes[]; .name == "temporary" and .emptyDir.sizeLimit == "1Gi"))) | length == 1' 'hardened Gateway container, bounded upload spool, and health probes'
assert_json 'map(select(.kind == "Deployment" and .metadata.name == "artifact-gateway" and any(.spec.template.spec.containers[]; .name == "reference-apt-signer" and .image == "artifact-gateway-apt-signer:k8s-local" and .securityContext.readOnlyRootFilesystem == true and .readinessProbe.exec.command == ["/usr/local/bin/reference-apt-signer-healthcheck"] and any(.volumeMounts[]; .name == "apt-signer-key" and .mountPath == "/var/lib/reference-apt-signer")) and any(.spec.template.spec.volumes[]; .name == "apt-signer-key" and .persistentVolumeClaim.claimName == "artifact-gateway-apt-signer-key"))) | length == 1' 'loopback APT reference signer sidecar with persistent key volume'
assert_json 'map(select(.kind == "PersistentVolumeClaim" and .metadata.name == "artifact-gateway-apt-signer-key" and .spec.accessModes == ["ReadWriteOnce"] and .spec.resources.requests.storage == "1Gi")) | length == 1' 'APT signer key persistent volume claim'
assert_json 'map(select(.kind == "Service" and (.metadata.name | contains("apt-signer")))) | length == 0' 'no network Service for the loopback-only APT signer'
assert_json 'map(select(.kind == "ConfigMap" and .metadata.name == "artifact-gateway-config" and .data.GATEWAY_APT_SIGNER_ENDPOINT == "http://127.0.0.1:18083/v1/sign-release")) | length == 1' 'Gateway loopback APT signer endpoint'
assert_json 'map(select(.kind == "Deployment" and .metadata.name == "artifact-gateway-console" and .spec.template.spec.securityContext.runAsNonRoot == true and .spec.template.spec.containers[0].securityContext.readOnlyRootFilesystem == true and .spec.template.spec.containers[0].readinessProbe.httpGet.path == "/console-healthz" and .spec.template.spec.containers[0].livenessProbe.httpGet.path == "/console-healthz")) | length == 1' 'hardened Console container and health probes'
assert_json 'map(select(.kind == "Deployment" and .metadata.name == "artifact-gateway" and .spec.template.spec.initContainers[0].name == "migrate" and .spec.template.spec.initContainers[0].securityContext.runAsNonRoot == true and .spec.template.spec.initContainers[0].securityContext.readOnlyRootFilesystem == true)) | length == 1' 'hardened database migration init container'
assert_json 'map(select(.kind == "Deployment" and .metadata.name == "artifact-gateway" and .spec.template.spec.containers[0].envFrom[1].secretRef.name == "artifact-gateway-secrets")) | length == 1' 'Gateway external Secret reference'
assert_json 'map(select(.kind == "Deployment" and (.metadata.name == "artifact-gateway" or .metadata.name == "artifact-gateway-console") and .spec.template.spec.automountServiceAccountToken == false)) | length == 2' 'disabled service-account token mounting for both Deployments'
assert_json 'map(select(.kind == "Service" and .metadata.name == "artifact-gateway-console" and .spec.type == "LoadBalancer" and .spec.ports[0].port == 18081)) | length == 1' 'fixed local Console service endpoint'
assert_json 'map(select(.kind == "Service" and .metadata.name == "artifact-gateway-ingress" and .spec.type == "LoadBalancer" and .spec.ports[0].port == 80)) | length == 1' 'local Ingress endpoint'
assert_json 'map(select(.kind == "IngressClass" and .metadata.name == "artifact-gateway-local" and .spec.controller == "traefik.io/ingress-controller")) | length == 1' 'dedicated local IngressClass'
assert_json 'map(select(.kind == "Ingress" and .metadata.name == "artifact-gateway" and .spec.ingressClassName == "artifact-gateway-local" and .spec.rules[0].host == "artifact-gateway.localhost" and .spec.rules[0].http.paths[0].backend.service.name == "artifact-gateway-console" and .spec.rules[0].http.paths[0].backend.service.port.name == "http")) | length == 1' 'same-origin Console, API, and protocol Ingress through the patched Service port'
assert_json 'map(select(.kind == "Role" and .metadata.name == "artifact-gateway-ingress" and any(.rules[]; .apiGroups == [""] and ((.resources | index("services")) != null) and .verbs == ["get", "list", "watch"]))) | length == 1' 'namespace-scoped Ingress service discovery RBAC'
assert_json 'map(select(.kind == "ClusterRole" and .metadata.name == "artifact-gateway-local-ingress-class-reader" and any(.rules[]; .apiGroups == [""] and .resources == ["nodes"] and .verbs == ["get", "list", "watch"]))) | length == 1' 'read-only node discovery required by the Ingress provider'

if grep -Eq 'local-(gateway|postgres|rustfs).*(password|token)' "$rendered"; then
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
