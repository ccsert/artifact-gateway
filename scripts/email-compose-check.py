#!/usr/bin/env python3
"""Render and run the opt-in overlay with disposable PG/S3/TLS, no .env input."""
import argparse
from email import policy
from email.parser import BytesParser
import json
import os
from pathlib import Path
import secrets
import subprocess
import tempfile
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


def check(render_only):
    project = "artifact-gateway-email-check-" + uuid.uuid4().hex[:12]
    # Compose interpolation accepts only these generated values, never checkout
    # .env, exported deployment credentials, roles, ports or COMPOSE_PROJECT_NAME.
    env = {k: os.environ[k] for k in (
        "PATH", "HOME", "DOCKER_HOST", "DOCKER_CONTEXT", "DOCKER_CONFIG"
    ) if k in os.environ}
    checks = []
    http = urllib.request.build_opener(urllib.request.ProxyHandler({}))
    with tempfile.TemporaryDirectory(prefix=project + "-") as temporary:
        work = Path(temporary)
        config, sink = work / "config", work / "sink"
        config.mkdir(mode=0o755)
        sink.mkdir(mode=0o700)
        key = secrets.token_hex(32)
        admin = secrets.token_hex(24)
        auth = {"username": "owned-synthetic", "password": secrets.token_hex(24)}
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
        environment_file = work / "fixture.env"
        environment_file.write_text("".join(k + "=" + v + "\n" for k, v in values.items()))
        environment_file.chmod(0o600)
        base = ["docker", "compose", "--env-file", str(environment_file),
                "--project-name", project, "-f", str(ROOT / "compose.yml")]

        def run(args, expected=0):
            result = subprocess.run(args, cwd=ROOT, env=env, capture_output=True, text=True)
            diagnostic = result.stdout + result.stderr
            for value in (*values.values(), auth["password"]):
                diagnostic = diagnostic.replace(value, "[fixture-value]")
            require(result.returncode == expected,
                    "command failed: " + " ".join(args[:2]) + "\n" + diagnostic[-4000:])
            return result.stdout

        baseline = json.loads(run(base + ["config", "--format", "json"]))
        require("GATEWAY_EMAIL_CONFIG_FILE" not in baseline["services"]["gateway"]["environment"],
                "default Compose unexpectedly enables email")
        require("gateway-email-worker" not in baseline["services"], "default adds worker")
        checks.append("default Compose stays disconnected/disabled")
        compose = base + ["-f", str(ROOT / "compose.email.yml"), "--profile", "email-roles"]
        rendered = json.loads(run(compose + ["config", "--format", "json"]))
        for name, role in (("gateway", "api"), ("gateway-scheduler", "scheduler"),
                           ("gateway-email-worker", "worker")):
            node = rendered["services"][name]
            require(node["environment"]["GATEWAY_NODE_ROLES"] == role, "role drift")
            require(node["environment"]["GATEWAY_SETTINGS_ENCRYPTION_KEY"] == key, "key drift")
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
        if render_only:
            return {"status": "passed", "checks": checks, "runtime": False}

        image = project + ":fixture"
        override = work / "fixture.json"
        nodes = ("gateway", "gateway-scheduler", "gateway-email-worker")
        model = {
            "services": {n: {"image": image, "ports": []} for n in nodes},
            "networks": {"default": {"internal": True}, "ingress": {}},
        }
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
        started = False
        second_worker = False
        base_url = None

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
            api("PUT", "/repository/" + name + "/synthetic.txt", b"x" * 900, 201,
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
            require(api("GET", "/api/v2/email-notifications")["reason"] == "email_disabled", "disabled channel reports ready")
            target = api("POST", "/api/v2/email-targets", {"name": "Owned synthetic", "recipient": "synthetic-recipient@example.test", "locale": "en", "enabled": True}, 201)
            require(explicit(target, status=503)["code"] == "email_disabled", "disabled test accepted")
            require(not attempts(), "disabled channel attempted SMTP")
            checks.append("disabled config refuses explicit test and never contacts SMTP")

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
            require(api("GET", "/api/v2/email-notifications")["reason"] == "ready", "valid API config unavailable")
            pending = explicit(target)
            time.sleep(6)
            require(delivery(pending["id"])["state"] == "pending" and not attempts(), "API-only became sender")
            checks.append("API-only ready queues but has no sender")

            mismatch("gateway-email-worker", {"GATEWAY_EMAIL_CONFIG_FILE": ""})
            time.sleep(6)
            require(delivery(pending["id"])["attempts"] == 0 and not attempts(), "unconfigured worker claimed")
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
            return {"status": "passed", "checks": checks, "runtime": True,
                    "acceptedMIME": 3, "permanentRejection": 1,
                    "limits": "owned internal-network SMTP only; no real relay, client inbox or cross-node readiness guarantee"}
        finally:
            if started:
                if second_worker:
                    run(["docker", "rm", "-f", project + "-second-worker"])
                run(compose + ["down", "-v", "--remove-orphans"])
            subprocess.run(["docker", "image", "rm", image], cwd=ROOT, env=env, capture_output=True)


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--render-only", action="store_true")
    options = parser.parse_args()
    print(json.dumps(check(options.render_only), indent=2))
