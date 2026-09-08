#!/usr/bin/env python3
"""HTTP-only Group fixture for the isolated backup/restore rehearsal."""

import argparse
import base64
import hashlib
import json
import os
from pathlib import Path
import urllib.error
import urllib.parse
import urllib.request
import uuid


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("action", choices=("seed", "mutate", "verify"))
    parser.add_argument("url")
    parser.add_argument("state", type=Path)
    args = parser.parse_args()
    admin = "Bearer " + os.environ["GATEWAY_ADMIN_TOKEN"]

    def request(
        method, path, body=None, *, authorization=admin, status=200, headers=None
    ):
        metadata = {"Idempotency-Key": str(uuid.uuid4()), **(headers or {})}
        if authorization:
            metadata["Authorization"] = authorization
        if isinstance(body, (dict, list)):
            metadata["Content-Type"] = "application/json"
            body = json.dumps(body).encode()
        req = urllib.request.Request(
            args.url.rstrip("/") + path, data=body, method=method, headers=metadata
        )
        try:
            response = urllib.request.urlopen(req, timeout=20)
        except urllib.error.HTTPError as error:
            response = error
        with response:
            payload = response.read()
            assert (
                response.status == status
            ), f"{method} {path}: HTTP {response.status}, expected {status}: {payload!r}"
            if response.headers.get_content_type() == "application/json":
                return json.loads(payload)
            return payload

    if args.action == "seed":
        suffix = uuid.uuid4().hex[:12]
        repos = []
        for index in range(2):
            repo = request(
                "POST",
                "/api/v2/repositories",
                {"name": f"group-restore-{suffix}-{index}", "format": "raw"},
                status=201,
            )
            repo["body"] = f"Group recovery source {index}\n"
            repo["digest"] = (
                "sha256:" + hashlib.sha256(repo["body"].encode()).hexdigest()
            )
            request(
                "PUT",
                f'/raw/{repo["name"]}/releases/candidate.txt',
                repo["body"].encode(),
                status=201,
            )
            repo["grants"] = [
                {"principal": "group-recovery-reader", "scopes": ["repositories:read"]}
            ]
            request(
                "PUT",
                f'/api/v2/repositories/{repo["id"]}/grants',
                repo["grants"],
                headers={"If-Match": "1"},
            )
            repos.append(repo)
        group = request(
            "POST",
            "/api/v2/groups",
            {
                "name": f"group-restore-{suffix}",
                "format": "raw",
                "members": [
                    {"repositoryId": repo["id"], "position": index}
                    for index, repo in enumerate(repos)
                ],
            },
            status=201,
        )
        args.state.write_text(
            json.dumps({"repositories": repos, "group": group}, indent=2) + "\n"
        )
        print("Seeded two conflicting Raw Group sources with explicit reader grants.")
        return

    state = json.loads(args.state.read_text())
    repos, group = state["repositories"], state["group"]
    if args.action == "mutate":
        request(
            "PUT",
            f'/api/v2/repositories/{repos[0]["id"]}/grants',
            [],
            headers={"If-Match": "2"},
        )
        request(
            "PUT",
            f'/api/v2/groups/{group["id"]}/members',
            [
                {"repositoryId": repo["id"], "position": index}
                for index, repo in enumerate(reversed(repos))
            ],
            headers={"If-Match": group["version"]},
        )
        request(
            "PUT",
            f'/raw/{repos[0]["name"]}/releases/after-backup.txt',
            b"after snapshot",
            status=201,
        )
        print(
            "Mutated Group order, reader grants, and Raw publication after the backup."
        )
        return

    def token(actor):
        credentials = base64.b64encode(
            f'{actor}:{os.environ["GATEWAY_RESOLVER_TOKEN"]}'.encode()
        ).decode()
        return (
            "Bearer "
            + request("GET", "/auth/token", authorization="Basic " + credentials)[
                "token"
            ]
        )

    reader, denied = token("group-recovery-reader"), token("group-recovery-denied")
    restored = request("GET", f'/api/v2/groups/{group["id"]}')
    assert (
        restored["members"] == group["members"]
    ), "Restored Group member order changed"
    for repo in repos:
        assert (
            request("GET", f'/api/v2/repositories/{repo["id"]}/grants')
            == repo["grants"]
        ), "Restored reader grants changed"
    path = f'/raw/{group["name"]}/releases/candidate.txt'
    assert request("GET", path, authorization=reader) == repos[0]["body"].encode()
    request("GET", path, authorization=denied, status=403)
    request("GET", path, authorization=None, status=401)
    request("GET", f'/raw/{repos[0]["name"]}/releases/after-backup.txt', status=404)
    browse = f'/api/v2/groups/{group["id"]}/browse'
    root = request("GET", browse, authorization=reader)
    assert [item["repositoryId"] for item in root["candidates"]] == [
        repo["id"] for repo in repos
    ]
    assert len(root["items"]) == 1 and root["items"][0]["name"] == "releases"
    query = urllib.parse.urlencode({"parent": root["items"][0]["id"], "pageSize": 1})
    leaf = request("GET", browse + "?" + query, authorization=reader)["items"][0]
    assert leaf["name"] == "candidate.txt" and "digest" not in leaf
    assert [
        (source["repositoryId"], source["digest"]) for source in leaf["sources"]
    ] == [(repo["id"], repo["digest"]) for repo in repos]
    request("GET", browse, authorization=denied, status=403)
    request("GET", browse, authorization=None, status=401)
    print(
        "Group recovery verified: member order, both source digests, reader allow/deny, anonymous denial, and removal of post-backup publication."
    )


if __name__ == "__main__":
    main()
