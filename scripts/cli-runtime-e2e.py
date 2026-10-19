#!/usr/bin/env python3
"""Exercise agctl runtime failure/restart contracts in an owned disposable stack."""
import json
import os
from pathlib import Path
import secrets
import subprocess
import tempfile
import uuid

ROOT = Path(__file__).resolve().parent.parent


def require(condition, message):
    if not condition:
        raise RuntimeError(message)


def check():
    project = "artifact-gateway-cli-runtime-" + uuid.uuid4().hex[:12]
    environment = {k: os.environ[k] for k in (
        "PATH", "HOME", "DOCKER_HOST", "DOCKER_CONTEXT", "DOCKER_CONFIG"
    ) if k in os.environ}
    with tempfile.TemporaryDirectory(prefix=project + "-") as temporary:
        work = Path(temporary)
        values = {
            "GATEWAY_POSTGRES_PASSWORD": secrets.token_hex(24),
            "GATEWAY_ADMIN_TOKEN": secrets.token_hex(24),
            "GATEWAY_RESOLVER_TOKEN": secrets.token_hex(24),
            "RUSTFS_ACCESS_KEY": "owned-" + secrets.token_hex(8),
            "RUSTFS_SECRET_KEY": secrets.token_hex(24),
            "RUSTFS_RPC_SECRET": secrets.token_hex(32),
            "GATEWAY_DATABASE_MAX_OPEN_CONNS": "8",
            "GATEWAY_DATABASE_MAX_IDLE_CONNS": "2",
            "GATEWAY_DATABASE_COORDINATOR_MAX_OPEN_CONNS": "2",
            "GATEWAY_DATABASE_COORDINATOR_MAX_IDLE_CONNS": "1",
            "GATEWAY_DATABASE_ARTIFACT_LOCK_MAX_OPEN_CONNS": "2",
            "GATEWAY_DATABASE_ARTIFACT_LOCK_MAX_IDLE_CONNS": "1",
            "GATEWAY_LOG_BUFFER_LINES": "1000",
        }
        private_values = [values[k] for k in (
            "GATEWAY_POSTGRES_PASSWORD", "GATEWAY_ADMIN_TOKEN", "GATEWAY_RESOLVER_TOKEN",
            "RUSTFS_ACCESS_KEY", "RUSTFS_SECRET_KEY", "RUSTFS_RPC_SECRET")]
        environment_file = work / "fixture.env"

        def write_environment():
            environment_file.write_text("".join(k + "=" + v + "\n" for k, v in values.items()))
            environment_file.chmod(0o600)

        write_environment()
        override = work / "ports.yml"
        override.write_text("services:\n" + "".join(
            "  " + service + ":\n    ports: !override " +
            ('["127.0.0.1::8080"]' if service == "gateway" else "[]") + "\n"
            for service in ("gateway", "postgres", "rustfs")))
        compose = ["docker", "compose", "--env-file", str(environment_file),
                   "--project-name", project, "-f", str(ROOT / "compose.yml"), "-f", str(override)]
        started = False
        base_url = ""
        cli = work / "agctl"

        def run(args):
            result = subprocess.run(args, cwd=ROOT, env=environment, capture_output=True, text=True)
            diagnostic = result.stdout + result.stderr
            for value in private_values:
                diagnostic = diagnostic.replace(value, "[fixture-value]")
            require(result.returncode == 0, "fixture command failed: " + " ".join(args[:2]) + "\n" + diagnostic[-3000:])
            return result.stdout

        def gateway(*extra):
            nonlocal base_url
            run(compose + ["up", "-d", "--no-build", "--wait", "--wait-timeout", "90", *extra, "gateway"])
            base_url = "http://" + run(compose + ["port", "gateway", "8080"]).strip()

        def ctl(*args, expected=0, token=None, stdin=None, stream=False):
            result = subprocess.run([str(cli), *args, "--json"], cwd=work,
                                    input=stdin, capture_output=True, text=True, timeout=45,
                                    env={**environment, "HOME": str(work), "AGCTL_SERVER": base_url,
                                         "AGCTL_TOKEN": token or values["GATEWAY_ADMIN_TOKEN"], "AGCTL_PROFILE": ""})
            require(not any(v in result.stdout + result.stderr for v in private_values), "agctl leaked a synthetic secret")
            require(result.returncode == expected,
                    "agctl " + " ".join(args) + " exited " + str(result.returncode) + ": " + result.stdout[-2000:] + result.stderr[-500:])
            if stream:
                events = [json.loads(line) for line in result.stdout.splitlines()]
                require(events and all(e["schemaVersion"] == 1 for e in events), "invalid NDJSON schema")
                require(sum(e["type"] == "end" for e in events) == 1 and events[-1]["type"] == "end", "stream has no unique terminal end")
                return events
            result = json.loads(result.stdout)
            require(result["schemaVersion"] == 1 and result["ok"] == (expected == 0), "invalid JSON envelope")
            return result

        def readiness(report):
            return next(c["status"] for c in report["checks"] if c["name"] == "readyz")

        try:
            run(["go", "build", "-o", str(cli), "./cmd/agctl"])
            run(compose + ["build", "gateway"])
            started = True
            gateway()
            require(readiness(ctl("status")["data"]) == "healthy", "fresh Gateway is not ready")
            member = values["GATEWAY_RESOLVER_TOKEN"]
            ctl("runtime", "logs", token=member, expected=6)
            limited = ctl("status", token=member)["data"]
            require(limited["overall"] == "unknown", "a member's unread diagnostics became healthy")
            ctl("status", "--strict", token=member, expected=1)
            password = secrets.token_hex(20)
            private_values.append(password)
            password_file = work / "password"
            password_file.write_text(password + "\n")
            password_file.chmod(0o600)
            for role, must_change, code in (("none", False, "authorization_pending"),
                                             ("admin", True, "password_change_required")):
                name = "runtime-" + role
                args = ["user", "create", name, "--role", role, "--password-file", str(password_file)]
                if must_change:
                    args.append("--must-change-password")
                ctl(*args)
                token_file = work / (name + "-session")
                ctl("auth", "login", "--username", name, "--password-stdin", "--secret-output", str(token_file), stdin=password + "\n")
                token = token_file.read_text().strip()
                private_values.append(token)
                denied = ctl("runtime", "logs", token=token, expected=6)
                require(denied["error"]["code"] == code, "account state lost its machine-readable denial")
            before = ctl("runtime", "logs")["meta"]["scope"]
            require(before["scope"] == "local" and before["instanceId"] and before["sessionId"], "runtime scope is incomplete")
            cursor = before["afterCursor"]
            gateway("--no-deps", "--force-recreate")
            after = ctl("runtime", "logs")["meta"]["scope"]
            require(after["instanceId"] == before["instanceId"] and after["sessionId"] != before["sessionId"], "restart retained the old session")
            events = ctl("runtime", "logs", "--after-cursor", cursor, "--follow", "--duration", "3s", expected=7, stream=True)
            require(events[-1]["data"]["reason"] == "scope_changed" and events[-1]["error"]["code"] == "log_cursor_scope_changed", "old cursor was silently reused after restart")
            require(events[-1]["data"]["resume"]["afterCursor"] == cursor, "scope refusal lost its original resume position")

            # Only this fixture's PostgreSQL is stopped; the Gateway remains
            # reachable, so readiness failure must be a health result, not 0.
            run(compose + ["stop", "postgres"])
            unhealthy = ctl("status", "--timeout", "30s", expected=1)
            require(unhealthy["data"]["overall"] == "unhealthy" and readiness(unhealthy["data"]) == "unhealthy", "dependency loss was reported as healthy")
            require(ctl("info")["data"]["readyz"]["status"] == "not_ready", "info hid not-ready state")
            run(compose + ["up", "-d", "--no-build", "--wait", "postgres"])
            require(readiness(ctl("status")["data"]) == "healthy", "restored dependency stayed unhealthy")

            values["GATEWAY_LOG_BUFFER_LINES"] = "0"
            write_environment()
            gateway("--no-deps", "--force-recreate")
            disabled = ctl("runtime", "logs", expected=1)
            require(disabled["error"]["httpStatus"] == 503 and disabled["error"]["code"] == "log_buffer_unavailable", "disabled logs became an empty successful collection")
            require(readiness(ctl("status")["data"]) == "healthy", "disabled optional logs changed readiness")
            return {"status": "passed", "project": project,
                    "checks": ["ready to not-ready to ready", "real restart changes session and refuses old cursor with bounded NDJSON",
                               "disabled runtime logs are 503/log_buffer_unavailable", "member/pending/password-change gates and unknown diagnostics", "versioned JSON, exit codes and synthetic secret redaction"],
                    "limits": "owned Gateway/PostgreSQL/RustFS only; no deployment or production runtime acceptance"}
        finally:
            if started:
                run(compose + ["down", "-v", "--remove-orphans"])


if __name__ == "__main__":
    print(json.dumps(check(), indent=2))
