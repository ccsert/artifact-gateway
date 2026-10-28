#!/usr/bin/env python3
"""Render and run the opt-in overlay with disposable PG/S3/TLS, no .env input."""
import argparse
from contextlib import contextmanager
from email import policy
from email.parser import BytesParser
import hashlib
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import ipaddress
import json
import os
from pathlib import Path
import secrets
import subprocess
import sys
import tempfile
import threading
import time
import urllib.error
import urllib.request
import uuid

ROOT = Path(__file__).resolve().parent.parent


def require(condition, message):
    if not condition:
        raise RuntimeError(message)


def eventually(probe, description, timeout=65):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        result = probe()
        if result:
            return result
        time.sleep(0.5)
    raise RuntimeError("deadline: " + description)


@contextmanager
def lost_response_proxy(upstream, path, http):
    """Commit a real API write, then withhold its HTTP reply until timeout."""
    release = threading.Event()
    records = []

    class Handler(BaseHTTPRequestHandler):
        def log_message(self, *_args):
            pass

        def do_POST(self):
            if self.path != path:
                self.send_error(404)
                return
            size = int(self.headers.get("Content-Length", "0"))
            if size > 4096:
                self.send_error(413)
                return
            request = urllib.request.Request(upstream + path,
                data=self.rfile.read(size), method="POST", headers={
                    name: self.headers[name] for name in (
                        "Authorization", "Content-Type", "If-Match", "Idempotency-Key"
                    ) if name in self.headers
                })
            try:
                with http.open(request, timeout=5) as response:
                    records.append({"status": response.code,
                        "cacheControl": response.headers.get("Cache-Control"),
                        "delivery": json.loads(response.read())})
            except (OSError, urllib.error.HTTPError):
                records.append({"upstreamFailed": True})
            release.wait(10)
            self.close_connection = True

    server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    server.daemon_threads = False
    thread = threading.Thread(target=server.serve_forever)
    thread.start()
    try:
        yield "http://127.0.0.1:" + str(server.server_port), records
    finally:
        release.set()
        server.shutdown()
        server.server_close()
        thread.join(timeout=5)
        require(not thread.is_alive(), "response-loss proxy did not stop")


