"""Exercise native producer startup and cancellation without Docker or networking."""

import ast
import hashlib
import json
import os
from pathlib import Path
import re
import shlex
import shutil
import signal
import subprocess
import sys
import tempfile
import time
from types import SimpleNamespace
import unittest


SCRIPT = Path(__file__).resolve().parents[1] / "native-rubygems-e2e.sh"
ROOT = SCRIPT.parents[1]


def producer_ast(path):
    source = path.read_text().partition("<<'PY'\n")[2].rsplit("\nPY", 1)[0]
    return ast.parse(source, filename=str(path))


class NativeProducerStartupTest(unittest.TestCase):
    def postgres_loop(self, script):
        return next(node for node in ast.walk(producer_ast(script))
                    if isinstance(node, ast.While) and any(
                        isinstance(value, ast.Constant) and value.value in ("pg_isready", "SELECT 1")
                        for value in ast.walk(node)))

    def test_postgres_probes_wait_for_authenticated_tcp_query(self):
        for name in ("native-rubygems-e2e.sh", "native-helm-e2e.sh"):
            with self.subTest(producer=name):
                script = SCRIPT.parent / name
                # Execute the actual producer loop, preserving its original
                # deadline/interval. Model the official entrypoint's Unix-only
                # init phase, then TCP rejection, then a usable database.
                loop = self.postgres_loop(script)
                phase = 0
                attempts = []

                def query(args, **_kwargs):
                    attempts.append(args)
                    if "pg_isready" in args and "-h" not in args:
                        return subprocess.CompletedProcess(args, 0, "Unix socket accepting connections\n", "")
                    expected = {"-h": "127.0.0.1", "-U": "gateway", "-d": "gateway_test"}
                    authenticated_tcp = "psql" in args and all(
                        flag in args and args[args.index(flag) + 1] == value
                        for flag, value in expected.items())
                    if authenticated_tcp and "SELECT 1" in args and phase >= 2:
                        return subprocess.CompletedProcess(args, 0, "1\n", "")
                    return subprocess.CompletedProcess(args, 2, "", "TCP connection or authentication rejected\n")

                def sleep(_seconds):
                    nonlocal phase
                    phase += 1

                scope = {"command": query, "pg": "owned-postgres", "deadline": 10,
                         "time": SimpleNamespace(monotonic=lambda: phase, sleep=sleep)}
                exec(compile(ast.Module(body=[loop], type_ignores=[]), str(script), "exec"), scope)
                self.assertGreaterEqual(phase, 2, "producer accepted the temporary Unix-only init server")
                self.assertGreaterEqual(len(attempts), 3)

    def test_compose_postgres_healthchecks_reject_unix_only_server(self):
        for name, database in (("compose.integration.yml", "gateway_test"), ("compose.yml", "gateway")):
            with self.subTest(compose=name):
                text = (ROOT / name).read_text()
                healthcheck = re.search(r'test: (\["CMD-SHELL", "pg_isready[^\n]+\])', text)
                self.assertIsNotNone(healthcheck)
                args = shlex.split(json.loads(healthcheck.group(1))[1])
                # pg_isready defaults to a Unix socket; only an explicit host
                # observes the TCP server used by dependent migrate services.
                self.assertIn("-h", args, "healthcheck accepts the Unix-only init server")
                self.assertEqual(args[args.index("-h") + 1], "127.0.0.1")
                self.assertEqual(args[args.index("-U") + 1], "gateway")
                self.assertEqual(args[args.index("-d") + 1], database)

    def command_scope(self, evidence, script=SCRIPT):
        functions = [node for node in producer_ast(script).body
                     if isinstance(node, ast.FunctionDef) and node.name in ("stop_process_group", "command")]
        scope = {"os": os, "signal": signal, "subprocess": subprocess, "time": time,
                 "env": dict(os.environ), "evidence": evidence, "cleaning": False,
                 "gate_signals": {signal.SIGTERM, signal.SIGINT}, "active_processes": {},
                 "process_receipts": [], "bounded_process_receipts": [], "cleanup_errors": []}
        scope["write_json"] = lambda name, value: (evidence / name).write_text(json.dumps(value) + "\n")
        exec(compile(ast.Module(body=functions, type_ignores=[]), str(script), "exec"), scope)
        return scope

    def test_blocked_postgres_probe_times_out_and_reaps_owned_process_group(self):
        for name, blocked in ((name, blocked) for name in ("native-rubygems-e2e.sh", "native-helm-e2e.sh") for blocked in (False, True)):
            with self.subTest(producer=name, blockedSignalMask=blocked), tempfile.TemporaryDirectory(prefix="native-postgres-watchdog-") as directory:
                evidence = Path(directory)
                docker = evidence / "docker"
                # Exercise each actual command helper with a real, owned
                # process group. Both the CLI and its child would outlive the
                # readiness deadline if only the outer while were bounded.
                docker.write_text("#!" + sys.executable + "\n" + r'''
import json,os,pathlib,signal,subprocess,sys,time
root=pathlib.Path(os.environ['WATCHDOG_TEST_ROOT'])
# Docker's Go runtime handles its own signal mask. Give this Python stand-in
# that TERM path, and separately test an intentionally blocked mask so the
# producer's bounded TERM-to-KILL cleanup is also exercised.
blocked=os.environ['WATCHDOG_KEEP_MASK']=='1'
signal.pthread_sigmask(signal.SIG_BLOCK if blocked else signal.SIG_UNBLOCK,{signal.SIGTERM,signal.SIGINT})
child=subprocess.Popen([sys.executable,'-c','import time;time.sleep(10)'])
(root/'children.json').write_text(json.dumps([os.getpid(),child.pid]))
def stop(_signum,_frame):
    child.wait(timeout=2)
    raise SystemExit(143)
signal.signal(signal.SIGTERM,stop)
time.sleep(3.5 if blocked else .6)
child.kill();child.wait(timeout=2)
print('1')
''')
                docker.chmod(0o700)
                script = SCRIPT.parent / name
                scope = self.command_scope(evidence, script)
                scope["env"].update(PATH=directory + os.pathsep + os.environ["PATH"], WATCHDOG_TEST_ROOT=directory,
                                    WATCHDOG_KEEP_MASK="1" if blocked else "0")
                scope.update(pg="owned-postgres", deadline=time.monotonic() + 0.3)
                started = time.monotonic()
                try:
                    with self.assertRaises(subprocess.TimeoutExpired) as raised:
                        exec(compile(ast.Module(body=[self.postgres_loop(script)], type_ignores=[]), str(script), "exec"), scope)
                finally:
                    children_path = evidence / "children.json"
                    children = json.loads(children_path.read_text()) if children_path.exists() else []
                    try:
                        for pid in children:
                            state = subprocess.run(["ps", "-o", "stat=", "-p", str(pid)], capture_output=True, text=True).stdout.strip()
                            reap_deadline = time.monotonic() + 0.5
                            while state.startswith(("Z", "X")) and time.monotonic() < reap_deadline:
                                time.sleep(0.01)
                                state = subprocess.run(["ps", "-o", "stat=", "-p", str(pid)], capture_output=True, text=True).stdout.strip()
                            self.assertFalse(state and not state.startswith(("Z", "X")), f"owned readiness process still running: {pid}")
                            self.assertFalse(state, f"owned readiness process was not reaped: {pid}")
                    finally:
                        if children:
                            try: os.killpg(children[0], signal.SIGKILL)
                            except ProcessLookupError: pass
                self.assertTrue(children, "blocking command did not reach its owned child boundary")
                duration = time.monotonic() - started
                self.assertLess(duration, 4 if blocked else 2)
                self.assertGreater(raised.exception.timeout, 0)
                self.assertLessEqual(raised.exception.timeout, 0.3)
                receipts = scope["process_receipts"] or scope["bounded_process_receipts"]
                self.assertTrue(receipts[-1]["waitReturned"])
                self.assertTrue(receipts[-1]["processGroupStopped"])
                report = {"producer": name, "blockedSignalMask": blocked, "exceptionType": type(raised.exception).__name__,
                          "timeoutSeconds": raised.exception.timeout, "durationSeconds": duration,
                          "cleanupGraceSecondsPerSignal": 2,
                          "ownedChildren": children, "allOwnedChildrenStopped": True, "allOwnedChildPidsAbsent": True,
                          "processes": receipts,
                          "realDockerAndNetworkUsed": False}
                print(json.dumps(report, sort_keys=True), flush=True)
                destination = os.environ.get("RUBYGEMS_CLEANUP_TEST_EVIDENCE_DIR")
                if destination:
                    target = Path(destination)
                    target.mkdir(parents=True, exist_ok=True)
                    with (target / ("watchdog-" + ("blocked-" if blocked else "unblocked-") + name + ".json")).open("x") as output:
                        output.write(json.dumps(report, indent=2) + "\n")

    def test_postgres_probe_rejects_success_at_deadline(self):
        for name in ("native-rubygems-e2e.sh", "native-helm-e2e.sh"):
            with self.subTest(producer=name):
                script = SCRIPT.parent / name
                now = 0
                timeouts = []

                def query(args, **kwargs):
                    nonlocal now
                    timeouts.append(kwargs.get("timeout"))
                    now = 1
                    return subprocess.CompletedProcess(args, 0, "1\n", "")

                scope = {"command": query, "pg": "owned-postgres", "deadline": 1,
                         "time": SimpleNamespace(monotonic=lambda: now, sleep=lambda _seconds: None)}
                with self.assertRaises(RuntimeError):
                    exec(compile(ast.Module(body=[self.postgres_loop(script)], type_ignores=[]), str(script), "exec"), scope)
                self.assertEqual(timeouts, [1])

    def test_postgres_retry_sleep_does_not_extend_remaining_budget(self):
        for name in ("native-rubygems-e2e.sh", "native-helm-e2e.sh"):
            with self.subTest(producer=name):
                script = SCRIPT.parent / name
                now = 0
                sleeps = []

                def query(args, **_kwargs):
                    nonlocal now
                    now = 0.9
                    return subprocess.CompletedProcess(args, 2, "", "connection refused")

                def sleep(seconds):
                    nonlocal now
                    sleeps.append(seconds)
                    now += seconds

                scope = {"command": query, "pg": "owned-postgres", "deadline": 1,
                         "time": SimpleNamespace(monotonic=lambda: now, sleep=sleep)}
                with self.assertRaises(RuntimeError):
                    exec(compile(ast.Module(body=[self.postgres_loop(script)], type_ignores=[]), str(script), "exec"), scope)
                self.assertEqual(len(sleeps), 1)
                self.assertAlmostEqual(sleeps[0], 0.1)

    def test_failed_stdin_command_preserves_real_exit_and_output(self):
        with tempfile.TemporaryDirectory(prefix="native-rubygems-command-failure-") as directory:
            scope = self.command_scope(Path(directory))
            args = [sys.executable, "-c", "import sys;sys.stdin.read();print('migration TCP connection refused');sys.exit(2)"]
            with self.assertRaises(subprocess.CalledProcessError) as raised:
                scope["command"](args, input="owned migration input\n", output="migration.log")
            self.assertEqual(raised.exception.returncode, 2)
            self.assertIn("migration TCP connection refused", (Path(directory) / "migration.log").read_text())
            self.assertFalse(scope["active_processes"])
            self.assertTrue(scope["process_receipts"][-1]["waitReturned"])
            self.assertEqual(scope["process_receipts"][-1]["exitCode"], 2)

    def test_timed_out_stdin_command_preserves_timeout_and_reaps_child(self):
        with tempfile.TemporaryDirectory(prefix="native-rubygems-command-timeout-") as directory:
            scope = self.command_scope(Path(directory))
            args = [sys.executable, "-c", "import sys,time;sys.stdin.read();time.sleep(10)"]
            with self.assertRaises(subprocess.TimeoutExpired):
                scope["command"](args, input="owned migration input\n", output="migration.log", timeout=0.1)
            self.assertFalse(scope["active_processes"])
            self.assertTrue(scope["process_receipts"][-1]["waitReturned"])
            self.assertTrue(scope["process_receipts"][-1]["processGroupStopped"])

