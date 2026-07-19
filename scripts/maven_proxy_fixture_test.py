#!/usr/bin/env python3
"""Regression tests for the controlled Maven Proxy E2E fixture."""

import importlib.util
import pathlib
import socket
import subprocess
import sys
import tempfile
import threading
import time
import unittest
import urllib.error
import urllib.request


FIXTURE_PATH = pathlib.Path(__file__).with_name("maven-proxy-fixture.py")
SPEC = importlib.util.spec_from_file_location("maven_proxy_fixture", FIXTURE_PATH)
fixture = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(fixture)


class MavenProxyFixtureTest(unittest.TestCase):
    def setUp(self):
        self.tempdir = tempfile.TemporaryDirectory()
        base = pathlib.Path(self.tempdir.name)
        self.root = base / "repository"
        self.root.mkdir()
        (self.root / "artifact.pom").write_text("artifact", encoding="utf-8")
        (base / "secret.txt").write_text("secret", encoding="utf-8")
        self.log = base / "requests.log"
        fixture.Handler.root = str(self.root)
        fixture.Handler.log_file = str(self.log)
        fixture.Handler.attempts = {}
        self.server = fixture.http.server.ThreadingHTTPServer(("127.0.0.1", 0), fixture.Handler)
        self.thread = threading.Thread(target=self.server.serve_forever)
        self.thread.start()
        self.base_url = f"http://127.0.0.1:{self.server.server_port}"

    def tearDown(self):
        self.server.shutdown()
        self.thread.join()
        self.server.server_close()
        self.tempdir.cleanup()

    def test_retry_logs_original_request_target_after_success(self):
        with self.assertRaises(urllib.error.HTTPError) as first:
            urllib.request.urlopen(self.base_url + "/retry/429/artifact.pom")
        self.assertEqual(first.exception.code, 429)
        first.exception.close()
        with urllib.request.urlopen(self.base_url + "/retry/429/artifact.pom") as response:
            self.assertEqual(response.status, 200)
        self.assertIn("GET /retry/429/artifact.pom 200", self.log.read_text(encoding="utf-8"))

    def test_path_traversal_is_not_served(self):
        with self.assertRaises(urllib.error.HTTPError) as response:
            urllib.request.urlopen(self.base_url + "/%2e%2e/secret.txt")
        self.assertEqual(response.exception.code, 404)
        response.exception.close()

    def test_cli_starts_fixture(self):
        with socket.socket() as listener:
            listener.bind(("127.0.0.1", 0))
            port = listener.getsockname()[1]
        process = subprocess.Popen(
            [sys.executable, str(FIXTURE_PATH), "--port", str(port), "--directory", str(self.root), "--log", str(self.log)],
            stdout=subprocess.DEVNULL,
            stderr=subprocess.PIPE,
            text=True,
        )
        try:
            for _ in range(20):
                if process.poll() is not None:
                    self.fail(process.stderr.read())
                try:
                    with urllib.request.urlopen(f"http://127.0.0.1:{port}/artifact.pom") as response:
                        self.assertEqual(response.status, 200)
                    return
                except urllib.error.HTTPError as error:
                    error.close()
                    time.sleep(0.05)
                except urllib.error.URLError:
                    time.sleep(0.05)
            self.fail("fixture did not start")
        finally:
            process.terminate()
            process.wait()
            process.stderr.close()


if __name__ == "__main__":
    unittest.main()
