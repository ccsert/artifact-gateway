#!/usr/bin/env python3
"""Exercise #131's Conan views and tombstone through the real agctl binary.

Called by cli-e2e.sh with its isolated Gateway and synthetic credentials.
Publication seeds only this fixture through the supported session API; all
views, authorization checks and deletion under test run through agctl.
"""

import hashlib
import json
import os
import subprocess
import sys
import urllib.error
import urllib.request
import uuid


binary = sys.argv[1]
server = os.environ["AGCTL_SERVER"].rstrip("/")
admin_token = os.environ["AGCTL_TOKEN"]
resolver_token = os.environ.get("CLI_CONAN_E2E_RESOLVER_TOKEN", "")


def cli(*args, want=0, token=None):
    env = dict(os.environ, AGCTL_PROFILE="", AGCTL_TOKEN=token or admin_token)
    result = subprocess.run(
        [binary, *args, "--json"], env=env, stdin=subprocess.DEVNULL,
        capture_output=True, text=True, timeout=45,
    )
    if result.returncode != want:
        raise AssertionError(f"agctl {args}: exit {result.returncode}, expected {want}: {result.stdout} {result.stderr}")
    value = json.loads(result.stdout)
    assert value["schemaVersion"] == 1
    assert value["ok"] == (want == 0)
    return value


def request(method, path, body=None, status=200):
    headers = {"Authorization": "Bearer " + admin_token}
    if isinstance(body, dict):
        body = json.dumps(body).encode()
        headers["Content-Type"] = "application/json"
    req = urllib.request.Request(server + path, data=body, headers=headers, method=method)
    try:
        response = urllib.request.urlopen(req, timeout=20)
    except urllib.error.HTTPError as error:
        response = error
    with response:
        payload = response.read()
        assert response.status == status, f"{method} {path}: HTTP {response.status}, expected {status}"
        return payload


def digest(payload):
    return "sha256:" + hashlib.sha256(payload).hexdigest()


def publish(repo, reference, recipe_revision, package_id="", package_revision=""):
    name = "conaninfo.txt" if package_id else "conanfile.py"
    payload = f"agctl conan fixture {reference} {recipe_revision} {package_id} {package_revision}\n".encode()
    declaration = {"kind": "package" if package_id else "recipe", "reference": reference,
                   "recipeRevision": recipe_revision,
                   "objects": [{"name": name, "digest": digest(payload), "size": len(payload)}]}
    coordinate = reference + "#" + recipe_revision
    if package_id:
        declaration.update(packageId=package_id, packageRevision=package_revision)
        coordinate += "/" + package_id + "#" + package_revision
    session = json.loads(request("POST", f"/api/v2/repositories/{repo}/conan-publish-sessions", declaration, 201))
    assert session["repositoryId"] == repo and session["coordinate"] == coordinate
    assert session["objects"] == declaration["objects"]
    session_id = session["id"]
    request("PUT", f"/api/v2/conan-publish-sessions/{session_id}/objects/{name}", payload, 204)
    committed = json.loads(request("POST", f"/api/v2/conan-publish-sessions/{session_id}:commit", status=200))
    assert committed == {"state": "visible", "revision": recipe_revision}
    expected_digest = digest((name + "\0" + digest(payload) + "\0").encode())
    native = f"/conan/v2/{repo_name}/conans/{reference}/revisions/{recipe_revision}"
    if package_id:
        native += f"/packages/{package_id}/revisions/{package_revision}"
    native += "/files/" + name
    downloaded = request("GET", native)
    assert downloaded == payload and len(downloaded) == len(payload) and digest(downloaded) == digest(payload)
    return native, payload, expected_digest


def scope_args(command, package=False):
    args = ["conan", command, "--repo", repo_id, "--reference", reference, "--recipe-revision", "r1"]
    if package:
        args.extend(["--package-id", "pkg1"])
    return args


