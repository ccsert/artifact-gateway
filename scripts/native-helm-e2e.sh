#!/usr/bin/env bash
set -euo pipefail
umask 077
repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repo_root"
exec python3 -B - "$repo_root" <<'PY'
import hashlib,json,os,pathlib,platform,secrets,shutil,subprocess,sys,tempfile,time,urllib.request
root=pathlib.Path(sys.argv[1])
for binary in ('docker','go','curl','node'):
    if not shutil.which(binary): raise SystemExit('Helm preview gate requires '+binary)
run_id='ag291-helm-'+secrets.token_hex(6)
evidence=root/'output/native-helm-e2e'/run_id
evidence.mkdir(parents=True)
work=pathlib.Path(tempfile.mkdtemp(prefix=run_id+'-'))
work.chmod(0o700)
base=dict(os.environ)
env=dict(base)
docker_ready=False
label='com.artifact-gateway.helm-e2e.run='+run_id
containers=[];volumes=set();exit_code=1
required={
 'TestHelmProxyInterruptedOldSourceCannotBlockRotatedClosureOrCollectReplacement',
 'TestHelmProxySnapshotCacheConditionalOfflineAndProvenance',
 'TestHelmProxyMissingCorruptOrCrossIdentityClosureNeverBecomesVisible',
 'TestHelmProxyRejectsAmbiguousBoundlessAndForbiddenIndex',
 'TestHelmProxyConcurrentReadCreatesOneImmutableClosureAndQuotaRejects',
 'TestHelmProxyCredentialRotationInvalidatesSnapshotsAndDenialNeverUsesStale',
 'TestHelmFrozenSourceAuditMatchesResponseStatusBytesAndAnonymousActor',
 'TestHelmGroupPinsSourceAcrossReorderAndFrozenIndex',
 'TestHelmGroupMissingAndCorruptOwnerNeverFallThrough',
 'TestHelmGroupRejectsNestingCyclesWritesAndMemberPermissionFallback',
 'TestHelmUpstreamPerHopCredentialAndEgressIsolation',
 'TestHelmMemorySourceStoreContract',
 'TestPostgresHelmSourceStoreContract',
 'TestPostgresRustFSHelmProxyInterruptedClosureRestartAndOffline',
 'TestHelmFixedOfficialCLIProxyGroupFrozenSourceOfflinePostgresRustFSRestart',
 'TestHelmArchiveRejectsUnsafeIncompleteAndAmbiguousInputs',
 'TestHelmProvenanceBindsChartMetadataFilenameAndDigest',
 'TestHelmHostedPublishesImmutableChartProvenanceAndDeterministicIndex',
 'TestHelmIndexFiltersChartScopedGrantsAndWrites',
 'TestHelmConcurrentPublicationsKeepIndexAndObjectIdentity',
 'TestHelmPreviewGatewayCompositionAndFormalCapabilitiesStayConsistent',
 'TestHelmGrantManagementAcceptsChartPrefixesWithoutFormalSearch',
 'TestHelmMemoryStoreContract',
 'TestPostgresHelmStoreContract',
 'TestPostgresRustFSHelmInterruptedClosureRestartAndCorruptObject',
 'TestHelmFixedOfficialCLIThroughGatewayPostgresRustFSAndRestart',
}
def write_json(name,value): (evidence/name).write_text(json.dumps(value,indent=2)+'\n')
write_json('mandatory-test-cases.json',{'target':'native-helm-e2e','mandatoryCount':len(required),'required':sorted(required)})
write_json('summary.json',{'target':'native-helm-e2e','mandatoryCount':len(required),'mandatoryPassedCount':0,'passed':False,'state':'not-yet-executed'})
def command(args,*,input=None,output=None,check=True):
    if output:
        with (evidence/output).open('w') as log:
            return subprocess.run(args,env=env,input=input,stdout=log,stderr=subprocess.STDOUT,text=True,check=check)
    return subprocess.run(args,env=env,input=input,stdout=subprocess.PIPE,stderr=subprocess.PIPE,text=True,check=check)
def inspect(name):
    result=command(['docker','inspect',name],check=False)
    return json.loads(result.stdout)[0] if result.returncode==0 else None
def start(name,args):
    containers.append(name)
    result=command(['docker','run','--detach','--name',name,'--label',label,'--network','bridge',*args])
    state=inspect(name)
    for mount in state.get('Mounts',[]):
        if mount.get('Type')=='volume': volumes.add(mount['Name'])
    return result.stdout.strip()
