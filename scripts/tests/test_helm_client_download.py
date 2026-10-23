import hashlib
import pathlib
import sys
import tempfile
import unittest

sys.path.insert(0, str(pathlib.Path(__file__).resolve().parents[1]))
from helm_client_download import download_archive


class HelmClientDownloadTests(unittest.TestCase):
    def test_default_requests_use_one_mebibyte_without_changing_whole_digest(self):
        original = b"a" * ((2 << 20) + 17)
        calls = []

        def fetch(url, start, end):
            calls.append((start, end))
            end = min(end, len(original) - 1)
            body = original[start:end + 1]
            return 206, {"content-range": [f"bytes {start}-{end}/{len(original)}"],
                         "content-length": [str(len(body))]}, body

        with tempfile.TemporaryDirectory() as work:
            path = pathlib.Path(work) / "archive"
            receipt = download_archive("https://owned.invalid/archive", path,
                                       hashlib.sha256(original).hexdigest(), fetch_range=fetch)
            self.assertEqual(calls, [(0, (1 << 20) - 1),
                                     (1 << 20, (2 << 20) - 1),
                                     (2 << 20, len(original) - 1)])
            self.assertEqual(path.read_bytes(), original)
            self.assertEqual(receipt["sha256"], hashlib.sha256(original).hexdigest())
            self.assertEqual(receipt["requestDeadlineSeconds"], 45)
            self.assertEqual(receipt["retries"], 0)

    def test_existing_file_is_never_replaced_or_deleted(self):
        with tempfile.TemporaryDirectory() as work:
            path = pathlib.Path(work) / "archive"
            path.write_bytes(b"existing owner")
            with self.assertRaises(FileExistsError):
                download_archive("https://owned.invalid/archive", path, "0" * 64)
            self.assertEqual(path.read_bytes(), b"existing owner")

    def test_complete_ordered_archive_retains_fixed_whole_digest(self):
        original = b"original official archive bytes"
        calls = []

        def fetch(url, start, end):
            calls.append((start, end))
            end = min(end, len(original) - 1)
            body = original[start:end + 1]
            return 206, {"content-range": [f"bytes {start}-{end}/{len(original)}"],
                         "content-length": [str(len(body))]}, body

        with tempfile.TemporaryDirectory() as work:
            path = pathlib.Path(work) / "archive"
            receipt = download_archive("https://owned.invalid/archive", path,
                                       hashlib.sha256(original).hexdigest(), fetch_range=fetch, part_bytes=8)
            self.assertEqual(path.read_bytes(), original)
            self.assertEqual(calls, [(0, 7), (8, 15), (16, 23), (24, 30)])
            self.assertEqual(receipt["bytes"], 31)
            self.assertEqual(receipt["requestDeadlineSeconds"], 45)
            self.assertEqual(receipt["retries"], 0)

    def test_invalid_range_or_bytes_never_publish_an_archive(self):
        original = b"original official archive bytes"
        for defect in ["status-200", "missing-range", "duplicate-range", "wrong-start",
                       "wrong-end", "changed-total", "oversized-total", "truncated",
                       "length", "encoding", "digest"]:
            with self.subTest(defect=defect), tempfile.TemporaryDirectory() as work:
                def fetch(url, start, end):
                    end = min(end, len(original) - 1)
                    body = original[start:end + 1]
                    status = 200 if defect == "status-200" else 206
                    first = start + 1 if defect == "wrong-start" else start
                    last = end - 1 if defect == "wrong-end" else end
                    total = len(original) + (1 if defect == "changed-total" and start else 0)
                    if defect == "oversized-total":
                        total = 65 << 20
                    headers = {"content-range": [f"bytes {first}-{last}/{total}"],
                               "content-length": [str(len(body))]}
                    if defect == "missing-range":
                        headers.pop("content-range")
                    if defect == "duplicate-range":
                        headers["content-range"] *= 2
                    if defect == "encoding":
                        headers["content-encoding"] = ["gzip"]
                    if defect == "length":
                        headers["content-length"] = [str(len(body) + 1)]
                    if defect == "truncated":
                        body = body[:-1]
                    if defect == "digest":
                        body = b"X" + body[1:]
                    return status, headers, body

                path = pathlib.Path(work) / "archive"
                with self.assertRaises(RuntimeError):
                    download_archive("https://owned.invalid/archive", path,
                                     hashlib.sha256(original).hexdigest(), fetch_range=fetch, part_bytes=8)
                self.assertFalse(path.exists())


if __name__ == "__main__":
    unittest.main()
