#!/usr/bin/env bash
set -euo pipefail
umask 077
repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repo_root"
exec python3 -B - "$repo_root" <<'PY'
import hashlib,json,os,pathlib,secrets,shutil,signal,socket,subprocess,sys,tempfile,time,urllib.request
gate_signals={signal.SIGTERM,signal.SIGINT}
initial_signal_mask=signal.pthread_sigmask(signal.SIG_BLOCK,gate_signals)
root=pathlib.Path(sys.argv[1]);run_id='ag459-rubygems-'+secrets.token_hex(6)
evidence=root/'output/native-rubygems-e2e'/run_id;evidence.mkdir(parents=True)
work=pathlib.Path(tempfile.mkdtemp(prefix=run_id+'-'));work.chmod(0o700)
env=dict(os.environ);containers=[];volumes=[];network=run_id+'-net';exit_code=1
cancelled_signal=None;cleaning=False;cleanup_deadline=None;active_processes={};process_receipts=[];cleanup_errors=[]
label='com.artifact-gateway.rubygems-e2e.run='+run_id
image='ruby:3.4.4-alpine@sha256:81096866ac15f906adc79867da3ed97a2aa271d6149363e216a174701345c53b'
rust_image='rustfs/rustfs:1.0.0-beta.12@sha256:41fe89380f4120a337790c02af192c3fe7bb55c3edc2e6e9357b487b47c6ab21'
required={
'TestRubyGemArchivePreservesDependenciesAcrossBenignTrailingFields',
'TestRubyGemRejectsYamlAndTarParserDifferentials',
'TestRubyGemPlatformDefaultsAndLongUSTARPathsKeepDependencies',
'TestRubyGemArchiveRejectsUnsafeIncompleteAmbiguousAndUnboundedInput',
'TestRubyGemLegacyAndCompactVersionPlatformIndexesAgree',
'TestRubyGemsHostedPublishesImmutableBytesAndMatchingIndexes',
'TestRubyGemsAuthorizationScopesFilterEveryIndexAndProtectWrites',
'TestRubyGemsQuotaAndConcurrentPushKeepOneAtomicIdentity',
'TestRubyGemsInterruptedUploadIsInvisibleAndExpiredObjectsAreCollected',
'TestRubyGemsAuditFailureRollsBackPublicationAndCorruptionFailsClosed',
'TestRubyGemsPreviewRequiresExplicitPortsAndNeverAdmitsFormalFormat',
'TestRubyGemsMemoryPublicationStoreContract',
'TestRubyGemsPublicationRechecksRevocationGrantAccountAndExpiryBeforeCommit',
'TestRubyGemsHeadConditionalAndRangeKeepAuthorizedRepresentation',
'TestRubyGemsAnonymousRequiresBothPoliciesAndExplicitInvalidKeyNeverFallsBack',
'TestRubyGemsInvalidAndOversizedUploadsCreateNoObjectsOrVisibleIndex',
'TestRubyGemsInvalidSpecificationIsRejectedBeforePublication',
'TestRubyGemsFixedOfficialSpecificationDateAndRequirePaths',
'TestRubyGemsPostgresPublicationStoreContract',
'TestRubyGemsPostgresRustFSAtomicAuditInterruptionQuotaAndRestart',
'TestRubyGemsFixedOfficialClientColdTransitiveInstallAndRestart',
}
class GateCancelled(Exception):
    def __init__(self,signum):self.signum=signum
def cancel_gate(signum,_frame):
    global cancelled_signal
    cancelled_signal=signum
    for sig in gate_signals:signal.signal(sig,signal.SIG_IGN)
    raise GateCancelled(signum)
def cancel_during_cleanup(signum,_frame):
    global cancelled_signal,exit_code
    cancelled_signal=signum;exit_code=128+signum
    for sig in gate_signals:signal.signal(sig,signal.SIG_IGN)
for sig in gate_signals:signal.signal(sig,cancel_gate)
def write(name,value):(evidence/name).write_text(json.dumps(value,indent=2)+'\n')
def stop_process_group(process):
    # Each command has an owned process group, including Go's test/Gateway
    # descendants. Reap the direct child and stop the complete group on cancel.
    # A completed input command has already closed stdin. Retrying communicate
    # must not replace its real failure with a flush of that closed stream.
    if process.stdin is not None and process.stdin.closed:process.stdin=None
    for sig in (signal.SIGTERM,signal.SIGKILL):
        try:os.killpg(process.pid,sig)
        except ProcessLookupError:pass
        try:process.communicate(timeout=2)
        except subprocess.TimeoutExpired:continue
    if process.poll() is None:process.wait(timeout=2)
