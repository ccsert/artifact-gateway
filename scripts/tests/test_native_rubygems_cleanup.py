"""Exercise the real producer's cancellation path without Docker or networking."""

import hashlib
import json
import os
from pathlib import Path
import shutil
import signal
import subprocess
import sys
import tempfile
import time
import unittest


SCRIPT = Path(__file__).resolve().parents[1] / "native-rubygems-e2e.sh"

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
