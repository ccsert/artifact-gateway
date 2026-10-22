#!/usr/bin/env bash
set -euo pipefail
umask 077

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repo_root"
for binary in docker go curl python3; do
  command -v "$binary" >/dev/null || { printf 'NuGet Hosted gate requires %s\n' "$binary" >&2; exit 1; }
done

# Read only the local endpoint, then use an empty, owned Docker credential
# configuration for every pull, container and Compose operation below.
docker_endpoint=${DOCKER_HOST:-$(docker context inspect "$(docker context show)" --format '{{.Endpoints.docker.Host}}')}
case "$docker_endpoint" in unix://*) ;; *) printf 'NuGet gate requires a local Docker socket\n' >&2; exit 1 ;; esac
work=$(mktemp -d)
project="artifact-gateway-nuget-$$-$RANDOM"
evidence="$repo_root/output/native-nuget-e2e/$project"
mkdir -p "$evidence" "$work/docker"
printf '{"auths":{}}\n' > "$work/docker/config.json"
export DOCKER_CONFIG="$work/docker" DOCKER_HOST="$docker_endpoint"
unset DOCKER_CONTEXT DOCKER_DEFAULT_PLATFORM
# Docker Desktop installs its Compose plugin in the app bundle and normally
# discovers it through the user's config directory. Point the owned directory
# at that executable without loading the user's auths or other settings.
if ! docker compose version >/dev/null 2>&1; then
  if [[ -x /Applications/Docker.app/Contents/Resources/cli-plugins/docker-compose ]]; then
    mkdir -p "$work/docker/cli-plugins"
    ln -s /Applications/Docker.app/Contents/Resources/cli-plugins/docker-compose "$work/docker/cli-plugins/docker-compose"
  else
    printf 'NuGet gate requires the installed Docker Compose plugin\n' >&2
    rm -rf -- "$work"
    exit 1
  fi
fi
compose=(docker compose --env-file /dev/null --project-name "$project" -f compose.integration.yml -f "$work/compose.json" -f "$work/ports.yml")
started=0
cleanup() {
  local result=$?
  local containers="" volumes="" networks="" remaining=""
  trap - EXIT
  if [[ "$started" == 1 ]]; then
    if ! "${compose[@]}" down -v --remove-orphans >"$evidence/cleanup.log" 2>&1; then result=1; fi
  fi
  for resource in container volume network; do
    if [[ "$resource" == container ]]; then
      remaining=$(docker ps -aq --filter "label=com.docker.compose.project=$project")
    else
      remaining=$(docker "$resource" ls -q --filter "label=com.docker.compose.project=$project")
    fi
    case "$resource" in
      container) containers="$remaining" ;;
      volume) volumes="$remaining" ;;
      network) networks="$remaining" ;;
    esac
    if [[ -n "$remaining" ]]; then result=1; fi
  done
  python3 - "$evidence/cleanup.json" "$project" "$result" "$containers" "$volumes" "$networks" <<'PY'
import json,pathlib,sys
pathlib.Path(sys.argv[1]).write_text(json.dumps({'project':sys.argv[2],'exitCode':int(sys.argv[3]),'remainingContainers':sys.argv[4].splitlines(),'remainingVolumes':sys.argv[5].splitlines(),'remainingNetworks':sys.argv[6].splitlines()},indent=2)+'\n')
PY
  rm -rf -- "$work"
  printf 'NuGet Hosted evidence: %s; cleanup exit=%s\n' "$evidence" "$result"
  exit "$result"
}
trap cleanup EXIT

platform=$(docker version --format '{{.Server.Os}}/{{.Server.Arch}}')
case "$platform" in
  linux/amd64) child=sha256:b84b696efd5c236918761d35975209ad15918c4216cc6f41a573ff62e962ce72 ;;
  linux/arm64) child=sha256:4c85fffe3c700195278ea4f86ca47ecac394da6d91b8fd3282fde63807e26659 ;;
  *) printf 'Unsupported official .NET SDK platform: %s\n' "$platform" >&2; exit 1 ;;
esac
sdk_image="mcr.microsoft.com/dotnet/sdk@$child"
curl --disable --fail --silent --show-error --proto '=https' --tlsv1.2 \
  --connect-timeout 15 --max-time 90 --retry 2 \
  -H 'Accept: application/vnd.docker.distribution.manifest.list.v2+json' \
  https://mcr.microsoft.com/v2/dotnet/sdk/manifests/sha256:c7445f141c04f1a6b454181bd098dcfa606c61ba0bd213d0a702489e5bd4cd71 \
  -o "$evidence/dotnet-index.json"
