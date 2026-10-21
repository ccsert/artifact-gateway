#!/usr/bin/env python3
"""Run the unchanged contract targets and verify their isolated evidence."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import platform
import re
import selectors
import shutil
import signal
import subprocess
import sys
import tempfile
import time
from datetime import datetime, timezone
import urllib.error
import urllib.request
import uuid

ROOT = Path(__file__).resolve().parent.parent
PLAN = json.loads((ROOT / 'scripts/ci-contract-lanes.json').read_text())
LANES = tuple(PLAN['lanes'])
BENCHMARK_LANES = tuple(PLAN['benchmarkOrder'])
RAW_DIRS = sorted({p for lane in PLAN['lanes'].values() for p in lane['rawDirectories']})


def now():
    return datetime.now(timezone.utc).isoformat()


def write(path, value):
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(value, indent=2) + '\n')


def sha(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def git(*args):
    return subprocess.check_output(['git', *args], cwd=ROOT, text=True).strip()


def business_source():
    # CI orchestration and its documentation are the only permitted differences
    # in a serial/parallel comparison. Product code, clients and targets remain.
    def excluded(name):
        return (name.startswith(('.github/workflows/', 'scripts/ci-', 'docs/ci-contract-lanes'))
                or name in ('scripts/tests/test_ci_contract.py', 'CONTRIBUTING.md',
                            'CONTRIBUTING.zh-CN.md', 'CHANGELOG.md', 'CHANGELOG.zh-CN.md'))
    files = {name: sha(ROOT / name) for name in git('ls-files').splitlines()
             if not excluded(name) and (ROOT / name).is_file()}
    digest = hashlib.sha256(json.dumps(files, sort_keys=True).encode()).hexdigest()
    return {'head': git('rev-parse', 'HEAD'), 'tree': git('rev-parse', 'HEAD^{tree}'),
            'businessDigest': digest, 'fileHashes': files}


def openapi_fresh():
    paths = ['api/openapi/native-hosted-v1.json', 'api/openapi/management-runtime-v1.json',
             'internal/admin/openapi/generated.go', 'console/src/client/sdk.gen.ts',
             'console/src/client/types.gen.ts']
    before = {p: sha(ROOT / p) for p in paths}
    subprocess.run(['make', 'openapi-generate-admin'], cwd=ROOT, check=True)
    subprocess.run(['npm', '--prefix', 'console', 'run', 'generate:api'], cwd=ROOT, check=True)
    if before != {p: sha(ROOT / p) for p in paths}:
        raise RuntimeError('generated OpenAPI artifacts were stale')


def docker_inventory():
    inventory = {}
    for kind, args in [('containers', ['ps', '-aq']), ('volumes', ['volume', 'ls', '-q']),
                       ('networks', ['network', 'ls', '-q'])]:
        result = subprocess.run(['docker', *args], capture_output=True, text=True, timeout=30)
        if result.returncode:
            raise RuntimeError('Docker inventory unavailable: ' + kind)
        inventory[kind] = sorted(result.stdout.split())
    return inventory


CORE_GO_CACHE_VOLUMES = ('artifact-gateway-go-mod', 'artifact-gateway-go-build')
VOLUME_RECEIPT_FIELDS = ('Name', 'Driver', 'Scope', 'CreatedAt', 'Labels', 'Options')


def docker_inspect(kind, name):
    result = subprocess.run(['docker', kind, 'inspect', name], capture_output=True,
                            text=True, timeout=30)
    if result.returncode:
        raise RuntimeError('Docker inspection unavailable: ' + kind + ' ' + name)
    values = json.loads(result.stdout)
    if not isinstance(values, list) or len(values) != 1 or not isinstance(values[0], dict):
        raise RuntimeError('invalid Docker inspection: ' + kind + ' ' + name)
    return values[0]


def docker_step_snapshot(path):
    """Persist attribution fields only; container environment/commands are private."""
    snapshot = {'recordedAt': now(), 'inventory': {}, 'volumes': [], 'containers': []}
    try:
        snapshot['inventory'] = docker_inventory()
        for name in snapshot['inventory']['volumes']:
            item = docker_inspect('volume', name)
            snapshot['volumes'].append({key: item.get(key) for key in VOLUME_RECEIPT_FIELDS})
        for identity in snapshot['inventory']['containers']:
            item = docker_inspect('container', identity)
            snapshot['containers'].append({
                'ID': item.get('Id'), 'Name': item.get('Name'), 'Image': item.get('Image'),
                'Config': {'Labels': item.get('Config', {}).get('Labels')},
                'Mounts': [{key: mount.get(key) for key in ('Type', 'Name', 'Destination', 'Driver', 'RW')}
                           for mount in item.get('Mounts', [])]})
    except Exception as error:
        snapshot['error'] = type(error).__name__
        raise
    finally:
        write(path, snapshot)


class CoreGoCacheVolumes:
    """Own only two caches absent before this dedicated core lane started."""
    def __init__(self, output, run_id, attempt):
        self.path = output / 'docker-cache-claims.json'
        self.labels = {'artifact-gateway.ci-owner': uuid.uuid4().hex,
                       'artifact-gateway.ci-run': run_id,
                       'artifact-gateway.ci-attempt': attempt,
                       'artifact-gateway.ci-lane': 'core'}
        self.names = CORE_GO_CACHE_VOLUMES
        self.state = {'schemaVersion': 1, 'labels': self.labels, 'attempted': [],
                      'receipts': {}, 'removed': [], 'remaining': [], 'errors': []}
        write(self.path, self.state)

    def error(self, stage, name, error):
        self.state['errors'].append({'stage': stage, 'name': name, 'error': str(error)})
        write(self.path, self.state)

    def receipt(self, name):
        item = docker_inspect('volume', name)
        if (item.get('Name') != name or item.get('Driver') != 'local'
                or item.get('Scope') != 'local' or not item.get('CreatedAt')
                or item.get('Labels') != self.labels or item.get('Options') not in (None, {})):
            raise RuntimeError('core cache ownership or creation receipt changed: ' + name)
        return {key: item.get(key) for key in VOLUME_RECEIPT_FIELDS}

    def claim(self, before):
        existing = sorted(set(self.names) & set(before['volumes']))
        if existing:
            error = RuntimeError('refusing pre-existing core Go caches: ' + ', '.join(existing))
            self.error('claim', ', '.join(existing), error)
            raise error
        for name in self.names:
            self.state['attempted'].append(name)
            write(self.path, self.state)
            command = ['docker', 'volume', 'create', '--driver', 'local']
            for key, value in self.labels.items():
                command.extend(['--label', key + '=' + value])
            command.append(name)
            try:
                result = subprocess.run(command, capture_output=True, text=True, timeout=30)
                if result.returncode or result.stdout.strip() != name:
                    raise RuntimeError('core cache create acknowledgement failed: ' + name)
                self.state['receipts'][name] = self.receipt(name)
                write(self.path, self.state)
            except Exception as error:
                # A lost CLI reply can follow a successful daemon-side create.
                # Cleanup re-inspects this exact attempted name with our random owner.
                self.error('claim', name, error)
                raise

    def cleanup(self):
        for name in reversed(self.state['attempted']):
            try:
                receipt = self.receipt(name)
                original = self.state['receipts'].get(name)
                if original is not None and original != receipt:
                    raise RuntimeError('core cache creation receipt changed: ' + name)
                self.state['receipts'][name] = receipt
                write(self.path, self.state)
                result = subprocess.run(['docker', 'volume', 'rm', name], capture_output=True,
                                        text=True, timeout=30)
                if result.returncode or result.stdout.strip() != name:
                    raise RuntimeError('owned core cache removal failed: ' + name)
                observed = subprocess.run(['docker', 'volume', 'ls', '-q'], capture_output=True,
                                          text=True, timeout=30)
                if observed.returncode or name in observed.stdout.split():
                    raise RuntimeError('owned core cache removal was not verified: ' + name)
                self.state['removed'].append(name)
            except Exception as error:
                self.state['remaining'].append(name)
                self.error('cleanup', name, error)
            finally:
                write(self.path, self.state)
        return self.state


TERMINATION_GRACE_SECONDS = 45


def live_group_members(pgid):
    """Only inspect the group created by this supervisor; zombies cannot run."""
    result = subprocess.run(['ps', '-A', '-o', 'pid=,pgid=,stat='],
                            capture_output=True, text=True, check=True, timeout=5)
    members = []
    for line in result.stdout.splitlines():
        fields = line.split()
        if len(fields) >= 3 and int(fields[1]) == pgid and not fields[2].startswith(('Z', 'X')):
            members.append(int(fields[0]))
    return members


def signal_group(pgid, sig):
    try:
        os.killpg(pgid, sig)
    except ProcessLookupError:
        pass
    except PermissionError:
        # Darwin returns EPERM for groups containing only zombies. A denied
        # signal is harmless only when a fresh inventory proves none can run.
        if live_group_members(pgid):
            raise


def run_command(command, log, env, timeout=None):
    """A closed pipe or exited leader cannot end supervision of a live group."""
    started = time.monotonic()
    proc = subprocess.Popen(command, cwd=ROOT, env=env, stdin=subprocess.DEVNULL, stdout=subprocess.PIPE,
                            stderr=subprocess.STDOUT, start_new_session=True)
    stopped = killed = None
    reason = None

    def stop(signum, _frame=None):
        nonlocal stopped, reason
        if stopped is None:
            reason = 'cancelled' if signum else 'timed_out'
            stopped = time.monotonic()
            signal_group(proc.pid, signal.SIGTERM)

    previous = {sig: signal.signal(sig, stop) for sig in (signal.SIGINT, signal.SIGTERM)}
    selector = selectors.DefaultSelector()
    selector.register(proc.stdout, selectors.EVENT_READ)
    try:
        with log.open('wb') as output:
            while True:
                running = proc.poll() is None
                alive = running or bool(live_group_members(proc.pid))
                if not selector.get_map() and not alive:
                    break
                tick = time.monotonic()
                if timeout and tick - started >= timeout and stopped is None:
                    stop(0)
                elif not running and alive and stopped is None:
                    stopped = tick
                    reason = 'owned_processes_remaining'
                    signal_group(proc.pid, signal.SIGTERM)
                if stopped is not None and tick - stopped >= TERMINATION_GRACE_SECONDS and killed is None:
                    signal_group(proc.pid, signal.SIGKILL)
                    killed = tick
                if killed is not None and tick - killed > 5:
                    raise RuntimeError('owned command group or output pipe did not settle after SIGKILL')
                if not selector.get_map():
                    time.sleep(0.05)
                    continue
                for key, _ in selector.select(0.1):
                    data = os.read(key.fileobj.fileno(), 65536)
                    if not data:
                        selector.unregister(key.fileobj)
                        continue
                    output.write(data)
                    output.flush()
                    sys.stdout.buffer.write(data)
                    sys.stdout.buffer.flush()
            code = proc.wait()
    finally:
        selector.close()
        proc.stdout.close()
        # Exception paths also terminate descendants after the leader exits.
        try:
            if proc.poll() is None or live_group_members(proc.pid):
                signal_group(proc.pid, signal.SIGKILL)
            proc.wait()
            settle_deadline = time.monotonic() + 5
            while live_group_members(proc.pid):
                if time.monotonic() >= settle_deadline:
                    raise RuntimeError('owned process group still contains running descendants')
                time.sleep(0.05)
        finally:
            for sig, handler in previous.items():
                signal.signal(sig, handler)
    return {'command': command, 'exitCode': code,
            'status': reason or ('success' if code == 0 else 'failure'),
            'durationSeconds': time.monotonic() - started, 'timeoutSeconds': timeout,
            'processGroup': proc.pid, 'remainingLiveGroupMembers': []}


def case_summary(directory):
    events = {k: set() for k in ('run', 'pass', 'fail', 'skip')}
    for path in directory.rglob('*.jsonl'):
        for line in path.read_text(errors='replace').splitlines():
            try:
                event = json.loads(line)
            except (ValueError, TypeError):
                continue
            if isinstance(event, dict) and event.get('Test') and event.get('Action') in events:
                events[event['Action']].add(event.get('Package', '') + '/' + event['Test'])
    for path in (directory / 'logs').glob('*.log'):
        for line in path.read_text(errors='replace').splitlines():
            match = re.match(r'\s*(?:=== (RUN)\s+|--- (PASS|FAIL|SKIP):\s+)(\S+)', line)
            if match:
                action = (match[1] or match[2]).lower()
                events[action].add(path.stem + '/' + match[3])
    return {k: sorted(v) for k, v in events.items()}


def run_lane(args):
    lane = PLAN['lanes'][args.lane]
    output = args.output.resolve()
    output.mkdir(parents=True, exist_ok=True)
    (output / 'logs').mkdir(exist_ok=True)
    started = now()
    source = business_source()
    write(output / 'source.json', source)
    before_raw = {str(p) for directory in lane['rawDirectories']
                  for p in (ROOT / directory).glob('*')}
    dedicated = os.environ.get('GITHUB_ACTIONS') == 'true'
    before_docker = docker_inventory() if dedicated else None
    report = {'schemaVersion': 1, 'lane': args.lane, 'prefix': args.prefix,
              'head': source['head'], 'tree': source['tree'],
              'businessDigest': source['businessDigest'], 'startedAt': started,
              'runId': os.environ.get('GITHUB_RUN_ID', 'local'),
              'attempt': os.environ.get('GITHUB_RUN_ATTEMPT', 'local'),
              'coldCache': args.cold_cache, 'status': 'failure', 'steps': [],
              'runner': {'os': platform.system(), 'architecture': platform.machine(),
                         'cpuCount': os.cpu_count(), 'imageOS': os.environ.get('ImageOS'),
                         'imageVersion': os.environ.get('ImageVersion'),
                         'memoryBytes': os.sysconf('SC_PAGE_SIZE') * os.sysconf('SC_PHYS_PAGES')}}
    work = Path(tempfile.mkdtemp(prefix='ag-contract-' + args.lane + '-'))
    env = dict(os.environ, TMPDIR=str(work))
    if args.cold_cache:
        env.update(GOCACHE=str(work / 'go-build'), GOMODCACHE=str(work / 'go-mod'),
                   GOFLAGS=(env.get('GOFLAGS', '') + ' -modcacherw').strip())
    dedicated_core = dedicated and args.lane == 'core' and not args.local
    caches = None
    def stop_lane(signum, _frame):
        report['status'] = 'failure'
        report['cancelledBySignal'] = signum
        raise SystemExit(128 + signum)

    def stop_during_cleanup(signum, _frame):
        # Finish bounded cleanup even when cancellation arrives between targets.
        report['status'] = 'failure'
        report['cancelledBySignal'] = signum

    previous_lane_signals = {sig: signal.signal(sig, stop_lane)
                             for sig in (signal.SIGINT, signal.SIGTERM)}
    try:
        if dedicated_core:
            caches = CoreGoCacheVolumes(output, report['runId'], report['attempt'])
            caches.claim(before_docker)
        for index, step in enumerate(lane['steps']):
            command = step['command']
            if args.local and command == ['make', 'openapi-check']:
                command = [sys.executable, str(Path(__file__).resolve()), 'openapi-fresh']
            if command == ['make', 'native-maven-e2e']:
                settings = work / 'maven-central-settings.xml'
                settings.write_text('<settings><mirrors><mirror><id>public-central-fixture</id><mirrorOf>central</mirrorOf><url>https://maven-central.storage-download.googleapis.com/maven2</url></mirror></mirrors></settings>\n')
                env.update(MAVEN_ARGS='--global-settings ' + str(settings),
                           MAVEN_POM_COMPAT_UPSTREAM='https://maven-central.storage-download.googleapis.com/maven2')
            print('Contract lane ' + args.lane + ': ' + step['name'], flush=True)
            if dedicated_core:
                docker_step_snapshot(output / 'docker' / f'{index:02d}-before.json')
            try:
                result = run_command(command, output / 'logs' / f'{index:02d}.log', env,
                                     step['timeoutSeconds'])
                result['name'] = step['name']
                result['plannedCommand'] = step['command']
                report['steps'].append(result)
            finally:
                if dedicated_core:
                    docker_step_snapshot(output / 'docker' / f'{index:02d}-after.json')
            if result['status'] != 'success' and not args.keep_going:
                break
        if len(report['steps']) == len(lane['steps']) and all(s['status'] == 'success' for s in report['steps']):
            report['status'] = 'success'
    finally:
        for sig in previous_lane_signals:
            signal.signal(sig, stop_during_cleanup)
        try:
            cleanup = {'dockerAuditScope': 'dedicated GitHub runner' if dedicated else 'fixture-owned reports only',
                       'errors': []}
            try:
                for directory in lane['rawDirectories']:
                    for path in (ROOT / directory).glob('*'):
                        if str(path) not in before_raw:
                            target = output / 'raw' / Path(directory).name / path.name
                            if path.is_dir():
                                shutil.copytree(path, target)
                            else:
                                target.parent.mkdir(parents=True, exist_ok=True)
                                shutil.copy2(path, target)
            except Exception as error:
                cleanup['errors'].append({'stage': 'raw-evidence', 'error': type(error).__name__})
            try:
                shutil.rmtree(work)
            except Exception as error:
                cleanup['errors'].append({'stage': 'temporary-directory', 'error': type(error).__name__})
            cleanup['ownedTemporaryDirectoryRemoved'] = not work.exists()
            if caches is not None:
                try:
                    cleanup['coreGoCaches'] = caches.cleanup()
                    if cleanup['coreGoCaches']['errors'] or cleanup['coreGoCaches']['remaining']:
                        report['status'] = 'failure'
                except Exception as error:
                    cleanup['errors'].append({'stage': 'core-go-caches', 'error': type(error).__name__})
            if dedicated:
                try:
                    after = docker_inventory()
                    cleanup['before'] = before_docker
                    cleanup['remainingNewResources'] = {k: sorted(set(v) - set(before_docker[k])) for k, v in after.items()}
                    if any(cleanup['remainingNewResources'].values()):
                        report['status'] = 'failure'
                except Exception as error:
                    cleanup['errors'].append({'stage': 'docker-inventory', 'error': type(error).__name__})
            if cleanup['errors'] or not cleanup['ownedTemporaryDirectoryRemoved']:
                report['status'] = 'failure'
            report['cleanup'] = cleanup
            report['cases'] = case_summary(output)
            if business_source()['businessDigest'] != source['businessDigest']:
                report['status'] = 'failure'
                report['sourceChangedDuringExecution'] = True
            report['completedAt'] = now()
            report['filesSHA256'] = {str(p.relative_to(output)): sha(p) for p in output.rglob('*') if p.is_file() and p.name != 'report.json'}
            write(output / 'report.json', report)
        finally:
            for sig, handler in previous_lane_signals.items():
                signal.signal(sig, handler)
    return 0 if report['status'] == 'success' else 1


def validate_evidence(root, prefix, expected_lanes, head, attempt):
    found = {}
    candidates = {}
    for path in root.rglob('report.json'):
        item = json.loads(path.read_text())
        if item.get('prefix') != prefix:
            continue
        lane = item.get('lane')
        if lane not in expected_lanes:
            raise ValueError('unexpected lane: ' + str(lane))
        observed_attempt = int(item['attempt'])
        if observed_attempt > int(attempt) or observed_attempt < 1:
            raise ValueError('invalid artifact attempt: ' + lane)
        key = (lane, observed_attempt)
        if key in candidates:
            raise ValueError('duplicated lane artifact: ' + lane)
        candidates[key] = (path, item)
    # A targeted GitHub rerun retains successful same-run, same-head jobs.
    # Select each lane's latest artifact; never replace a newer failure with an
    # older success, and never claim inherited jobs executed a second time.
    selected = {lane: max((key for key in candidates if key[0] == lane), key=lambda key: key[1])
                for lane in expected_lanes if any(key[0] == lane for key in candidates)}
    for lane, key in selected.items():
        path, item = candidates[key]
        if (item.get('status') != 'success' or item.get('head') != head
                or item.get('runId') != os.environ.get('GITHUB_RUN_ID', item.get('runId'))):
            raise ValueError('failed, stale, cancelled or incomplete lane: ' + lane)
        steps = PLAN['lanes'][lane]['steps']
        if len(item.get('steps', [])) != len(steps):
            raise ValueError('missing steps: ' + lane)
        for observed, planned in zip(item['steps'], steps):
            if (observed.get('plannedCommand') != planned['command'] or observed.get('status') != 'success'
                    or type(observed.get('exitCode')) is not int or observed['exitCode'] != 0
                    or observed.get('timeoutSeconds') != planned['timeoutSeconds']
                    or type(observed.get('processGroup')) is not int or observed['processGroup'] < 1
                    or observed.get('remainingLiveGroupMembers') != []):
                raise ValueError('step mismatch: ' + lane)
        cleanup = item.get('cleanup', {})
        inventories = cleanup.get('remainingNewResources')
        kinds = {'containers', 'volumes', 'networks'}
        if (cleanup.get('ownedTemporaryDirectoryRemoved') is not True
                or cleanup.get('dockerAuditScope') != 'dedicated GitHub runner'
                or not isinstance(inventories, dict) or set(inventories) != kinds
                or any(not isinstance(v, list) or v for v in inventories.values())
                or not isinstance(cleanup.get('before'), dict) or set(cleanup['before']) != kinds
                or any(not isinstance(v, list) for v in cleanup['before'].values())):
            raise ValueError('owned cleanup failed: ' + lane)
        cases = item.get('cases')
        if (not isinstance(cases, dict) or set(cases) != {'run', 'pass', 'fail', 'skip'}
                or any(not isinstance(v, list) or any(not isinstance(case, str) or not case for case in v)
                       for v in cases.values())):
            raise ValueError('case evidence missing: ' + lane)
        if lane != 'core' and (not cases['run'] or not cases['pass'] or cases['fail'] or cases['skip']):
            raise ValueError('mandatory native cases missing, failed or skipped: ' + lane)
        files = item.get('filesSHA256', {})
        required_logs = {f'logs/{i:02d}.log' for i in range(len(steps))}
        if not files or not {'source.json', *required_logs}.issubset(files):
            raise ValueError('missing file manifest: ' + lane)
        for directory in PLAN['lanes'][lane]['rawDirectories']:
            if lane != 'core':
                prefix_path = 'raw/' + Path(directory).name + '/'
                if not any(name.startswith(prefix_path) and name.endswith('/cleanup.json') for name in files):
                    raise ValueError('original producer cleanup report missing: ' + lane)
                if not any(name.startswith(prefix_path) and name.endswith('.jsonl') for name in files):
                    raise ValueError('original producer case log missing: ' + lane)
        for name, digest in files.items():
            file = path.parent / name
            if not file.resolve().is_relative_to(path.parent.resolve()) or not file.is_file() or sha(file) != digest:
                raise ValueError('artifact bytes mismatch: ' + lane + '/' + name)
            if name.startswith('raw/') and name.endswith('/cleanup.json'):
                producer_cleanup = json.loads(file.read_text())
                if type(producer_cleanup.get('exitCode')) is not int or producer_cleanup['exitCode'] != 0:
                    raise ValueError('producer cleanup failed: ' + lane)
        source = json.loads((path.parent / 'source.json').read_text())
        if any(source.get(k) != item.get(k) for k in ('head', 'tree', 'businessDigest')):
            raise ValueError('source manifest differs from report: ' + lane)
        found[lane] = item
    if set(found) != set(expected_lanes):
        raise ValueError('missing lanes: ' + ', '.join(sorted(set(expected_lanes) - set(found))))
    if len({(x['tree'], x['businessDigest']) for x in found.values()}) != 1:
        raise ValueError('lanes ran different source trees')
    return found


def validate_needs(needs, expected):
    if set(needs) != set(expected) or any(needs[x].get('result') != 'success' for x in expected):
        raise ValueError('a required job failed, was skipped/cancelled, or has no result')


class StripRedirectCredential(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        redirected = super().redirect_request(req, fp, code, msg, headers, newurl)
        if redirected is not None:
            redirected.remove_header('Authorization')
        return redirected


def collect_job_logs(destination, prefixes=('contract',), lanes=LANES):
    api = os.environ.get('GITHUB_API_URL', 'https://api.github.com')
    repository = os.environ['GITHUB_REPOSITORY']
    run = os.environ['GITHUB_RUN_ID']
    token = os.environ['GITHUB_TOKEN']
    head = os.environ.get('GITHUB_SHA', '')
    attempt = os.environ.get('GITHUB_RUN_ATTEMPT', '')
    event = os.environ.get('GITHUB_EVENT_NAME', '')
    api_head = head
    event_read = False
    opener = urllib.request.build_opener(StripRedirectCredential())
    budget = 120
    deadline = time.monotonic() + budget
    required = [p + '/' + lane for p in prefixes for lane in lanes]
    manifest = {'required': required, 'selectedJobIDs': [], 'missing': required,
                'missingLogs': required, 'complete': False, 'deadlineSeconds': budget,
                'head': head, 'runId': run, 'attempt': attempt, 'event': event}
    identities = {}
    downloaded = set()
    poll = 0

    def remaining():
        available = deadline - time.monotonic()
        if available <= 0:
            raise ValueError('complete job log collection deadline exceeded (120s)')
        return available

    def fetch(url):
        request = urllib.request.Request(url, headers={'Authorization': 'Bearer ' + token,
                                         'Accept': 'application/vnd.github+json',
                                         'X-GitHub-Api-Version': '2022-11-28',
                                         'Cache-Control': 'no-cache'})
        with opener.open(request, timeout=min(45, remaining())) as response:
            data = response.read()
        remaining()
        return data

    # A dependent job can start while the jobs API still reports its completed
    # prerequisite as in_progress. Wait only for read-only publication of that
    # same job and its log; never substitute another execution or hide errors.
    while True:
        poll += 1
        readback = {'poll': poll, 'elapsedSeconds': budget - (deadline - time.monotonic()),
                    'logReads': [], 'status': 'waiting'}
        try:
            jobs = []
            page = 1
            while True:
                data = fetch(f'{api}/repos/{repository}/actions/runs/{run}/jobs?filter=latest&per_page=100&page={page}')
                page_path = destination / 'job-log-polls' / f'{poll:03d}-page-{page:03d}.json'
                page_path.parent.mkdir(parents=True, exist_ok=True)
                page_path.write_bytes(data)
                batch = json.loads(data)['jobs']
                jobs.extend(batch)
                if len(batch) < 100:
                    break
                page += 1
            write(destination / 'jobs.json', jobs)
            write(destination / 'job-log-polls' / f'{poll:03d}-jobs.json', jobs)
            matching = {name: [j for j in jobs if j['name'].endswith(name)] for name in required}
            manifest['missing'] = [name for name in required if len(matching[name]) != 1]
            if manifest['missing']:
                raise ValueError('complete job logs unavailable: ' + ', '.join(manifest['missing']))
            if (not re.fullmatch(r'[0-9a-f]{40}', head) or not run.isdecimal()
                    or not attempt.isdecimal() or int(run) < 1 or int(attempt) < 1):
                raise ValueError('GitHub job source binding unavailable')
            if not event_read:
                if event == 'pull_request':
                    # Jobs API identifies the PR head; GITHUB_SHA and artifact
                    # validation identify its synthetic merge commit.
                    try:
                        payload = json.loads(Path(os.environ['GITHUB_EVENT_PATH']).read_text())
                        api_head = payload['pull_request']['head']['sha']
                        if (payload['repository']['full_name'] != repository
                                or not isinstance(api_head, str)
                                or not re.fullmatch(r'[0-9a-f]{40}', api_head)):
                            raise ValueError('invalid event binding')
                    except (KeyError, OSError, TypeError, ValueError):
                        raise ValueError('GitHub pull request source binding unavailable') from None
                manifest['apiHead'] = api_head
                event_read = True
            selected = []
            for name in required:
                job = matching[name][0]
                if (job.get('head_sha') != api_head or type(job.get('run_id')) is not int
                        or job['run_id'] != int(run) or type(job.get('run_attempt')) is not int
                        or not 1 <= job['run_attempt'] <= int(attempt)):
                    raise ValueError('GitHub job source binding mismatch: ' + name)
                if type(job.get('id')) is not int or job['id'] < 1:
                    raise ValueError('GitHub job identity unavailable: ' + name)
                identity = (job['id'], job['run_attempt'])
                if name in identities and identities[name] != identity:
                    raise ValueError('GitHub job identity changed during collection: ' + name)
                identities[name] = identity
                if (job['run_attempt'] < int(attempt)
                        and (job['status'] != 'completed' or job.get('conclusion') != 'success')):
                    raise ValueError('GitHub retained job source binding is not successful: ' + name)
                if (job['status'] == 'completed' and job.get('conclusion') in
                        ('success', 'failure', 'cancelled', 'skipped', 'timed_out',
                         'action_required', 'neutral', 'stale', 'startup_failure')):
                    selected.append(job)
                elif (job['status'] in ('queued', 'in_progress', 'waiting', 'pending', 'requested')
                      or (job['status'] == 'completed' and job.get('conclusion') is None)):
                    manifest['missing'].append(name)
                else:
                    raise ValueError('unknown GitHub job status: ' + name)
            manifest['selectedJobIDs'] = [job['id'] for job in selected]
            manifest['selectedAttempts'] = {str(job['id']): job['run_attempt'] for job in selected}
            for job in selected:
                if job['id'] in downloaded:
                    continue
                observed = {'jobId': job['id'], 'status': 'not_ready'}
                readback['logReads'].append(observed)
                try:
                    data = fetch(f'{api}/repos/{repository}/actions/jobs/{job["id"]}/logs')
                except urllib.error.HTTPError as error:
                    if error.code != 404:
                        raise
                    observed['httpStatus'] = 404
                    error.close()
                    continue
                if not data.strip():
                    observed['reason'] = 'empty_log'
                    continue
                (destination / f'job-{job["id"]}.log').write_bytes(data)
                downloaded.add(job['id'])
                observed.update(status='ready', bytes=len(data), sha256=hashlib.sha256(data).hexdigest())
            manifest['missingLogs'] = [name for name in required if identities[name][0] not in downloaded]
            if not manifest['missing'] and not manifest['missingLogs']:
                manifest['complete'] = True
                readback['status'] = 'success'
                return jobs
            remaining()
        except Exception as error:
            readback['status'] = 'failure'
            readback['error'] = {'type': type(error).__name__}
            if isinstance(error, ValueError):
                readback['error']['reason'] = str(error)
            if isinstance(error, urllib.error.HTTPError):
                readback['error']['httpStatus'] = error.code
                error.close()
            raise
        finally:
            manifest['polls'] = poll
            readback['missing'] = list(manifest['missing'])
            readback['missingLogs'] = list(manifest['missingLogs'])
            write(destination / 'job-log-manifest.json', manifest)
            write(destination / 'job-log-polls' / f'{poll:03d}-readback.json', readback)
        delay = min(2, max(0, deadline - time.monotonic()))
        if delay:
            time.sleep(delay)


def aggregate(args):
    if os.environ.get('GITHUB_ACTIONS') == 'true':
        if os.environ.get('CI_CONTRACT_ARTIFACT_RESULT') != 'success':
            raise ValueError('lane artifact download did not succeed')
        if not args.benchmark_only and os.environ.get('CI_CONTRACT_JOB_LOG_RESULT') != 'success':
            raise ValueError('complete job log collection did not succeed')
    required_jobs = [args.needs_job] if args.needs_job else ['contract-core', 'contract-native']
    validate_needs(json.loads(os.environ['CI_CONTRACT_NEEDS']), required_jobs)
    expected = BENCHMARK_LANES if args.benchmark_only else LANES
    found = validate_evidence(args.input, args.prefix, expected, os.environ['GITHUB_SHA'], os.environ['GITHUB_RUN_ATTEMPT'])
    write(args.output / 'aggregate.json', {'status': 'success', 'head': os.environ['GITHUB_SHA'],
                                        'runId': os.environ['GITHUB_RUN_ID'],
                                        'attempt': os.environ['GITHUB_RUN_ATTEMPT'],
                                        'prefix': args.prefix,
                                        'lanes': sorted(found), 'completedAt': now()})
    return 0


def timestamp(value):
    return datetime.fromisoformat(value.replace('Z', '+00:00')).timestamp()


def validate_benchmark_aggregates(root, head, run, attempt):
    found = {}
    for prefix in ('benchmark-serial', 'benchmark-parallel'):
        candidates = {}
        for path in root.rglob('aggregate.json'):
            item = json.loads(path.read_text())
            if item.get('prefix') != prefix:
                continue
            observed = int(item['attempt'])
            if observed < 1 or observed > int(attempt) or observed in candidates:
                raise ValueError('invalid benchmark aggregate attempt: ' + prefix)
            candidates[observed] = item
        item = candidates.get(int(attempt))
        if (not item or item.get('status') != 'success' or item.get('head') != head
                or item.get('runId') != run or item.get('lanes') != sorted(BENCHMARK_LANES)):
            raise ValueError('benchmark aggregate must succeed in the current execution attempt: ' + prefix)
        found[prefix] = item
    return found


def benchmark(args):
    if os.environ.get('GITHUB_ACTIONS') == 'true' and os.environ.get('CI_CONTRACT_ARTIFACT_RESULT') != 'success':
        raise ValueError('benchmark artifact download did not succeed')
    validate_needs(json.loads(os.environ['CI_CONTRACT_NEEDS']), ['serial-gate', 'parallel-gate'])
    serial = validate_evidence(args.input, 'benchmark-serial', BENCHMARK_LANES, os.environ['GITHUB_SHA'], os.environ['GITHUB_RUN_ATTEMPT'])
    parallel = validate_evidence(args.input, 'benchmark-parallel', BENCHMARK_LANES, os.environ['GITHUB_SHA'], os.environ['GITHUB_RUN_ATTEMPT'])
    reports = [*serial.values(), *parallel.values()]
    # GitHub gives retained successful jobs new IDs/run_attempt values while
    # preserving their old timestamps. Artifact provenance is the execution
    # authority; targeted reruns remain valid for correctness, not performance.
    if any(int(r['attempt']) != int(os.environ['GITHUB_RUN_ATTEMPT']) for r in reports):
        raise ValueError('benchmark lanes must use the current execution attempt; rerun all jobs for a comparable sample')
    aggregates = validate_benchmark_aggregates(args.input, os.environ['GITHUB_SHA'],
                                               os.environ['GITHUB_RUN_ID'], os.environ['GITHUB_RUN_ATTEMPT'])
    if not all(r['coldCache'] for r in reports) or len({r['businessDigest'] for r in reports}) != 1:
        raise ValueError('benchmark business inputs or cache conditions differ')
    constraints = {(r['runner']['os'], r['runner']['architecture'], r['runner']['cpuCount'],
                    r['runner']['memoryBytes'] // (512 * 1024**2), r['runner']['imageOS'],
                    r['runner']['imageVersion']) for r in reports}
    if len(constraints) != 1:
        raise ValueError('benchmark runner constraints differ')
    jobs = collect_job_logs(args.output, ('benchmark-serial', 'benchmark-parallel'), BENCHMARK_LANES)
    results = {}
    for mode, prefix in [('serial', 'benchmark-serial'), ('parallel', 'benchmark-parallel')]:
        lanes = [j for j in jobs if any(j['name'].endswith(prefix + '/' + lane) for lane in BENCHMARK_LANES)]
        gates = [j for j in jobs if j['name'] == mode + '-gate']
        if len(lanes) != len(BENCHMARK_LANES) or len(gates) != 1 or any(j['conclusion'] != 'success' for j in [*lanes, *gates]):
            raise ValueError('missing successful benchmark jobs: ' + mode)
        start = min(timestamp(j['started_at']) for j in lanes)
        end = timestamp(gates[0]['completed_at'])
        points = sorted([(timestamp(j['started_at']), 1) for j in lanes]
                        + [(timestamp(j['completed_at']), -1) for j in lanes])
        count = peak = 0
        for _, delta in points:
            count += delta
            peak = max(peak, count)
        results[mode] = {'criticalPathSecondsIncludingSetupArtifactsCleanupAndAggregate': end - start,
                         'peakAllocatedRunners': peak,
                         'queueSecondsByJob': {str(j['id']): timestamp(j['started_at']) - timestamp(j['created_at']) for j in [*lanes, *gates]},
                         'jobs': lanes, 'aggregate': gates[0]}
    old = results['serial']['criticalPathSecondsIncludingSetupArtifactsCleanupAndAggregate']
    new = results['parallel']['criticalPathSecondsIncludingSetupArtifactsCleanupAndAggregate']
    improvement = (old - new) / old
    passed = results['parallel']['peakAllocatedRunners'] >= 2 and improvement >= 0.25
    write(args.output / 'benchmark.json', {'status': 'success' if passed else 'performance_unproven',
          'businessDigest': reports[0]['businessDigest'], 'coldCache': True,
          'executionAttempt': os.environ['GITHUB_RUN_ATTEMPT'], 'aggregateEvidence': aggregates,
          'runnerConstraints': list(next(iter(constraints))), 'modes': results,
          'improvementFraction': improvement, 'thresholdFraction': 0.25,
          'note': 'Critical paths end after the corresponding aggregate job, including its artifact upload. Performance auditing is separate from the contract gate.'})
    return 0 if passed else 1


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest='command', required=True)
    lane = commands.add_parser('run')
    lane.add_argument('lane', choices=LANES)
    lane.add_argument('--output', required=True, type=Path)
    lane.add_argument('--prefix', default='contract')
    lane.add_argument('--cold-cache', action='store_true')
    lane.add_argument('--local', action='store_true')
    lane.add_argument('--keep-going', action='store_true')
    commands.add_parser('openapi-fresh')
    commands.add_parser('list')
    agg = commands.add_parser('aggregate')
    agg.add_argument('--input', required=True, type=Path)
    agg.add_argument('--output', required=True, type=Path)
    agg.add_argument('--prefix', default='contract', choices=['contract', 'benchmark-serial', 'benchmark-parallel'])
    agg.add_argument('--benchmark-only', action='store_true')
    agg.add_argument('--needs-job', choices=['serial', 'parallel'])
    logs = commands.add_parser('collect-job-logs')
    logs.add_argument('--output', required=True, type=Path)
    bench = commands.add_parser('benchmark')
    bench.add_argument('--input', required=True, type=Path)
    bench.add_argument('--output', required=True, type=Path)
    args = parser.parse_args()
    if args.command == 'run':
        return run_lane(args)
    if args.command == 'openapi-fresh':
        openapi_fresh()
    elif args.command == 'list':
        print(json.dumps(PLAN, indent=2))
    elif args.command == 'aggregate':
        return aggregate(args)
    elif args.command == 'collect-job-logs':
        args.output.mkdir(parents=True, exist_ok=True)
        collect_job_logs(args.output)
    elif args.command == 'benchmark':
        args.output.mkdir(parents=True, exist_ok=True)
        return benchmark(args)
    return 0


if __name__ == '__main__':
    try:
        sys.exit(main())
    except (ValueError, RuntimeError, OSError, subprocess.SubprocessError) as error:
        print('Contract verification failed: ' + str(error), file=sys.stderr)
        sys.exit(1)
