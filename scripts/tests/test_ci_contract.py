import copy
import argparse
import base64
from email.message import Message
import hashlib
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
import zipfile

ROOT = Path(__file__).resolve().parents[2]
spec = importlib.util.spec_from_file_location('ci_contract', ROOT / 'scripts/ci-contract.py')
ci = importlib.util.module_from_spec(spec)
spec.loader.exec_module(ci)


class CoreCacheOwnershipTests(unittest.TestCase):
    def setUp(self):
        self.work = tempfile.TemporaryDirectory()
        self.addCleanup(self.work.cleanup)
        self.output = Path(self.work.name)
        self.volumes = {'unrelated-sentinel': self.volume('unrelated-sentinel', {'foreign': 'preserve'})}
        self.calls = []
        self.fail_create_ack = False
        self.fail_rm = None
        self.ignore_rm = False
        self.fail_inventory = False
        self.containers = {}
        self.patch = patch.object(ci.subprocess, 'run', side_effect=self.docker)
        self.patch.start()
        self.addCleanup(self.patch.stop)

    @staticmethod
    def volume(name, labels):
        return {'Name': name, 'Driver': 'local', 'Scope': 'local',
                'CreatedAt': '2026-10-21T00:00:00Z', 'Labels': labels, 'Options': None}

    def docker(self, command, **kwargs):
        self.calls.append(command)
        args = command[1:]
        if args in (['ps', '-aq'], ['volume', 'ls', '-q'], ['network', 'ls', '-q']):
            if self.fail_inventory:
                return subprocess.CompletedProcess(command, 1, '', 'inventory unavailable')
            names = self.containers if args[0] == 'ps' else self.volumes if args[0] == 'volume' else {}
            return subprocess.CompletedProcess(command, 0, '\n'.join(names) + '\n', '')
        if args[:2] == ['container', 'inspect']:
            return subprocess.CompletedProcess(command, 0, json.dumps([self.containers[args[-1]]]), '')
        if args[:2] == ['volume', 'create']:
            name = args[-1]
            labels = dict(args[i + 1].split('=', 1) for i, arg in enumerate(args) if arg == '--label')
            self.volumes.setdefault(name, self.volume(name, labels))
            if self.fail_create_ack:
                raise subprocess.TimeoutExpired(command, 30)
            return subprocess.CompletedProcess(command, 0, name + '\n', '')
        if args[:2] == ['volume', 'inspect']:
            name = args[-1]
            if name not in self.volumes:
                return subprocess.CompletedProcess(command, 1, '', 'not found')
            return subprocess.CompletedProcess(command, 0, json.dumps([self.volumes[name]]), '')
        if args[:2] == ['volume', 'rm']:
            name = args[-1]
            if name == self.fail_rm:
                return subprocess.CompletedProcess(command, 1, '', 'volume remains in use')
            if not self.ignore_rm:
                del self.volumes[name]
            return subprocess.CompletedProcess(command, 0, name + '\n', '')
        raise AssertionError('unexpected Docker operation: ' + repr(command))

    def test_claims_and_removes_only_two_new_core_caches(self):
        caches = ci.CoreGoCacheVolumes(self.output, 'run-123', '2')
        caches.claim({'volumes': ['unrelated-sentinel']})
        for name in ('artifact-gateway-go-mod', 'artifact-gateway-go-build'):
            self.assertEqual(self.volumes[name]['Labels']['artifact-gateway.ci-run'], 'run-123')
            self.assertEqual(self.volumes[name]['Labels']['artifact-gateway.ci-attempt'], '2')
            self.assertEqual(self.volumes[name]['Labels']['artifact-gateway.ci-lane'], 'core')
        result = caches.cleanup()
        self.assertEqual(set(result['removed']), {'artifact-gateway-go-mod', 'artifact-gateway-go-build'})
        self.assertEqual(result['remaining'], [])
        self.assertEqual(result['errors'], [])
        self.assertEqual(set(self.volumes), {'unrelated-sentinel'})
        proof = json.loads((self.output / 'docker-cache-claims.json').read_text())
        self.assertEqual(proof['removed'], result['removed'])


    def test_preexisting_core_cache_is_never_consumed_or_removed(self):
        for name in ci.CORE_GO_CACHE_VOLUMES:
            with self.subTest(name=name):
                self.volumes[name] = self.volume(name, {'foreign': 'preserve'})
                original = copy.deepcopy(self.volumes)
                self.calls.clear()
                caches = ci.CoreGoCacheVolumes(self.output, 'run-123', '2')
                with self.assertRaisesRegex(RuntimeError, 'pre-existing'):
                    caches.claim({'volumes': list(self.volumes)})
                self.assertEqual(caches.cleanup()['removed'], [])
                self.assertEqual(self.volumes, original)
                self.assertEqual(self.calls, [])
                del self.volumes[name]

    def test_changed_owner_or_creation_receipt_prevents_removal(self):
        for field, value in [('Labels', {'foreign': 'preserve'}),
                             ('CreatedAt', '2026-10-22T00:00:00Z'), ('Driver', 'foreign'),
                             ('Scope', 'global'), ('Options', {'device': '/foreign'})]:
            with self.subTest(field=field):
                self.volumes = {'unrelated-sentinel': self.volume('unrelated-sentinel', {})}
                caches = ci.CoreGoCacheVolumes(self.output, 'run-123', '2')
                caches.claim({'volumes': ['unrelated-sentinel']})
                name = 'artifact-gateway-go-mod'
                self.volumes[name][field] = value
                self.calls.clear()
                result = caches.cleanup()
                self.assertEqual(result['remaining'], [name])
                self.assertTrue(result['errors'])
                self.assertIn(name, self.volumes)
                self.assertNotIn(['docker', 'volume', 'rm', name], self.calls)
                self.assertIn('unrelated-sentinel', self.volumes)

    def test_lost_create_ack_recovers_only_our_receipt_for_cleanup(self):
        self.fail_create_ack = True
        caches = ci.CoreGoCacheVolumes(self.output, 'run-123', '2')
        with self.assertRaises(subprocess.TimeoutExpired):
            caches.claim({'volumes': ['unrelated-sentinel']})
        self.assertEqual(caches.state['receipts'], {})
        result = caches.cleanup()
        self.assertEqual(result['removed'], ['artifact-gateway-go-mod'])
        self.assertEqual(result['remaining'], [])
        self.assertEqual(result['errors'][0]['stage'], 'claim')
        self.assertEqual(set(self.volumes), {'unrelated-sentinel'})

    def test_lost_create_ack_does_not_adopt_replaced_foreign_volume(self):
        self.fail_create_ack = True
        caches = ci.CoreGoCacheVolumes(self.output, 'run-123', '2')
        with self.assertRaises(subprocess.TimeoutExpired):
            caches.claim({'volumes': ['unrelated-sentinel']})
        name = 'artifact-gateway-go-mod'
        self.volumes[name] = self.volume(name, {'foreign': 'preserve'})
        self.calls.clear()
        result = caches.cleanup()
        self.assertEqual(result['removed'], [])
        self.assertEqual(result['remaining'], [name])
        self.assertNotIn(['docker', 'volume', 'rm', name], self.calls)
        self.assertIn('unrelated-sentinel', self.volumes)

    def test_removal_failure_remains_failed_and_preserves_volume(self):
        caches = ci.CoreGoCacheVolumes(self.output, 'run-123', '2')
        caches.claim({'volumes': ['unrelated-sentinel']})
        self.fail_rm = 'artifact-gateway-go-mod'
        result = caches.cleanup()
        self.assertEqual(result['remaining'], [self.fail_rm])
        self.assertEqual(result['removed'], ['artifact-gateway-go-build'])
        self.assertTrue(result['errors'])
        self.assertIn(self.fail_rm, self.volumes)
        self.assertIn('unrelated-sentinel', self.volumes)

    def test_diagnostics_never_store_environment_commands_or_arguments(self):
        self.containers['owned-id'] = {
            'Id': 'owned-id', 'Name': '/owned-container', 'Image': 'immutable-image-id',
            'Config': {'Labels': {'com.docker.compose.project': 'owned'},
                       'Env': ['PRIVATE_TOKEN=never-write-this-secret'],
                       'Cmd': ['never-write-this-command']},
            'Args': ['never-write-these-arguments'],
            'Mounts': [{'Type': 'volume', 'Name': 'unrelated-sentinel',
                        'Destination': '/data', 'RW': True, 'Private': 'never-write-this-mount-field'}]}
        path = self.output / 'snapshot.json'
        ci.docker_step_snapshot(path)
        data = path.read_text()
        self.assertNotIn('never-write-', data)
        snapshot = json.loads(data)
        self.assertEqual(set(snapshot['volumes'][0]), {'Name', 'Driver', 'Scope', 'CreatedAt', 'Labels', 'Options'})
        container = snapshot['containers'][0]
        self.assertEqual(set(container), {'ID', 'Name', 'Image', 'Config', 'Mounts'})
        self.assertEqual(set(container['Config']), {'Labels'})
        self.assertEqual(container['Mounts'][0]['Name'], 'unrelated-sentinel')

    def run_lane(self, action=None, local=False, result_status='success', raw_directories=None):
        root = self.output / 'repository'
        root.mkdir(exist_ok=True)
        output = self.output / 'lane'
        args = argparse.Namespace(lane='core', output=output, prefix='contract',
                                  cold_cache=False, local=local, keep_going=False)
        step = {'name': 'unchanged target', 'command': ['make', 'original-target'], 'timeoutSeconds': 600}
        source = {'head': '1' * 40, 'tree': '2' * 40, 'businessDigest': 'unchanged-business'}
        def command(command, log, env, timeout):
            if action:
                action()
            log.write_text('original producer result\n')
            return {'command': command, 'status': result_status,
                    'exitCode': 0 if result_status == 'success' else 7,
                    'timeoutSeconds': timeout, 'processGroup': 123, 'remainingLiveGroupMembers': []}
        with patch.dict(os.environ, {'GITHUB_ACTIONS': 'true', 'GITHUB_RUN_ID': '123', 'GITHUB_RUN_ATTEMPT': '2'}):
            with patch.object(ci, 'ROOT', root), patch.object(ci, 'business_source', return_value=source):
                with patch.object(ci, 'PLAN', {'lanes': {'core': {'steps': [step], 'rawDirectories': raw_directories or []}}}):
                    with patch.object(ci, 'run_command', side_effect=command) as ran:
                        try:
                            status = ci.run_lane(args)
                        except Exception:
                            self.last_business_calls = ran.call_count
                            raise
                        self.last_business_calls = ran.call_count
        return status, json.loads((output / 'report.json').read_text())

    def test_local_core_never_claims_or_deletes_caches(self):
        self.volumes['artifact-gateway-go-mod'] = self.volume('artifact-gateway-go-mod', {'foreign': 'preserve'})
        original = copy.deepcopy(self.volumes)
        with patch.object(ci, 'CoreGoCacheVolumes') as caches:
            status, report = self.run_lane(local=True)
            caches.assert_not_called()
        self.assertEqual(status, 0)
        self.assertEqual(report['status'], 'success')
        self.assertEqual(self.volumes, original)
        self.assertFalse(any(c[1:3] in (['volume', 'create'], ['volume', 'rm']) for c in self.calls))

    def test_dedicated_core_preserves_unknown_volume_and_fails(self):
        anonymous = 'a' * 64
        def producer():
            self.volumes[anonymous] = self.volume(anonymous, {})
        status, report = self.run_lane(action=producer)
        self.assertEqual(status, 1)
        self.assertEqual(report['status'], 'failure')
        self.assertEqual(report['cleanup']['remainingNewResources']['volumes'], [anonymous])
        self.assertEqual(set(report['cleanup']['coreGoCaches']['removed']), set(ci.CORE_GO_CACHE_VOLUMES))
        self.assertEqual(report['cleanup']['coreGoCaches']['errors'], [])
        self.assertEqual(set(self.volumes), {'unrelated-sentinel', anonymous})
        self.assertEqual(report['steps'][0]['timeoutSeconds'], 600)
        for path, digest in report['filesSHA256'].items():
            self.assertEqual(ci.sha(self.output / 'lane' / path), digest)
        self.assertIn('docker/00-before.json', report['filesSHA256'])
        self.assertIn('docker/00-after.json', report['filesSHA256'])
        self.assertIn('docker-cache-claims.json', report['filesSHA256'])

    def test_dedicated_step_failure_is_not_hidden_by_successful_cleanup(self):
        status, report = self.run_lane(result_status='failure')
        self.assertEqual(status, 1)
        self.assertEqual(report['steps'][0]['exitCode'], 7)
        self.assertEqual(report['cleanup']['coreGoCaches']['remaining'], [])
        self.assertEqual(set(self.volumes), {'unrelated-sentinel'})

    def test_dedicated_lost_ack_runs_no_business_target_and_stays_failed(self):
        self.fail_create_ack = True
        with self.assertRaises(subprocess.TimeoutExpired):
            self.run_lane()
        report = json.loads((self.output / 'lane/report.json').read_text())
        self.assertEqual(report['status'], 'failure')
        self.assertEqual(self.last_business_calls, 0)
        self.assertEqual(report['cleanup']['coreGoCaches']['removed'], ['artifact-gateway-go-mod'])
        self.assertTrue(report['cleanup']['coreGoCaches']['errors'])
        self.assertEqual(set(self.volumes), {'unrelated-sentinel'})

    def test_failed_diagnostics_stay_failed_and_still_clean_owned_caches(self):
        def producer():
            self.fail_inventory = True
        with self.assertRaisesRegex(RuntimeError, 'inventory'):
            self.run_lane(action=producer)
        report = json.loads((self.output / 'lane/report.json').read_text())
        self.assertEqual(report['status'], 'failure')
        self.assertEqual(report['cleanup']['coreGoCaches']['removed'], [])
        self.assertEqual(set(report['cleanup']['coreGoCaches']['remaining']), set(ci.CORE_GO_CACHE_VOLUMES))
        self.assertTrue(report['cleanup']['coreGoCaches']['errors'])
        self.assertTrue(report['cleanup']['errors'])
        self.assertEqual(set(self.volumes), {'unrelated-sentinel'})

    def test_raw_evidence_copy_failure_still_cleans_and_fails(self):
        def producer():
            (self.output / 'repository/output/raw/new-evidence').mkdir(parents=True)
        with patch.object(ci.shutil, 'copytree', side_effect=OSError('retain failure')):
            status, report = self.run_lane(action=producer, raw_directories=['output/raw'])
        self.assertEqual(status, 1)
        self.assertEqual(report['status'], 'failure')
        self.assertEqual(report['cleanup']['errors'][0]['stage'], 'raw-evidence')
        self.assertEqual(set(self.volumes), {'unrelated-sentinel'})


    def test_acknowledged_removal_without_readback_stays_failed(self):
        caches = ci.CoreGoCacheVolumes(self.output, 'run-123', '2')
        caches.claim({'volumes': ['unrelated-sentinel']})
        self.ignore_rm = True
        result = caches.cleanup()
        self.assertEqual(result['removed'], [])
        self.assertEqual(set(result['remaining']), set(ci.CORE_GO_CACHE_VOLUMES))
        self.assertTrue(result['errors'])
        self.assertIn('unrelated-sentinel', self.volumes)

    def test_dedicated_preexisting_cache_runs_no_business_target(self):
        self.volumes['artifact-gateway-go-mod'] = self.volume('artifact-gateway-go-mod', {'foreign': 'preserve'})
        original = copy.deepcopy(self.volumes)
        with self.assertRaisesRegex(RuntimeError, 'pre-existing'):
            self.run_lane()
        report = json.loads((self.output / 'lane/report.json').read_text())
        self.assertEqual(report['status'], 'failure')
        self.assertEqual(self.last_business_calls, 0)
        self.assertEqual(self.volumes, original)
        self.assertFalse(any(c[1:3] == ['volume', 'rm'] for c in self.calls))


    def test_cancellation_between_commands_still_cleans_owned_caches(self):
        previous = {sig: signal.getsignal(sig) for sig in (signal.SIGINT, signal.SIGTERM)}
        def cancelled():
            signal.raise_signal(signal.SIGTERM)
        with self.assertRaises(SystemExit) as raised:
            self.run_lane(action=cancelled)
        self.assertEqual(raised.exception.code, 128 + signal.SIGTERM)
        report = json.loads((self.output / 'lane/report.json').read_text())
        self.assertEqual(report['status'], 'failure')
        self.assertEqual(report['cancelledBySignal'], signal.SIGTERM)
        self.assertEqual(set(report['cleanup']['coreGoCaches']['removed']), set(ci.CORE_GO_CACHE_VOLUMES))
        self.assertEqual(set(self.volumes), {'unrelated-sentinel'})
        self.assertEqual({sig: signal.getsignal(sig) for sig in previous}, previous)


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
        # New native gates extend the matrix without removing any original
        # target from the fixed six-lane performance scope.
        self.assertCountEqual(actual, [*expected, 'native-oci-discovery-e2e',
                                     'nexus-npm-migration-e2e', 'native-helm-e2e'])
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
        collector = workflow.split('      - name: Preserve complete job logs', 1)[1].split('      - name:', 1)[0]
        self.assertIn('CI_CONTRACT_NEEDS: ${{ toJSON(needs) }}', collector)
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

    def test_npm_migration_and_helm_are_required_without_expanding_benchmark(self):
        for lane, target, directory in (
                ('npm-migration', 'nexus-npm-migration-e2e', 'output/nexus-npm-migration-e2e'),
                ('helm', 'native-helm-e2e', 'output/native-helm-e2e')):
            with self.subTest(lane=lane):
                self.assertIn(lane, ci.LANES)
                self.assertIn(lane, ci.PLAN['nativeOrder'])
                self.assertNotIn(lane, ci.BENCHMARK_LANES)
                plan = ci.PLAN['lanes'][lane]
                self.assertEqual(plan['steps'][-1]['command'], ['make', target])
                self.assertEqual(plan['steps'][-1]['timeoutSeconds'], 900)
                self.assertEqual(plan['rawDirectories'], [directory])