try:
    endpoint=base.get('DOCKER_HOST')
    if not endpoint:
        context=subprocess.check_output(['docker','context','show'],text=True).strip()
        endpoint=subprocess.check_output(['docker','context','inspect',context,'--format','{{.Endpoints.docker.Host}}'],text=True).strip()
    if not endpoint.startswith('unix://'): raise SystemExit('Helm preview gate requires a local Docker socket')
    (work/'docker').mkdir();(work/'docker/config.json').write_text('{"auths":{}}\n')
    env=dict(base,DOCKER_HOST=endpoint,DOCKER_CONFIG=str(work/'docker'))
    env.pop('DOCKER_CONTEXT',None);env.pop('DOCKER_DEFAULT_PLATFORM',None)
    docker_ready=True
    host_platform=platform.system().lower()+'-'+({'aarch64':'arm64','x86_64':'amd64'}.get(platform.machine(),platform.machine()))
    hashes={'darwin-arm64':'31513e1193da4eb4ae042eb5f98ef9aca7890cfa136f4707c8d4f70e2115bef6','linux-amd64':'a7f81ce08007091b86d8bd696eb4d86b8d0f2e1b9f6c714be62f82f96a594496','linux-arm64':'440cf7add0aee27ebc93fada965523c1dc2e0ab340d4348da2215737fc0d76ad'}
    if host_platform not in hashes: raise RuntimeError('Unsupported fixed Helm client platform: '+host_platform)
    archive=work/'helm.tar.gz';url='https://get.helm.sh/helm-v3.19.0-'+host_platform+'.tar.gz'
    command(['curl','--disable','--fail','--silent','--show-error','--proto','=https','--tlsv1.2','--connect-timeout','15','--max-time','120','--retry','2','--retry-all-errors',url,'-o',str(archive)],output='helm-download.log')
    actual=hashlib.sha256(archive.read_bytes()).hexdigest()
    if actual!=hashes[host_platform]: raise RuntimeError('Official Helm archive differs from reviewed fixed SHA-256')
    # Extract only the verified Helm executable from the official archive.
    import tarfile
    with tarfile.open(archive) as tar:
        member=tar.getmember(host_platform+'/helm')
        if not member.isfile(): raise RuntimeError('Official archive Helm executable is not a regular file')
        source=tar.extractfile(member)
        binary=work/'helm';binary.write_bytes(source.read());binary.chmod(0o700)
    client_version=command([str(binary),'version','--template','{{.Version}} {{.GitCommit}}']).stdout.strip().split()
    if len(client_version)!=2 or client_version[0]!='v3.19.0': raise RuntimeError('Official Helm version identity mismatch')
    source_proof={}
    for filename,relative in [('helm-install-action.go.txt','pkg/action/install.go'),('helm-install-command.go.txt','cmd/helm/install.go'),('helm-template-command.go.txt','cmd/helm/template.go')]:
        source_url='https://raw.githubusercontent.com/helm/helm/'+client_version[1]+'/'+relative
        command(['curl','--disable','--fail','--silent','--show-error','--proto','=https','--tlsv1.2','--connect-timeout','15','--max-time','90','--retry','2','--retry-all-errors',source_url,'-o',str(evidence/filename)],output=filename+'.log')
        source_proof[relative]={'url':source_url,'sha256':hashlib.sha256((evidence/filename).read_bytes()).hexdigest()}
    action=(evidence/'helm-install-action.go.txt').read_text()
    if 'if !i.ClientOnly {' not in action or 'i.cfg.KubeClient.IsReachable()' not in action: raise RuntimeError('Pinned Helm reachability source branch changed')
    write_json('helm-install-source-proof.json',{'gitCommit':client_version[1],'argv':['install','owned-release','owned/owned','--version','1.2.3','--dry-run=client','--disable-openapi-validation','--verify','--keyring','owned-public-keyring','--namespace','owned'],'evidence':'cmd/helm/install.go configures DryRun/DryRunOption; pkg/action/install.go checks IsReachable unless ClientOnly, which helm install leaves false. helm template sets ClientOnly.','sources':source_proof,'fixture':'owned GET-only Kubernetes discovery; all writes fail','clusterInstallVerified':False})
    write_json('tool-provenance.json',{'cliGitCommit':client_version[1],'installSourceProof':'helm-install-source-proof.json','repositoryReads':'real full Gateway/PostgreSQL/RustFS original objects','installMode':'client dry-run with owned read-only Kubernetes discovery fixture','realKubernetesClusterInstall':False,'client':'official Helm','version':'v3.19.0','release':'https://github.com/helm/helm/releases/tag/v3.19.0','archiveURL':url,'archiveSHA256':actual,'platform':host_platform,'binarySHA256':hashlib.sha256(binary.read_bytes()).hexdigest(),'credentialConfig':'owned-empty','HelmConfig':'owned-target-only','HelmCache':'owned-empty','signingKey':'ephemeral-owned-fixture','verificationKeyring':'explicit-owned-public-keyring'})
    files=command(['git','ls-files','-co','--exclude-standard','-z']).stdout.split('\0')
    hashes_by_path={name:hashlib.sha256((root/name).read_bytes()).hexdigest() for name in sorted(set(files)) if name and (root/name).is_file()}
    write_json('source.json',{'head':command(['git','rev-parse','HEAD']).stdout.strip(),'tree':command(['git','rev-parse','HEAD^{tree}']).stdout.strip(),'dirty':bool(command(['git','status','--porcelain']).stdout),'fileHashes':hashes_by_path,'goVersion':command(['go','version']).stdout.strip()})
    password=secrets.token_hex(24);access='helm'+secrets.token_hex(8);secret=secrets.token_hex(24)
    pg_env=work/'pg.env';pg_env.write_text('POSTGRES_DB=gateway_test\nPOSTGRES_USER=gateway\nPOSTGRES_PASSWORD='+password+'\nPGPASSWORD='+password+'\n')
    rust_env=work/'rust.env';rust_env.write_text('RUSTFS_VOLUMES=/data\nRUSTFS_ADDRESS=0.0.0.0:9000\nRUSTFS_CONSOLE_ENABLE=false\nRUSTFS_ACCESS_KEY='+access+'\nRUSTFS_SECRET_KEY='+secret+'\nRUSTFS_RPC_SECRET='+secrets.token_hex(32)+'\n')
    pg=run_id+'-pg';rust=run_id+'-rustfs'
    rust_image='rustfs/rustfs:1.0.0-beta.12@sha256:41fe89380f4120a337790c02af192c3fe7bb55c3edc2e6e9357b487b47c6ab21'
    start(pg,['--env-file',str(pg_env),'--tmpfs','/var/lib/postgresql/data:rw','-p','127.0.0.1::5432','postgres:16-alpine','postgres','-c','shared_preload_libraries=pg_stat_statements'])
    start(rust,['--env-file',str(rust_env),'--tmpfs','/data:rw,mode=1777','--tmpfs','/logs:rw,mode=1777','-p','127.0.0.1::9000',rust_image])
    deadline=time.monotonic()+90
    while time.monotonic()<deadline:
        if command(['docker','exec',pg,'pg_isready','-U','gateway','-d','gateway_test'],check=False).returncode==0: break
        time.sleep(0.5)
    else: raise RuntimeError('Owned PostgreSQL failed readiness')
    pg_address=command(['docker','port',pg,'5432/tcp']).stdout.strip();rust_address=command(['docker','port',rust,'9000/tcp']).stdout.strip()
    deadline=time.monotonic()+90
    while time.monotonic()<deadline:
        try:
            with urllib.request.urlopen('http://'+rust_address+'/health',timeout=2) as response:
                if response.status==200: break
        except Exception: pass
        time.sleep(0.5)
    else: raise RuntimeError('Owned RustFS failed readiness')
    command(['docker','cp',str(root/'migrations'),pg+':/migrations'])
    migration=(root/'scripts/run-migrations.sh').read_text()
    migration_args=['docker','exec','-i','--env','PGHOST=127.0.0.1','--env','PGUSER=gateway','--env','PGDATABASE=gateway_test',pg,'sh']
    command(migration_args,input=migration,output='migrations-first.log')
    command(migration_args,input=migration,output='migrations-noop.log')
    env.update(TEST_DATABASE_URL='postgres://gateway:'+password+'@'+pg_address+'/gateway_test?sslmode=disable',TEST_RUSTFS_ENDPOINT='http://'+rust_address,TEST_RUSTFS_ACCESS_KEY=access,TEST_RUSTFS_SECRET_KEY=secret,HELM_TEST_BINARY=str(binary),HELM_OWNED_EVIDENCE_DIR=str(evidence),GATEWAY_REPOSITORY_PRESETS='none')
    env.update(HELM_SDK_NODE=shutil.which('node'),HELM_SDK_PUBLISH_SCRIPT=str(root/'scripts/helm-sdk-publish.ts'))
    result=command(['go','test','-json','-count=1','-p','1','-tags=integration','./internal/protocol/helm','./internal/repository','./internal/format/helm','./internal/app','-run','Helm','-timeout','600s'],output='results.jsonl',check=False)
    events=[]
    for line in (evidence/'results.jsonl').read_text().splitlines():
        try: events.append(json.loads(line))
        except ValueError: pass
    passed={item['Test'] for item in events if item.get('Action')=='pass' and 'Test' in item}
    bad=[item for item in events if item.get('Action') in ('skip','fail')]
    report={'target':'native-helm-e2e','exitCode':result.returncode,'required':sorted(required),'requiredPassed':sorted(required&passed),'missing':sorted(required-passed),'mandatoryCount':len(required),'mandatoryPassedCount':len(required&passed),'passedTests':len(passed),'passedTopLevelTests':len([name for name in passed if '/' not in name]),'failOrSkip':bad,'resultsSHA256':hashlib.sha256((evidence/'results.jsonl').read_bytes()).hexdigest()}
    write_json('required.json',report)
    write_json('summary.json',{'target':'native-helm-e2e','mandatoryCount':len(required),'mandatoryPassedCount':len(required&passed),'missing':sorted(required-passed),'failOrSkipCount':len(bad),'client':'Helm v3.19.0','scope':'traditional-http-hosted-proxy-group-preview','proxyGroup':True,'formalAdmission':False,'passed':result.returncode==0 and not bad and not required-passed})
    exit_code=0 if result.returncode==0 and not bad and not required-passed else 1
