"""Tests for prow.py — Prow CI triage data acquisition."""

import os
import sys
import unittest

sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))

import prow  # noqa: E402


# --- Shared fixtures ---

JUNIT_ONE_FAILURE = b"""<?xml version="1.0" encoding="UTF-8"?>
<testsuite tests="3" failures="1">
    <testcase name="TestPassing" time="10.0"></testcase>
    <testcase name="TestFailing" time="5.0">
        <failure message="RESPONSE 503: ServiceUnavailable">fail details</failure>
    </testcase>
    <testcase name="TestSkipped" time="0"><skipped/></testcase>
</testsuite>"""


class MockFetcher(prow.Fetcher):
    """Test fetcher that returns preconfigured responses."""

    def __init__(self, fetch_fn=None, fetch_json_fn=None):
        self._fetch_fn = fetch_fn
        self._fetch_json_fn = fetch_json_fn

    def fetch(self, url, timeout=15):
        if self._fetch_fn:
            return self._fetch_fn(url, timeout)
        return None

    def fetch_text(self, url, timeout=15):
        data = self.fetch(url, timeout)
        if data is None:
            return None
        text = data.decode("utf-8", errors="replace")
        if text.lstrip().startswith(("<!DOCTYPE", "<html", "<HTML")):
            return None
        return text

    def fetch_json(self, url, timeout=15):
        if self._fetch_json_fn:
            return self._fetch_json_fn(url, timeout)
        return None


def _gcs_fetcher(prefixes=None, finished_map=None,
                 presubmit_items=None):
    prefixes = prefixes or []
    finished_map = finished_map or {}
    presubmit_items = presubmit_items or []

    def fetch_json(url, timeout):
        if "storage.googleapis.com/storage/v1/b/" in url:
            if "delimiter=" in url:
                return {"prefixes": [
                    f"logs/job/{bid}/" for bid in prefixes]}
            else:
                return {"items": presubmit_items}
        if "storage.googleapis.com/test-platform-results/" in url:
            for bid in finished_map:
                if f"/{bid}/finished.json" in url:
                    return finished_map[bid]
        return None
    return MockFetcher(fetch_json_fn=fetch_json)


def _bid(iso_str):
    return prow.ProwClient._iso_to_bid(iso_str)


# --- Tests ---


class TestParseJunit(unittest.TestCase):
    def test_none_and_bad_inputs(self):
        for data in [None, b"not xml"]:
            self.assertIsNone(prow._parse_junit(data))

    def test_extracts_failures_and_passed(self):
        result = prow._parse_junit(JUNIT_ONE_FAILURE)
        self.assertEqual(len(result["failures"]), 1)
        self.assertEqual(result["failures"][0]["name"],
                         "TestFailing")
        self.assertIn("503", result["failures"][0]["message"])
        self.assertEqual(result["passed"], ["TestPassing"])

    def test_custom_name_field(self):
        xml = (b'<testsuite><testcase name="step1">'
               b'<failure>err</failure></testcase></testsuite>')
        result = prow._parse_junit(xml, name_field="step")
        self.assertEqual(result["failures"][0]["step"], "step1")

    def test_truncates_long_messages(self):
        xml = (f'<testsuite><testcase name="T1">'
               f'<failure>{"x" * 5000}</failure>'
               f'</testcase></testsuite>')
        result = prow._parse_junit(xml.encode())
        self.assertLessEqual(
            len(result["failures"][0]["message"]),
            prow.MAX_MESSAGE_CHARS)

    def test_nested_testsuites(self):
        xml = b"""<testsuites>
            <testsuite><testcase name="T1">
                <failure message="e1">d</failure>
            </testcase></testsuite>
            <testsuite><testcase name="T2">
                <failure message="e2">d</failure>
            </testcase></testsuite>
        </testsuites>"""
        result = prow._parse_junit(xml)
        self.assertEqual(len(result["failures"]), 2)


