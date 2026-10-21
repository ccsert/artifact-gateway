#!/usr/bin/env bash
set -euo pipefail
umask 077
repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repo_root"
for binary in docker go curl python3 tar; do
  command -v "$binary" >/dev/null || { printf 'OCI discovery gate requires %s\n' "$binary" >&2; exit 1; }
done
# Sources are owned in-process TLS fixtures. Every official Docker pull uses a
# new labelled daemon/volume, never the user's image cache or registry config.
docker_endpoint=${DOCKER_HOST:-$(docker context inspect "$(docker context show)" --format '{{.Endpoints.docker.Host}}')}
case "$docker_endpoint" in unix://*) ;; *) printf 'OCI discovery gate requires a local Docker socket\n' >&2; exit 1 ;; esac
work=$(mktemp -d)
project="artifact-gateway-oci-discovery-$$-$RANDOM"
evidence="$repo_root/output/native-oci-discovery-e2e/$project"
mkdir -p "$work/docker/cli-plugins" "$work/bin" "$evidence"
printf '{"auths":{}}\n' > "$work/docker/config.json"
export DOCKER_CONFIG="$work/docker" DOCKER_HOST="$docker_endpoint"
unset DOCKER_CONTEXT DOCKER_DEFAULT_PLATFORM
compose=(docker compose --env-file /dev/null --project-name "$project" -f compose.integration.yml -f "$work/compose.json")
started=0
cleanup() {
  local result=$? owned
  trap - EXIT
  # These exact, fresh labels also cover interruption before Go's cleanup can
  # run. They never select a resource belonging to another lane or a user.
  owned=$(docker ps -aq --filter "label=ag.oci-discovery-owner=$project" --filter "label=com.docker.compose.project=$project")
  if [[ -n "$owned" ]]; then
    while IFS= read -r id; do docker container rm --force "$id" >>"$evidence/cleanup.log" 2>&1 || result=1; done <<< "$owned"
  fi
  owned=$(docker volume ls -q --filter "label=ag.oci-discovery-owner=$project" --filter "label=com.docker.compose.project=$project")
  if [[ -n "$owned" ]]; then
    while IFS= read -r name; do docker volume rm "$name" >>"$evidence/cleanup.log" 2>&1 || result=1; done <<< "$owned"
  fi
  if [[ "$started" == 1 ]]; then "${compose[@]}" down -v --remove-orphans >>"$evidence/cleanup.log" 2>&1 || result=1; fi
  python3 - "$project" "$evidence/cleanup.json" "$result" <<'PY'
import json,pathlib,subprocess,sys
project=sys.argv[1];result={'project':project,'exitCode':int(sys.argv[3])}
for kind,args in [('remainingContainers',['ps','-aq']),('remainingVolumes',['volume','ls','-q']),('remainingNetworks',['network','ls','-q'])]:
    result[kind]=subprocess.check_output(['docker',*args,'--filter','label=com.docker.compose.project='+project],text=True).splitlines()
if any(result[k] for k in ('remainingContainers','remainingVolumes','remainingNetworks')):result['exitCode']=1
pathlib.Path(sys.argv[2]).write_text(json.dumps(result,indent=2)+'\n')
PY
  if ! python3 - "$evidence/cleanup.json" <<'PY'
import json,sys
raise SystemExit(json.load(open(sys.argv[1]))['exitCode'])
PY
  then result=1; fi
  rm -rf -- "$work"
  python3 - "$evidence/cleanup.json" "$work" <<'PY'
import json,pathlib,sys
p=pathlib.Path(sys.argv[1]);data=json.loads(p.read_text());data['ownedWorkRemoved']=not pathlib.Path(sys.argv[2]).exists();p.write_text(json.dumps(data,indent=2)+'\n')
PY
  printf 'OCI discovery evidence: %s; cleanup exit=%s\n' "$evidence" "$result"
  exit "$result"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
if ! docker compose version >/dev/null 2>&1; then
  if [[ -x /Applications/Docker.app/Contents/Resources/cli-plugins/docker-compose ]]; then
    ln -s /Applications/Docker.app/Contents/Resources/cli-plugins/docker-compose "$work/docker/cli-plugins/docker-compose"
  else printf 'Docker Compose plugin is required\n' >&2; exit 1; fi
fi
python3 - "$work" "$evidence" <<'PY'
import hashlib,json,pathlib,secrets,subprocess,sys
work,evidence=map(pathlib.Path,sys.argv[1:])
x={'password':secrets.token_hex(24),'access':'oci'+secrets.token_hex(10),'secret':secrets.token_hex(24)}
(work/'credentials.json').write_text(json.dumps(x))
(work/'compose.json').write_text(json.dumps({'services':{'postgres':{'environment':{'POSTGRES_PASSWORD':x['password']}},'migrate':{'environment':{'PGPASSWORD':x['password']}},'rustfs':{'environment':{'RUSTFS_ACCESS_KEY':x['access'],'RUSTFS_SECRET_KEY':x['secret'],'RUSTFS_RPC_SECRET':secrets.token_hex(32)}}}}))
files=subprocess.check_output(['git','ls-files','-co','--exclude-standard','-z']).decode().split('\0')
hashes={name:hashlib.sha256(pathlib.Path(name).read_bytes()).hexdigest() for name in sorted(set(files)) if name and pathlib.Path(name).is_file()}
(evidence/'source.json').write_text(json.dumps({'head':subprocess.check_output(['git','rev-parse','HEAD'],text=True).strip(),'tree':subprocess.check_output(['git','rev-parse','HEAD^{tree}'],text=True).strip(),'dirty':bool(subprocess.check_output(['git','status','--porcelain'])),'fileHashes':hashes},indent=2)+'\n')
PY
curl_args=(--disable --fail --silent --show-error --location --proto '=https' --tlsv1.2 --connect-timeout 15 --max-time 120 --retry 2)
case "$(uname -s)/$(uname -m)" in
  Linux/x86_64) oras_platform=linux_amd64; oras_sha=9ce999f8d2de03fc03968b29d743077a58783e545e5eaa53917ca177352d0e59 ;;
  Linux/aarch64|Linux/arm64) oras_platform=linux_arm64; oras_sha=ac7156f93a21e903f7ad606c792f3560f17e0cd0e36365634701b1e7cc4e4eca ;;
  Darwin/x86_64) oras_platform=darwin_amd64; oras_sha=aeb684d8c24c18dce28fd1f7326636e4782b573108e244a93d4b1c4a5ec50f48 ;;
  Darwin/arm64) oras_platform=darwin_arm64; oras_sha=f33fc12753c54172b0d0d19eaa0318d3f90fe9b094d96e8b259c881713c92e1c ;;
  *) printf 'Unsupported official ORAS platform\n' >&2; exit 1 ;;