# The deterministic boundary models an owned native client and network, then
# blocks Docker's first context query. The actual unmodified producer must
# stop/reap that command group, discover the labelled client, and remove only
# its own network/private work. No Docker daemon, source fetch or Go is used.
FAKE_DOCKER = r'''#!@PYTHON@
import json,os,pathlib,signal,subprocess,sys,time
root=pathlib.Path(os.environ['FAKE_GATE_ROOT'])
args=sys.argv[1:]
with (root/'calls.jsonl').open('a') as out:out.write(json.dumps(args)+'\n')
statefile=root/'docker-state.json'
state=json.loads(statefile.read_text()) if statefile.exists() else {'containers':{},'network':None}
if args==['context','show']:
    run=next((root/'repo/output/native-rubygems-e2e').iterdir()).name
    label={'com.artifact-gateway.rubygems-e2e.run':run}
    client=run+'-cli-cancellation-probe'
    state={'containers':{client:label},'network':{'Name':run+'-net','Labels':label}}
    statefile.write_text(json.dumps(state))
    private=next((root/'tmp').glob('ag459-rubygems-*'))
    (private/'owned-secret.env').write_text('OWNED_SECRET=private-cancellation-value\n')
    if os.environ['FAKE_GATE_MODE']=='failure':sys.exit(7)
    def stop(_signum,_frame):raise SystemExit(143)
    signal.signal(signal.SIGTERM,stop);signal.signal(signal.SIGINT,stop)
    child=subprocess.Popen([sys.executable,'-c','import signal;signal.pause()'])
    (root/'boundary.json').write_text(json.dumps({'pid':os.getpid(),'descendant':child.pid,'runId':run,'client':client,'privateWork':str(private)}))
    try:signal.pause()
    finally:
        if child.poll() is None:child.terminate()
        child.wait(timeout=2)
    sys.exit(0)
if args[:2]==['ps','-aq']:
    if os.environ['FAKE_GATE_MODE']!='failure' and not (root/'cleanup-ready').exists():
        (root/'cleanup-ready').write_text('ready\n')
        deadline=time.monotonic()+5
        while not (root/'cleanup-release').exists():
            if time.monotonic()>deadline:sys.exit(9)
            time.sleep(.01)
    print('\n'.join(state['containers']));sys.exit(0)
if args[:2]==['rm','--force']:
    state['containers'].pop(args[2],None);statefile.write_text(json.dumps(state));sys.exit(0)
if args[:2]==['network','inspect']:
    if state['network'] and state['network']['Name']==args[2]:print(json.dumps([state['network']]));sys.exit(0)
    sys.exit(1)
if args[:2]==['network','rm']:
    state['network']=None;statefile.write_text(json.dumps(state));sys.exit(0)
if args[:1]==['logs']:sys.exit(0)
sys.exit(1)
'''


