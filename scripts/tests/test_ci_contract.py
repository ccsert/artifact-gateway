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
        self.assertEqual(len(json.loads((destination / 'job-log-manifest.json').read_text())['missing']), 7)

    def test_failed_artifact_or_log_collection_rejects_aggregate(self):
        args = argparse.Namespace(native_only=False, needs_job=None, input=self.root,
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

    def test_mapping_preserves_original_make_targets_and_timeouts(self):
        baseline = subprocess.check_output(['git', 'show', ci.PLAN['baseline'] + ':.github/workflows/ci.yml'], cwd=ROOT, text=True)
        original = baseline.split('  contract-and-test:', 1)[1].split('  quality:', 1)[0]
        expected = re.findall(r'^\s+(?:run:\s+)?make ([a-z0-9-]+)\s*$', original, re.M)
        actual = [s['command'][1] for lane in ci.PLAN['lanes'].values() for s in lane['steps'] if s['command'][0] == 'make']
        self.assertCountEqual(actual, expected)
        self.assertEqual(ci.PLAN['lanes']['nuget']['steps'][0]['timeoutSeconds'], 900)
        self.assertEqual(ci.PLAN['lanes']['six-format-auth']['steps'][0]['timeoutSeconds'], 900)
        for lane in ('migration-clients', 'inventory', 'oci-migration', 'maven-oci-auth'):
            self.assertEqual(ci.PLAN['lanes'][lane]['steps'][0]['timeoutSeconds'], 600)
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


if __name__ == '__main__':
    unittest.main()