def command(args,*,input=None,output=None,check=True,timeout=None):
    process=None;log=None;interrupted=False
    if cleaning:
        remaining=cleanup_deadline-time.monotonic()
        if remaining<=0:
            cleanup_errors.append({'command':args[:2],'errorType':'CleanupDeadlineExceeded'})
            return subprocess.CompletedProcess(args,124,'','owned cleanup deadline exceeded')
        timeout=min(timeout or 5,remaining)
    try:
        if output:log=(evidence/output).open('w')
        previous=signal.pthread_sigmask(signal.SIG_BLOCK,gate_signals)
        try:
            process=subprocess.Popen(args,env=env,stdin=subprocess.PIPE if input is not None else None,stdout=log or subprocess.PIPE,stderr=subprocess.STDOUT if log else subprocess.PIPE,text=True,start_new_session=True)
            active_processes[process.pid]=process
        finally:signal.pthread_sigmask(signal.SIG_SETMASK,previous)
        stdout,stderr=process.communicate(input=input,timeout=timeout)
        result=subprocess.CompletedProcess(args,process.returncode,stdout,stderr)
        if check:result.check_returncode()
        return result
    except BaseException as error:
        interrupted=True
        if process is not None:stop_process_group(process)
        if cleaning:
            cleanup_errors.append({'command':args[:2],'errorType':type(error).__name__})
            return subprocess.CompletedProcess(args,124,'','owned cleanup command failed')
        raise
    finally:
        if process is not None:
            active_processes.pop(process.pid,None)
            process_receipts.append({'pid':process.pid,'command':args[:2],'exitCode':process.returncode,'waitReturned':process.returncode is not None,'processGroupStopped':interrupted,'cleanup':cleaning})
        if log:log.close()
def inspect(name):
    r=command(['docker','inspect',name],check=False)
    return json.loads(r.stdout)[0] if r.returncode==0 else None
def start(name,args):
    containers.append(name);return command(['docker','run','--detach','--name',name,'--label',label,'--network',network,*args]).stdout.strip()
