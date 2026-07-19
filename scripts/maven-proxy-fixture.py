#!/usr/bin/env python3
"""Small controlled Maven HTTP Proxy fixture for the local E2E contract."""

import argparse
import http.server
import pathlib
import urllib.parse


class Handler(http.server.SimpleHTTPRequestHandler):
    root = "."
    log_file = None
    attempts = {}

    def log_message(self, format, *args):
        pass

    def translate_path(self, path):
        root = pathlib.Path(self.root).resolve()
        parts = pathlib.PurePosixPath(urllib.parse.unquote(urllib.parse.urlparse(path).path)).parts
        candidate = root.joinpath(*(part for part in parts if part != "/"))
        try:
            candidate.resolve().relative_to(root)
        except ValueError:
            return str(root / ".invalid-path")
        return str(candidate)

    def send_response(self, code, message=None):
        self._status = code
        super().send_response(code, message)

    def finish(self):
        try:
            super().finish()
        finally:
            if self.log_file and hasattr(self, "_status"):
                with open(self.log_file, "a", encoding="utf-8") as output:
                    output.write(f"{self.command} {self._request_path} {self._status}\n")

    def do_GET(self):
        self._serve_controlled()

    def do_HEAD(self):
        self._serve_controlled()

    def _serve_controlled(self):
        self._request_path = self.path
        path = urllib.parse.urlparse(self.path).path
        for status in (429, 503):
            prefix = f"/retry/{status}/"
            if path.startswith(prefix):
                count = self.attempts.get(path, 0)
                self.attempts[path] = count + 1
                if count == 0:
                    self.send_response(status)
                    self.end_headers()
                    return
                self.path = path[len(prefix) - 1:]
                break
        return super().do_GET() if self.command == "GET" else super().do_HEAD()


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--port", type=int, required=True)
    parser.add_argument("--directory", required=True)
    parser.add_argument("--log", required=True)
    args = parser.parse_args()
    Handler.root = str(pathlib.Path(args.directory).resolve())
    Handler.log_file = args.log
    with http.server.ThreadingHTTPServer(("0.0.0.0", args.port), Handler) as server:
        server.serve_forever()


if __name__ == "__main__":
    main()