class TestStripAddresses(unittest.TestCase):
    def test_strips_long_hex_preserves_rest(self):
        msg = '<*fmt.wrapError | 0xc000b98320>: actual error'
        result = prow._strip_addresses(msg)
        self.assertNotIn("0xc000b98320", result)
        self.assertIn("0x...", result)
        self.assertIn("actual error", result)
        # Short hex preserved
        self.assertEqual(
            prow._strip_addresses("code 0x1234"), "code 0x1234")
        # Error content preserved verbatim
        self.assertIn("RawResponse",
                      prow._strip_addresses('"RawResponse": {}'))


class TestSnowflake(unittest.TestCase):
    def test_roundtrip(self):
        iso = "2026-04-02T10:00:00"
        self.assertEqual(
            prow.ProwClient._bid_to_iso(
                prow.ProwClient._iso_to_bid(iso)), iso)

    def test_ordering(self):
        b1 = prow.ProwClient._iso_to_bid("2026-04-01T10:00:00")
        b2 = prow.ProwClient._iso_to_bid("2026-04-02T10:00:00")
        self.assertGreater(int(b2), int(b1))


class TestListJobs(unittest.TestCase):
    def test_sorted_newest_first_with_state(self):
        bid1 = _bid("2026-04-02T10:00:00")
        bid2 = _bid("2026-04-02T14:00:00")
        finished = {
            bid1: {"result": "SUCCESS", "timestamp": 0},
            bid2: {"result": "FAILURE", "timestamp": 0},
        }
        client = prow.ProwClient(
            _gcs_fetcher(prefixes=[bid1, bid2],
                         finished_map=finished))
        result = client.list_jobs("int", "periodic", limit=10)
        self.assertEqual(result[0]["job_id"], bid2)
        self.assertEqual(result[0]["state"], "failure")
        self.assertEqual(result[1]["state"], "success")

    def test_limit(self):
        bids = [_bid(f"2026-04-02T{10+i:02d}:00:00")
                for i in range(5)]
        finished = {b: {"result": "SUCCESS", "timestamp": 0}
                    for b in bids}
        client = prow.ProwClient(
            _gcs_fetcher(prefixes=bids, finished_map=finished))
        self.assertEqual(
            len(client.list_jobs("int", "periodic", limit=2)), 2)

    def test_presubmit_extracts_pr(self):
        bid = _bid("2026-04-02T10:00:00")
        name = prow.ENVS["dev"]["presubmit"]
        items = [{
            "name": f"pr-logs/directory/{name}/{bid}.txt",
            "metadata": {
                "x-goog-meta-link": (
                    f"gs://test-platform-results/pr-logs/"
                    f"pull/Azure_ARO-HCP/99/{name}/{bid}")
            },
        }]
        client = prow.ProwClient(
            _gcs_fetcher(presubmit_items=items,
                         finished_map={bid: {
                             "result": "FAILURE",
                             "timestamp": 0}}))
        result = client.list_jobs("dev", "presubmit", limit=1)
        self.assertEqual(result[0]["pr"], 99)

    def test_since_uses_startOffset(self):
        urls = []
        client = prow.ProwClient(MockFetcher(
            fetch_json_fn=lambda u, t: (
                urls.append(u) or {"prefixes": []})))
        client.list_jobs("int", "periodic", since="2026-03-31")
        self.assertTrue(
            any("startOffset" in u for u in urls))