write('mandatory-test-cases.json',{'target':'native-rubygems-e2e','mandatoryCount':len(required),'required':sorted(required)})
write('summary.json',{'target':'native-rubygems-e2e','mandatoryCount':len(required),'mandatoryPassedCount':0,'passed':False,'state':'not-yet-executed'})
try:
    signal.pthread_sigmask(signal.SIG_SETMASK,initial_signal_mask)
    for binary in ('go','docker','curl'):
        if not shutil.which(binary):raise RuntimeError('RubyGems preview gate requires '+binary)
    endpoint=env.get('DOCKER_HOST')
    if not endpoint:
        context=command(['docker','context','show']).stdout.strip();endpoint=command(['docker','context','inspect',context,'--format','{{.Endpoints.docker.Host}}']).stdout.strip()
    if not endpoint.startswith('unix://'):raise RuntimeError('Owned gate requires a local Docker socket')
    (work/'docker').mkdir();(work/'docker/config.json').write_text('{"auths":{}}\n');env.update(DOCKER_HOST=endpoint,DOCKER_CONFIG=str(work/'docker'));env.pop('DOCKER_CONTEXT',None);env.pop('DOCKER_DEFAULT_PLATFORM',None)
    # Every download command has a 45-second request budget. A failed pull is
    # retained as a failed gate, never converted into a client skip.
    for tool_image,name in ((image,'ruby'),(rust_image,'rustfs')):
        if command(['docker','image','inspect',tool_image],check=False).returncode!=0:command(['docker','pull',tool_image],output=name+'-pull.log',timeout=45)
    ruby_inspect=json.loads(command(['docker','image','inspect',image]).stdout)[0]
    probe=command(['docker','run','--rm','--network','none','--label',label,image,'ruby','-e','require "rubygems";require "bundler";puts [RUBY_VERSION,Gem::VERSION,Bundler::VERSION].join(" ")']).stdout.strip()
    if probe!='3.4.4 3.6.7 2.6.7':raise RuntimeError('Fixed official Ruby/RubyGems/Bundler identity mismatch: '+probe)
    source_proof={}
    source_files=['lib/rubygems/specification.rb','lib/rubygems/version.rb','lib/rubygems/requirement.rb','lib/rubygems/package.rb','lib/rubygems/platform.rb','lib/rubygems/commands/push_command.rb','lib/rubygems/gemcutter_utilities.rb','bundler/lib/bundler/compact_index_client/parser.rb']
    expected_source_hashes={'lib/rubygems/platform.rb':'741d03e920690639402c5a39a84abd27e9b5f9e93d5569170d3a23aa62677bb4','lib/rubygems/specification.rb':'bcf89944d63f082c54474af101c6419605ac5c2534384debce11eab0e6eae434','lib/rubygems/version.rb':'712bca6acd43c9a6cc226afc65a44cfc38e3eb9737a4b357f734c5e1ed20f519','lib/rubygems/requirement.rb':'76b56c47ca3372cdcc85829d96296cbb5923504ff464f6556de9c9fccbcdc913','lib/rubygems/package.rb':'712310184686c9a2bb692a86f544fd7e2f0fbe911eabdbe34e35fe39ee9b4686','lib/rubygems/commands/push_command.rb':'4bbaa13cf60df4f65c7a40932add4fc34640d4ecef1465138773b5bbcedab282','lib/rubygems/gemcutter_utilities.rb':'edcb9cdaf3826c7c781d057e9cb0e77f9f6bf0173b5fe148713bf71543686476','bundler/lib/bundler/compact_index_client/parser.rb':'ea6d6d5698731ebed1e2e7523db81d8556c4b4772f35bba9d6f36f9e6a7841eb'}
    for path in source_files:
        url='https://raw.githubusercontent.com/rubygems/rubygems/v3.6.7/'+path;filename='official-source-'+path.replace('/','_')
        command(['curl','--disable','--fail','--silent','--show-error','--proto','=https','--tlsv1.2','--connect-timeout','15','--max-time','45',url,'-o',str(evidence/filename)],output=filename+'.log')
        actual_source_hash=hashlib.sha256((evidence/filename).read_bytes()).hexdigest()
        if actual_source_hash!=expected_source_hashes[path]:raise RuntimeError('Pinned official RubyGems source digest mismatch: '+path)
        source_proof[path]={'url':url,'sha256':actual_source_hash}
    write('tool-provenance.json',{'toolchain':probe,'image':image,'imageId':ruby_inspect['Id'],'repoDigests':ruby_inspect.get('RepoDigests',[]),'platform':ruby_inspect['Os']+'/'+ruby_inspect['Architecture'],'rubyGemsSourceTag':'v3.6.7','bundlerVersion':'2.6.7','sources':source_proof,'packaging':'official gem build in network-none container','consumers':'owned-empty GEM_HOME/GEM_PATH/GEM_SPEC_CACHE/Bundler caches and target-only source','formalAdmission':False})
    files=command(['git','ls-files','-co','--exclude-standard','-z']).stdout.split('\0');hashes={name:hashlib.sha256((root/name).read_bytes()).hexdigest() for name in sorted(set(files)) if name and (root/name).is_file()}
    write('source.json',{'head':command(['git','rev-parse','HEAD']).stdout.strip(),'tree':command(['git','rev-parse','HEAD^{tree}']).stdout.strip(),'dirty':bool(command(['git','status','--porcelain']).stdout),'fileHashes':hashes,'goVersion':command(['go','version']).stdout.strip()})
    command(['docker','network','create','--label',label,network]);password=secrets.token_hex(24);access='ruby'+secrets.token_hex(8);secret=secrets.token_hex(24)
    for suffix in ('pg','rust','logs'):
        name=run_id+'-'+suffix+'-data';volumes.append(name);command(['docker','volume','create','--label',label,name])
    pg_env=work/'pg.env';pg_env.write_text('POSTGRES_DB=gateway_test\nPOSTGRES_USER=gateway\nPOSTGRES_PASSWORD='+password+'\nPGPASSWORD='+password+'\n')
    rust_env=work/'rust.env';rust_env.write_text('RUSTFS_VOLUMES=/data\nRUSTFS_ADDRESS=0.0.0.0:9000\nRUSTFS_CONSOLE_ENABLE=false\nRUSTFS_ACCESS_KEY='+access+'\nRUSTFS_SECRET_KEY='+secret+'\nRUSTFS_RPC_SECRET='+secrets.token_hex(32)+'\n')
    pg=run_id+'-pg';rust=run_id+'-rustfs'
    def owned_port():
        with socket.socket() as sock:
            sock.bind(('127.0.0.1',0));return sock.getsockname()[1]
    pg_port=owned_port();rust_port=owned_port()
    start(pg,['--env-file',str(pg_env),'--mount','type=volume,src='+volumes[0]+',dst=/var/lib/postgresql/data','-p','127.0.0.1:'+str(pg_port)+':5432','postgres:16-alpine','postgres','-c','shared_preload_libraries=pg_stat_statements'])
    start(rust,['--env-file',str(rust_env),'--mount','type=volume,src='+volumes[1]+',dst=/data','--mount','type=volume,src='+volumes[2]+',dst=/logs','-p','127.0.0.1:'+str(rust_port)+':9000',rust_image])
    deadline=time.monotonic()+45
    while time.monotonic()<deadline:
        ready=command(['docker','exec',pg,'psql','-X','-h','127.0.0.1','-U','gateway','-d','gateway_test','-v','ON_ERROR_STOP=1','-Atqc','SELECT 1'],check=False)
        if ready.returncode==0 and ready.stdout.strip()=='1':break
        time.sleep(0.2)
    else:raise RuntimeError('Owned PostgreSQL readiness failed')
    pg_address=command(['docker','port',pg,'5432/tcp']).stdout.strip();rust_address=command(['docker','port',rust,'9000/tcp']).stdout.strip();deadline=time.monotonic()+45
    while time.monotonic()<deadline:
        r=command(['curl','--disable','--fail','--silent','--show-error','--max-time','2','http://'+rust_address+'/health'],check=False)
        if r.returncode==0:break
        time.sleep(0.2)
    else:raise RuntimeError('Owned RustFS readiness failed')
    command(['docker','cp',str(root/'migrations'),pg+':/migrations']);migration=(root/'scripts/run-migrations.sh').read_text();migration_args=['docker','exec','-i','--env','PGHOST=127.0.0.1','--env','PGUSER=gateway','--env','PGDATABASE=gateway_test',pg,'sh'];command(migration_args,input=migration,output='migrations-first.log');command(migration_args,input=migration,output='migrations-noop.log')
    env.update(TEST_DATABASE_URL='postgres://gateway:'+password+'@'+pg_address+'/gateway_test?sslmode=disable',TEST_RUSTFS_ENDPOINT='http://'+rust_address,TEST_RUSTFS_ACCESS_KEY=access,TEST_RUSTFS_SECRET_KEY=secret,RUBYGEMS_TEST_IMAGE=image,RUBYGEMS_OWNED_EVIDENCE_DIR=str(evidence),RUBYGEMS_OWNED_PROJECT=run_id,RUBYGEMS_OWNED_RESTART_CONTAINERS=pg+','+rust,GATEWAY_REPOSITORY_PRESETS='none')
    command(['go','build','-o',str(work/'gateway'),'./cmd/gateway'],output='gateway-build.log')
    env['RUBYGEMS_GATEWAY_BIN']=str(work/'gateway')
    write('gateway-binary.json',{'sha256':hashlib.sha256((work/'gateway').read_bytes()).hexdigest(),'build':'real cmd/gateway from recorded source snapshot','runtime':'production config/composition with explicit RubyGems preview flag'})
    result=command(['go','test','-json','-count=1','-p','1','-race','-tags=integration','./internal/protocol/rubygems','./internal/rubygemspublication/...','./internal/format/rubygems','-run','RubyGem','-timeout','600s'],output='results.jsonl',check=False)
    events=[]
    for line in (evidence/'results.jsonl').read_text().splitlines():
        try:events.append(json.loads(line))
        except ValueError:pass
    passed={x['Test'] for x in events if x.get('Action')=='pass' and 'Test' in x};bad=[x for x in events if x.get('Action') in ('skip','fail')];report={'target':'native-rubygems-e2e','exitCode':result.returncode,'required':sorted(required),'requiredPassed':sorted(required&passed),'missing':sorted(required-passed),'mandatoryCount':len(required),'mandatoryPassedCount':len(required&passed),'passedTests':len(passed),'passedTopLevelTests':len([n for n in passed if '/' not in n]),'failOrSkip':bad,'resultsSHA256':hashlib.sha256((evidence/'results.jsonl').read_bytes()).hexdigest()};write('required.json',report);exit_code=0 if result.returncode==0 and not bad and not required-passed else 1
    write('summary.json',{'target':'native-rubygems-e2e','mandatoryCount':len(required),'mandatoryPassedCount':len(required&passed),'missing':sorted(required-passed),'failOrSkipCount':len(bad),'passed':exit_code==0,'toolchain':probe,'scope':'hosted-preview','formalAdmission':False})
