import copy
import argparse
import importlib.util
import json
import os
from pathlib import Path
import re
import selectors
import signal
import subprocess
import sys
import tempfile
import time
import unittest
from unittest.mock import patch

ROOT = Path(__file__).resolve().parents[2]
spec = importlib.util.spec_from_file_location('ci_contract', ROOT / 'scripts/ci-contract.py')
ci = importlib.util.module_from_spec(spec)
spec.loader.exec_module(ci)


class ContractGateTests(unittest.TestCase):
    def setUp(self):
        self.work = tempfile.TemporaryDirectory()
        self.root = Path(self.work.name)
        self.addCleanup(self.work.cleanup)
        self.head = '1' * 40
        self.reports = {}
        for lane in ci.LANES:
            directory = self.root / lane
            directory.mkdir()
            source = {'head': self.head, 'tree': '2' * 40, 'businessDigest': '3' * 64}
            ci.write(directory / 'source.json', source)
            (directory / 'logs').mkdir()
            for index in range(len(ci.PLAN['lanes'][lane]['steps'])):
                (directory / 'logs' / f'{index:02d}.log').write_text('original unmodified producer log\n')
            for raw in ci.PLAN['lanes'][lane]['rawDirectories']:
                if lane != 'core':
                    ci.write(directory / 'raw' / Path(raw).name / 'owned-project' / 'cleanup.json', {'exitCode': 0})
                    (directory / 'raw' / Path(raw).name / 'owned-project' / 'results.jsonl').write_text('{"Action":"pass","Test":"mandatory-native-case"}\n')
            item = {**source, 'lane': lane, 'prefix': 'contract', 'attempt': '1',
                    'status': 'success', 'runId': os.environ.get('GITHUB_RUN_ID', 'local-test'),
                    'steps': [{'name': step['name'], 'plannedCommand': step['command'],
                               'status': 'success', 'exitCode': 0,
                               'processGroup': 123, 'remainingLiveGroupMembers': [],
                               'timeoutSeconds': step['timeoutSeconds']}
                              for step in ci.PLAN['lanes'][lane]['steps']],
                    'cleanup': {'ownedTemporaryDirectoryRemoved': True,
                                'dockerAuditScope': 'dedicated GitHub runner',
                                'before': {k: [] for k in ('containers', 'volumes', 'networks')},
                                'remainingNewResources': {k: [] for k in ('containers', 'volumes', 'networks')}},
                    'cases': {'run': ['mandatory-native-case'], 'pass': ['mandatory-native-case'], 'fail': [], 'skip': []},
                    'filesSHA256': {str(p.relative_to(directory)): ci.sha(p) for p in directory.rglob('*') if p.is_file()}}
            self.reports[lane] = item
            ci.write(directory / 'report.json', item)

    def validate(self):
        return ci.validate_evidence(self.root, 'contract', ci.LANES, self.head, '2')

    def change(self, lane='inventory', **changes):
        self.reports[lane].update(changes)
        ci.write(self.root / lane / 'report.json', self.reports[lane])

    def test_complete_matching_results_succeed(self):
        self.assertEqual(set(self.validate()), set(ci.LANES))

    def test_all_success_is_the_only_needs_result_allowed(self):
        good = {'contract-core': {'result': 'success'}, 'contract-native': {'result': 'success'}}
        ci.validate_needs(good, list(good))
        for result in ('failure', 'cancelled', 'skipped', None, '', 'timed_out'):
            with self.subTest(result=result), self.assertRaises(ValueError):
                ci.validate_needs({**good, 'contract-native': {'result': result}}, list(good))
        with self.assertRaises(ValueError):
            ci.validate_needs({'contract-core': good['contract-core']}, list(good))

    def test_failure_skip_cancel_timeout_and_missing_results_reject(self):
        for status in ('failure', 'skipped', 'cancelled', 'timed_out', None):
            with self.subTest(status=status):
                self.change(status=status)
                with self.assertRaises(ValueError):
                    self.validate()
        self.change(status='success')
        (self.root / 'inventory/report.json').unlink()
        with self.assertRaises(ValueError):
            self.validate()

    def test_bootstrap_only_cannot_satisfy_a_lane(self):
        (self.root / 'inventory/report.json').unlink()
        ci.write(self.root / 'inventory/bootstrap.json', {'status': 'bootstrap_incomplete'})
        with self.assertRaises(ValueError):
            self.validate()

    def test_omitted_command_or_weakened_timeout_rejects(self):
        original = copy.deepcopy(self.reports['inventory']['steps'])
        for changed in ([], [{**original[0], 'plannedCommand': ['true']}],
                        [{**original[0], 'timeoutSeconds': 601}],
                        [{**original[0], 'exitCode': 1}],
                        [{**original[0], 'exitCode': False}],
                        [{**original[0], 'processGroup': None}],
                        [{**original[0], 'remainingLiveGroupMembers': None}],
                        [{**original[0], 'remainingLiveGroupMembers': [456]}]):
            self.change(steps=changed)
            with self.subTest(steps=changed), self.assertRaises(ValueError):
                self.validate()

    def test_corrupt_or_missing_raw_logs_reject(self):
        (self.root / 'inventory/logs/00.log').write_text('altered')
        with self.assertRaises(ValueError):
            self.validate()
        (self.root / 'inventory/logs/00.log').unlink()
        with self.assertRaises(ValueError):
            self.validate()

    def test_missing_or_traversing_manifest_rejects(self):
        self.change(filesSHA256={})
        with self.assertRaises(ValueError):
            self.validate()
        self.change(filesSHA256={'source.json': ci.sha(self.root / 'inventory/source.json'), '../outside': 'bad'})
        with self.assertRaises(ValueError):
            self.validate()

    def test_stale_head_run_tree_and_source_manifest_reject(self):
        original = copy.deepcopy(self.reports['inventory'])
        for changes in ({'head': '0' * 40}, {'runId': 'another-run'}, {'tree': '0' * 40},
                        {'businessDigest': 'different'}, {'attempt': '3'}):
            self.reports['inventory'] = copy.deepcopy(original)
            self.change(**changes)
            with self.subTest(changes=changes), self.assertRaises(ValueError):
                with patch.dict(os.environ, {'GITHUB_RUN_ID': original['runId']}):
                    self.validate()

    def test_owned_resource_or_temp_directory_leak_rejects(self):
        for cleanup in ({'ownedTemporaryDirectoryRemoved': False},
                        {'ownedTemporaryDirectoryRemoved': True, 'remainingNewResources': {'containers': ['owned-leftover']}}):
            self.change(cleanup=cleanup)
            with self.subTest(cleanup=cleanup), self.assertRaises(ValueError):
                self.validate()

    def test_missing_cleanup_cases_or_planned_logs_never_passes(self):
        original = copy.deepcopy(self.reports['inventory'])
        variants = [
            {'cleanup': {'ownedTemporaryDirectoryRemoved': True}},
            {'cases': None},
            {'cases': {'run': [], 'pass': [], 'fail': [], 'skip': []}},
            {'cases': {'run': [None], 'pass': ['case'], 'fail': [], 'skip': []}},
            {'filesSHA256': {'source.json': ci.sha(self.root / 'inventory/source.json')}}]
        for changes in variants:
            self.reports['inventory'] = copy.deepcopy(original)
            self.change(**changes)
            with self.subTest(changes=changes), self.assertRaises(ValueError):
                self.validate()

    def test_duplicate_same_attempt_rejects(self):
        other = self.root / 'duplicate'
        other.mkdir()
        ci.write(other / 'report.json', self.reports['inventory'])
        with self.assertRaises(ValueError):
            self.validate()

    def test_successful_previous_attempt_is_reused_with_provenance(self):
        found = self.validate()
        self.assertEqual(found['inventory']['attempt'], '1')

    def test_newer_failed_attempt_never_falls_back_to_old_success(self):
        other = self.root / 'newer'
        other.mkdir()
        ci.write(other / 'report.json', {**self.reports['inventory'], 'attempt': '2', 'status': 'failure'})
        with self.assertRaises(ValueError):
            self.validate()

    def test_redirects_strip_github_token(self):
        import urllib.request
        original = urllib.request.Request('https://api.github.com/repos/a/b/actions/jobs/1/logs',
                                          headers={'Authorization': 'Bearer synthetic-token'})
        redirected = ci.StripRedirectCredential().redirect_request(original, None, 302, '', {}, 'https://logs.example.test/signed')
        self.assertFalse(redirected.has_header('Authorization'))

    def test_missing_job_logs_cannot_count_as_collected(self):
        class Response:
            def __enter__(self):
                return self
            def __exit__(self, *_):
                return False
            def read(self):
                return b'{"jobs": []}'
        class Opener:
            def open(self, *_args, **_kwargs):
                return Response()
        env = {'GITHUB_REPOSITORY': 'synthetic/repository', 'GITHUB_RUN_ID': '123',
               'GITHUB_TOKEN': 'synthetic-token-not-printed'}
        destination = self.root / 'collector'
        destination.mkdir()
        with patch.dict(os.environ, env), patch.object(ci.urllib.request, 'build_opener', return_value=Opener()):
            with self.assertRaises(ValueError):
                ci.collect_job_logs(destination)
        self.assertEqual(len(json.loads((destination / 'job-log-manifest.json').read_text())['missing']), len(ci.LANES))

    def test_failed_artifact_or_log_collection_rejects_aggregate(self):
        args = argparse.Namespace(benchmark_only=False, needs_job=None, input=self.root,
                                  output=self.root / 'aggregate', prefix='contract')
        for artifact, logs in [('failure', 'success'), ('success', 'failure'),
                               ('skipped', 'success'), ('success', 'cancelled'), ('', '')]:
            env = {'GITHUB_ACTIONS': 'true', 'CI_CONTRACT_ARTIFACT_RESULT': artifact,
                   'CI_CONTRACT_JOB_LOG_RESULT': logs}
            with self.subTest(artifact=artifact, logs=logs), patch.dict(os.environ, env):
                with self.assertRaises(ValueError):
                    ci.aggregate(args)

    def test_owned_command_success_failure_and_timeout(self):
        for body, expected, timeout in [('print("owned command")', 'success', 5),
                                        ('raise SystemExit(7)', 'failure', 5),
                                        ('import time; time.sleep(30)', 'timed_out', 0.05)]:
            with self.subTest(expected=expected):
                result = ci.run_command([sys.executable, '-c', body], self.root / 'child.log', dict(os.environ), timeout)
                self.assertEqual(result['status'], expected)

    def test_benchmark_rejects_failed_or_skipped_artifact_collection(self):
        for result in ('failure', 'cancelled', 'skipped', ''):
            env = {'GITHUB_ACTIONS': 'true', 'CI_CONTRACT_ARTIFACT_RESULT': result}
            with self.subTest(result=result), patch.dict(os.environ, env):
                with self.assertRaisesRegex(ValueError, 'benchmark artifact download did not succeed'):
                    ci.benchmark(argparse.Namespace())

    def benchmark_sample(self, lane_attempt='2', gate_attempt='2', parallel_span=200, serial_gate_delay=0):
        import shutil
        jobs = []
        origin = 1770000000

        def stamp(offset):
            return ci.datetime.fromtimestamp(origin + offset, ci.timezone.utc).isoformat()

        for mode in ('serial', 'parallel'):
            prefix = 'benchmark-' + mode
            for index, lane in enumerate(ci.BENCHMARK_LANES):
                directory = self.root / prefix / lane
                shutil.copytree(self.root / lane, directory)
                item = {**self.reports[lane], 'prefix': prefix, 'attempt': lane_attempt,
                        'runId': '1234', 'coldCache': True,
                        'runner': {'os': 'Linux', 'architecture': 'x86_64', 'cpuCount': 4,
                                   'memoryBytes': 16 * 1024**3, 'imageOS': 'ubuntu24',
                                   'imageVersion': 'same-reviewed-image'}}
                ci.write(directory / 'report.json', item)
                start = index * 100 if mode == 'serial' else (index // 3) * parallel_span / 2
                duration = 100 if mode == 'serial' else parallel_span / 2
                jobs.append({'id': len(jobs) + 1, 'name': 'matrix / ' + prefix + '/' + lane,
                             'status': 'completed', 'conclusion': 'success', 'run_attempt': 2,
                             'created_at': stamp(0), 'started_at': stamp(start),
                             'completed_at': stamp(start + duration)})
            end = 600 + serial_gate_delay if mode == 'serial' else parallel_span
            jobs.append({'id': len(jobs) + 1, 'name': mode + '-gate', 'status': 'completed',
                         'conclusion': 'success', 'run_attempt': 2, 'created_at': stamp(end),
                         'started_at': stamp(end), 'completed_at': stamp(end + 20)})
            ci.write(self.root / (prefix + '-gate') / 'aggregate.json',
                     {'status': 'success', 'head': self.head, 'runId': '1234',
                      'attempt': gate_attempt, 'prefix': prefix,
                      'lanes': sorted(ci.BENCHMARK_LANES), 'completedAt': stamp(end + 10)})
        return jobs

    def measured_benchmark(self, jobs):
        env = {'GITHUB_ACTIONS': 'true', 'CI_CONTRACT_ARTIFACT_RESULT': 'success',
               'GITHUB_SHA': self.head, 'GITHUB_RUN_ID': '1234', 'GITHUB_RUN_ATTEMPT': '2',
               'CI_CONTRACT_NEEDS': json.dumps({name: {'result': 'success'}
                                               for name in ('serial-gate', 'parallel-gate')})}
        args = argparse.Namespace(input=self.root, output=self.root / 'measurement')
        with patch.dict(os.environ, env), patch.object(ci, 'collect_job_logs', return_value=jobs):
            return ci.benchmark(args)

    def test_benchmark_rejects_retained_lane_timestamps_after_gate_rerun(self):
        # GitHub clones retained successful jobs with the new run_attempt and
        # job IDs, while keeping their original execution timestamps.
        jobs = self.benchmark_sample(lane_attempt='1', parallel_span=600, serial_gate_delay=600)
        with self.assertRaisesRegex(ValueError, 'current execution attempt'):
            self.measured_benchmark(jobs)

    def test_benchmark_rejects_retained_aggregate_after_lane_rerun(self):
        jobs = self.benchmark_sample(gate_attempt='1', parallel_span=600, serial_gate_delay=600)
        with self.assertRaisesRegex(ValueError, 'benchmark aggregate'):
            self.measured_benchmark(jobs)

    def test_benchmark_checks_original_aggregate_identity_and_success(self):
        jobs = self.benchmark_sample()
        path = self.root / 'benchmark-serial-gate' / 'aggregate.json'
        original = json.loads(path.read_text())
        for change in ({'status': 'failure'}, {'head': '0' * 40}, {'runId': 'another-run'},
                       {'attempt': '3'}, {'lanes': []}):
            ci.write(path, {**original, **change})
            with self.subTest(change=change), self.assertRaisesRegex(ValueError, 'benchmark aggregate'):
                self.measured_benchmark(jobs)

    def test_benchmark_accepts_a_complete_current_attempt_sample(self):
        jobs = self.benchmark_sample()
        for mode in ('serial', 'parallel'):
            args = argparse.Namespace(input=self.root, output=self.root / ('benchmark-' + mode + '-gate'),
                                      prefix='benchmark-' + mode, needs_job=mode, benchmark_only=True)
            env = {'GITHUB_ACTIONS': 'true', 'CI_CONTRACT_ARTIFACT_RESULT': 'success',
                   'GITHUB_SHA': self.head, 'GITHUB_RUN_ID': '1234', 'GITHUB_RUN_ATTEMPT': '2',
                   'CI_CONTRACT_NEEDS': json.dumps({mode: {'result': 'success'}})}
            with patch.dict(os.environ, env):
                self.assertEqual(ci.aggregate(args), 0)
        self.assertEqual(self.measured_benchmark(jobs), 0)
        measured = json.loads((self.root / 'measurement' / 'benchmark.json').read_text())
        self.assertEqual(measured['status'], 'success')
        self.assertEqual(measured['executionAttempt'], '2')
        self.assertEqual(measured['aggregateEvidence']['benchmark-serial']['attempt'], '2')
        self.assertEqual(measured['modes']['serial']['criticalPathSecondsIncludingSetupArtifactsCleanupAndAggregate'], 620)
        self.assertEqual(measured['modes']['parallel']['criticalPathSecondsIncludingSetupArtifactsCleanupAndAggregate'], 220)

    def test_benchmark_without_actual_speedup_remains_unproven(self):
        self.assertEqual(self.measured_benchmark(self.benchmark_sample(parallel_span=600)), 1)
        measured = json.loads((self.root / 'measurement' / 'benchmark.json').read_text())
        self.assertEqual(measured['status'], 'performance_unproven')
        self.assertEqual(measured['improvementFraction'], 0)

    def test_actual_cancellation_waits_for_its_owned_child(self):
        source = str(ROOT / 'scripts/ci-contract.py')
        code = ('import importlib.util,json,os,sys; from pathlib import Path; '
                f's=importlib.util.spec_from_file_location("c",{source!r}); '
                'c=importlib.util.module_from_spec(s); s.loader.exec_module(c); '
                f'r=c.run_command([sys.executable,"-c","import time; print(12345,flush=True); time.sleep(30)"],Path({str(self.root / "cancel.log")!r}),dict(os.environ)); '
                'print(json.dumps(r),flush=True)')
        proc = subprocess.Popen([sys.executable, '-B', '-c', code], stdout=subprocess.PIPE,
                                stderr=subprocess.STDOUT, start_new_session=True)
        try:
            with selectors.DefaultSelector() as selector:
                selector.register(proc.stdout, selectors.EVENT_READ)
                self.assertTrue(selector.select(10), 'owned child did not become ready')
                self.assertEqual(proc.stdout.readline().strip(), b'12345')
            proc.send_signal(signal.SIGTERM)
            output = proc.communicate(timeout=10)[0]
            self.assertEqual(proc.returncode, 0)
            result = json.loads(output)
            self.assertEqual(result['status'], 'cancelled')
            self.assertNotEqual(result['exitCode'], 0)
        finally:
            if proc.poll() is None:
                proc.terminate()
                proc.communicate(timeout=50)

    def test_local_all_lane_entrypoint_survives_a_stdin_reader(self):
        binary = self.root / 'fake-bin'
        binary.mkdir()
        trace = self.root / 'invoked-lanes.txt'
        worker = binary / 'python3'
        worker.write_text(f'#!{sys.executable}\n'
                          'import os,sys\n'
                          "if sys.argv[1:2] == ['-c']:\n"
                          f'    os.execv({sys.executable!r}, [{sys.executable!r}, *sys.argv[1:]])\n'
                          f'with open({str(trace)!r}, "a") as output: output.write(sys.argv[3]+"\\n")\n'
                          'sys.stdin.read()\n')
        worker.chmod(0o700)
        make = binary / 'make'
        make.write_text('#!/bin/sh\nexit 0\n')
        make.chmod(0o700)
        env = dict(os.environ, PATH=str(binary) + os.pathsep + os.environ['PATH'])
        result = subprocess.run([str(ROOT / 'scripts/ci-local.sh'), '--job', 'contract'], cwd=ROOT,
                                env=env, stdin=subprocess.DEVNULL, capture_output=True, text=True, timeout=15)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertEqual(trace.read_text().splitlines(), ['core', *ci.PLAN['nativeOrder']])

    def test_deadline_and_resistant_descendants_survive_neither_pipe_shape(self):
        for mode in ('leader-eof', 'descendant-eof', 'descendant-inherit'):
            for trigger in ('timed_out', 'cancelled'):
                with self.subTest(mode=mode, trigger=trigger):
                    name = mode + '-' + trigger
                    ready = self.root / (name + '.json')
                    if mode == 'leader-eof':
                        body = ('import json,os,pathlib,time; '
                                f'pathlib.Path({str(ready)!r}).write_text(json.dumps({{"pid":os.getpid(),"pgid":os.getpgrp()}})); '
                                'os.close(1); os.close(2); time.sleep(30)')
                    else:
                        child = ('import json,os,pathlib,signal,time; signal.signal(signal.SIGTERM,signal.SIG_IGN); '
                                 f'pathlib.Path({str(ready)!r}).write_text(json.dumps({{"pid":os.getpid(),"pgid":os.getpgrp()}})); time.sleep(30)')
                        redirect = ',stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL' if mode == 'descendant-eof' else ''
                        body = ('import pathlib,subprocess,sys,time; '
                                f'subprocess.Popen([sys.executable,"-B","-c",{child!r}]{redirect}); '
                                f'p=pathlib.Path({str(ready)!r});\nwhile not p.exists(): time.sleep(.01)\n'
                                'print("owned descendant ready",flush=True); time.sleep(30)')
                    deadline = '.4' if trigger == 'timed_out' else 'None'
                    driver = ('import importlib.util,json,os,sys; from pathlib import Path; '
                              f's=importlib.util.spec_from_file_location("c",{str(ROOT / "scripts/ci-contract.py")!r}); '
                              'c=importlib.util.module_from_spec(s); s.loader.exec_module(c); c.TERMINATION_GRACE_SECONDS=.1; '
                              f'r=c.run_command([sys.executable,"-B","-c",{body!r}],Path({str(self.root / (name + ".log"))!r}),dict(os.environ),{deadline}); '
                              'print(json.dumps(r),flush=True)')
                    proc = subprocess.Popen([sys.executable, '-B', '-c', driver], stdout=subprocess.PIPE,
                                            stderr=subprocess.STDOUT, start_new_session=True)
                    try:
                        if trigger == 'cancelled':
                            expires = time.monotonic() + 5
                            while not ready.exists() and time.monotonic() < expires:
                                time.sleep(.01)
                            self.assertTrue(ready.exists(), 'owned child did not become ready')
                            proc.send_signal(signal.SIGTERM)
                        output = proc.communicate(timeout=8)[0].decode()
                        self.assertEqual(proc.returncode, 0, output)
                        result = json.loads(output.splitlines()[-1])
                        self.assertEqual(result['status'], trigger)
                        self.assertEqual(result['remainingLiveGroupMembers'], [])
                        self.assertFalse(ci.live_group_members(result['processGroup']))
                    finally:
                        if ready.exists():
                            owned_group = json.loads(ready.read_text())['pgid']
                            if ci.live_group_members(owned_group):
                                ci.signal_group(owned_group, signal.SIGKILL)
                        if proc.poll() is None:
                            proc.terminate()
                            try:
                                proc.communicate(timeout=3)
                            except subprocess.TimeoutExpired:
                                proc.kill()
                                proc.communicate(timeout=3)

    def test_owned_zombie_group_does_not_block_cleanup(self):
        proc = subprocess.Popen([sys.executable, '-B', '-c', 'pass'], start_new_session=True)
        try:
            deadline = time.monotonic() + 5
            while time.monotonic() < deadline:
                state = subprocess.check_output(['ps', '-p', str(proc.pid), '-o', 'stat='], text=True).strip()
                if state.startswith('Z'):
                    break
                time.sleep(.01)
            self.assertTrue(state.startswith('Z'), 'owned child did not reach zombie state: ' + state)
            self.assertEqual(ci.live_group_members(proc.pid), [])
            for sig in (0, signal.SIGTERM, signal.SIGKILL):
                ci.signal_group(proc.pid, sig)
        finally:
            proc.wait(timeout=5)

    def test_kernel_permission_error_for_empty_group_settles(self):
        observed = subprocess.CompletedProcess([], 0, stdout='123 456 Z\n124 456 X\n')
        with patch.object(ci.os, 'killpg', side_effect=PermissionError('owned zombie group')):
            with patch.object(ci.subprocess, 'run', return_value=observed):
                self.assertEqual(ci.live_group_members(456), [])
                for sig in (0, signal.SIGTERM, signal.SIGKILL):
                    ci.signal_group(456, sig)

    def test_kernel_permission_error_with_live_group_fails_cleanup(self):
        observed = subprocess.CompletedProcess([], 0, stdout='123 456 S\n124 456 Z\n')
        with patch.object(ci.os, 'killpg', side_effect=PermissionError('live group denied')):
            with patch.object(ci.subprocess, 'run', return_value=observed):
                self.assertEqual(ci.live_group_members(456), [123])
                for sig in (signal.SIGTERM, signal.SIGKILL):
                    with self.assertRaises(PermissionError):
                        ci.signal_group(456, sig)

    def test_failed_inventory_cannot_prove_empty_group(self):
        failure = subprocess.CalledProcessError(1, ['ps'])
        with patch.object(ci.os, 'killpg', side_effect=PermissionError('unverified group')):
            with patch.object(ci.subprocess, 'run', side_effect=failure):
                with self.assertRaises(subprocess.CalledProcessError):
                    ci.live_group_members(456)
                with self.assertRaises(subprocess.CalledProcessError):
                    ci.signal_group(456, signal.SIGKILL)

    def test_mapping_preserves_original_make_targets_and_timeouts(self):
        baseline = subprocess.check_output(['git', 'show', ci.PLAN['baseline'] + ':.github/workflows/ci.yml'], cwd=ROOT, text=True)
        original = baseline.split('  contract-and-test:', 1)[1].split('  quality:', 1)[0]
        expected = re.findall(r'^\s+(?:run:\s+)?make ([a-z0-9-]+)\s*$', original, re.M)
        actual = [s['command'][1] for lane in ci.PLAN['lanes'].values() for s in lane['steps'] if s['command'][0] == 'make']
        # The new discovery gate extends the matrix without removing any of
        # the original targets used by the fixed six-lane performance scope.
        self.assertCountEqual(actual, [*expected, 'native-oci-discovery-e2e'])
        self.assertEqual(ci.PLAN['lanes']['oci-discovery']['steps'][-1]['timeoutSeconds'], 900)
        self.assertEqual(ci.PLAN['lanes']['nuget']['steps'][-1]['timeoutSeconds'], 900)
        self.assertEqual(ci.PLAN['lanes']['six-format-auth']['steps'][-1]['timeoutSeconds'], 900)
        for lane in ('migration-clients', 'inventory', 'oci-migration', 'maven-oci-auth'):
            self.assertEqual(ci.PLAN['lanes'][lane]['steps'][-1]['timeoutSeconds'], 600)
        for lane in ci.PLAN['nativeOrder']:
            self.assertEqual(ci.PLAN['lanes'][lane]['steps'][0]['command'], ['go', 'mod', 'download'])
            self.assertEqual(ci.PLAN['lanes'][lane]['steps'][0]['timeoutSeconds'], 300)
        steps = {s['name']: s for s in ci.PLAN['lanes']['core']['steps']}
        self.assertEqual(steps['published-native-backup-test']['timeoutSeconds'], 900)
        self.assertEqual(steps['upgrade-readiness']['timeoutSeconds'], 600)

    def test_workflow_preserves_stable_aggregate_and_publication_dependencies(self):
        workflow = (ROOT / '.github/workflows/ci.yml').read_text()
        self.assertIn('    needs: [contract-core, contract-native]', workflow)
        self.assertIn('  contract-and-test:\n    if: always()', workflow)
        self.assertEqual(workflow.count('    needs: [contract-and-test, quality, console]'), 2)
        native = workflow.split('  contract-native:', 1)[1].split('  contract-and-test:', 1)[0]
        self.assertIn('fail-fast: false', native)
        self.assertIn('max-parallel: 3', native)
        self.assertNotIn('needs:', native)
        self.assertNotIn('continue-on-error', workflow)
        mapped = re.search(r'lane: \[([^]]+)\]', native).group(1).split(', ')
        self.assertEqual(mapped, ci.PLAN['nativeOrder'])

    def test_new_discovery_lane_is_required_without_changing_benchmark_scope(self):
        self.assertIn('oci-discovery', ci.LANES)
        self.assertIn('oci-discovery', ci.PLAN['nativeOrder'])
        self.assertNotIn('oci-discovery', ci.BENCHMARK_LANES)
        workflow = (ROOT / '.github/workflows/contract-benchmark.yml').read_text()
        matrices = re.findall(r'lane: \[([^]]+)\]', workflow)
        self.assertEqual(len(matrices), 2)
        for matrix in matrices:
            self.assertEqual(matrix.split(', '), list(ci.BENCHMARK_LANES))
        self.assertEqual(workflow.count('--benchmark-only'), 2)


if __name__ == '__main__':
    unittest.main()