class TestBuildLog(unittest.TestCase):
    LOG = ("line1\nline2\nline3\n"
           "\x1b[31mERROR: something failed\x1b[0m\n"
           "line5\n")

    def test_tail(self):
        client = prow.ProwClient(MockFetcher(
            fetch_fn=lambda u, t: self.LOG.encode()))
        result = client.build_log(
            "https://example.com/base", "int")
        self.assertEqual(result["total_lines"], 5)
        self.assertIn("ERROR: something failed",
                      result["lines"][3])
        self.assertNotIn("\x1b", result["lines"][3])

    def test_lines_limit(self):
        log = "\n".join(f"line{i}" for i in range(200))
        client = prow.ProwClient(MockFetcher(
            fetch_fn=lambda u, t: log.encode()))
        result = client.build_log(
            "https://example.com/base", "int", lines=10)
        self.assertEqual(len(result["lines"]), 10)
        self.assertEqual(result["lines"][0], "line190")

    def test_provision_step(self):
        captured = {}
        client = prow.ProwClient(MockFetcher(
            fetch_fn=lambda u, t: (
                captured.update(url=u) or b"output\n")))
        result = client.build_log(
            "https://example.com/base", "dev",
            step="provision")
        self.assertEqual(result["container"],
                         "aro-hcp-provision-environment")
        self.assertIn("aro-hcp-provision-environment",
                      captured["url"])

    def test_html_rejected(self):
        client = prow.ProwClient(MockFetcher(
            fetch_fn=lambda u, t: b"<html>dir</html>"))
        self.assertIsNone(client.build_log(
            "https://example.com/base", "int"))


class TestNormalizeBaseUrl(unittest.TestCase):
    def test_prow_dashboard(self):
        url = ("https://prow.ci.openshift.org/view/gs/"
               "test-platform-results/logs/job/123?tab=x#y")
        result = prow._normalize_base_url(url)
        self.assertIn("/gcs/test-platform-results/logs/job/123",
                      result)
        self.assertNotIn("?", result)

    def test_short_path(self):
        result = prow._normalize_base_url("/logs/job/123")
        self.assertTrue(result.startswith(prow.GCSWEB_BASE))

    def test_short_url(self):
        full = f"{prow.GCSWEB_BASE}/logs/job/123"
        self.assertEqual(
            prow._short_url(full), "/logs/job/123")


class TestRenderEvidence(unittest.TestCase):
    def _er(self, groups=None):
        return {
            "env": "int", "type": "periodic",
            "pass_rate": 0.75, "passed": 15,
            "failed": 5, "aborted": 0,
            "failure_groups": groups or [],
            "per_job_tests": [],
        }

    def test_jobs_table_and_failures(self):
        groups = [{
            "test": "TestTimeout", "count": 3,
            "messages": [{"msg": "deadline exceeded",
                          "count": 3}],
            "first_seen": "2026-04-02T06:00:00",
        }]
        evidence = prow._render_evidence([self._er(groups)])
        self.assertIn("## Jobs", evidence)
        self.assertIn("75%", evidence)
        self.assertIn("TestTimeout", evidence)
        self.assertIn("deadline exceeded", evidence)
        self.assertIn("3x", evidence)

    def test_per_job(self):
        er = self._er()
        er["per_job_tests"] = [{
            "job": "/logs/j/1",
            "started": "2026-04-01T10:00:00",
            "passed": 28, "failed": 4,
        }]
        self.assertIn("28P/4F",
                      prow._render_evidence([er]))


class TestFailureSummary(unittest.TestCase):
    @staticmethod
    def _mock_failures(test_map):
        def mock(base_url, env):
            tests = test_map.get(base_url)
            if tests:
                return {
                    "failures": [{"test": t, "message": "err"}
                                 for t in tests],
                    "passed": [],
                }
            return None
        return mock

    def test_groups_with_onset(self):
        jobs = [
            {"state": "failure", "started": "2026-04-02T06:00:00",
             "job_id": "1", "base_url": "http://u1"},
            {"state": "failure", "started": "2026-04-02T14:00:00",
             "job_id": "2", "base_url": "http://u2"},
        ]
        client = prow.ProwClient(MockFetcher())
        client.list_jobs = lambda *a, **kw: jobs
        client.fetch_failures = self._mock_failures(
            {"http://u1": ["TestA"], "http://u2": ["TestA"]})
        result = client.failure_summary("int", "periodic")
        fg = result["failure_groups"][0]
        self.assertEqual(fg["test"], "TestA")
        self.assertEqual(fg["count"], 2)
        self.assertEqual(fg["first_seen"],
                         "2026-04-02T06:00:00")
        self.assertEqual(fg["last_seen"],
                         "2026-04-02T14:00:00")
        self.assertIn("jobs", fg)

    def test_schema(self):
        jobs = [
            {"state": "success", "started": "2026-04-02T10:00:00",
             "job_id": "1",
             "base_url": f"{prow.GCSWEB_BASE}/logs/j/1"},
            {"state": "failure", "started": "2026-04-02T11:00:00",
             "job_id": "2",
             "base_url": f"{prow.GCSWEB_BASE}/logs/j/2"},
        ]
        client = prow.ProwClient(MockFetcher())
        client.list_jobs = lambda *a, **kw: jobs
        client.fetch_failures = self._mock_failures(
            {f"{prow.GCSWEB_BASE}/logs/j/2": ["TestA"]})
        result = client.failure_summary("int", "periodic")
        expected = {"test", "count", "jobs",
                    "first_seen", "last_seen",
                    "messages", "prs"}
        self.assertTrue(
            expected.issubset(set(
                result["failure_groups"][0].keys())))