except Exception as error:
    # Credential-bearing Docker argument vectors and environment values are
    # deliberately excluded from evidence and error reports.
    message='Owned Helm gate failed: '+type(error).__name__
    if isinstance(error,RuntimeError): message+=': '+str(error)
    if isinstance(error,subprocess.CalledProcessError):
        message+=': '+str(error.cmd[:3])+' exit='+str(error.returncode)
        detail=error.stderr or ''
        for secret_value in (locals().get('password'),locals().get('secret'),locals().get('access')):
            if secret_value: detail=detail.replace(secret_value,'[owned credential]')
        (evidence/'command-failure.log').write_text(detail)
    (evidence/'failure.txt').write_text(message+'\n')
    print(message,file=sys.stderr)
finally:
    remaining=[];remaining_volumes=[];cleanup_ok=True
    for name in reversed(containers) if docker_ready else []:
        state=inspect(name)
        if state is None: continue
        if state.get('Config',{}).get('Labels',{}).get('com.artifact-gateway.helm-e2e.run')!=run_id:
            cleanup_ok=False;remaining.append(name);continue
        command(['docker','logs',name],output=name+'-runtime.log',check=False)
        if command(['docker','rm','--force','--volumes',name],check=False).returncode!=0: cleanup_ok=False
        if inspect(name) is not None: remaining.append(name)
    for volume in sorted(volumes):
        if command(['docker','volume','inspect',volume],check=False).returncode==0: remaining_volumes.append(volume)
    leftovers=[]
    if docker_ready:
        readback=command(['docker','ps','--all','--quiet','--filter','label='+label],check=False)
        leftovers=readback.stdout.splitlines()
        if readback.returncode!=0: cleanup_ok=False
    try: shutil.rmtree(work)
    except Exception: cleanup_ok=False
    work_removed=not work.exists()
    if leftovers or remaining or remaining_volumes or not cleanup_ok or not work_removed: exit_code=1
    write_json('cleanup.json',{'runId':run_id,'exitCode':int(exit_code),'ownedContainers':containers,'remainingContainers':remaining+leftovers,'ownedVolumes':sorted(volumes),'remainingVolumes':remaining_volumes,'createdNetworks':[],'remainingNetworks':[],'networkMode':'existing-default-bridge','workDirectoryRemoved':work_removed,'cleanupReadBackSucceeded':cleanup_ok})
    print('Helm preview evidence: '+str(evidence)+'; cleanup exit='+str(exit_code))
sys.exit(exit_code)
PY