python3 - "$work" "$evidence" "$platform" "$child" <<'PY'
import hashlib,json,pathlib,secrets,socket,subprocess,sys
work,evidence=map(pathlib.Path,sys.argv[1:3])
index=(evidence/'dotnet-index.json').read_bytes()
assert hashlib.sha256(index).hexdigest()=='c7445f141c04f1a6b454181bd098dcfa606c61ba0bd213d0a702489e5bd4cd71','official SDK index changed'
assert index==pathlib.Path('internal/format/nuget/testdata/dotnet-sdk-10.0.100-index.json').read_bytes(),'reviewed SDK root bytes differ'
matches=[m for m in json.loads(index)['manifests'] if m['platform']['os']+'/'+m['platform']['architecture']==sys.argv[3]]
assert len(matches)==1 and matches[0]['digest']==sys.argv[4],'SDK child/platform not in official immutable index'
(evidence/'tool-provenance.json').write_text(json.dumps({'client':'official .NET SDK','version':'10.0.100','registry':'mcr.microsoft.com/dotnet/sdk','rootDigest':'sha256:'+hashlib.sha256(index).hexdigest(),'platform':sys.argv[3],'childDigest':sys.argv[4],'credentialConfig':'owned-empty','clientHome':'owned','NuGet.Config':'owned-target-only'},indent=2)+'\n')
password,access,secret=secrets.token_hex(24),'nuget'+secrets.token_hex(10),secrets.token_hex(24)
(work/'credentials.json').write_text(json.dumps({'password':password,'access':access,'secret':secret}))
(work/'compose.json').write_text(json.dumps({'services':{'postgres':{'environment':{'POSTGRES_PASSWORD':password}},'migrate':{'environment':{'PGPASSWORD':password}},'rustfs':{'environment':{'RUSTFS_ACCESS_KEY':access,'RUSTFS_SECRET_KEY':secret,'RUSTFS_RPC_SECRET':secrets.token_hex(32)}}}}))
sockets=[socket.socket(),socket.socket()]
try:
    for sock in sockets: sock.bind(('127.0.0.1',0))
    pg,s3=[sock.getsockname()[1] for sock in sockets]
    (work/'ports.yml').write_text(f'services:\n  postgres:\n    ports: !override ["127.0.0.1:{pg}:5432"]\n  rustfs:\n    ports: !override ["127.0.0.1:{s3}:9000"]\n')
finally:
    for sock in sockets: sock.close()