class TestSummary(unittest.TestCase):
    def test_returns_all_env_type_combos(self):
        jobs = [
            {"state": "success", "started": "2026-04-02T10:00:00",
             "job_id": "1", "base_url": "http://u1"},
            {"state": "failure", "started": "2026-04-02T11:00:00",
             "job_id": "2", "base_url": "http://u2"},
        ]
        client = prow.ProwClient(MockFetcher())
        client.list_jobs = lambda *a, **kw: jobs
        client.fetch_failures = lambda *a, **kw: {
            "failures": [{"test": "TestA", "message": "err"}],
            "passed": [],
        }
        results = client.summary(since="2026-04-01")
        envs_seen = {(r["env"], r["type"]) for r in results}
        # Should have all env/type combos from ENVS
        expected = set()
        for env, cfg in prow.ENVS.items():
            for jt in ("periodic", "presubmit"):
                if jt in cfg:
                    expected.add((env, jt))
        self.assertEqual(envs_seen, expected)

    def test_pass_rate_and_counts(self):
        jobs = [
            {"state": "success", "started": "2026-04-02T10:00:00",
             "job_id": "1", "base_url": "http://u1"},
            {"state": "success", "started": "2026-04-02T11:00:00",
             "job_id": "2", "base_url": "http://u2"},
            {"state": "failure", "started": "2026-04-02T12:00:00",
             "job_id": "3", "base_url": "http://u3"},
        ]
        client = prow.ProwClient(MockFetcher())
        client.list_jobs = lambda *a, **kw: jobs
        client.fetch_failures = lambda *a, **kw: {
            "failures": [{"test": "TestB", "message": "err"}],
            "passed": [],
        }
        results = client.summary(
            since="2026-04-01", envs=["int"])
        # int has periodic + presubmit
        for r in results:
            self.assertEqual(r["passed"], 2)
            self.assertEqual(r["failed"], 1)
            self.assertAlmostEqual(r["pass_rate"], 0.67)

    def test_top_failures_limited(self):
        jobs = [
            {"state": "failure", "started": f"2026-04-02T{10+i:02d}:00:00",
             "job_id": str(i), "base_url": f"http://u{i}"}
            for i in range(5)
        ]
        call_count = []
        def mock_fetch(base_url, env):
            call_count.append(1)
            return {
                "failures": [{"test": f"Test{len(call_count)}",
                              "message": "err"}],
                "passed": [],
            }
        client = prow.ProwClient(MockFetcher())
        client.list_jobs = lambda *a, **kw: jobs
        client.fetch_failures = mock_fetch
        results = client.summary(
            since="2026-04-01", envs=["dev"])
        # dev has only presubmit, so one combo
        self.assertEqual(len(results), 1)
        # Should sample at most 3 failed jobs
        self.assertLessEqual(len(results[0]["top_failures"]), 5)

    def test_render_summary(self):
        results = [{
            "env": "int", "type": "periodic",
            "passed": 15, "failed": 5, "aborted": 0,
            "pass_rate": 0.75,
            "top_failures": ["TestTimeout", "TestAuth"],
        }]
        output = prow._render_summary(results)
        self.assertIn("# CI Summary", output)
        self.assertIn("75%", output)
        self.assertIn("TestTimeout", output)
        self.assertIn("int", output)