class ColdModuleCacheTests(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory(prefix='ag-cold-module-controls-')
        self.addCleanup(self.directory.cleanup)
        self.root = Path(self.directory.name)
        self.work = self.root / 'owned-work'
        self.work.mkdir()
        self.project = self.root / 'project'
        self.project.mkdir()
        self.proxy = self.root / 'proxy' / 'example.test' / 'cold-cache' / '@v'
        self.proxy.mkdir(parents=True)
        self.mod = 'module example.test/cold-cache\n\ngo 1.20\n'
        (self.proxy / 'v1.0.0.mod').write_text(self.mod)
        (self.proxy / 'v1.0.0.info').write_text('{"Version":"v1.0.0","Time":"2026-01-01T00:00:00Z"}')
        self.archive()
        (self.project / 'go.mod').write_text('module example.test/consumer\n\ngo 1.20\n\nrequire example.test/cold-cache v1.0.0\n')
        self.sums = ('example.test/cold-cache v1.0.0 h1:S2G5cFkb+JhWvCkT8nfHSMlNsONlwMIIkU72iYj8mPo=\n'
                     'example.test/cold-cache v1.0.0/go.mod h1:37wizKh8CH2eu2LLMbQD03xAs8Eqc8N1g2d2CYStrZI=\n')
        (self.project / 'go.sum').write_text(self.sums)
        self.foreign = self.root / 'existing-cache'
        self.foreign.mkdir()
        (self.foreign / 'sentinel').write_text('preserve existing cache')

    def archive(self, content='verified fixture'):
        with zipfile.ZipFile(self.proxy / 'v1.0.0.zip', 'w') as archive:
            prefix = 'example.test/cold-cache@v1.0.0/'
            archive.writestr(prefix + 'go.mod', self.mod)
            archive.writestr(prefix + 'cache.go', 'package cache\n\nconst Value = "' + content + '"\n')

    def lane(self, cold=True, command=None, cleanup_error=False):
        output = self.root / 'evidence'
        source = {'head': 'a' * 40, 'tree': 'b' * 40, 'businessDigest': 'c' * 64}
        command = command or ['go', '-C', str(self.project), 'mod', 'download', 'example.test/cold-cache@v1.0.0']
        plan = {'lanes': {'cold-probe': {'rawDirectories': [], 'steps': [
            {'name': 'verified local Go module', 'command': command, 'timeoutSeconds': 30}]}}}
        args = argparse.Namespace(lane='cold-probe', output=output, prefix='control',
                                  cold_cache=cold, local=True, keep_going=False)
        environment = {'GITHUB_ACTIONS': 'false', 'GOFLAGS': '-trimpath', 'GOENV': 'off',
                       'GOTOOLCHAIN': 'local', 'GOWORK': 'off',
                       'GONOSUMDB': 'example.test/cold-cache',
                       'GOPROXY': (self.root / 'proxy').as_uri(),
                       'GOCACHE': str(self.foreign / 'build'), 'GOMODCACHE': str(self.foreign / 'mod')}
        original_command, original_remove = ci.run_command, ci.shutil.rmtree
        observed = {}
        def producer(command, log, env, timeout):
            observed['flags'] = env['GOFLAGS']
            observed['moduleCache'] = env['GOMODCACHE']
            result = original_command(command, log, env, timeout)
            module = Path(env['GOMODCACHE']) / 'example.test' / 'cold-cache@v1.0.0'
            if module.exists():
                observed['moduleMode'] = module.stat().st_mode & 0o777
                observed['content'] = (module / 'cache.go').read_text()
            return result
        def remove(path, *arguments, **keywords):
            if cleanup_error and Path(path) == self.work:
                raise PermissionError('owned cleanup denied')
            return original_remove(path, *arguments, **keywords)
        with patch.object(ci, 'PLAN', plan), patch.object(ci, 'business_source', return_value=source), \
                patch.object(ci.tempfile, 'mkdtemp', return_value=str(self.work)), \
                patch.object(ci, 'run_command', side_effect=producer), \
                patch.object(ci.shutil, 'rmtree', side_effect=remove), \
                patch.dict(os.environ, environment):
            status = ci.run_lane(args)
            self.assertEqual(os.environ['GOFLAGS'], '-trimpath')
            self.assertEqual(os.environ['GOMODCACHE'], str(self.foreign / 'mod'))
        self.assertEqual((self.project / 'go.sum').read_text(), self.sums)
        self.assertEqual((self.foreign / 'sentinel').read_text(), 'preserve existing cache')
        return status, json.loads((output / 'report.json').read_text()), observed

    def test_cold_verified_module_is_writable_and_removed(self):
        status, report, observed = self.lane()
        self.assertEqual(status, 0)
        self.assertIn('-trimpath', observed['flags'])
        self.assertTrue(observed['moduleMode'] & 0o200)
        self.assertIn('verified fixture', observed['content'])
        self.assertEqual(observed['moduleCache'], str(self.work / 'go-mod'))
        self.assertEqual(report['cleanup']['errors'], [])
        self.assertTrue(report['cleanup']['ownedTemporaryDirectoryRemoved'])
        self.assertEqual(list(self.foreign.iterdir()), [self.foreign / 'sentinel'])

    def test_cold_flag_does_not_bypass_recorded_module_checksum(self):
        self.archive('tampered fixture')
        status, report, _ = self.lane()
        self.assertEqual(status, 1)
        self.assertNotEqual(report['steps'][0]['exitCode'], 0)
        self.assertIn('checksum mismatch', (self.root / 'evidence/logs/00.log').read_text())
        self.assertEqual(report['cleanup']['errors'], [])
        self.assertTrue(report['cleanup']['ownedTemporaryDirectoryRemoved'])

    def test_non_cold_lane_preserves_existing_flags_and_cache(self):
        status, report, observed = self.lane(cold=False, command=['go', 'env', 'GOFLAGS'])
        self.assertEqual(status, 0)
        self.assertEqual(observed['flags'], '-trimpath')
        self.assertEqual(observed['moduleCache'], str(self.foreign / 'mod'))
        self.assertEqual((self.root / 'evidence/logs/00.log').read_text().strip(), '-trimpath')
        self.assertTrue(report['cleanup']['ownedTemporaryDirectoryRemoved'])

    def test_cold_cleanup_error_still_fails_successful_producer(self):
        status, report, _ = self.lane(cleanup_error=True)
        self.assertEqual(status, 1)
        self.assertEqual(report['steps'][0]['exitCode'], 0)
        self.assertEqual(report['cleanup']['errors'], [
            {'stage': 'temporary-directory', 'error': 'PermissionError'}])
        self.assertFalse(report['cleanup']['ownedTemporaryDirectoryRemoved'])


class JobLogConsistencyTests(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory(prefix='ag-job-log-consistency-')
        self.addCleanup(self.directory.cleanup)
        self.output = Path(self.directory.name)
        self.env = {'GITHUB_REPOSITORY': 'synthetic/repository', 'GITHUB_RUN_ID': '123',
                    'GITHUB_RUN_ATTEMPT': '2', 'GITHUB_SHA': '1' * 40,
                    'GITHUB_EVENT_NAME': 'push',
                    'GITHUB_TOKEN': 'synthetic-token-not-recorded'}
        self.job = {'id': 42, 'name': 'contract-core / contract/core',
                    'run_id': 123, 'run_attempt': 2, 'head_sha': self.env['GITHUB_SHA'],
                    'status': 'completed', 'conclusion': 'success'}
        self.log = b'Set up job\nOriginal producer output\nComplete job\n'
        self.elapsed = 0
        self.calls = []
        self.sleeps = []
        self.request_elapsed = {}
        self.log_receipt = None

    def collect(self, snapshots, logs, prefixes=('contract',), lanes=('core',), receipts=None):
        calls = {'metadata': 0, 'log': 0}
        class Response:
            def __init__(self, body, receipt=None):
                self.body = body
                receipt = receipt or {}
                self.status = receipt.get('status')
                self.url = receipt.get('url', '')
                self.headers = Message()
                for key, value in receipt.get('headers', {}).items():
                    self.headers[key] = value
            def geturl(self):
                return self.url
            def __enter__(self):
                return self
            def __exit__(self, *_):
                return False
            def read(self):
                return self.body
        def open_response(request, *, timeout):
            endpoint = 'metadata' if '/jobs?filter=latest&' in request.full_url else 'log'
            self.calls.append({'endpoint': endpoint, 'timeout': timeout})
            self.elapsed += self.request_elapsed.get(endpoint, 0)
            values = snapshots if endpoint == 'metadata' else logs
            value = values[min(calls[endpoint], len(values) - 1)]
            calls[endpoint] += 1
            if isinstance(value, Exception):
                raise value
            receipt = None
            if endpoint == 'log':
                receipt = (receipts[min(calls[endpoint] - 1, len(receipts) - 1)]
                           if receipts is not None else self.log_receipt)
            return Response(json.dumps({'jobs': value}).encode() if endpoint == 'metadata' else value, receipt)
        def sleep(seconds):
            self.assertGreater(seconds, 0)
            self.sleeps.append(seconds)
            self.elapsed += seconds
        class Opener:
            open = staticmethod(open_response)
        with patch.dict(os.environ, self.env), \
                patch.object(ci.urllib.request, 'build_opener', return_value=Opener()), \
                patch.object(ci.time, 'monotonic', side_effect=lambda: self.elapsed), \
                patch.object(ci.time, 'sleep', side_effect=sleep):
            return ci.collect_job_logs(self.output, prefixes=prefixes, lanes=lanes)

    def manifest(self):
        return json.loads((self.output / 'job-log-manifest.json').read_text())

    def poll_readbacks(self):
        return [json.loads(path.read_text())
                for path in sorted((self.output / 'job-log-polls').glob('*-readback.json'))]

    def scheduler_completed_job(self, lane='core'):
        self.env['CI_CONTRACT_NEEDS'] = json.dumps({
            'contract-core': {'result': 'success'}, 'contract-native': {'result': 'success'}})
        actions = {'checkout': 'actions/checkout@' + 'a' * 40,
                   'go': 'actions/setup-go@' + 'b' * 40,
                   'rust': 'dtolnay/rust-toolchain@' + 'c' * 40,
                   'node': 'actions/setup-node@' + 'd' * 40}
        names = ['Set up job', 'Initialize failure evidence before checkout or tool setup',
                 'Run ' + actions['checkout'], 'Run ' + actions['go'], 'Run ' + actions['rust'],
                 'Run ' + actions['node'], 'Verify public npm lockfile sources before install',
                 'Install OpenAPI tooling', 'Install Console code generation dependencies',
                 'Run unchanged lane targets and preserve raw evidence',
                 'Preserve lane evidence including bootstrap failures',
                 'Post Run ' + actions['node'], 'Post Run ' + actions['go'],
                 'Post Run ' + actions['checkout'], 'Complete job']
        steps = []
        for index, name in enumerate(names, 1):
            skipped = ((index == 5 and lane not in ('core', 'six-format-auth'))
                       or (index in (7, 8, 9) and lane != 'core'))
            steps.append({'name': name, 'number': index, 'status': 'completed',
                          'conclusion': 'skipped' if skipped else 'success',
                          'started_at': f'2026-10-22T00:00:{index:02d}Z',
                          'completed_at': f'2026-10-22T00:00:{index:02d}Z'})
        group = 'contract-core' if lane == 'core' else f'contract-native ({lane})'
        job = {**self.job, 'name': f'{group} / contract/{lane}',
               'status': 'in_progress', 'conclusion': None, 'completed_at': None,
               'started_at': '2026-10-22T00:00:00Z', 'steps': steps}
        self.log = (b'\xef\xbb\xbf2026-10-22T00:00:01.1000000Z Current runner version: \'2.338.0\'\n'
                    + b''.join(f'2026-10-22T00:00:{index:02d}.1000000Z Step original output\n'.encode()
                               for index in range(2, 12)
                               if steps[index - 1]['conclusion'] != 'skipped')
                    + b'2026-10-22T00:00:12.1000000Z Post job cleanup.\n'
                    b'2026-10-22T00:00:13.1000000Z Post job cleanup.\n'
                    b'2026-10-22T00:00:14.1000000Z Post job cleanup.\n'
                    b'2026-10-22T00:00:15.1000000Z Cleaning up orphan processes\n'
                    b'2026-10-22T00:00:15.2000000Z ##[warning]Runner warning after cleanup\n')
        self.log_receipt = self.final_log_receipt(self.log)
        return job

    def final_log_receipt(self, body):
        return {'status': 200,
                'url': 'https://synthetic.blob.core.windows.net/job-log?sig=must-not-be-recorded',
                'headers': {'Content-Length': str(len(body)),
                            'Content-MD5': base64.b64encode(hashlib.md5(body, usedforsecurity=False).digest()).decode(),
                            'Content-Type': 'text/plain', 'x-ms-blob-type': 'BlockBlob',
                            'ETag': '"0x123ABC"', 'Last-Modified': 'Thu, 22 Oct 2026 00:00:17 GMT'}}

    def test_scheduler_and_complete_runner_log_collect_despite_stale_outer_status(self):
        stale = self.scheduler_completed_job()
        self.assertEqual(self.collect([[stale]], [self.log]), [stale])
        self.assertEqual(json.loads((self.output / 'jobs.json').read_text()), [stale])
        self.assertEqual((self.output / 'job-42.log').read_bytes(), self.log)
        self.assertEqual(self.elapsed, 0)
        self.assertEqual(self.sleeps, [])
        manifest = self.manifest()
        self.assertTrue(manifest['complete'])
        witness = manifest['completionEvidence']['42']
        self.assertEqual(witness['source'], 'github-needs-and-runner-completion')
        self.assertEqual(witness['schedulerJob'], 'contract-core')
        self.assertEqual(witness['runnerCompleteStep'], stale['steps'][-1])
        self.assertEqual(witness['sealedLogObject']['runnerVersion'], '2.338.0')
        self.assertEqual(witness['sealedLogObject']['verifiedBytes'], len(self.log))
        self.assertEqual(len(witness['coveredRunnerSteps']), len(stale['steps']))
        self.assertNotIn('must-not-be-recorded', ''.join(path.read_text()
                         for path in self.output.rglob('*.json')))

    def test_provider_receipt_rejects_a_true_prefix_truncation_after_machine_cleanup(self):
        job = self.scheduler_completed_job()
        body = self.log.rsplit(b'2026-10-22T00:00:15.2000000Z', 1)[0]
        with self.assertRaises(ValueError):
            self.collect([[job]], [body])
        self.assertFalse(self.manifest()['complete'])
        self.assertEqual(self.manifest()['completionEvidence'], {})
        self.assertFalse((self.output / 'job-42.log').exists())

    def test_provider_receipt_rejects_log_deletions_preserving_all_completion_markers(self):
        job = self.scheduler_completed_job()
        lines = self.log.splitlines(keepends=True)
        body = b''.join(line for index, line in enumerate(lines)
                        if index == 0 or b'Post job cleanup.' in line
                        or b'Cleaning up orphan processes' in line or index == len(lines) - 1)
        with self.assertRaises(ValueError):
            self.collect([[job]], [body])
        self.assertFalse(self.manifest()['complete'])
        self.assertEqual(self.manifest()['completionEvidence'], {})

    def test_stale_completion_requires_every_independent_final_object_property(self):
        job = self.scheduler_completed_job()
        good = self.log_receipt
        invalid = [None]
        for key in good['headers']:
            receipt = copy.deepcopy(good)
            receipt['headers'].pop(key)
            invalid.append(receipt)
        for key, value in (('Content-Length', str(len(self.log) - 1)),
                           ('Content-Length', '0'), ('Content-MD5', 'invalid base64'),
                           ('Content-MD5', base64.b64encode(b'0' * 16).decode()),
                           ('ETag', 'W/"weak"'), ('x-ms-blob-type', 'AppendBlob'),
                           ('Content-Type', 'application/zip'), ('Content-Encoding', 'gzip'),
                           ('Content-Encoding', 'identity'), ('Transfer-Encoding', 'chunked'),
                           ('Content-Range', 'bytes 0-100/200'),
                           ('Last-Modified', 'Thu, 22 Oct 2026 00:00:14 GMT'),
                           ('Last-Modified', 'Fri, 23 Oct 2099 00:00:17 GMT'),
                           ('Last-Modified', 'invalid date')):
            receipt = copy.deepcopy(good)
            receipt['headers'][key] = value
            invalid.append(receipt)
        for status in (206, 302):
            invalid.append({**good, 'status': status})
        for index, receipt in enumerate(invalid):
            with self.subTest(case=index), self.assertRaises(ValueError):
                self.collect([[job]], [self.log], receipts=[receipt])
            self.assertFalse(self.manifest()['complete'])
            self.assertEqual(self.manifest()['completionEvidence'], {})
            self.assertFalse((self.output / 'job-42.log').exists())
        readback = self.poll_readbacks()[0]['logReads'][0]
        self.assertEqual((self.output / readback['rawLog']).read_bytes(), self.log)
        self.assertTrue((self.output / readback['responseReceipt']).is_file())

    def test_final_log_object_must_come_from_the_bound_https_job_redirect(self):
        job = self.scheduler_completed_job()
        invalid = ['http://synthetic.blob.core.windows.net/job-log',
                   'https://api.github.com/job-log', 'https://untrusted.example/job-log',
                   'https://synthetic.blob.core.windows.net.untrusted.example/job-log',
                   'https://user:must-not-be-recorded@synthetic.blob.core.windows.net/job-log',
                   'https://synthetic.blob.core.windows.net:444/job-log',
                   'https://synthetic.blob.core.windows.net:invalid/job-log',
                   'https://synthetic.blob.core.windows.net/job-log#fragment']
        for url in invalid:
            with self.subTest(url=url), self.assertRaises(ValueError):
                self.collect([[job]], [self.log], receipts=[{**self.log_receipt, 'url': url}])
            self.assertFalse(self.manifest()['complete'])
        for api in ('http://api.github.com', 'https://untrusted.example',
                    'https://api.github.com/artifact-api'):
            self.env['GITHUB_API_URL'] = api
            with self.subTest(api=api), self.assertRaises(ValueError):
                self.collect([[job]], [self.log])
            self.assertFalse(self.manifest()['complete'])
        self.assertNotIn('must-not-be-recorded', ''.join(path.read_text()
                         for path in self.output.rglob('*.json')))

    def test_self_consistent_transport_still_requires_all_executed_step_coverage(self):
        job = self.scheduler_completed_job()
        body = self.log.replace(b'2026-10-22T00:00:05.1000000Z Step original output\n', b'')
        with self.assertRaises(ValueError):
            self.collect([[job]], [body], receipts=[self.final_log_receipt(body)])
        self.assertFalse(self.manifest()['complete'])
        self.assertEqual(self.manifest()['completionEvidence'], {})

    def test_unknown_runner_protocol_waits_for_original_rest_terminal_metadata(self):
        job = self.scheduler_completed_job()
        body = self.log.replace(b"version: '2.338.0'", b"version: '2.338.1'")
        with self.assertRaises(ValueError):
            self.collect([[job]], [body], receipts=[self.final_log_receipt(body)])
        self.assertFalse(self.manifest()['complete'])
        terminal = {**job, 'status': 'completed', 'conclusion': 'success'}
        self.assertEqual(self.collect([[terminal]], [body], receipts=[None]), [terminal])
        self.assertTrue(self.manifest()['complete'])
        self.assertEqual(self.manifest()['completionEvidence'], {})

    def test_scheduler_completion_accepts_only_existing_conditional_native_skips(self):
        for lane in ci.PLAN['nativeOrder']:
            with self.subTest(lane=lane):
                stale = self.scheduler_completed_job(lane)
                self.assertEqual(self.collect([[stale]], [self.log], lanes=(lane,)), [stale])
                self.assertEqual(self.manifest()['completionEvidence']['42']['schedulerJob'],
                                 'contract-native')

    def test_scheduler_completion_requires_exact_successful_needs(self):
        invalid = [None, {}, {'contract-core': {'result': 'success'}},
                   {'contract-native': {'result': 'success'}}, [], 'invalid JSON']
        good = {'contract-core': {'result': 'success'}, 'contract-native': {'result': 'success'}}
        invalid.append({**good, 'other': {'result': 'success'}})
        for result in ('failure', 'cancelled', 'skipped', None):
            invalid.append({**good, 'contract-native': {'result': result}})
        for needs in invalid:
            with self.subTest(needs=needs):
                stale = self.scheduler_completed_job()
                if needs is None:
                    self.env.pop('CI_CONTRACT_NEEDS')
                else:
                    self.env['CI_CONTRACT_NEEDS'] = (needs if isinstance(needs, str)
                                                     else json.dumps(needs))
                with self.assertRaises(ValueError):
                    self.collect([[stale]], [self.log])
                self.assertFalse(self.manifest()['complete'])
                self.assertFalse(any(c['endpoint'] == 'log' for c in self.calls))

    def test_scheduler_success_cannot_override_a_terminal_failure_skip_or_cancellation(self):
        for result in ('failure', 'cancelled', 'skipped', 'timed_out', 'neutral'):
            with self.subTest(result=result):
                job = self.scheduler_completed_job()
                job.update(status='completed', conclusion=result)
                with self.assertRaisesRegex(ValueError, 'contradict'):
                    self.collect([[job]], [self.log])
                self.assertFalse(self.manifest()['complete'])
                self.assertFalse(any(c['endpoint'] == 'log' for c in self.calls))

    def test_scheduler_completion_requires_the_entire_successful_machine_step_plan(self):
        good = self.scheduler_completed_job()
        invalid = []
        for index in range(len(good['steps'])):
            job = copy.deepcopy(good)
            job['steps'].pop(index)
            invalid.append(job)
        for change in ({'status': 'in_progress'}, {'conclusion': 'failure'},
                       {'conclusion': 'skipped'}, {'conclusion': 'cancelled'},
                       {'number': True}, {'number': 1}, {'started_at': None},
                       {'started_at': '2026-10-22T00:00:15'},
                       {'completed_at': '2026-10-22T00:00:14Z'}):
            job = copy.deepcopy(good)
            job['steps'][-1].update(change)
            invalid.append(job)
        for index in (4, 9, 10, 11, 12, 13):
            job = copy.deepcopy(good)
            job['steps'][index]['conclusion'] = 'skipped'
            invalid.append(job)
        job = copy.deepcopy(good)
        job['steps'][11]['name'] = 'Post Run actions/setup-node@' + 'e' * 40
        invalid.append(job)
        job = copy.deepcopy(good)
        job['steps'].append({**job['steps'][-1], 'name': 'Unexpected step after completion', 'number': 99})
        invalid.append(job)
        for index, job in enumerate(invalid):
            with self.subTest(case=index), self.assertRaises(ValueError):
                self.collect([[job]], [self.log])
            self.assertFalse(self.manifest()['complete'])
        self.assertFalse(any(c['endpoint'] == 'log' for c in self.calls))

    def test_scheduler_completion_rejects_partial_logs_and_stdout_completion_substrings(self):
        job = self.scheduler_completed_job()
        good = self.log
        lines = good.splitlines(keepends=True)
        invalid = [b' \n', b'ordinary output says Cleaning up orphan processes\n',
                   b''.join(lines[1:]), b''.join(lines[:5]), b''.join(lines[:3] + lines[4:]),
                   good[:-1], good + b'\xff\n',
                   good.replace(b'Z Cleaning up orphan processes', b'Z stdout: Cleaning up orphan processes'),
                   good.replace(b'00:00:15.1000000Z Cleaning', b'00:00:10.1000000Z Cleaning'),
                   good.replace(b'00:00:15.1000000Z Cleaning', b'00:00:16.1000000Z Cleaning'),
                   b''.join(lines[:4] + lines[5:6] + lines[4:5] + lines[6:]),
                   good + lines[5], good.replace(lines[2], lines[2] + lines[2])]
        for index, log in enumerate(invalid):
            with self.subTest(case=index), self.assertRaises(ValueError):
                self.collect([[job]], [log])
            self.assertFalse(self.manifest()['complete'])
            self.assertEqual(self.manifest()['completionEvidence'], {})
            self.assertFalse((self.output / 'job-42.log').exists())
        self.assertTrue(any((self.output / 'job-log-polls').glob('*-job-42.log')))

    def test_partial_runner_log_is_preserved_and_polled_until_original_completion(self):
        job = self.scheduler_completed_job()
        partial = self.log.split(b'2026-10-22T00:00:15.', 1)[0]
        self.assertEqual(self.collect([[job]], [partial, self.log]), [job])
        readbacks = self.poll_readbacks()
        self.assertEqual(readbacks[0]['logReads'][0]['reason'], 'incomplete_runner_log')
        self.assertEqual(readbacks[1]['logReads'][0]['status'], 'ready')
        self.assertEqual((self.output / readbacks[0]['logReads'][0]['rawLog']).read_bytes(), partial)
        self.assertEqual((self.output / 'job-42.log').read_bytes(), self.log)
        self.assertEqual(self.elapsed, 2)
        self.assertTrue(self.manifest()['complete'])

    def test_scheduler_completion_never_applies_to_retained_or_benchmark_jobs(self):
        job = self.scheduler_completed_job()
        with self.assertRaisesRegex(ValueError, 'retained job source binding'):
            self.collect([[{**job, 'run_attempt': 1}]], [self.log])
        job['name'] = 'serial (core) / benchmark-serial/core'
        with self.assertRaisesRegex(ValueError, 'deadline'):
            self.collect([[job]], [self.log], prefixes=('benchmark-serial',))
        self.assertFalse(any(c['endpoint'] == 'log' for c in self.calls))
        self.assertFalse(self.manifest()['complete'])

    def test_scheduler_completion_requires_a_running_job_in_the_exact_scheduler_group(self):
        good = self.scheduler_completed_job()
        for change in ({'status': 'queued'}, {'status': 'waiting'}, {'status': 'pending'},
                       {'status': 'requested'}, {'status': 'completed'},
                       {'name': 'unrelated / contract/core'},
                       {'completed_at': '2026-10-22T00:00:15Z'}):
            with self.subTest(change=change), self.assertRaisesRegex(ValueError, 'deadline'):
                self.collect([[{**good, **change}]], [self.log])
            self.assertFalse(self.manifest()['complete'])
        self.assertFalse(any(c['endpoint'] == 'log' for c in self.calls))

    def test_scheduler_completion_cannot_replace_missing_duplicate_or_changed_job_identities(self):
        good = self.scheduler_completed_job()
        for jobs in ([], [good, good]):
            with self.subTest(jobs=len(jobs)), self.assertRaises(ValueError):
                self.collect([jobs], [self.log])
            self.assertFalse(self.manifest()['complete'])
        unavailable = ci.urllib.error.HTTPError('https://api.github.com/synthetic/logs', 404,
                                               'not ready', {}, None)
        for change in ({'id': 43}, {'head_sha': '0' * 40}, {'run_id': 456}, {'run_attempt': 1}):
            with self.subTest(change=change), self.assertRaisesRegex(ValueError, 'binding|identity'):
                self.collect([[good], [{**good, **change}]], [unavailable, self.log])
            self.assertFalse(self.manifest()['complete'])
            self.assertEqual(self.manifest()['completionEvidence'], {})

    def test_scheduler_completion_still_obeys_the_original_request_deadline(self):
        job = self.scheduler_completed_job()
        self.request_elapsed = {'metadata': 119, 'log': 2}
        with self.assertRaisesRegex(ValueError, 'deadline'):
            self.collect([[job]], [self.log])
        self.assertEqual([call['timeout'] for call in self.calls], [45, 1])
        self.assertFalse(self.manifest()['complete'])
        self.assertFalse((self.output / 'job-42.log').exists())

    def test_runner_proof_validation_is_inside_the_same_collection_deadline(self):
        job = self.scheduler_completed_job()
        self.request_elapsed = {'metadata': 119}
        validate = ci.runner_log_completion

        def delayed_validation(*args):
            result = validate(*args)
            self.elapsed += 2
            return result

        with patch.object(ci, 'runner_log_completion', side_effect=delayed_validation):
            with self.assertRaisesRegex(ValueError, 'deadline'):
                self.collect([[job]], [self.log])
        self.assertFalse(self.manifest()['complete'])

    def test_failed_scheduler_dependencies_keep_terminal_failure_logs_for_diagnosis(self):
        job = self.scheduler_completed_job()
        job.update(status='completed', conclusion='failure')
        needs = {'contract-core': {'result': 'failure'}, 'contract-native': {'result': 'success'}}
        self.env['CI_CONTRACT_NEEDS'] = json.dumps(needs)
        self.assertEqual(self.collect([[job]], [self.log]), [job])
        self.assertEqual((self.output / 'job-42.log').read_bytes(), self.log)
        self.assertEqual(self.manifest()['completionEvidence'], {})
        with self.assertRaisesRegex(ValueError, 'required job failed'):
            ci.validate_needs(needs, ['contract-core', 'contract-native'])

    def test_successful_steps_with_stale_job_status_are_read_again(self):
        stale = {**self.job, 'status': 'in_progress', 'conclusion': None,
                 'steps': [{'name': 'Complete job', 'status': 'completed', 'conclusion': 'success'}]}
        jobs = self.collect([[stale], [self.job]], [self.log])
        self.assertEqual(jobs, [self.job])
        self.assertEqual((self.output / 'job-42.log').read_bytes(), self.log)
        snapshots = sorted((self.output / 'job-log-polls').glob('*-jobs.json'))
        self.assertEqual([json.loads(path.read_text())[0]['status'] for path in snapshots],
                         ['in_progress', 'completed'])
        self.assertEqual(self.manifest()['missing'], [])
        self.assertTrue(self.manifest()['complete'])

    def test_permanently_stale_metadata_stops_at_the_collection_deadline(self):
        stale = {**self.job, 'status': 'in_progress', 'conclusion': None}
        with self.assertRaisesRegex(ValueError, 'deadline'):
            self.collect([[stale]], [self.log])
        self.assertEqual(self.elapsed, 120)
        self.assertLessEqual(len(self.sleeps), 60)
        self.assertFalse(self.manifest()['complete'])
        self.assertEqual(self.manifest()['missing'], ['contract/core'])
        self.assertFalse((self.output / 'job-42.log').exists())
        self.assertGreater(len(self.poll_readbacks()), 1)

    def test_job_log_not_ready_404_is_read_again_without_rerunning_a_job(self):
        unavailable = ci.urllib.error.HTTPError('https://api.github.com/synthetic/logs', 404,
                                               'not ready', {}, None)
        self.collect([[self.job]], [unavailable, self.log])
        self.assertEqual((self.output / 'job-42.log').read_bytes(), self.log)
        self.assertEqual([r['logReads'][0]['status'] for r in self.poll_readbacks()],
                         ['not_ready', 'ready'])
        self.assertEqual(self.poll_readbacks()[0]['logReads'][0]['httpStatus'], 404)
        self.assertTrue(self.manifest()['complete'])

    def test_log_forbidden_fails_immediately_and_diagnostics_omit_credentials(self):
        forbidden = ci.urllib.error.HTTPError('https://api.github.com/synthetic/logs', 403,
                                             'forbidden', {}, None)
        with self.assertRaises(ci.urllib.error.HTTPError) as raised:
            self.collect([[self.job]], [forbidden, self.log])
        self.assertEqual(raised.exception.code, 403)
        self.assertEqual(self.sleeps, [])
        self.assertFalse(self.manifest()['complete'])
        self.assertEqual(self.poll_readbacks()[-1]['error']['httpStatus'], 403)
        self.assertNotIn(self.env['GITHUB_TOKEN'], ''.join(
            path.read_text() for path in self.output.rglob('*.json')))

    def test_metadata_api_errors_are_not_polled_away(self):
        for code in (403, 500):
            with self.subTest(status=code):
                error = ci.urllib.error.HTTPError('https://api.github.com/synthetic/jobs', code,
                                                 'unavailable', {}, None)
                with self.assertRaises(ci.urllib.error.HTTPError):
                    self.collect([error, [self.job]], [self.log])
                self.assertEqual(self.sleeps, [])
                self.assertFalse(self.manifest()['complete'])
                self.assertEqual(self.poll_readbacks()[-1]['error']['httpStatus'], code)

    def test_unavailable_logs_never_satisfy_complete_collection(self):
        unavailable = ci.urllib.error.HTTPError('https://api.github.com/synthetic/logs', 404,
                                               'not ready', {}, None)
        with self.assertRaisesRegex(ValueError, 'deadline'):
            self.collect([[self.job]], [unavailable])
        self.assertEqual(self.elapsed, 120)
        self.assertEqual(self.manifest()['missingLogs'], ['contract/core'])
        self.assertFalse(self.manifest()['complete'])
        self.assertFalse((self.output / 'job-42.log').exists())

    def test_empty_log_is_not_ready_evidence(self):
        self.collect([[self.job]], [b' \n', self.log])
        self.assertEqual((self.output / 'job-42.log').read_bytes(), self.log)
        self.assertEqual(self.poll_readbacks()[0]['logReads'][0]['reason'], 'empty_log')
        self.assertTrue(self.manifest()['complete'])

    def test_wrong_source_run_or_attempt_is_rejected_before_log_download(self):
        for change in ({'head_sha': '0' * 40}, {'run_id': 456}, {'run_attempt': 3}):
            with self.subTest(change=change), self.assertRaisesRegex(ValueError, 'binding'):
                self.collect([[{**self.job, **change}]], [self.log])
            self.assertFalse(self.manifest()['complete'])
            self.assertFalse(any(call['endpoint'] == 'log' for call in self.calls))

    def test_settling_cannot_switch_to_another_same_named_job(self):
        stale = {**self.job, 'status': 'in_progress', 'conclusion': None}
        with self.assertRaisesRegex(ValueError, 'identity'):
            self.collect([[stale], [{**self.job, 'id': 43}]], [self.log])
        self.assertFalse(self.manifest()['complete'])
        self.assertFalse(any(call['endpoint'] == 'log' for call in self.calls))

    def test_source_run_and_observed_attempt_cannot_drift_between_polls(self):
        stale = {**self.job, 'status': 'in_progress', 'conclusion': None}
        for change in ({'head_sha': '0' * 40}, {'run_id': 456}, {'run_attempt': 1}):
            with self.subTest(change=change), self.assertRaisesRegex(ValueError, 'binding|identity'):
                self.collect([[stale], [{**self.job, **change}]], [self.log])
            self.assertFalse(self.manifest()['complete'])
            self.assertFalse(any(call['endpoint'] == 'log' for call in self.calls))

    def test_each_request_uses_remaining_budget_and_late_success_still_fails(self):
        self.request_elapsed = {'metadata': 119, 'log': 2}
        with self.assertRaisesRegex(ValueError, 'deadline'):
            self.collect([[self.job]], [self.log])
        self.assertEqual([call['timeout'] for call in self.calls], [45, 1])
        self.assertFalse(self.manifest()['complete'])
        self.assertFalse((self.output / 'job-42.log').exists())

    def test_completed_failed_job_keeps_its_original_result_and_log(self):
        failed = {**self.job, 'conclusion': 'failure'}
        self.assertEqual(self.collect([[failed]], [self.log]), [failed])
        self.assertEqual(json.loads((self.output / 'jobs.json').read_text()), [failed])
        self.assertEqual((self.output / 'job-42.log').read_bytes(), self.log)
        self.assertTrue(self.manifest()['complete'])

    def test_retained_successful_attempt_keeps_its_provenance(self):
        retained = {**self.job, 'run_attempt': 1}
        self.assertEqual(self.collect([[retained]], [self.log]), [retained])
        self.assertEqual(self.manifest()['selectedAttempts'], {'42': 1})
        self.assertTrue(self.manifest()['complete'])

    def test_previous_failed_attempt_cannot_satisfy_the_latest_execution(self):
        retained = {**self.job, 'run_attempt': 1, 'conclusion': 'failure'}
        with self.assertRaisesRegex(ValueError, 'retained job source binding'):
            self.collect([[retained]], [self.log])
        self.assertFalse(self.manifest()['complete'])
        self.assertFalse(any(call['endpoint'] == 'log' for call in self.calls))

    def test_pull_request_api_head_is_bound_separately_from_the_merge_commit(self):
        payload = {'repository': {'full_name': self.env['GITHUB_REPOSITORY']},
                   'pull_request': {'head': {'sha': self.job['head_sha']}}}
        event = self.output / 'event.json'
        ci.write(event, payload)
        self.env.update(GITHUB_EVENT_NAME='pull_request', GITHUB_EVENT_PATH=str(event),
                        GITHUB_SHA='2' * 40)
        self.assertEqual(self.collect([[self.job]], [self.log]), [self.job])
        self.assertEqual(self.manifest()['head'], '2' * 40)
        self.assertEqual(self.manifest()['apiHead'], self.job['head_sha'])
        self.assertTrue(self.manifest()['complete'])

    def test_pull_request_event_binding_cannot_be_missing_or_point_to_another_source(self):
        event = self.output / 'event.json'
        self.env.update(GITHUB_EVENT_NAME='pull_request', GITHUB_EVENT_PATH=str(event),
                        GITHUB_SHA='2' * 40)
        for payload in ({}, {'repository': {'full_name': 'other/repository'},
                             'pull_request': {'head': {'sha': self.job['head_sha']}}},
                        {'repository': {'full_name': self.env['GITHUB_REPOSITORY']},
                         'pull_request': {'head': {'sha': '3' * 40}}}):
            with self.subTest(payload=payload):
                ci.write(event, payload)
                with self.assertRaisesRegex(ValueError, 'binding'):
                    self.collect([[self.job]], [self.log])
                self.assertFalse(self.manifest()['complete'])
                self.assertFalse(any(call['endpoint'] == 'log' for call in self.calls))


if __name__ == '__main__':
    unittest.main()