repo_name = "cli-conan-" + uuid.uuid4().hex[:12]
repo_id = cli("repo", "create", repo_name, "--format", "conan")["data"]["id"]
reference = "widget/1.0/user/stable"
try:
    fixtures = {}
    for ref, rrev in [(reference, "r1"), (reference, "r2"), ("widget/2.0/user/stable", "r1")]:
        fixtures[(ref, rrev)] = publish(repo_id, ref, rrev)
    packages = {}
    for pkg, prev in [("pkg1", "p1"), ("pkg1", "p2"), ("pkg2", "p3")]:
        packages[(pkg, prev)] = publish(repo_id, reference, "r1", pkg, prev)

    refs = cli("conan", "references", "--repo", repo_id, "--q", "widget/", "--page-size", "1")
    assert [item["reference"] for item in refs["data"]["items"]] == [reference]
    assert refs["data"]["nextCursor"] and not refs["meta"]["pagination"]["complete"]
    next_refs = cli("conan", "references", "--repo", repo_id, "--q", "widget/", "--page-size", "1", "--cursor", refs["data"]["nextCursor"])
    assert [item["reference"] for item in next_refs["data"]["items"]] == ["widget/2.0/user/stable"]
    assert next_refs["data"]["nextCursor"] is None
    partial = cli("conan", "references", "--repo", repo_id, "--q", "widget/", "--page-size", "1", "--all", "--max-pages", "1", want=3)
    assert partial["data"]["nextCursor"] and len(partial["data"]["items"]) == 1
    all_refs = cli("conan", "references", "--repo", repo_id, "--page-size", "1", "--all")
    assert [item["reference"] for item in all_refs["data"]["items"]] == [reference, "widget/2.0/user/stable"]
    assert all_refs["meta"]["pagination"]["pages"] == 2

    revisions = cli("conan", "recipe-revisions", "--repo", repo_id, "--reference", reference, "--page-size", "1", "--all")
    assert [(item["revision"], item["digest"], item["state"]) for item in revisions["data"]["items"]] == [
        (rrev, fixtures[(reference, rrev)][2], "visible") for rrev in ["r1", "r2"]]
    assert revisions["meta"]["scope"]["reference"] == reference
    filtered = cli("conan", "recipe-revisions", "--repo", repo_id, "--reference", reference, "--q", "r2")
    assert [item["revision"] for item in filtered["data"]["items"]] == ["r2"]

    ids = cli(*scope_args("package-ids"))
    assert ids["data"]["items"] == ["pkg1", "pkg2"] and ids["data"]["nextCursor"] is None
    assert ids["meta"]["pagination"]["mode"] == "none"
    package_args = scope_args("package-revisions", package=True)
    listed = cli(*package_args)
    assert sorted((item["revision"], item["digest"], item["state"]) for item in listed["data"]["items"]) == [
        (prev, packages[("pkg1", prev)][2], "visible") for prev in ["p1", "p2"]]
    assert listed["meta"]["scope"]["packageId"] == "pkg1"

    delete_args = ["conan", "package-revision", "delete", *package_args[2:], "--revision", "p1"]
    cli(*delete_args, want=2)
    plan = cli(*delete_args, "--dry-run")
    assert plan["data"]["mode"] == "client-plan"
    assert plan["data"]["current"]["digest"] == packages[("pkg1", "p1")][2]
    assert len(cli(*package_args)["data"]["items"]) == 2

    if resolver_token:
        for args in [["conan", "references", "--repo", repo_id],
                     ["conan", "recipe-revisions", "--repo", repo_id, "--reference", reference],
                     scope_args("package-ids"), package_args, [*delete_args, "--yes"]]:
            denied = cli(*args, want=6, token=resolver_token)
            assert denied["error"]["httpStatus"] == 403
        assert len(cli(*package_args)["data"]["items"]) == 2
    missing = cli("conan", "references", "--repo", "00000000-0000-4000-8000-000000000000", want=4)
    assert missing["error"]["httpStatus"] == 404

    deleted = cli(*delete_args, "--yes")["data"]
    assert deleted == {"repositoryId": repo_id, "reference": reference, "recipeRevision": "r1",
                       "packageId": "pkg1", "revision": "p1", "status": "tombstoned", "visibility": "not_listed"}
    assert [item["revision"] for item in cli(*package_args)["data"]["items"]] == ["p2"]
    assert cli(*delete_args, "--yes")["data"] == deleted
    cli(*[*delete_args[:-1], "missing", "--yes"], want=4)
    request("GET", packages[("pkg1", "p1")][0], status=404)
    for native, payload, _ in [*fixtures.values(), packages[("pkg1", "p2")], packages[("pkg2", "p3")]]:
        actual = request("GET", native)
        assert actual == payload and len(actual) == len(payload) and digest(actual) == digest(payload)
    audits = cli("audit", "list", "--repository", repo_name, "--operation", "conan.package_revision.tombstone", "--all")["data"]["items"]
    coordinate = reference + "#r1/pkg1#p1"
    assert any(item["resource"] == coordinate and item["status"] == 204 and item["outcome"] == "resolved" for item in audits)
    print("Conan CLI E2E passed: four views, exact cursors/filters, read-back tombstone, repeat/refusals, audit and complete object bytes")
finally:
    cli("repo", "delete", repo_id, "--yes")