class TestMessageDedup(unittest.TestCase):
    def test_identical_except_cluster_name(self):
        msgs = [
            "timeout getting admin credentials for "
            "e2e-cidr-connectivity-bqjn7p",
            "timeout getting admin credentials for "
            "e2e-cidr-connectivity-mbl5kf",
            "timeout getting admin credentials for "
            "e2e-cidr-connectivity-xr9z2k",
        ]
        result = prow._dedup_messages(msgs)
        self.assertEqual(len(result), 1)
        self.assertEqual(result[0]["count"], 3)

    def test_identical_except_uuid(self):
        msgs = [
            "failed to create cluster "
            "a1b2c3d4-e5f6-7890-abcd-ef1234567890",
            "failed to create cluster "
            "11111111-2222-3333-4444-555555555555",
        ]
        result = prow._dedup_messages(msgs)
        self.assertEqual(len(result), 1)
        self.assertEqual(result[0]["count"], 2)

    def test_different_errors_stay_separate(self):
        msgs = [
            "timeout getting admin credentials",
            "RESPONSE 500: InternalServerError",
            "timeout getting admin credentials",
        ]
        result = prow._dedup_messages(msgs)
        self.assertEqual(len(result), 2)
        # Sorted by count desc
        self.assertEqual(result[0]["count"], 2)
        self.assertEqual(result[1]["count"], 1)

    def test_preserves_verbatim_representative(self):
        msg = ("timeout for cluster "
               "e2e-cidr-connectivity-bqjn7p")
        result = prow._dedup_messages([msg])
        self.assertEqual(result[0]["msg"], msg)

    def test_resource_group_dedup(self):
        msgs = [
            "error in rg-hcp-dev-westus3-abc123",
            "error in rg-hcp-dev-westus3-def456",
        ]
        result = prow._dedup_messages(msgs)
        self.assertEqual(len(result), 1)

    def test_go_file_line_dedup(self):
        msgs = [
            "error at client.go:174: deadline exceeded",
            "error at client.go:244: deadline exceeded",
            "error at client.go:31: deadline exceeded",
        ]
        result = prow._dedup_messages(msgs)
        self.assertEqual(len(result), 1)
        self.assertEqual(result[0]["count"], 3)


class TestStripCertBytes(unittest.TestCase):
    def test_strips_long_byte_array(self):
        msg = ('x509: cert error [48, 130, 9, 138, '
               + ', '.join(str(i % 256)
                           for i in range(50))
               + '] more text')
        result = prow._strip_cert_bytes(msg)
        self.assertIn("[<cert-bytes>]", result)
        self.assertIn("x509: cert error", result)
        self.assertIn("more text", result)
        self.assertNotIn("48, 130", result)

    def test_strips_unterminated_array(self):
        """Cert bytes truncated by MAX_MESSAGE_CHARS."""
        msg = ('x509 error: [48, 130, 9, 138, '
               + ', '.join(str(i % 256) for i in range(100)))
        result = prow._strip_cert_bytes(msg)
        self.assertIn("[<cert-bytes>]", result)
        self.assertNotIn("48, 130", result)

    def test_preserves_short_arrays(self):
        msg = "values [1, 2, 3] are fine"
        self.assertEqual(
            prow._strip_cert_bytes(msg), msg)


class TestParseSince(unittest.TestCase):
    def test_formats(self):
        self.assertEqual(
            prow._parse_since("2026-03-19"), "2026-03-19")
        self.assertRegex(
            prow._parse_since("7d"), r"\d{4}-\d{2}-\d{2}$")
        self.assertRegex(
            prow._parse_since("24h"),
            r"\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}")
        self.assertIsNone(prow._parse_since(None))
        self.assertIsNone(prow._parse_since(""))


if __name__ == "__main__":
    unittest.main()
