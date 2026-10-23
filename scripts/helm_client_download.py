"""Download one fixed Helm archive in strictly checked, bounded HTTPS ranges."""

import hashlib
import pathlib
import re
import subprocess
import tempfile


def _curl_range(url, start, end):
    with tempfile.TemporaryDirectory(prefix="ag-helm-range-") as work:
        headers = pathlib.Path(work) / "headers"
        body = pathlib.Path(work) / "part"
        result = subprocess.run(
            ["curl", "--disable", "--fail", "--silent", "--show-error",
             "--proto", "=https", "--proto-redir", "=https", "--location",
             "--max-redirs", "3", "--tlsv1.2", "--connect-timeout", "10",
             "--max-time", "45", "--retry", "0", "--range", f"{start}-{end}",
             "--dump-header", str(headers), "--write-out", "%{http_code}",
             url, "-o", str(body)],
            check=True, capture_output=True, text=True, timeout=47,
        )
        # Ignore preceding CONNECT/redirect headers. The final HTTP response
        # must agree with curl's independently reported final status.
        blocks = re.split(r"\r?\n\r?\n", headers.read_text())
        blocks = [block for block in blocks if block.startswith("HTTP/")]
        if not blocks:
            raise RuntimeError("Helm archive response headers are absent")
        lines = blocks[-1].splitlines()
        values = {}
        for line in lines[1:]:
            if ":" not in line:
                raise RuntimeError("Malformed Helm archive response header")
            key, value = line.split(":", 1)
            values.setdefault(key.lower(), []).append(value.strip())
        if len(lines[0].split()) < 2 or lines[0].split()[1] != result.stdout:
            raise RuntimeError("Helm archive response status differs")
        return int(result.stdout), values, body.read_bytes()


def download_archive(url, destination, expected_sha256, *, fetch_range=None,
                     part_bytes=1 << 20, max_bytes=64 << 20):
    """Require complete ordered 206 ranges and the original pinned whole SHA.

    fetch_range is an explicit transport seam for offline control tests. Each
    production request has curl's 45-second total deadline and no retry. Small
    ranges keep that bound practical for slower official archive connections.
    """
    fetch_range = fetch_range or _curl_range
    if not url.startswith("https://") or not re.fullmatch(r"[0-9a-f]{64}", expected_sha256) or not 1 <= part_bytes <= max_bytes:
        raise RuntimeError("Invalid fixed Helm archive identity")
    destination = pathlib.Path(destination)
    parts, offset, total, created = [], 0, None, False
    try:
        with destination.open("xb") as output:
            created = True
            while total is None or offset < total:
                end = offset + part_bytes - 1
                if total is not None:
                    end = min(end, total - 1)
                status, headers, body = fetch_range(url, offset, end)
                ranges, lengths = headers.get("content-range", []), headers.get("content-length", [])
                encodings = headers.get("content-encoding", [])
                match = re.fullmatch(r"bytes ([0-9]+)-([0-9]+)/([0-9]+)", ranges[0]) if len(ranges) == 1 else None
                if status != 206 or not match or len(lengths) != 1 or not re.fullmatch(r"[0-9]+", lengths[0]) or encodings not in ([], ["identity"]):
                    raise RuntimeError("Helm archive requires one exact unencoded partial response")
                first, last, declared_total = map(int, match.groups())
                if not 0 < declared_total <= max_bytes or total is not None and declared_total != total:
                    raise RuntimeError("Helm archive range total changed or exceeded its bound")
                total = declared_total
                expected_end = min(end, total - 1)
                if first != offset or last != expected_end or len(body) != last - first + 1 or int(lengths[0]) != len(body):
                    raise RuntimeError("Helm archive range is overlapping, missing or truncated")
                output.write(body)
                parts.append({"start": first, "end": last, "total": total,
                              "bytes": len(body), "sha256": hashlib.sha256(body).hexdigest()})
                offset = last + 1
        digest = hashlib.sha256(destination.read_bytes()).hexdigest()
        if digest != expected_sha256:
            raise RuntimeError("Official Helm archive differs from reviewed fixed SHA-256")
        return {"mode": "ordered-https-ranges", "requestDeadlineSeconds": 45,
                "retries": 0, "bytes": total, "sha256": digest, "parts": parts}
    except BaseException:
        if created:
            destination.unlink(missing_ok=True)
        raise
