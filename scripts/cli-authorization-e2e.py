#!/usr/bin/env python3
"""Verify #134 catalogue CRUD through the real agctl and an isolated Gateway."""

import datetime
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile


binary, suffix, repository_id = sys.argv[1:]
server = os.environ["CLI_E2E_SERVER"]
admin_token = os.environ["CLI_E2E_TOKEN"]
owned_project = os.environ.get("CLI_E2E_OWNED_PROJECT", "")
started = datetime.datetime.now(datetime.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")
secrets = [admin_token]
owned = []
calls = 0


with tempfile.TemporaryDirectory(prefix="agctl-authorization-") as directory:
    private = Path(directory)
    environment = os.environ.copy()
    environment.update(HOME=directory, AGCTL_SERVER=server, AGCTL_TOKEN=admin_token, AGCTL_PROFILE="")

    def cli(*args, want=0, token=admin_token, stdin=None):
        global calls
        calls += 1
        env = environment | {"AGCTL_TOKEN": token}
        result = subprocess.run([binary, *args, "--json"], env=env, input=stdin or "", capture_output=True, text=True, timeout=45, check=False)
        output = result.stdout + result.stderr
        if any(secret and secret in output for secret in secrets):
            raise AssertionError("a synthetic secret reached CLI output")
        document = json.loads(result.stdout)
        assert document["schemaVersion"] == 1 and document["ok"] == (want == 0), (args[:2], document)
        assert result.returncode == want, (args[:2], result.returncode, want, document)
        return document

    def data(*args, **kwargs):
        return cli(*args, **kwargs)["data"]

    def grants_file(name, grants):
        path = private / name
        path.write_text(json.dumps(grants), encoding="utf-8")
        return str(path)

    def mutations(operation, entry_id):
        records = data("audit", "list", "--operation", operation, "--from", started, "--all")["items"]
        return [record for record in records if record.get("resource", "").endswith("/" + entry_id)]

    original_grants = data("grant", "list", "--repo", repository_id)
    existing_admins = data("user", "list", "--role", "admin", "--state", "active")["items"]
    grant = {"principal": "service-account:cli-catalogue-fixture", "scopes": ["repositories:read"], "resourcePrefix": "releases/"}
    populated = grants_file("grants.json", [grant])
    empty = grants_file("empty.json", [])
    catalogues = {}
    teardown_admin = None
    teardown_admin_session = None
    try:
        for kind in ("role", "template"):
            name = f"cli-e2e-catalogue-{kind}-{suffix}"
            contents = ("--scope", "repositories:read") if kind == "role" else ("--grants-file", populated)
            before = data(kind, "list")["items"]
            plan = data(kind, "create", name, *contents, "--dry-run")
            assert plan["method"] == "POST" and plan["mode"] == "client-plan"
            assert data(kind, "list")["items"] == before
            created = data(kind, "create", name, "--description", "initial", *contents)
            entry_id = created["id"]
            owned.append((kind, entry_id))
            catalogues[kind] = entry_id
            assert created["version"] == "1" and created["name"] == name
            cli(kind, "create", name, *contents, want=7)
            for builtin in ("none", "member", "admin"):
                cli(kind, "delete", builtin, "--yes", want=2)
                cli(kind, "update", builtin, "--name", "changed", "--yes", want=2)
            cli(kind, "update", entry_id, "--description", "changed", want=2)
            cli(kind, "delete", entry_id, want=2)
            plan = data(kind, "update", entry_id, "--description", "changed", "--dry-run")
            assert plan["headers"]["If-Match"] == "1"
            assert plan["current"]["diff"]["before"]["description"] == "initial"
            assert plan["current"]["diff"]["after"]["description"] == "changed"
            assert data(kind, "show", entry_id) == created
            updated = data(kind, "update", entry_id, "--description", "changed", "--yes")
            assert updated["version"] == "2"
            field = "scopes" if kind == "role" else "grants"
            assert updated[field] == created[field], (kind, "unspecified content was lost")
            replacement = ("--scope", "repositories:write", "--scope", "repositories:intelligence") if kind == "role" else ("--grants-file", empty)
            updated = data(kind, "update", entry_id, "--description=", *replacement, "--yes")
            assert updated["version"] == "3" and updated.get("description", "") == ""
            assert updated[field] == (["repositories:write", "repositories:intelligence"] if kind == "role" else [])
            conflict = cli(kind, "update", entry_id, "--name", "stale", "--if-match", "1", "--yes", want=7)
            assert conflict["error"]["httpStatus"] == 412
            assert conflict["data"]["current"] == updated and conflict["data"]["proposed"]["name"] == "stale"
            assert data(kind, "show", entry_id) == updated
            plan = data(kind, "delete", entry_id, "--dry-run")
            assert any("no If-Match support" in value for value in plan["notVerified"])
            assert data(kind, "show", entry_id) == updated
            assert len(mutations(f"authorization_{kind}.create", entry_id)) == 1
            assert len(mutations(f"authorization_{kind}.update", entry_id)) == 2
            assert len(mutations(f"authorization_{kind}.delete", entry_id)) == 0

        # Real pending/member users and an administrator required to change
        # their password must all be rejected, without changing either entry.
        password = f"Synthetic-cli-catalogue-{suffix}-password"
        secrets.append(password)
        password_file = private / "password"
        password_file.write_text(password + "\n", encoding="utf-8")
        password_file.chmod(0o600)
        # An empty isolated database must keep its last local administrator.
        # It is removed with that fixture's volume after its sessions are
        # revoked here. On a reused Gateway without a local admin, use a
        # password-change member instead so this script cannot leave an
        # undeletable account behind.
        password_change_role = "admin" if existing_admins or owned_project else "member"
        for label, role, must_change in (("pending", "none", False), ("member", "member", False), ("password-change", password_change_role, True)):
            username = f"cli-e2e-catalogue-{label}-{suffix}"
            user = data("user", "create", username, "--role", role, "--password-file", str(password_file), f"--must-change-password={str(must_change).lower()}")
            owned.append(("user", user["id"]))
            if role == "admin" and not existing_admins:
                teardown_admin = user["id"]
            session_file = private / f"{label}-session"
            data("auth", "login", "--username", username, "--password-stdin", "--secret-output", str(session_file), stdin=password + "\n")
            session = session_file.read_text(encoding="utf-8").strip()
            secrets.append(session)
            if user["id"] == teardown_admin:
                teardown_admin_session = session
            if role == "member" and not must_change:
                assert data("auth", "whoami", token=session)["role"] == "member"
            snapshots = {kind: data(kind, "show", entry_id) for kind, entry_id in catalogues.items()}
            for kind, entry_id in catalogues.items():
                contents = ("--scope", "repositories:read") if kind == "role" else ("--grants-file", populated)
                for args in (
                    (kind, "create", f"denied-{kind}-{suffix}", *contents),
                    (kind, "update", entry_id, "--description", "denied", "--yes"),
                    (kind, "delete", entry_id, "--yes"),
                ):
                    refused = cli(*args, token=session, want=6)
                    assert refused["error"]["httpStatus"] == 403
                    if must_change:
                        assert refused["error"]["code"] == "password_change_required"
                assert data(kind, "show", entry_id) == snapshots[kind]
            records = data("audit", "list", "--actor", "user:" + username, "--from", started, "--all")["items"]
            assert not any(record.get("operation", "").startswith(("authorization_role.", "authorization_template.")) for record in records)
            if role == "member" and not must_change:
                assert data("auth", "whoami", token=session)["role"] == "member"
        assert data("grant", "list", "--repo", repository_id) == original_grants
    finally:
        # Delete only IDs created by this invocation, including their sessions.
        for kind, entry_id in reversed(owned):
            if kind == "user" and entry_id == teardown_admin:
                refused = cli("user", "delete", entry_id, "--yes", want=7)
                assert refused["error"]["code"] == "last_admin" and owned_project
                data("user", "sessions", "revoke", entry_id, "--all", "--yes")
                cli("auth", "whoami", token=teardown_admin_session, want=5)
                continue
            data(kind, "delete", entry_id, "--yes")
            cli(kind, "show", entry_id, want=4)
            if kind != "user":
                assert len(mutations(f"authorization_{kind}.delete", entry_id)) == 1
    assert data("grant", "list", "--repo", repository_id) == original_grants
    print(json.dumps({"scenario": "authorization-catalogue-crud", "cliCalls": calls, "roleTemplateCRUD": "passed", "casReadBack": "passed", "dryRunAndConfirmation": "passed", "memberPendingPasswordChangeRefused": "passed", "passwordChangeRole": password_change_role, "passwordChangeAdmin": "passed" if password_change_role == "admin" else "not-run: reused Gateway has no existing local administrator", "permissionRefusalExitCode": 6, "casConflictExitCode": 7, "missingConfirmationExitCode": 2, "deleteNotFoundExitCode": 4, "existingGrantsUnchanged": True, "catalogueEntriesDeleted": True, "userCleanup": "last-admin user via owned Compose volume teardown; its sessions revoked and refused" if teardown_admin else "all owned users and sessions deleted", "ownedProject": owned_project or None, "privateFilesRemovedOnExit": True}))