esac
oras_bootstrap=official-download
if [[ -n "${OCI_DISCOVERY_ORAS_ARCHIVE:-}" ]]; then
  # A local dependency cache supplies only the original archive. The platform's
  # pinned digest below is still mandatory, and every client HOME remains new.
  cp -- "$OCI_DISCOVERY_ORAS_ARCHIVE" "$work/oras.tar.gz"
  oras_bootstrap=verified-local-archive
else
  curl "${curl_args[@]}" "https://github.com/oras-project/oras/releases/download/v1.3.3/oras_1.3.3_${oras_platform}.tar.gz" --output "$work/oras.tar.gz"
fi
python3 - "$work/oras.tar.gz" "$oras_sha" <<'PY'
import hashlib,pathlib,sys
assert hashlib.sha256(pathlib.Path(sys.argv[1]).read_bytes()).hexdigest()==sys.argv[2], 'official ORAS 1.3.3 archive SHA mismatch'
PY
tar -xzf "$work/oras.tar.gz" -C "$work/bin" oras
platform=$(docker version --format '{{.Server.Os}}/{{.Server.Arch}}')
python3 - "$evidence" "$platform" "$oras_platform" "$oras_sha" "$oras_bootstrap" <<'PY'
import hashlib,json,pathlib,subprocess,sys
out=pathlib.Path(sys.argv[1]);platform=sys.argv[2]
children={'linux/amd64':'sha256:6acc6aaf783ac1c1100822e542534c3dab3f1d38782760b0bdcb688280574d9e','linux/arm64':'sha256:1cf1cad2a305cb46c39d4b5d2920508fe8a4b81a8dcd2963cae3d72f7f3c8ae3'}
if platform not in children:raise SystemExit('unsupported official Docker platform')
base=['curl','--disable','--fail','--silent','--show-error','--proto','=https','--tlsv1.2','--connect-timeout','15','--max-time','120','--retry','2']
token=json.loads(subprocess.check_output(base+['https://auth.docker.io/token?service=registry.docker.io&scope=repository:library/docker:pull']))['token']
config='header = "Authorization: Bearer '+token+'"\nheader = "Accept: application/vnd.oci.image.index.v1+json"\n'
body=subprocess.check_output(base+['--config','-','https://registry-1.docker.io/v2/library/docker/manifests/sha256:3ef33f2e220b79ed3ef3b99d81746f06f306cd6340e2cb7331d17ae996e74cb6'],input=config.encode())
assert hashlib.sha256(body).hexdigest()=='3ef33f2e220b79ed3ef3b99d81746f06f306cd6340e2cb7331d17ae996e74cb6'
assert body==pathlib.Path('internal/app/testdata/oci-discovery-docker-29.7.2-dind-index.json').read_bytes()
matched=[m for m in json.loads(body)['manifests'] if m['platform']['os']+'/'+m['platform']['architecture']==platform]
assert len(matched)==1 and matched[0]['digest']==children[platform]
(out/'docker-index.json').write_bytes(body)
(out/'tool-provenance.json').write_text(json.dumps({'ORAS':{'version':'1.3.3','platform':sys.argv[3],'archiveSHA256':sys.argv[4],'bootstrap':sys.argv[5]},'Docker':{'version':'29.7.2','platform':platform,'rootDigest':'sha256:'+hashlib.sha256(body).hexdigest(),'childDigest':children[platform],'consumer':'new owned dind daemon and volume per pull'},'Go':subprocess.check_output(['go','version'],text=True).strip()},indent=2)+'\n')
PY
child=$(python3 - "$evidence/tool-provenance.json" <<'PY'
import json,sys
print(json.load(open(sys.argv[1]))['Docker']['childDigest'])
PY
)
docker_image="docker.io/library/docker@$child"
docker pull --platform "$platform" "$docker_image" >"$evidence/docker-pull.log" 2>&1
started=1
"${compose[@]}" up -d --wait --wait-timeout 60 postgres rustfs >"$evidence/start.log" 2>&1
"${compose[@]}" run --rm --no-deps rustfs-ready >>"$evidence/start.log" 2>&1
"${compose[@]}" run --rm --no-deps migrate >"$evidence/migrate-first.log" 2>&1
"${compose[@]}" run --rm --no-deps migrate >"$evidence/migrate-second.log" 2>&1
pg=$("${compose[@]}" port postgres 5432)
s3=$("${compose[@]}" port rustfs 9000)
python3 - "$work" "$evidence" "$pg" "$s3" "$project" "$docker_image" <<'PY'
import hashlib,json,os,pathlib,subprocess,sys
work,evidence=map(pathlib.Path,sys.argv[1:3]);x=json.loads((work/'credentials.json').read_text())
env=dict(os.environ,TEST_DATABASE_URL='postgres://gateway:'+x['password']+'@'+sys.argv[3]+'/gateway_test?sslmode=disable',TEST_RUSTFS_ENDPOINT='http://'+sys.argv[4],TEST_RUSTFS_ACCESS_KEY=x['access'],TEST_RUSTFS_SECRET_KEY=x['secret'],OCI_DISCOVERY_REQUIRED='1',OCI_DISCOVERY_ORAS=str(work/'bin/oras'),OCI_DISCOVERY_PROJECT=sys.argv[5],OCI_DISCOVERY_DIND_IMAGE=sys.argv[6],TMPDIR=str(work))
command=['go','test','-json','-race','-count=1','-p','1','-timeout=8m','-tags=integration,ocidiscoveryclient','./internal/ocidiscovery','./internal/repository','./internal/app','-run','^Test(OCICatalog|OCIReferrers|MemoryOCIDiscovery|ReferrerManifest|Postgres.*OCIDiscovery)']
(evidence/'command.json').write_text(json.dumps(command,indent=2)+'\n')
with (evidence/'results.jsonl').open('w') as out:result=subprocess.run(command,env=env,stdout=out,stderr=subprocess.STDOUT)
events=[]
for line in (evidence/'results.jsonl').read_text().splitlines():
    try:events.append(json.loads(line))
    except ValueError:pass