files=subprocess.check_output(['git','ls-files','-co','--exclude-standard','-z']).decode().split('\0')
hashes={name:hashlib.sha256(pathlib.Path(name).read_bytes()).hexdigest() for name in sorted(set(files)) if name and pathlib.Path(name).is_file()}
(evidence/'source.json').write_text(json.dumps({'head':subprocess.check_output(['git','rev-parse','HEAD'],text=True).strip(),'tree':subprocess.check_output(['git','rev-parse','HEAD^{tree}'],text=True).strip(),'dirty':bool(subprocess.check_output(['git','status','--porcelain'])),'fileHashes':hashes},indent=2)+'\n')
PY
docker pull --platform "$platform" "$sdk_image" >"$evidence/dotnet-pull.log" 2>&1
go build -o "$work/gateway" ./cmd/gateway
GOOS=linux GOARCH=${platform#linux/} CGO_ENABLED=0 go build -o "$work/gateway-linux" ./cmd/gateway
python3 - "$work/gateway" "$sdk_image" "$evidence/tool-provenance.json" <<'PY'
import hashlib,json,pathlib,subprocess,sys
p=pathlib.Path(sys.argv[3]);data=json.loads(p.read_text())
data.update({'mode':'official-container','runtimeImageID':subprocess.check_output(['docker','image','inspect',sys.argv[2],'--format','{{.Id}}'],text=True).strip(),'gatewayBinarySHA256':hashlib.sha256(pathlib.Path(sys.argv[1]).read_bytes()).hexdigest(),'proxyGroupGatewayRuntime':'linux-sdk-container','proxyGroupGatewayBinarySHA256':hashlib.sha256((pathlib.Path(sys.argv[1]).parent/'gateway-linux').read_bytes()).hexdigest(),'goVersion':subprocess.check_output(['go','version'],text=True).strip()})
p.write_text(json.dumps(data,indent=2)+'\n')
PY
started=1
"${compose[@]}" up -d --wait postgres rustfs
"${compose[@]}" run --rm --no-deps rustfs-ready
"${compose[@]}" run --rm --no-deps migrate
"${compose[@]}" run --rm --no-deps migrate
postgres_address=$("${compose[@]}" port postgres 5432)
rustfs_address=$("${compose[@]}" port rustfs 9000)
python3 - "$work" "$evidence" "$postgres_address" "$rustfs_address" "$sdk_image" "$project" <<'PY'
import hashlib,json,os,pathlib,subprocess,sys
work,evidence=map(pathlib.Path,sys.argv[1:3]);credentials=json.loads((work/'credentials.json').read_text())
env=dict(os.environ)
env.update({'TEST_DATABASE_URL':'postgres://gateway:'+credentials['password']+'@'+sys.argv[3]+'/gateway_test?sslmode=disable','TEST_RUSTFS_ENDPOINT':'http://'+sys.argv[4],'TEST_RUSTFS_ACCESS_KEY':credentials['access'],'TEST_RUSTFS_SECRET_KEY':credentials['secret'],'NUGET_GATEWAY_BIN':str(work/'gateway'),'NUGET_GATEWAY_LINUX_BIN':str(work/'gateway-linux'),'NUGET_DOTNET_IMAGE':sys.argv[5],'NUGET_COMPOSE_PROJECT':sys.argv[6],'NUGET_EVIDENCE_DIR':str(evidence)})
with (evidence/'results.jsonl').open('w') as output:
    result=subprocess.run(['go','test','-json','-count=1','-p','1','-tags=integration,nugetclient','./internal/protocol/nuget','./internal/repository','./internal/format/nuget','./internal/app','./internal/backupops','-run','^Test(NuGet|PostgresNuGet|PostgresRustFSNuGet|NativeNuGetRealDotnet)','-timeout','1200s'],env=env,stdout=output,stderr=subprocess.STDOUT)
events=[]
for line in (evidence/'results.jsonl').read_text().splitlines():
    try: events.append(json.loads(line))
    except ValueError: pass
required={'TestNuGetGovernanceRequiresCompleteDecisionEvidence','TestNuGetGovernanceRecoveryRejectsCorruptRetainedBytes','TestNuGetMemoryGovernanceStoreContract','TestPostgresNuGetGovernanceStoreContract','TestPostgresNuGetGovernanceAuditRollbackAndReopen','TestNuGetNativeUnlistRelistPreservesExactResolution','TestNuGetGovernanceTombstoneQuarantineAndRecovery','TestNuGetGroupGovernanceCannotSelectFallbackOwner','TestNuGetQuarantineReadPolicyFailureIsClosed','TestNuGetDependencyGraphCanonicalization','TestNuGetMemoryStoreContract','TestPostgresNuGetStoreContract','TestPostgresNuGetPaginationAndReopen','TestNuGetPreviewExplicitCompositionAndFormalDiscoveryBoundary','TestPostgresRustFSNuGetStagedRestartAndLostCommitResponse','TestPostgresRustFSNuGetLostUploadResponseAndConcurrentQuota','TestPostgresRustFSNuGetCollectionAndPublicationSerialize','TestPostgresRustFSNuGetCredentialRotationAndRevocation','TestPostgresNuGetPublishedBackupReferences','TestNativeNuGetRealDotnetPushAndColdTransitiveRestore','TestNativeNuGetRealDotnetPrivateProxyGroupRestore','TestNuGetMemorySourceStoreContract','TestPostgresNuGetSourceStoreContract','TestPostgresRustFSNuGetProxyGroupRestartAndLostResponse','TestPostgresRustFSNuGetProxySourceCredentialBindingAndQuota','TestNuGetGroupCanonicalAuthoritySurvivesReorderAndMissingOwner','TestNuGetGroupResourceAuthorizationBlocksCachedAndUncachedFallback','TestNuGetGroupSearchAuthorizesBeforeBinding','TestNuGetProxyDynamicDiscoveryVerifiedCacheAndOffline','TestNuGetUpstreamPerHopCredentialAndEgressIsolation','TestNuGetGroupPreviewDoesNotEnterFormalCatalogOrDeepLinks','TestNuGetGroupExplicitRevalidationPreservesIdentityAfterCredentialRotation','TestPostgresRustFSNuGetProxyGroupLocalCredentialRevocation','TestNuGetProxyTLSFailureCannotUseStaleCache','TestNuGetProxyUnavailableEgressUsesVerifiedCache','TestNuGetProxyRejectsAmbiguousIdentityAndCrossSourceContent','TestNuGetProxyRegistrationPageBoundaries','TestNuGetProxyListedAndUnlistedVersionsKeepIndependentIdentity','TestNuGetProxyExpiredPartialFillCanStartNewReservation','TestNuGetProxySearchPaginationDoesNotTruncateOrRepeat','TestNuGetProxyNegativeCacheAndSourceDenialDoNotFallBack','TestNuGetPreviewSourceCASSecretSeparationAndReadOnlyProxy'}
passed={e['Test'] for e in events if e.get('Action')=='pass' and 'Test'in e}
bad=[e for e in events if e.get('Action') in ('skip','fail')]
report={'exitCode':result.returncode,'required':sorted(required),'requiredPassed':sorted(required&passed),'missing':sorted(required-passed),'passedTests':len(passed),'passedTopLevelTests':len([t for t in passed if '/'not in t]),'failOrSkip':bad,'resultsSHA256':hashlib.sha256((evidence/'results.jsonl').read_bytes()).hexdigest()}
(evidence/'required.json').write_text(json.dumps(report,indent=2)+'\n')
print(json.dumps({k:v for k,v in report.items()if k!='failOrSkip'},indent=2))
if result.returncode or bad or required-passed: raise SystemExit(1)
PY