except GateCancelled as error:
    exit_code=128+error.signum
    write('failure.json',{'message':'Owned RubyGems gate cancelled','cancelledBySignal':error.signum,'exitCode':exit_code})
except Exception as error:
    message='Owned RubyGems gate failed: '+type(error).__name__
    if isinstance(error,RuntimeError):message+=': '+str(error)
    if isinstance(error,subprocess.CalledProcessError):message+=': '+str(error.cmd[:3])+' exit='+str(error.returncode)
    write('failure.json',{'message':message});print(message,file=sys.stderr)
finally:
    cleaning=True;cleanup_deadline=time.monotonic()+30
    for sig in gate_signals:signal.signal(sig,cancel_during_cleanup if cancelled_signal is None else signal.SIG_IGN)
    for process in list(active_processes.values()):stop_process_group(process)
    cleanup={'runId':run_id,'containers':[],'volumes':[],'network':None,'privateWorkRemoved':False,'remainingOwnedContainers':[]}
    for name in reversed(containers):
        state=inspect(name)
        logs=command(['docker','logs',name],check=False)
        safe_logs=logs.stdout+logs.stderr
        for envfile in ('pg.env','rust.env'):
            if (work/envfile).exists():
                for line in (work/envfile).read_text().splitlines():
                    key,_,value=line.partition('=')
                    if value and any(s in key for s in ('PASSWORD','SECRET','KEY')):safe_logs=safe_logs.replace(value,'[owned-private-value]')
        for secret_name in ('password','access','secret'):
            if secret_name in locals():safe_logs=safe_logs.replace(locals()[secret_name],'[owned-private-value]')
        (evidence/(name+'-service.log')).write_text(safe_logs)
        if state:write(name+'-final-state.json',{'state':state.get('State'),'ports':state.get('NetworkSettings',{}).get('Ports'),'image':state.get('Config',{}).get('Image'),'mountNames':[m.get('Name') for m in state.get('Mounts',[]) if m.get('Type')=='volume']})
        if state and state.get('Config',{}).get('Labels',{}).get(label.split('=')[0])==run_id:command(['docker','rm','--force',name],check=False)
        cleanup['containers'].append({'name':name,'absent':inspect(name) is None})
    # Timed out native clients also carry this exact run label.
    remaining=command(['docker','ps','-aq','--filter','label='+label],check=False).stdout.split()
    for ident in remaining:command(['docker','rm','--force',ident],check=False)
    cleanup['remainingOwnedContainers']=command(['docker','ps','-aq','--filter','label='+label],check=False).stdout.split()
    for name in reversed(volumes):
        r=command(['docker','volume','inspect',name],check=False)
        if r.returncode==0 and json.loads(r.stdout)[0].get('Labels',{}).get(label.split('=')[0])==run_id:command(['docker','volume','rm',name],check=False)
        cleanup['volumes'].append({'name':name,'absent':command(['docker','volume','inspect',name],check=False).returncode!=0})
    r=command(['docker','network','inspect',network],check=False)
    if r.returncode==0 and json.loads(r.stdout)[0].get('Labels',{}).get(label.split('=')[0])==run_id:command(['docker','network','rm',network],check=False)
    cleanup['network']={'name':network,'absent':command(['docker','network','inspect',network],check=False).returncode!=0}
    shutil.rmtree(work);cleanup['privateWorkRemoved']=not work.exists();cleanup['passed']=all(x['absent'] for x in cleanup['containers']+cleanup['volumes']) and cleanup['network']['absent'] and cleanup['privateWorkRemoved'] and not cleanup['remainingOwnedContainers']
    if not cleanup['passed']:exit_code=1
    if cancelled_signal is not None:
        exit_code=128+cancelled_signal
        write('failure.json',{'message':'Owned RubyGems gate cancelled','cancelledBySignal':cancelled_signal,'exitCode':exit_code})
        summary=json.loads((evidence/'summary.json').read_text());summary.update(passed=False,cancelledBySignal=cancelled_signal);write('summary.json',summary)
    cleanup['processes']=process_receipts;cleanup['cleanupErrors']=cleanup_errors;cleanup['allCommandChildrenReaped']=all(x['waitReturned'] for x in process_receipts);cleanup['cancelledBySignal']=cancelled_signal
    if cleanup_errors or not cleanup['allCommandChildrenReaped']:cleanup['passed']=False;exit_code=exit_code or 1
    cleanup['exitCode']=int(exit_code);write('cleanup.json',cleanup)
    manifest={str(p.relative_to(evidence)):hashlib.sha256(p.read_bytes()).hexdigest() for p in sorted(evidence.rglob('*')) if p.is_file()};write('manifest.json',{'target':'native-rubygems-e2e','files':manifest})
print(str(evidence));raise SystemExit(exit_code)
PY