required={'TestReferrerManifestSubjectAndDescriptors','TestMemoryOCIDiscoverySnapshotAndBinding','TestPostgresOCIDiscoverySnapshotAndBinding','TestPostgresOCIDiscoverySnapshotReopenAndGrantFence','TestPostgresOCIDiscoveryPublicationGuardsSerialize','TestPostgresRustFSOCIDiscoveryRestartAndOffline','TestPostgresRustFSOCIDiscoveryOfficialORASAndDocker','TestOCICatalogProxyGroupSnapshotAndAuthorization','TestOCICatalogUnsupportedPartialAndPaginationSafety','TestOCICatalogSourceVersionAndMissingPort','TestOCICatalogBearerUsesRegistryScope','TestOCIReferrersVerifiedSourceBindingAndColdReads','TestOCIReferrersRejectIncompleteAndUnsupportedAuthority','TestOCIReferrersStablePaginationAndLocalFilter','TestOCIReferrersRevocationNeverSelectsAnotherSource','TestOCIReferrersPrincipalAuthorityPartition','TestPostgresRustFSOCIDiscoveryPrincipalAuthorityPartition','TestOCICatalogMixedSourcesReturnReadableCanonicalNames','TestOCIReferrersRejectInFlightSourceChange','TestPostgresRustFSOCIDiscoveryRejectInFlightSourceChange','TestOCIReferrersRequireExplicitAuthorityBeforeSourceAccess','TestOCICatalogRejectsConflictingSourceVersionsAcrossViews','TestPostgresRustFSOCIDiscoveryCatalogConflictingVersions','TestOCIReferrersExplicitCredentialsNeverBecomeAnonymous','TestPostgresRustFSOCIDiscoveryExplicitCredentialsNeverBecomeAnonymous','TestOCIReferrersDescriptorAnnotationsFromVerifiedManifest','TestPostgresRustFSOCIDiscoveryDescriptorAnnotationsFromVerifiedManifest','TestOCIReferrersAnonymousIdentityNeverSharesAuthenticatedAuthority','TestPostgresRustFSOCIDiscoveryAnonymousIdentityNeverSharesAuthenticatedAuthority'}
run={e.get('Test') for e in events if e.get('Action')=='run'};passed={e.get('Test') for e in events if e.get('Action')=='pass'};bad=[e for e in events if e.get('Action') in ('skip','fail')]
source=json.loads((evidence/'source.json').read_text());after={name:hashlib.sha256(pathlib.Path(name).read_bytes()).hexdigest() for name in source['fileHashes']}
summary={'required':sorted(required),'requiredRun':sorted(required&run),'requiredPassed':sorted(required&passed),'missingRun':sorted(required-run),'missingPass':sorted(required-passed),'actualNamedPassed':len(passed-{None}),'failedOrSkipped':bad,'exitCode':result.returncode,'sourceUnchanged':after==source['fileHashes'],'rawSHA256':hashlib.sha256((evidence/'results.jsonl').read_bytes()).hexdigest()}
(evidence/'results.json').write_text(json.dumps(summary,indent=2)+'\n')
for e in events:
    if e.get('Output'):print(e['Output'],end='')
if result.returncode or required-run or required-passed or bad or not summary['sourceUnchanged']:raise SystemExit('mandatory OCI discovery proof missing, failed, skipped or source changed')
print(f'All {len(required)} OCI discovery required fixtures passed: real PG/RustFS, pinned ORAS discover/pull and fresh Docker daemon pulls, restart/offline and restricted grants.')
PY
