# Copyright 2026 Microsoft Corporation
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

from contextlib import contextmanager, redirect_stderr, redirect_stdout
from email.message import Message
import io
import json
import os
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch
from urllib.error import URLError
from urllib.response import addinfourl

import fetch_prow_logs as fetch


JOB_PATH = (
    "pr-logs/pull/Azure_ARO-HCP/7892/"
    "pull-ci-Azure-ARO-HCP-main-e2e-parallel/2117218708026822656"
)
PROW_URL = "https://prow.ci.openshift.org/view/gs/test-platform-results-public/" + JOB_PATH
BASE = "https://storage.googleapis.com/test-platform-results-public/" + JOB_PATH + "/"
FINISHED = b'{\n  "result": "FAILURE", "timestamp": 1770000000\n}\n'
LOG = b"starting job\r\n" + bytes(range(256)) * 600 + b"\nlast failure details\n"


class InterruptedStream(io.BytesIO):
    def read(self, size=-1):
        if self.tell():
            raise OSError("connection interrupted during artifact download")
        return super().read(min(size, 12))


class FetchProwLogsTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.output = Path(self.temporary.name) / "artifacts"

    @contextmanager
    def transport(self, routes):
        """Replace HTTPS I/O, retaining urllib's real status/redirect handling."""
        requests = []

        def https_open(request):
            url = request.full_url
            requests.append((url, request.timeout))
            if url not in routes:
                raise AssertionError("unexpected artifact request: " + url)
            value = routes[url]
            if isinstance(value, BaseException):
                raise value
            if isinstance(value, tuple):
                body, status, headers = value
            else:
                body, status, headers = value, 200, {}
            message = Message()
            for name, content in headers.items():
                message[name] = str(content)
            stream = io.BytesIO(body) if isinstance(body, bytes) else body
            response = addinfourl(stream, message, url, status)
            response.msg = "fixture response"
            return response

        with patch("urllib.request.HTTPSHandler.https_open", side_effect=https_open):
            yield requests

    def routes(self, finished=FINISHED, log=LOG):
        return {BASE + "finished.json": finished, BASE + "build-log.txt": log}

    def run_main(self, routes, url=PROW_URL):
        stdout, stderr = io.StringIO(), io.StringIO()
        with self.transport(routes) as requests, redirect_stdout(stdout), redirect_stderr(stderr):
            code = fetch.main([url, str(self.output)])
        return code, stdout.getvalue(), stderr.getvalue(), requests

    def assert_no_artifacts(self):
        if self.output.exists():
            self.assertEqual(list(self.output.iterdir()), [], "failed fetch left published or temporary artifacts")

    def assert_failure(self, routes, expected_error):
        code, stdout, stderr, requests = self.run_main(routes)
        self.assertNotEqual(code, 0)
        self.assertEqual(stdout, "", "failure must not print a success manifest")
        self.assertIn(expected_error, stderr)
        self.assert_no_artifacts()
        return stderr, requests

    def test_preserves_exact_bucket_and_both_job_path_layouts(self):
        for bucket in ("test-platform-results-public", "test-platform-results", "another-ci-bucket"):
            for path in (JOB_PATH, "logs/periodic-custom-job/2117218708026822656"):
                for suffix in ("", "/"):
                    with self.subTest(bucket=bucket, path=path, suffix=suffix):
                        url = f"https://prow.ci.openshift.org/view/gs/{bucket}/{path}{suffix}"
                        self.assertEqual(
                            fetch.artifact_base(url),
                            f"https://storage.googleapis.com/{bucket}/{path}/",
                        )

    def test_rejects_noncanonical_and_unsafe_urls(self):
        prefix = "https://prow.ci.openshift.org/view/gs/"
        invalid = [
            "http://prow.ci.openshift.org/view/gs/test-platform-results-public/" + JOB_PATH,
            PROW_URL.replace("prow.ci.openshift.org", "evil.example"),
            PROW_URL.replace("prow.ci.openshift.org", "prow.ci.openshift.org.evil.example"),
            PROW_URL.replace("prow.ci.openshift.org", "user:password@prow.ci.openshift.org"),
            PROW_URL.replace("prow.ci.openshift.org", "prow.ci.openshift.org:443"),
            PROW_URL + "?download=1",
            PROW_URL + "#fragment",
            PROW_URL + "?",
            PROW_URL + "#",
            prefix + "test-platform-results-public",
            prefix + "test-platform-results-public/",
            prefix + "test-platform-results-public/logs/job",
            prefix + "test-platform-results-public/logs/job/not-a-run",
            prefix + "test-platform-results-public/logs/job/123/extra",
            prefix + "test-platform-results-public/pr-logs/pull/repo/no-pr/job/123",
            prefix + "test-platform-results-public/pr-logs/pull/repo/123/job",
            prefix + "test-platform-results-public/arbitrary/object/path/123",
            prefix + "test-platform-results-public/logs/../123",
            prefix + "test-platform-results-public/logs/./123",
            prefix + "test-platform-results-public/logs/job%2fother/123",
            prefix + "test-platform-results-public/logs/job%2Fother/123",
            prefix + "test-platform-results-public/logs/job%5cother/123",
            prefix + "test-platform-results-public/logs/%2e%2e/123",
            prefix + "test-platform-results-public/logs/%252e%252e/123",
            prefix + "test-platform-results-public/logs/job\\other/123",
            prefix + "test-platform-results-public/logs//123",
            prefix + "test-platform-results-public/logs/job/123//",
            prefix + "test-platform-results-public/logs/jób/123",
            prefix + "test-platform-results-public/logs/job/１２３",
            prefix + "test-platform-results-public/logs/job/123\n",
            prefix + "test-platform-results-public/logs/job/123\t",
            prefix + "ab/logs/job/123",
            prefix + "Uppercase/logs/job/123",
            prefix + "-bad-bucket/logs/job/123",
            prefix + "bad-bucket-/logs/job/123",
            prefix + ("a" * 64) + "/logs/job/123",
            prefix + "bucket@evil.example/logs/job/123",
        ]
        for url in invalid:
            with self.subTest(url=url):
                with self.assertRaises(ValueError):
                    fetch.artifact_base(url)

    def test_invalid_cli_url_never_reaches_transport(self):
        code, stdout, stderr, requests = self.run_main({}, PROW_URL + "?bucket=private")
        self.assertNotEqual(code, 0)
        self.assertEqual(stdout, "")
        self.assertTrue(stderr)
        self.assertEqual(requests, [])
        self.assert_no_artifacts()

    def test_fetch_publishes_complete_original_bytes_and_absolute_manifest_paths(self):
        with self.transport(self.routes()) as requests:
            manifest = fetch.fetch_logs(PROW_URL, os.path.relpath(self.output))
        self.assertEqual(set(manifest), {"prow_url", "artifact_base", "files"})
        self.assertEqual(manifest["prow_url"], PROW_URL)
        self.assertEqual(manifest["artifact_base"], BASE)
        self.assertEqual(set(manifest["files"]), {"finished.json", "build-log.txt"})
        for name, expected in (("finished.json", FINISHED), ("build-log.txt", LOG)):
            path = Path(manifest["files"][name])
            self.assertTrue(path.is_absolute())
            self.assertEqual(path, self.output / name)
            self.assertEqual(path.read_bytes(), expected)
        self.assertCountEqual([url for url, _ in requests],
                              [BASE + name for name in ("finished.json", "build-log.txt")])
        self.assertEqual({path.name for path in self.output.iterdir()}, {"finished.json", "build-log.txt", "manifest.json"})
        self.assertEqual(json.loads((self.output / "manifest.json").read_bytes()), manifest)

    def test_cli_stdout_is_only_the_success_manifest(self):
        code, stdout, stderr, _ = self.run_main(self.routes())
        self.assertEqual(code, 0, stderr)
        self.assertEqual(stderr, "")
        manifest = json.loads(stdout)
        self.assertEqual(manifest["artifact_base"], BASE)
        self.assertEqual(Path(manifest["files"]["finished.json"]).read_bytes(), FINISHED)
        self.assertEqual(Path(manifest["files"]["build-log.txt"]).read_bytes(), LOG)
        self.assertEqual(json.loads((self.output / "manifest.json").read_bytes()), manifest)

    def test_http_errors_report_actual_url_and_status_without_bucket_fallback(self):
        for filename in ("finished.json", "build-log.txt"):
            for status in (403, 404):
                with self.subTest(filename=filename, status=status):
                    routes = self.routes()
                    routes[BASE + filename] = (b"access denied or missing", status, {})
                    stderr, requests = self.assert_failure(routes, str(status))
                    self.assertIn(BASE + filename, stderr)
                    self.assertTrue(all(url.startswith(BASE) for url, _ in requests))
                    self.assertEqual(len({url for url, _ in requests}), len(requests), "must not retry failed artifacts")

    def test_private_bucket_fetch_does_not_try_public_variant(self):
        private_url = PROW_URL.replace("test-platform-results-public", "test-platform-results")
        private_base = BASE.replace("test-platform-results-public", "test-platform-results")
        routes = {private_base + "finished.json": (b"denied", 403, {})}
        code, stdout, stderr, requests = self.run_main(routes, private_url)
        self.assertNotEqual(code, 0)
        self.assertEqual(stdout, "")
        self.assertIn(private_base + "finished.json", stderr)
        self.assertIn("403", stderr)
        self.assertEqual([url for url, _ in requests], [private_base + "finished.json"])
        self.assert_no_artifacts()

    def test_timeout_and_stream_interruption_publish_nothing(self):
        for error in (TimeoutError("timed out"), URLError("connection refused")):
            for filename in ("finished.json", "build-log.txt"):
                with self.subTest(error=error, filename=filename):
                    routes = self.routes()
                    routes[BASE + filename] = error
                    stderr, _ = self.assert_failure(routes, str(error))
                    self.assertIn(BASE + filename, stderr)
        routes = self.routes(log=InterruptedStream(LOG))
        stderr, _ = self.assert_failure(routes, "connection interrupted")
        self.assertIn(BASE + "build-log.txt", stderr)

    def test_malformed_finished_artifacts_are_not_published(self):
        for body in (b"", b"not json", b'{"result":', b"[]", b"null", b"{}", b'{"result": null}', b'{"result": 1}', b"\xff"):
            with self.subTest(body=body):
                code, stdout, stderr, _ = self.run_main(self.routes(finished=body))
                self.assertNotEqual(code, 0)
                self.assertEqual(stdout, "")
                self.assertTrue(stderr)
                self.assert_no_artifacts()

    def test_empty_build_log_is_not_published(self):
        self.assert_failure(self.routes(log=b""), "build-log.txt")

    def test_redirects_do_not_follow_even_same_host_or_cross_host_locations(self):
        for location in ("https://evil.example/stolen", BASE + "other.txt"):
            for status in (301, 302, 307, 308):
                with self.subTest(location=location, status=status):
                    routes = self.routes()
                    routes[BASE + "finished.json"] = (b"", status, {"Location": location})
                    stderr, requests = self.assert_failure(routes, str(status))
                    self.assertIn(BASE + "finished.json", stderr)
                    self.assertEqual([url for url, _ in requests], [BASE + "finished.json"])

    def test_bounded_download_rejects_advertised_and_streamed_oversize(self):
        for headers in ({}, {"Content-Length": "257"}):
            with self.subTest(headers=headers), patch.object(fetch, "MAX_BYTES", 256):
                routes = self.routes(log=(b"x" * 257, 200, headers))
                self.assert_failure(routes, "build-log.txt")

    def test_limit_sized_download_is_complete_not_silently_truncated(self):
        log = b"x" * 256
        with patch.object(fetch, "MAX_BYTES", 256), self.transport(self.routes(log=log)):
            manifest = fetch.fetch_logs(PROW_URL, self.output)
        self.assertEqual(Path(manifest["files"]["build-log.txt"]).read_bytes(), log)

    def test_failed_rerun_cannot_reuse_or_overwrite_stale_artifacts(self):
        with self.transport(self.routes()):
            fetch.fetch_logs(PROW_URL, self.output)
        before = {path.name: path.read_bytes() for path in self.output.iterdir()}
        code, stdout, stderr, requests = self.run_main({})
        self.assertNotEqual(code, 0)
        self.assertEqual(stdout, "")
        self.assertTrue(stderr)
        self.assertEqual(requests, [], "existing outputs must be refused before any fetch")
        self.assertEqual({path.name: path.read_bytes() for path in self.output.iterdir()}, before)

    def test_any_existing_output_blocks_partial_overwrite(self):
        self.output.mkdir()
        for name in ("finished.json", "build-log.txt", "manifest.json"):
            with self.subTest(name=name):
                path = self.output / name
                path.write_bytes(b"prior artifact")
                code, stdout, stderr, requests = self.run_main({})
                self.assertNotEqual(code, 0)
                self.assertEqual(stdout, "")
                self.assertTrue(stderr)
                self.assertEqual(requests, [])
                self.assertEqual(list(self.output.iterdir()), [path])
                self.assertEqual(path.read_bytes(), b"prior artifact")
                path.unlink()

    def test_truncated_http_body_is_not_published_as_complete(self):
        for filename in ("finished.json", "build-log.txt"):
            with self.subTest(filename=filename):
                routes = self.routes()
                body = routes[BASE + filename]
                routes[BASE + filename] = (body, 200, {"Content-Length": str(len(body) + 1)})
                self.assert_failure(routes, filename)

    def test_neither_artifact_is_published_while_downloads_are_in_progress(self):
        owner = self

        class UnpublishedStream(io.BytesIO):
            def read(self, size=-1):
                owner.assertFalse((owner.output / "finished.json").exists())
                owner.assertFalse((owner.output / "build-log.txt").exists())
                owner.assertFalse((owner.output / "manifest.json").exists())
                return super().read(size)

        routes = self.routes(finished=UnpublishedStream(FINISHED), log=UnpublishedStream(LOG))
        with self.transport(routes):
            manifest = fetch.fetch_logs(PROW_URL, self.output)
        self.assertEqual(Path(manifest["files"]["finished.json"]).read_bytes(), FINISHED)
        self.assertEqual(Path(manifest["files"]["build-log.txt"]).read_bytes(), LOG)


if __name__ == "__main__":
    unittest.main()