class NativeRubyGemsCleanupTest(unittest.TestCase):
    def wait_for(self, path, process, seconds=8):
        deadline = time.monotonic() + seconds
        while time.monotonic() < deadline:
            if path.exists():
                return
            if process.poll() is not None:
                break
            time.sleep(0.02)
        self.fail(f"actual producer did not reach deterministic boundary {path.name}")

    def run_gate(self, signum):
        source = SCRIPT.read_bytes()
        source_hash = hashlib.sha256(source).hexdigest()
        with tempfile.TemporaryDirectory(prefix="native-rubygems-cancel-test-") as directory:
            root = Path(directory)
            scripts = root / "repo/scripts"
            scripts.mkdir(parents=True)
            script = scripts / SCRIPT.name
            script.write_bytes(source)
            script.chmod(0o700)
            binaries = root / "bin"
            binaries.mkdir()
            (root / "tmp").mkdir()
            docker = binaries / "docker"
            docker.write_text(FAKE_DOCKER.replace("@PYTHON@", sys.executable))
            docker.chmod(0o700)
            (binaries / "python3").symlink_to(sys.executable)
            for name in ("go", "curl"):
                executable = binaries / name
                executable.write_text("#!/bin/sh\nexit 97\n")
                executable.chmod(0o700)
            env = dict(os.environ, PATH=str(binaries) + os.pathsep + os.environ["PATH"],
                       TMPDIR=str(root / "tmp"), FAKE_GATE_ROOT=str(root),
                       FAKE_GATE_MODE="signal" if signum else "failure")
            env.pop("DOCKER_HOST", None)
            env.pop("DOCKER_CONTEXT", None)
            started = time.monotonic()
            process = subprocess.Popen([str(script)], cwd=root / "repo", env=env,
                                       stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                                       text=True, start_new_session=True)
            boundary = None
            out = err = ""
            try:
                if signum:
                    self.wait_for(root / "boundary.json", process)
                    boundary = json.loads((root / "boundary.json").read_text())
                    os.killpg(process.pid, signum)
                    self.wait_for(root / "cleanup-ready", process)
                    # A second group signal during cleanup must not interrupt
                    # cleanup, erase the first cancellation, or turn it green.
                    os.killpg(process.pid, signal.SIGTERM)
                    (root / "cleanup-release").write_text("continue\n")
                out, err = process.communicate(timeout=12)
            finally:
                if process.poll() is None:
                    os.killpg(process.pid, signal.SIGKILL)
                    process.communicate(timeout=3)
                if boundary:
                    for pid in (boundary["pid"], boundary["descendant"]):
                        try:
                            os.killpg(boundary["pid"], signal.SIGKILL)
                        except ProcessLookupError:
                            pass
                        self.assertFalse(self.running(pid), f"owned command child still running: {pid}")
            raw = list((root / "repo/output/native-rubygems-e2e").iterdir())
            self.assertEqual(len(raw), 1)
            receipt_path = raw[0] / "cleanup.json"
            receipt = json.loads(receipt_path.read_text()) if receipt_path.exists() else None
            calls = [json.loads(line) for line in (root / "calls.jsonl").read_text().splitlines()]
            state = json.loads((root / "docker-state.json").read_text())
            report = {"scriptSHA256": source_hash, "signal": signum, "exitCode": process.returncode,
                      "durationSeconds": time.monotonic() - started, "cleanup": receipt,
                      "modelledDockerFinalState": state, "dockerCalls": calls,
                      "stdout": out, "stderr": err, "realDockerAndNetworkUsed": False,
                      "actualProducerBytesUnchanged": script.read_bytes() == source,
                      "privateWorkDirectoriesRemaining": [p.name for p in (root / "tmp").iterdir()]}
            print(json.dumps(report, sort_keys=True), flush=True)
            destination = os.environ.get("RUBYGEMS_CLEANUP_TEST_EVIDENCE_DIR")
            if destination:
                target = Path(destination) / ("signal-" + str(signum) if signum else "ordinary-failure")
                target.mkdir(parents=True, exist_ok=False)
                shutil.copytree(raw[0], target / "actual-producer")
                (target / "result.json").write_text(json.dumps(report, indent=2) + "\n")
            self.assertEqual(process.returncode, 128 + signum if signum else 1)
            self.assertIsNotNone(receipt, "actual producer omitted cleanup receipt")
            self.assertEqual(receipt["exitCode"], process.returncode)
            self.assertTrue(receipt["passed"])
            self.assertEqual(receipt.get("cancelledBySignal"), signum)
            self.assertTrue(receipt["allCommandChildrenReaped"])
            self.assertFalse(report["privateWorkDirectoriesRemaining"])
            self.assertEqual(state, {"containers": {}, "network": None})
            self.assertTrue(any(call[:2] == ["rm", "--force"] for call in calls))
            self.assertTrue(any(call[:2] == ["network", "rm"] for call in calls))
            if boundary:
                self.assertTrue(any(child["pid"] == boundary["pid"] and child["waitReturned"] and child["processGroupStopped"] for child in receipt["processes"]))
            self.assertLess(report["durationSeconds"], 20)
            self.assertEqual(SCRIPT.read_bytes(), source)

    def running(self, pid):
        result = subprocess.run(["ps", "-o", "stat=", "-p", str(pid)], capture_output=True, text=True, check=False)
        return bool(result.stdout.strip()) and not result.stdout.strip().startswith(("Z", "X"))

    def test_sigterm_reaps_children_and_cleans_owned_resources(self):
        self.run_gate(signal.SIGTERM)

    def test_sigint_reaps_children_and_cleans_owned_resources(self):
        self.run_gate(signal.SIGINT)

    def test_ordinary_failure_preserves_nonzero_and_cleans(self):
        self.run_gate(None)


if __name__ == "__main__":
    unittest.main()