def check(render_only, evidence=None, fixture_subnets=None):
    project = "artifact-gateway-email-check-" + uuid.uuid4().hex[:12]
    # Compose interpolation accepts only these generated values, never checkout
    # .env, exported deployment credentials, roles, ports or COMPOSE_PROJECT_NAME.
    env = {k: os.environ[k] for k in (
        "PATH", "HOME", "DOCKER_HOST", "DOCKER_CONTEXT", "DOCKER_CONFIG"
    ) if k in os.environ}
    checks = []
    if evidence:
        evidence.mkdir(parents=True, exist_ok=False)
        sources = ("scripts/email-compose-check.py", "scripts/testdata/email-smtp-sink.py",
                   "internal/agctl/alert_commands.go", "compose.yml", "compose.email.yml")
        (evidence / "source.json").write_text(json.dumps({
            "head": subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=ROOT, text=True).strip(),
            "sha256": {name: hashlib.sha256((ROOT / name).read_bytes()).hexdigest() for name in sources},
            "project": project,
        }, indent=2) + "\n")
        (evidence / "executed-source.py").write_bytes((ROOT / "scripts/email-compose-check.py").read_bytes())
        (evidence / "executed-smtp-sink.py").write_bytes((ROOT / "scripts/testdata/email-smtp-sink.py").read_bytes())
    http = urllib.request.build_opener(urllib.request.ProxyHandler({}))
    with tempfile.TemporaryDirectory(prefix=project + "-") as temporary:
        work = Path(temporary)
        config, sink = work / "config", work / "sink"
        config.mkdir(mode=0o755)
        sink.mkdir(mode=0o700)
        key = secrets.token_hex(32)
        admin = secrets.token_hex(24)
        auth = {"username": "owned-synthetic", "password": secrets.token_hex(24)}
        recipient = "synthetic-recipient@example.test"
        values = {
            "GATEWAY_POSTGRES_PASSWORD": secrets.token_hex(24),
            "GATEWAY_ADMIN_TOKEN": admin,
            "GATEWAY_RESOLVER_TOKEN": secrets.token_hex(24),
            "RUSTFS_ACCESS_KEY": "owned-" + secrets.token_hex(8),
            "RUSTFS_SECRET_KEY": secrets.token_hex(24),
            "RUSTFS_RPC_SECRET": secrets.token_hex(32),
            "GATEWAY_SETTINGS_ENCRYPTION_KEY": key,
            "GATEWAY_EMAIL_CONFIG_DIR": str(config),
            "GATEWAY_NODE_ROLES": "api",
            "GATEWAY_DATABASE_MAX_OPEN_CONNS": "8",
            "GATEWAY_DATABASE_MAX_IDLE_CONNS": "2",
            "GATEWAY_DATABASE_COORDINATOR_MAX_OPEN_CONNS": "2",
            "GATEWAY_DATABASE_COORDINATOR_MAX_IDLE_CONNS": "1",
            "GATEWAY_DATABASE_ARTIFACT_LOCK_MAX_OPEN_CONNS": "2",
            "GATEWAY_DATABASE_ARTIFACT_LOCK_MAX_IDLE_CONNS": "1",
        }
        private_values = [recipient, auth["username"], auth["password"], key,
                          *[values[name] for name in (
                              "GATEWAY_ADMIN_TOKEN", "GATEWAY_RESOLVER_TOKEN", "GATEWAY_POSTGRES_PASSWORD",
                              "RUSTFS_ACCESS_KEY", "RUSTFS_SECRET_KEY", "RUSTFS_RPC_SECRET")]]
        environment_file = work / "fixture.env"
        environment_file.write_text("".join(k + "=" + v + "\n" for k, v in values.items()))
        environment_file.chmod(0o600)
        base = ["docker", "compose", "--env-file", str(environment_file),
                "--project-name", project, "-f", str(ROOT / "compose.yml")]

        def run(args, expected=0):
            result = subprocess.run(args, cwd=ROOT, env=env, capture_output=True, text=True)
            diagnostic = result.stdout + result.stderr
            for value in private_values:
                diagnostic = diagnostic.replace(value, "[fixture-secret]")
            if evidence and ((args[:2] == ["docker", "compose"] and "build" in args) or result.returncode != expected):
                number = len(list(evidence.glob("command-*.log"))) + 1
                (evidence / ("command-" + str(number) + ".log")).write_text(diagnostic)
            require(result.returncode == expected,
                    "command failed: " + " ".join(args[:2]) + "\n" + diagnostic[-4000:])
            return result.stdout

        baseline = json.loads(run(base + ["config", "--format", "json"]))
        require("GATEWAY_EMAIL_CONFIG_FILE" not in baseline["services"]["gateway"]["environment"],
                "default Compose unexpectedly enables email")
        require("gateway-email-worker" not in baseline["services"], "default adds worker")
        checks.append("default Compose stays disconnected/disabled")
        private_key = "GATEWAY_MIGRATION_SOURCE_PRIVATE_ENDPOINTS"
        require(baseline["services"]["gateway"]["environment"].get(private_key, "") == "",
                "default Compose unexpectedly approves private migration sources")
        values[private_key] = "nexus.example.test=10.20.30.0/24"
        environment_file.write_text("".join(k + "=" + v + "\n" for k, v in values.items()))
        configured = json.loads(run(base + ["config", "--format", "json"]))
        require(configured["services"]["gateway"]["environment"].get(private_key) == values[private_key],
                "Compose drops the operator migration-source allowlist")
        checks.append("private migration sources default denied; explicit operator allowlist reaches Gateway")
        compose = base + ["-f", str(ROOT / "compose.email.yml"), "--profile", "email-roles"]
        rendered = json.loads(run(compose + ["config", "--format", "json"]))
        for name, role in (("gateway", "api"), ("gateway-scheduler", "scheduler"),
                           ("gateway-email-worker", "worker")):
            node = rendered["services"][name]
            require(node["environment"]["GATEWAY_NODE_ROLES"] == role, "role drift")
            require(node["environment"]["GATEWAY_SETTINGS_ENCRYPTION_KEY"] == key, "key drift")
            require(node["environment"].get(private_key) == values[private_key], "migration-source allowlist drift")
            require(node["environment"]["GATEWAY_EMAIL_CONFIG_FILE"] == "/etc/gateway-email/relay.json", "config drift")
            mount = next(v for v in node["volumes"] if v["target"] == "/etc/gateway-email")
            # Compose's canonical JSON may omit a false create_host_path value.
            # The overlay explicitly sets false; still reject a rendered true.
            require(mount["read_only"] and
                    mount.get("bind", {}).get("create_host_path", False) is False,
                    "unsafe mount")
            require(mount["source"] == str(config), "mount drift")
            if name != "gateway":
                require(not node.get("ports"), "background role publishes host ports")
                require("migrate" in node["depends_on"], "role lost migration dependency")
        require(rendered["services"]["gateway-email-worker"]["environment"]["GATEWAY_WORKER_KINDS"] == "email", "worker kind drift")
        checks.append("all three roles share read-only config and key; background ports absent")
        image = project + ":fixture"
        override = work / "fixture.json"
        nodes = ("gateway", "gateway-scheduler", "gateway-email-worker")
        model = {
            "services": {n: {"image": image, "ports": []} for n in nodes},
            "networks": {"default": {"internal": True}, "ingress": {}},
        }
        if fixture_subnets:
            # Explicit fixture-only override for a daemon with exhausted default
            # pools. Reject overlap; never prune networks or change daemon IPAM.
            require(all(net.version == 4 and net.is_private and net.prefixlen <= 27 for net in fixture_subnets)
                    and not fixture_subnets[0].overlaps(fixture_subnets[1]),
                    "fixture subnets must be distinct private IPv4 networks with at least 30 addresses")
            identifiers = run(["docker", "network", "ls", "-q"]).splitlines()
            existing = json.loads(run(["docker", "network", "inspect", *identifiers])) if identifiers else []
            occupied = [ipaddress.ip_network(cfg["Subnet"]) for network in existing
                        for cfg in (network.get("IPAM", {}).get("Config") or []) if cfg.get("Subnet")]
            require(not any(net.overlaps(used) for net in fixture_subnets for used in occupied if used.version == 4),
                    "requested fixture subnet overlaps an existing Docker network")
            for name, subnet in zip(("default", "ingress"), fixture_subnets):
                model["networks"][name]["ipam"] = {"config": [{"subnet": str(subnet)}]}
            if evidence:
                (evidence / "owned-subnets.json").write_text(json.dumps([str(net) for net in fixture_subnets], indent=2) + "\n")
        # Only the API has a second bridge for its ephemeral loopback host port.
        # SMTP/PG/S3 and background roles remain on the internal network only.
        model["services"]["gateway"]["networks"] = {"default": {}, "ingress": {}}
        # !override/!reset need YAML; generated fixture JSON is augmented below
        # in a YAML override so base published ports cannot be accidentally kept.
        model["services"]["smtp"] = {
            "image": "python:3.12-alpine",
            "command": ["python", "/sink.py"],
            "networks": {"default": {"aliases": ["smtp.example.test"]}},
            "volumes": [str(sink) + ":/fixture", str(ROOT / "scripts/testdata/email-smtp-sink.py") + ":/sink.py:ro"],
        }
        override.write_text(json.dumps(model))
        ports = work / "ports.yml"
        ports.write_text("services:\n" + "".join(
            "  " + n + ":\n    ports: !override " +
            ('["127.0.0.1::8080"]' if n == "gateway" else "[]") + "\n"
            for n in (*nodes, "postgres", "rustfs")))
        compose += ["-f", str(override), "-f", str(ports)]
        if render_only:
            fixture = json.loads(run(compose + ["config", "--format", "json"]))
            require(fixture["networks"]["default"]["internal"], "fixture SMTP network allows outbound traffic")
            if fixture_subnets:
                for name, subnet in zip(("default", "ingress"), fixture_subnets):
                    require(fixture["networks"][name]["ipam"]["config"] == [{"subnet": str(subnet)}],
                            "fixture-only subnet override did not reach Compose")
                checks.append("explicit fixture-only subnets are non-overlapping and preserve the internal SMTP network")
            return {"status": "passed", "checks": checks, "runtime": False}
        started = False
        second_worker = False
        base_url = None
        agctl = work / "agctl"
        recipient_file = work / "recipient"
        recipient_file.write_text(recipient + "\n")
        recipient_file.chmod(0o600)
        def ctl(*args, expected=0, token=None, server=None, stdin="", actor="admin", json_output=True):
            bounded = list(args) + ([] if "--timeout" in args else ["--timeout", "20s"])
            started_at = time.monotonic()
            result = subprocess.run([str(agctl), *bounded, *(["--json"] if json_output else [])], cwd=work,
                input=stdin, capture_output=True, text=True, timeout=90, env={
                    "PATH": os.environ.get("PATH", ""), "HOME": str(work),
                    "AGCTL_SERVER": server or base_url, "AGCTL_TOKEN": admin if token is None else token,
                    "AGCTL_PROFILE": "",
                })
            output = result.stdout + result.stderr
            require(not any(value in output for value in private_values),
                    "agctl printed a fixture secret or recipient")
            if evidence:
                with (evidence / "cli-cases.jsonl").open("a") as stream:
                    stream.write(json.dumps({"args": args, "actor": actor,
                        "jsonOutput": json_output,
                        "exit": result.returncode, "expectedExit": expected,
                        "elapsedSeconds": round(time.monotonic() - started_at, 3),
                        "stdout": result.stdout, "stderr": result.stderr}) + "\n")
            require(result.returncode == expected,
                    "agctl " + " ".join(args) + " exited " + str(result.returncode) + ": " + output[-2000:])
            if not json_output:
                return output
            report = json.loads(result.stdout)
            require(report["schemaVersion"] == 1 and report["ok"] == (expected == 0),
                    "agctl envelope disagrees with its exit code")
            return report

        def start(*services):
            nonlocal started, base_url
            started = True
            run(compose + ["up", "-d", "--no-build", "--wait", "--wait-timeout", "90", *services])
            address = run(compose + ["port", "gateway", "8080"]).strip()
            base_url = "http://" + address

        def api(method, path, body=None, status=200, headers=None):
            data = None if body is None else (body if isinstance(body, bytes) else json.dumps(body).encode())
            request = urllib.request.Request(base_url + path, data=data, method=method, headers={
                "Authorization": "Bearer " + admin,
                "Content-Type": "application/json", **(headers or {}),
            })
            try:
                response = http.open(request, timeout=5)
            except urllib.error.HTTPError as error:
                response = error
            with response:
                content = response.read()
                require(response.code == status, "unexpected HTTP status: " + method + " " + path + " " + str(response.code))
                require(not any(s.encode() in content for s in (key, auth["password"], values["GATEWAY_POSTGRES_PASSWORD"])), "API leaked fixture secret")
                return json.loads(content) if content else None

        def attempts():
            return (sink / "attempts").read_text().splitlines() if (sink / "attempts").exists() else []

        def relay(enabled=True):
            cfg = {"enabled": enabled, "host": "smtp.example.test", "port": 465,
                   "mode": "implicit_tls", "from": "synthetic-gateway@example.test",
                   "approvedIPs": [smtp_ip], "caFile": "/etc/gateway-email/ca.pem",
                   "authFile": "/etc/gateway-email/auth.json",
                   "consoleOrigin": "https://console.example.invalid"}
            (config / "relay.json").write_text(json.dumps(cfg))
            (config / "relay.json").chmod(0o644)

        def delivery(identifier):
            return api("GET", "/api/v2/email-deliveries/" + identifier)

        def explicit(target, scenario="warning", status=202):
            return api("POST", "/api/v2/email-notifications:test", {
                "targetId": target["id"], "scenario": scenario,
            }, status, {"If-Match": target["version"], "Idempotency-Key": str(uuid.uuid4())})

        def restart(*services):
            nonlocal base_url
            run(compose + ["up", "-d", "--no-build", "--force-recreate", "--wait", "--wait-timeout", "90", *services])
            if "gateway" in services:
                base_url = "http://" + run(compose + ["port", "gateway", "8080"]).strip()

        def mismatch(service, additions):
            nonlocal base_url
            path = work / (service + "-mismatch.json")
            path.write_text(json.dumps({"services": {service: {"environment": additions}}}))
            run(compose + ["-f", str(path), "up", "-d", "--no-build", "--force-recreate", "--wait", "--wait-timeout", "90", service])
            if service == "gateway":
                base_url = "http://" + run(compose + ["port", "gateway", "8080"]).strip()

        def rule(name, target):
            repo = api("POST", "/api/v2/repositories", {"name": name, "format": "raw", "type": "hosted"}, 201, {"Idempotency-Key": str(uuid.uuid4())})
            api("PUT", "/api/v2/repositories/" + repo["id"] + "/capacity", {"quotaBytes": 1000})
            api("PUT", "/raw/" + name + "/synthetic.txt", b"x" * 900, 201,
                {"Content-Type": "application/octet-stream"})
            return api("POST", "/api/v2/repository-quota-alert-rules", {
                "repositoryId": repo["id"], "targetId": target["id"], "enabled": True,
                "policy": {"warningBasisPoints": 8500, "criticalBasisPoints": 9500,
                           "recoveryBelowBasisPoints": 8000, "warningForSeconds": 1,
                           "criticalForSeconds": 1, "recoveryForSeconds": 1,
                           "maxSampleAgeSeconds": 60}}, 201)

        def events(r):
            return api("GET", "/api/v2/repository-quota-alert-rules/" + r["id"] + "/events")

        try:
            run(["go", "build", "-o", str(agctl), "./cmd/agctl"])
            run(compose + ["build", "gateway"])
            run(["openssl", "req", "-x509", "-newkey", "rsa:2048", "-nodes", "-days", "1",
                 "-subj", "/CN=smtp.example.test", "-addext", "subjectAltName=DNS:smtp.example.test",
                 "-keyout", str(sink / "key.pem"), "-out", str(sink / "cert.pem")])
            for directory in (config, sink):
                (directory / "auth.json").write_text(json.dumps(auth))
                (directory / "auth.json").chmod(0o600)
            (sink / "key.pem").chmod(0o600)
            (sink / "rcpt-code").write_text("250")
            (config / "ca.pem").write_bytes((sink / "cert.pem").read_bytes())
            (config / "ca.pem").chmod(0o644)
            # The real image runs as UID 65532. Provision only this disposable
            # auth file, with the same owner and 0600 permissions as the runbook.
            run(["docker", "run", "--rm", "--network", "none", "-v", str(config) + ":/owned",
                 "postgres:16-alpine", "chown", "65532:65532", "/owned/auth.json"])
            started = True
            run(compose + ["up", "-d", "smtp"])
            eventually(lambda: (sink / "ready").exists(), "TLS sink readiness", 15)
            smtp_id = run(compose + ["ps", "-q", "smtp"]).strip()
            smtp_ip = run(["docker", "inspect", "--format", "{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}", smtp_id]).strip()
            relay(False)
            start("gateway")
            require(ctl("email", "capability")["data"] == {"enabled": False, "reason": "email_disabled"}, "disabled channel reports ready")
            target = api("POST", "/api/v2/email-targets", {"name": "Owned synthetic", "recipient": "synthetic-recipient@example.test", "locale": "en", "enabled": True}, 201)
            require(explicit(target, status=503)["code"] == "email_disabled", "disabled test accepted")
            disabled = ctl("email", "test", "--target", target["id"], "--scenario", "warning", "--yes", expected=1)
            require(disabled["error"]["code"] == "email_disabled" and not disabled.get("data"), "fresh disabled test was not a definite refusal")
            require(not attempts(), "disabled channel attempted SMTP")
            checks.append("API and agctl: disabled channel is a definite refusal and never contacts SMTP")

            # Invalid config and permissive auth fail offline, before HTTP starts.
            (config / "relay.json").write_text('{"enabled":true,"private-marker":"redacted"}')
            result = subprocess.run(compose + ["run", "--rm", "--no-deps", "gateway"], cwd=ROOT, env=env, capture_output=True, text=True)
            require(result.returncode != 0 and "invalid configuration" in result.stdout + result.stderr, "invalid config did not fail closed: " + (result.stdout + result.stderr)[-2000:])
            require("private-marker" not in result.stdout + result.stderr, "config field leaked")
            relay()
            public_auth = config / "public-auth.json"
            public_auth.write_text(json.dumps(auth))
            public_auth.chmod(0o644)
            invalid_auth = json.loads((config / "relay.json").read_text())
            invalid_auth["authFile"] = "/etc/gateway-email/public-auth.json"
            (config / "relay.json").write_text(json.dumps(invalid_auth))
            result = subprocess.run(compose + ["run", "--rm", "--no-deps", "gateway"], cwd=ROOT, env=env, capture_output=True, text=True)
            require(result.returncode != 0 and "invalid configuration" in result.stdout + result.stderr, "public auth file accepted")
            public_auth.unlink()
            relay()
            checks.append("malformed config and non-private auth fail startup with fixed safe error")
            restart("gateway")
            require(ctl("email", "capability")["data"]["reason"] == "ready", "valid API config unavailable")
            mismatch("gateway", {"GATEWAY_SETTINGS_ENCRYPTION_KEY": ""})
            require(ctl("email", "capability")["data"] == {"enabled": False, "reason": "encryption_key_unavailable"}, "missing API encryption key reported ready")
            no_key = ctl("email", "test", "--target", target["id"], "--scenario", "warning", "--yes", expected=1)
            require(no_key["error"]["code"] == "encryption_key_unavailable" and not no_key.get("data"), "missing API key was not a definite refusal")
            require(not ctl("email", "delivery", "list")["data"]["items"] and not attempts(), "unready channel queued or sent mail")
            checks.append("agctl: missing API settings key is unavailable and refuses before queue insertion")
            restart("gateway")
            pending = ctl("email", "test", "--target", target["id"], "--scenario", "warning", "--yes")["data"]
            time.sleep(6)
            pending_read = ctl("email", "delivery", "show", pending["id"])["data"]
            require(pending_read["state"] == "pending" and pending_read["attempts"] == 0 and not attempts(), "API-only became sender")
            timed_out = ctl("email", "delivery", "wait", pending["id"], "--timeout", "1s", expected=8)
            require(timed_out["error"]["code"] == "wait_timeout" and timed_out["data"]["state"] == "pending", "no-sender wait lost its last queued state")
            require(ctl("email", "delivery", "show", pending["id"])["data"] == pending_read, "stopping a wait cancelled or changed the queued delivery")
            checks.append("agctl: API-only ready queues with zero attempts; bounded wait exits 8 and leaves delivery unchanged")

            mismatch("gateway-email-worker", {"GATEWAY_EMAIL_CONFIG_FILE": ""})
            time.sleep(6)
            require(ctl("email", "delivery", "show", pending["id"])["data"]["attempts"] == 0 and not attempts(), "unconfigured worker claimed")
            mismatch("gateway-email-worker", {"GATEWAY_SETTINGS_ENCRYPTION_KEY": secrets.token_hex(32)})
            failed_key = eventually(lambda: (d if (d := delivery(pending["id"])).get("errorCode") == "encryption_key_unavailable" else None), "mismatched worker key")
            require(failed_key["state"] == "retrying" and not attempts(), "bad key contacted SMTP")
            checks.append("concurrent ready API / absent worker config / different worker key never sends")
            restart("gateway-email-worker")
            # A second actual worker shares the contract and PostgreSQL fencing.
            run(compose + ["run", "-d", "--no-deps", "--name", project + "-second-worker", "gateway-email-worker"])
            second_worker = True
            accepted = eventually(lambda: (d if (d := delivery(pending["id"]))["state"] == "accepted" else None), "corrected workers accept MIME")
            require(accepted["attempts"] > 1 and attempts() == ["250"], "concurrent workers duplicated SMTP")

            (sink / "rcpt-code").write_text("550")
            rejected = explicit(target, "critical")
            dead = eventually(lambda: (d if (d := delivery(rejected["id"]))["state"] == "dead" else None), "permanent SMTP rejection")
            require(dead["errorCode"] == "smtp_permanent_rejection", "unsafe failure status")
            require(len(list(sink.glob("*.eml"))) == 1, "rejected RCPT received MIME")
            (sink / "rcpt-code").write_text("250")
            checks.append("corrected concurrent workers accept one MIME; RCPT 550 is dead with safe code")

            mismatch("gateway-scheduler", {"GATEWAY_EMAIL_CONFIG_FILE": ""})
            missing_rule = rule("owned-missing-scheduler", target)
            missing = eventually(lambda: events(missing_rule), "disabled scheduler event")
            require(len(missing) == 1 and missing[0]["notificationCode"] == "email_disabled" and not missing[0].get("deliveryId"), "scheduler mismatch secretly queued")
            restart("gateway-scheduler")
            good_rule = rule("owned-wired-scheduler", target)
            good = eventually(lambda: (e if (e := events(good_rule)) and e[0].get("deliveryState") == "accepted" else None), "wired scheduler quota MIME")
            require(good[0]["notificationCode"] == "queued", "configured scheduler did not queue")
            require(len(events(missing_rule)) == 1 and events(missing_rule)[0]["notificationCode"] == "email_disabled", "scheduler correction backfilled suppressed event")
            checks.append("API-ready / disabled scheduler persists email_disabled; correction does not backfill; shared scheduler delivers quota warning")

            run(compose + ["stop", "gateway-scheduler", "gateway-email-worker"])
            run(["docker", "rm", "-f", project + "-second-worker"])
            second_worker = False
            mismatch("gateway", {"GATEWAY_NODE_ROLES": "standalone"})
            standalone = explicit(target, "resolved")
            eventually(lambda: delivery(standalone["id"])["state"] == "accepted", "standalone MIME")
            messages = list(sink.glob("*.eml"))
            require(len(messages) == 3, "unexpected accepted MIME count")
            for path in messages:
                raw = path.read_bytes()
                message = BytesParser(policy=policy.default).parsebytes(raw)
                require(message["To"] == "synthetic-recipient@example.test" and message["From"] == "synthetic-gateway@example.test", "wrong synthetic envelope headers")
                require(message.get_content_type() == "multipart/alternative", "missing MIME alternatives")
                parts = list(message.iter_parts())
                require([p.get_content_type() for p in parts] == ["text/plain", "text/html"], "wrong MIME types")
                require(all(p.get_content().strip() for p in parts), "empty MIME body")
                require(auth["password"].encode() not in raw and key.encode() not in raw, "secret entered MIME")
                require("https://console.example.invalid/system?tab=diagnostics" in parts[1].get_content(), "configured origin missing")
            checks.append("standalone delivers; three real accepted messages contain HTML/plain and synthetic recipient")

            # agctl (#336) drives the same channel. Its input is private and
            # every invocation checks both output streams for synthetic secrets.
            mismatch("gateway", {"GATEWAY_NODE_ROLES": "api"})
            start("gateway-email-worker")
            require(ctl("email", "capability")["data"] == {"enabled": True, "reason": "ready"}, "agctl capability is not ready")
            failed_wait = ctl("email", "delivery", "wait", dead["id"], "--timeout", "10s", expected=1)
            require(not failed_wait["ok"] and failed_wait["error"]["code"] == "delivery_failed" and failed_wait["error"]["httpStatus"] is None, "agctl dead wait reported success or a server refusal")
            failed_record = failed_wait["data"]
            require(failed_record == dead and failed_record["errorCode"] == "smtp_permanent_rejection", "agctl failure changed the actual dead delivery record")
            require(ctl("email", "delivery", "show", dead["id"])["data"] == failed_record, "agctl wait mutated or replayed the dead delivery")
            checks.append("agctl: SMTP 550 dead wait exits 1 with delivery_failed and the unchanged complete delivery record")
            inventory = {item["name"]: item for item in ctl("commands")["data"]["items"]}
            names = ("quota-alert list", "quota-alert show", "quota-alert create", "quota-alert update",
                     "quota-alert delete", "quota-alert events", "email capability", "email target list",
                     "email target create", "email target show", "email target update", "email preview",
                     "email test", "email delivery list", "email delivery show", "email delivery replay")
            require(all(inventory[name]["meta"]["stage"] == "m2" and
                        inventory[name]["meta"]["minimumRole"] == "platform admin" for name in names),
                    "email/quota command metadata drift")
            require(inventory["email delivery wait"]["meta"]["cancelSupported"] is False and
                    "email target delete" not in inventory and "email delivery cancel" not in inventory,
                    "introspection invented unsupported email operations")
            agctl_target = ctl("email", "target", "create", "agctl synthetic", "--locale", "zh-CN", "--recipient-file", str(recipient_file))["data"]
            require(agctl_target["recipientConfigured"] and not agctl_target["enabled"], "agctl target did not default disabled")
            refused = ctl("email", "test", "--target", agctl_target["id"], "--scenario", "warning", "--yes", expected=7)
            require(refused["error"]["code"] == "target_disabled", "disabled target accepted a test")
            agctl_target = ctl("email", "target", "update", agctl_target["id"], "--enabled=true")["data"]
            renamed = ctl("email", "target", "update", agctl_target["id"], "--name", "agctl renamed")["data"]
            require(renamed["enabled"] and renamed["name"] == "agctl renamed", "a rename disabled the target")
            ctl("email", "target", "update", agctl_target["id"], "--name", "stale", "--if-match", agctl_target["version"], expected=7)
            require(ctl("email", "target", "show", agctl_target["id"])["data"] == renamed, "stale target write changed current configuration")
            ctl("email", "target", "show", agctl_target["id"], "--verbose", json_output=False)
            ctl("email", "target", "update", agctl_target["id"], "--name", "stale human output", "--if-match", agctl_target["version"],
                "--verbose", expected=7, json_output=False)
            before = len(ctl("email", "delivery", "list", "--limit", "100")["data"]["items"])
            ctl("email", "preview", "--scenario", "critical", "--locale", "zh-CN", "--html-output", str(work / "preview.html"), "--text-output", str(work / "preview.txt"))
            require((work / "preview.html").read_text().strip() and (work / "preview.txt").read_text().strip(), "empty preview files")
            ctl("email", "test", "--target", agctl_target["id"], "--scenario", "warning", "--dry-run")
            ctl("email", "test", "--target", agctl_target["id"], "--scenario", "warning", expected=2)
            require(len(ctl("email", "delivery", "list", "--limit", "100")["data"]["items"]) == before, "preview or plan queued a delivery")
            test_key = str(uuid.uuid4())
            queued = ctl("email", "test", "--target", agctl_target["id"], "--scenario", "warning", "--yes", "--idempotency-key", test_key)["data"]
            again = ctl("email", "test", "--target", agctl_target["id"], "--scenario", "warning", "--yes", "--idempotency-key", test_key)["data"]
            require(again["id"] == queued["id"], "same idempotency key queued a second delivery")
            waited = ctl("email", "delivery", "wait", queued["id"], "--timeout", "60s")["data"]
            require(waited["state"] == "accepted", "agctl test was not accepted")
            require(len(list(sink.glob("*.eml"))) == 4, "agctl test did not deliver exactly one more MIME")
            ctl("email", "delivery", "replay", queued["id"], "--yes", expected=7)
            quota_repo = api("POST", "/api/v2/repositories", {"name": "owned-agctl-quota", "format": "raw", "type": "hosted"}, 201, {"Idempotency-Key": str(uuid.uuid4())})
            policy_args = ["--warning-bp", "8500", "--critical-bp", "9500", "--recovery-below-bp", "8000", "--warning-for", "1s",
                           "--critical-for", "1s", "--recovery-for", "1s", "--max-sample-age", "60s"]
            created = ctl("quota-alert", "create", "--repo", quota_repo["id"], "--target", agctl_target["id"], *policy_args)["data"]
            require(not created["enabled"], "a new rule was enabled without --enabled")
            enabled = ctl("quota-alert", "update", created["id"], "--enabled=true", "--yes")["data"]
            raised = ctl("quota-alert", "update", created["id"], "--warning-bp", "8700")["data"]
            require(enabled["enabled"] and raised["enabled"] and raised["policy"]["warningBasisPoints"] == 8700, "a threshold change disabled the rule")
            ctl("quota-alert", "update", created["id"], "--enabled=false", "--if-match", created["version"], expected=7)
            require(ctl("quota-alert", "show", created["id"])["data"]["version"] == raised["version"], "stale rule update changed its configuration")
            before_rules = ctl("quota-alert", "list")["data"]["items"]
            before_targets = ctl("email", "target", "list")["data"]["items"]
            before_deliveries = ctl("email", "delivery", "list", "--limit", "100")["data"]["items"]
            before_mime = len(list(sink.glob("*.eml")))
            ctl("quota-alert", "create", "--repo", quota_repo["id"], "--target", agctl_target["id"], *policy_args, "--dry-run")
            ctl("quota-alert", "update", created["id"], "--warning-bp", "8900", "--dry-run")
            ctl("quota-alert", "delete", created["id"], "--dry-run")
            ctl("email", "target", "create", "dry-run target", "--locale", "en", "--recipient-file", str(recipient_file), "--dry-run")
            ctl("email", "target", "update", agctl_target["id"], "--name", "dry-run name", "--recipient-file", str(recipient_file), "--dry-run")
            ctl("email", "delivery", "replay", dead["id"], "--dry-run")
            ctl("email", "delivery", "replay", dead["id"], expected=2)
            ctl("quota-alert", "delete", created["id"], expected=2)
            ctl("quota-alert", "update", created["id"], "--enabled=true", expected=2)
            ctl("email", "target", "create", "missing recipient", "--locale", "en", expected=2)
            public_recipient = work / "public-recipient"
            public_recipient.write_text(recipient + "\n")
            public_recipient.chmod(0o644)
            ctl("email", "target", "create", "public recipient", "--locale", "en", "--recipient-file", str(public_recipient), expected=2)
            ctl("email", "delivery", "list", "--limit", "101", expected=2)
            require(ctl("quota-alert", "list")["data"]["items"] == before_rules and
                    ctl("email", "target", "list")["data"]["items"] == before_targets and
                    ctl("email", "delivery", "list", "--limit", "100")["data"]["items"] == before_deliveries and
                    len(list(sink.glob("*.eml"))) == before_mime,
                    "dry-run or invalid input changed rules, targets, deliveries, versions or SMTP")
            bounded = ctl("email", "delivery", "list", "--limit", "1")
            require(bounded["meta"]["pagination"] == {"mode": "bounded", "limit": 1} and
                    bounded["data"]["nextCursor"] is None, "bounded delivery window claims a complete total or continuation")
            ctl("quota-alert", "events", created["id"])
            ctl("quota-alert", "delete", created["id"], "--yes")
            require(ctl("quota-alert", "show", created["id"])["data"]["deleted"], "deleted rule still active")
            checks.append("agctl: private recipient, rename keeps enabled, preview/plan queue nothing, keyed test delivers one MIME, replay refused, quota rule lifecycle")

            # All existing email/quota management operations are admin-only.
            # Local member/admin sessions are real; no forged principal headers.
            password = "Synthetic-" + secrets.token_hex(24)
            private_values.append(password)
            password_file = work / "password"
            password_file.write_text(password + "\n")
            password_file.chmod(0o600)
            denied_commands = [
                ("quota-alert", "list"), ("quota-alert", "show", created["id"]),
                ("quota-alert", "create", "--repo", quota_repo["id"], "--target", agctl_target["id"], *policy_args),
                ("quota-alert", "update", created["id"], "--enabled=false"),
                ("quota-alert", "delete", created["id"], "--yes", "--if-match", raised["version"]),
                ("quota-alert", "events", created["id"]), ("email", "capability"),
                ("email", "target", "list"), ("email", "target", "show", agctl_target["id"]),
                ("email", "target", "create", "forbidden target", "--locale", "en", "--recipient-file", str(recipient_file)),
                ("email", "target", "update", agctl_target["id"], "--name", "forbidden change"),
                ("email", "preview", "--scenario", "warning", "--locale", "en"),
                ("email", "test", "--target", agctl_target["id"], "--scenario", "warning", "--yes", "--if-match", renamed["version"]),
                ("email", "delivery", "list"), ("email", "delivery", "show", queued["id"]),
                ("email", "delivery", "replay", dead["id"], "--yes", "--if-match", dead["version"]),
                ("email", "delivery", "wait", queued["id"], "--timeout", "1s"),
            ]
            for username, role, reset, code in (("email-member", "member", False, "access_denied"),
                                                ("email-reset-admin", "admin", True, "password_change_required")):
                args = ["user", "create", username, "--role", role, "--password-file", str(password_file)]
                if reset:
                    args.append("--must-change-password")
                user = ctl(*args)["data"]
                secret_file = work / (username + "-session")
                login = ctl("auth", "login", "--username", username, "--password-stdin", "--secret-output", str(secret_file), stdin=password + "\n", token="")
                require(login["data"]["mustChangePassword"] == reset and secret_file.stat().st_mode & 0o777 == 0o600,
                        "local login lost forced-password state or exposed its token")
                session_token = secret_file.read_text().strip()
                private_values.append(session_token)
                for args in denied_commands:
                    refused = ctl(*args, token=session_token, actor=username, expected=6)
                    require(refused["error"]["code"] == code and refused["error"]["httpStatus"] == 403,
                            "mail/quota authorization returned the wrong machine reason")
                if not reset:
                    ctl("user", "delete", user["id"], "--yes")
                # The forced-change account is the only local administrator;
                # preserve last_admin protection. The owned database volume is
                # destroyed in finally, including this account and its session.
                secret_file.unlink()
            require(ctl("email", "target", "show", agctl_target["id"])["data"] == renamed and
                    ctl("email", "delivery", "list", "--limit", "100")["data"]["items"] == before_deliveries,
                    "refused callers changed email configuration or deliveries")
            checks.append("agctl: real member and must-change admin sessions refuse all 16 operations plus wait, without side effects")

            # A request reaches PostgreSQL, but no HTTP reply reaches agctl.
            # Recovery reuses the original key/version even after target edit.
            run(compose + ["stop", "gateway-email-worker"])
            unknown_key = str(uuid.uuid4())
            before_unknown = ctl("email", "delivery", "list", "--limit", "100")["data"]["items"]
            with lost_response_proxy(base_url, "/api/v2/email-notifications:test", http) as (proxy_url, committed):
                unknown = ctl("email", "test", "--target", agctl_target["id"], "--scenario", "critical", "--yes",
                              "--idempotency-key", unknown_key, "--if-match", renamed["version"],
                              "--timeout", "2s", server=proxy_url, expected=8)
            recovery = unknown["data"]
            require(recovery["outcome"] == "unknown" and recovery["idempotencyKey"] == unknown_key and
                    recovery["version"] == renamed["version"] and recovery["readBack"] == "agctl email delivery list",
                    "HTTP timeout lost the safe email recovery contract")
            require(committed and all(row["status"] == 202 and row["cacheControl"] == "no-store" for row in committed),
                    "HTTP loss fixture did not commit a real uncached queue insertion")
            lost_delivery = committed[0]["delivery"]
            after_unknown = ctl("email", "delivery", "list", "--limit", "100")["data"]["items"]
            require(len(after_unknown) == len(before_unknown) + 1 and
                    all(row["delivery"]["id"] == lost_delivery["id"] for row in committed),
                    "lost reply or automatic retry created duplicate queue rows")
            changed_target = ctl("email", "target", "update", agctl_target["id"], "--name", "agctl version changed")["data"]
            stale_test = ctl("email", "test", "--target", agctl_target["id"], "--scenario", "resolved", "--yes",
                             "--if-match", renamed["version"], expected=7)
            require(stale_test["error"]["code"] == "version_conflict", "stale target version queued a new test")
            recovered = ctl("email", "test", "--target", agctl_target["id"], "--scenario", "critical", "--yes",
                            "--idempotency-key", unknown_key, "--if-match", renamed["version"])["data"]
            require(recovered["id"] == lost_delivery["id"] and recovered["targetVersion"] == renamed["version"],
                    "same-key recovery followed a newer target version")
            conflicting = ctl("email", "test", "--target", agctl_target["id"], "--scenario", "resolved", "--yes",
                              "--idempotency-key", unknown_key, "--if-match", renamed["version"], expected=7)
            require(conflicting["error"]["code"] == "idempotency_conflict" and
                    len(ctl("email", "delivery", "list", "--limit", "100")["data"]["items"]) == len(after_unknown),
                    "key reuse with a different scenario queued another test")
            start("gateway-email-worker")
            superseded = ctl("email", "delivery", "wait", recovered["id"], "--timeout", "30s", expected=1)["data"]
            require(superseded["errorCode"] == "target_changed", "worker sent an obsolete target binding")
            checks.append("agctl: HTTP reply lost after real commit reports unknown; original key/version recovers one row after target edit; mismatched key scenario and stale versions exit 7")

            # Replay is a conditional, non-idempotent write. Its lost reply must
            # be read back rather than automatically submitted a second time.
            run(compose + ["stop", "gateway-email-worker"])
            with lost_response_proxy(base_url, "/api/v2/email-deliveries/" + dead["id"] + ":replay", http) as (proxy_url, replayed):
                unknown_replay = ctl("email", "delivery", "replay", dead["id"], "--yes", "--if-match", dead["version"],
                                     "--timeout", "2s", server=proxy_url, expected=8)
            require(unknown_replay["data"]["outcome"] == "unknown" and len(replayed) == 1 and
                    replayed[0]["status"] == 200, "uncertain replay was retried or reported certain")
            replay_pending = ctl("email", "delivery", "show", dead["id"])["data"]
            require(replay_pending["state"] == "pending" and replay_pending["version"] != dead["version"] and
                    replay_pending["attempts"] == 0, "replay read-back did not preserve the committed pending record")
            ctl("email", "delivery", "replay", dead["id"], "--yes", "--if-match", dead["version"], expected=7)
            start("gateway-email-worker")
            ctl("email", "delivery", "wait", dead["id"], "--timeout", "30s")
            checks.append("agctl: replay response loss reports unknown after exactly one write; show recovers pending and stale replay conflicts")

            # The relay stores DATA but loses the final acknowledgement. One
            # durable delivery can produce more than one received MIME.
            drop_reply = sink / "drop-final-reply"
            drop_reply.touch()
            before_ambiguous = len(list(sink.glob("*.eml")))
            ambiguous = ctl("email", "test", "--target", target["id"], "--scenario", "resolved", "--yes")["data"]
            uncertain = eventually(lambda: (d if (d := ctl("email", "delivery", "show", ambiguous["id"])["data"]).get("errorCode") == "outcome_unknown" else None), "CLI observes missing final SMTP acknowledgement")
            require(uncertain["state"] == "retrying" and uncertain["possibleDuplicate"] and uncertain["attempts"] >= 1,
                    "lost SMTP acknowledgement was called delivered or exactly once")
            drop_reply.unlink()
            final_ambiguous = ctl("email", "delivery", "wait", ambiguous["id"], "--timeout", "60s")["data"]
            require(final_ambiguous["state"] == "accepted" and final_ambiguous["possibleDuplicate"] and
                    final_ambiguous["attempts"] >= 2 and len(list(sink.glob("*.eml"))) >= before_ambiguous + 2,
                    "accepted retry erased possible duplicate evidence")
            checks.append("agctl: lost SMTP final reply preserves outcome_unknown/retrying and possibleDuplicate through accepted retry; duplicate MIME is observed")

            # No clock injection: use the production 15s scheduler cadence,
            # real logical capacity, and an actual sample-age gap above 30s.
            for baseline_rule in (missing_rule, good_rule):
                ctl("quota-alert", "update", baseline_rule["id"], "--enabled=false")
            quota_repo = ctl("repo", "create", "owned-agctl-timeline", "--format", "raw")["data"]
            api("PUT", "/api/v2/repositories/" + quota_repo["id"] + "/capacity", {"quotaBytes": 1000})
            api("PUT", "/raw/owned-agctl-timeline/synthetic.txt", b"x" * 900, 201, {"Content-Type": "application/octet-stream"})
            timeline_policy = policy_args[:-1] + ["30s"]
            timeline_rule = ctl("quota-alert", "create", "--repo", quota_repo["id"], "--target", target["id"], *timeline_policy)["data"]
            require(not timeline_rule["enabled"] and not ctl("quota-alert", "events", timeline_rule["id"])["data"]["items"],
                    "disabled timeline rule emitted an event")
            ctl("quota-alert", "update", timeline_rule["id"], "--enabled=true", "--yes")
            start("gateway-scheduler")

            def timeline_events():
                return ctl("quota-alert", "events", timeline_rule["id"])["data"]["items"]

            def scenario_record(scenario):
                return next((event for event in timeline_events() if event["scenario"] == scenario), None)

            warning = eventually(lambda: scenario_record("warning"), "CLI quota warning at production cadence")
            ctl("email", "delivery", "wait", warning["deliveryId"], "--timeout", "30s")
            api("PUT", "/raw/owned-agctl-timeline/critical.txt", b"x" * 80, 201, {"Content-Type": "application/octet-stream"})
            critical = eventually(lambda: scenario_record("critical"), "CLI quota critical at production cadence")
            ctl("email", "delivery", "wait", critical["deliveryId"], "--timeout", "30s")
            run(compose + ["stop", "gateway-scheduler"])
            stale = eventually(lambda: (r if (r := ctl("quota-alert", "show", timeline_rule["id"])["data"])["state"]["dataState"] == "stale" else None), "real quota sample expires after scheduler stops", 45)
            require(stale["state"]["severity"] == "critical" and stale["state"]["phase"] == "firing" and
                    len(timeline_events()) == 2, "stale capacity was converted to healthy or resolved")
            listed_stale = next(row for row in ctl("quota-alert", "list")["data"]["items"] if row["id"] == timeline_rule["id"])
            require(listed_stale["state"] == stale["state"], "rule list disagrees with stale show")
            # Raising quota resets continuity; delete one actual artifact instead
            # so recovery follows the same quota and actual logical usage.
            api("DELETE", "/raw/owned-agctl-timeline/critical.txt", status=204)
            api("DELETE", "/raw/owned-agctl-timeline/synthetic.txt", status=204)
            start("gateway-scheduler")
            resolved = eventually(lambda: scenario_record("resolved"), "CLI quota resolves after fresh recovery samples")
            ctl("email", "delivery", "wait", resolved["deliveryId"], "--timeout", "30s")
            ordered = sorted(timeline_events(), key=lambda row: row["sequence"])
            require([row["scenario"] for row in ordered] == ["warning", "critical", "resolved"] and
                    [row["usedBytes"] for row in ordered] == [900, 980, 0] and
                    len({row["episodeId"] for row in ordered}) == 1 and
                    all(row["notificationCode"] == "queued" and row["deliveryState"] == "accepted" for row in ordered),
                    "quota events changed ordering, episode, usage or actual SMTP results")
            ctl("quota-alert", "update", timeline_rule["id"], "--enabled=false")
            checks.append("agctl: real warning→critical→resolved episode and accepted deliveries; stopped scheduler becomes stale without false recovery; fresh samples recover at unchanged 15s cadence")

            # A rule pins its target version, even when that target is edited.
            run(compose + ["stop", "gateway-scheduler"])
            binding_repo = ctl("repo", "create", "owned-agctl-binding", "--format", "raw")["data"]
            api("PUT", "/api/v2/repositories/" + binding_repo["id"] + "/capacity", {"quotaBytes": 1000})
            api("PUT", "/raw/owned-agctl-binding/synthetic.txt", b"x" * 900, 201, {"Content-Type": "application/octet-stream"})
            pinned = ctl("quota-alert", "create", "--repo", binding_repo["id"], "--target", agctl_target["id"], *timeline_policy)["data"]
            pinned = ctl("quota-alert", "update", pinned["id"], "--enabled=true", "--yes")["data"]
            updated_target = ctl("email", "target", "update", agctl_target["id"], "--name", "new bound target version")["data"]
            before_binding = len(ctl("email", "delivery", "list", "--limit", "100")["data"]["items"])
            start("gateway-scheduler")
            suppressed = eventually(lambda: ctl("quota-alert", "events", pinned["id"])["data"]["items"], "old target binding suppresses quota mail")
            require(suppressed[0]["notificationCode"] == "target_changed" and not suppressed[0].get("deliveryId") and
                    suppressed[0]["targetVersion"] == pinned["targetVersion"] and
                    len(ctl("email", "delivery", "list", "--limit", "100")["data"]["items"]) == before_binding,
                    "old target binding silently followed a newer address/version")
            rebound = ctl("quota-alert", "update", pinned["id"], "--target", agctl_target["id"], "--enabled=false")["data"]
            require(rebound["targetVersion"] == updated_target["version"], "explicit rule update did not rebind the current target version")
            checks.append("agctl: old target binding persists target_changed without a delivery; only explicit rule update binds the newer version")

            return {"status": "passed", "checks": checks, "runtime": True,
                    "acceptedMIME": len(list(sink.glob("*.eml"))), "permanentRejection": attempts().count("550"),
                    "cliCases": sum(1 for _ in (evidence / "cli-cases.jsonl").open()) if evidence else None,
                    "limits": "owned internal-network SMTP only; no real relay, client inbox or cross-node readiness guarantee"}
        finally:
            failed = sys.exc_info()[0] is not None
            cleanup_errors = []
            if started:
                if failed and evidence:
                    try:
                        logs = run(compose + ["logs", "--no-color"])
                        for value in private_values:
                            logs = logs.replace(value, "[fixture-secret]")
                        (evidence / "fixture-failure.log").write_text(logs)
                    except RuntimeError as error:
                        cleanup_errors.append(str(error))
                for command in (["docker", "rm", "-f", project + "-second-worker"],) if second_worker else ():
                    try:
                        run(command)
                    except RuntimeError as error:
                        cleanup_errors.append(str(error))
                try:
                    run(compose + ["down", "-v", "--remove-orphans"])
                except RuntimeError as error:
                    cleanup_errors.append(str(error))
            removed_image = subprocess.run(["docker", "image", "rm", image], cwd=ROOT, env=env, capture_output=True)
            image_absent = subprocess.run(["docker", "image", "inspect", image], cwd=ROOT, env=env, capture_output=True).returncode != 0
            remaining = {}
            for resource, operation in (("containers", ["container", "ls", "-aq"]),
                                         ("volumes", ["volume", "ls", "-q"]),
                                         ("networks", ["network", "ls", "-q"])):
                remaining[resource] = run(["docker", *operation, "--filter", "label=com.docker.compose.project=" + project]).splitlines()
            cleanup = {"project": project, "remaining": remaining, "errors": cleanup_errors,
                       "imageRemoved": removed_image.returncode == 0,
                       "imageAbsent": image_absent,
                       "status": "passed" if image_absent and not cleanup_errors and not any(remaining.values()) else "failed"}
            if evidence:
                (evidence / "cleanup.json").write_text(json.dumps(cleanup, indent=2) + "\n")
            if not failed:
                require(cleanup["status"] == "passed", "owned email fixture cleanup failed")


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--render-only", action="store_true")
    parser.add_argument("--evidence-dir", type=Path,
                        help="new directory for secret-checked CLI cases, source hashes and owned cleanup evidence")
    parser.add_argument("--fixture-subnets", nargs=2, type=ipaddress.ip_network, metavar=("INTERNAL", "INGRESS"),
                        help="optional non-overlapping private IPv4 subnets for only this disposable fixture")
    options = parser.parse_args()
    report = check(options.render_only, options.evidence_dir, options.fixture_subnets)
    if options.evidence_dir:
        (options.evidence_dir / "report.json").write_text(json.dumps(report, indent=2) + "\n")
    print(json.dumps(report, indent=2))
