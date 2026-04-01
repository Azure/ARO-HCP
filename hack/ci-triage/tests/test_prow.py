"""Tests for prow.py — Prow CI triage data acquisition."""

import json
import os
import subprocess
import sys
import unittest
import urllib.error
from unittest.mock import MagicMock, patch

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

OPERATOR_ONE_FAILURE = b"""<?xml version="1.0" encoding="UTF-8"?>
<testsuite tests="2" failures="1">
    <testcase name="provision-step" time="100"></testcase>
    <testcase name="Run multi-stage test e2e-parallel - provision container" time="60">
        <failure>quota exceeded in WestUS3</failure>
    </testcase>
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


def _periodic_fetcher(listing_html, prowjob_fn):
    """Build a MockFetcher for periodic list_jobs tests."""
    def fetch(url, timeout):
        if "/logs/" in url and "prowjob" not in url:
            return listing_html
        return None

    def fetch_json(url, timeout):
        if "prowjob.json" in url:
            segment = url.split("/prowjob.json")[0].rsplit("/", 1)[-1]
            return prowjob_fn(segment)
        return None

    return MockFetcher(fetch_fn=fetch, fetch_json_fn=fetch_json)


def _make_prowjob(state="success", started="2026-03-31T10:00:00Z",
                  completed="2026-03-31T11:00:00Z", pulls=None):
    """Build a prowjob.json dict for tests."""
    pj = {
        "status": {
            "state": state,
            "startTime": started,
            "completionTime": completed,
        },
        "spec": {"refs": {}},
    }
    if pulls:
        pj["spec"]["refs"]["pulls"] = pulls
    return pj


# --- Tests ---


class TestConstants(unittest.TestCase):
    def test_periodic_jobs_exist(self):
        for env in ("int", "stg", "prod"):
            self.assertIn(env, prow.PERIODIC_JOBS)

    def test_presubmit_jobs_exist(self):
        for env in ("dev", "int", "stg", "prod"):
            self.assertIn(env, prow.PRESUBMIT_JOBS)

    def test_dev_has_no_periodic(self):
        self.assertNotIn("dev", prow.PERIODIC_JOBS)

    def test_step_and_container_names(self):
        cases = [
            ("dev", "e2e-parallel", "aro-hcp-test-local"),
            ("int", "integration-e2e-parallel",
             "aro-hcp-test-persistent"),
            ("stg", "stage-e2e-parallel",
             "aro-hcp-test-persistent"),
            ("prod", "prod-e2e-parallel",
             "aro-hcp-test-persistent"),
        ]
        for env, step, container in cases:
            with self.subTest(env=env):
                self.assertEqual(prow.TEST_STEPS[env], step)
                self.assertEqual(prow.TEST_CONTAINERS[env], container)


class TestParseJunit(unittest.TestCase):
    def test_none_and_bad_inputs(self):
        cases = [
            ("none", None),
            ("bad_xml", b"not xml"),
            ("unclosed", b"<testsuite><testcase name='T1'>"
                         b"<failure>oops"),
        ]
        for name, data in cases:
            with self.subTest(name):
                self.assertIsNone(prow._parse_junit(data))

    def test_no_failures(self):
        xml = b'<testsuite><testcase name="T1"></testcase></testsuite>'
        result = prow._parse_junit(xml)
        self.assertEqual(result["failures"], [])

    def test_extracts_failures(self):
        result = prow._parse_junit(JUNIT_ONE_FAILURE)
        self.assertEqual(len(result["failures"]), 1)
        self.assertEqual(result["failures"][0]["name"], "TestFailing")
        self.assertEqual(result["failures"][0]["message"],
                         "RESPONSE 503: ServiceUnavailable")

    def test_custom_name_field(self):
        xml = (b'<testsuite><testcase name="step1">'
               b'<failure>err</failure></testcase></testsuite>')
        result = prow._parse_junit(xml, name_field="step")
        self.assertEqual(result["failures"][0]["step"], "step1")

    def test_falls_back_to_text_when_no_message_attr(self):
        xml = (b'<testsuite><testcase name="T1">'
               b'<failure>text only</failure></testcase></testsuite>')
        result = prow._parse_junit(xml)
        self.assertEqual(result["failures"][0]["message"],
                         "text only")

    def test_truncates_long_messages(self):
        msg = "x" * 5000
        xml = (f'<testsuite><testcase name="T1">'
               f'<failure>{msg}</failure></testcase></testsuite>')
        result = prow._parse_junit(xml.encode())
        self.assertEqual(len(result["failures"][0]["message"]), 2000)

    def test_handles_nested_testsuites(self):
        xml = b"""<testsuites>
            <testsuite name="suite1">
                <testcase name="T1">
                    <failure message="err1">d</failure>
                </testcase>
            </testsuite>
            <testsuite name="suite2">
                <testcase name="T2">
                    <failure message="err2">d</failure>
                </testcase>
            </testsuite>
        </testsuites>"""
        result = prow._parse_junit(xml)
        self.assertEqual(len(result["failures"]), 2)
        names = [r["name"] for r in result["failures"]]
        self.assertIn("T1", names)
        self.assertIn("T2", names)


class TestFetchText(unittest.TestCase):
    def test_rejects_html_content_type(self):
        mock_response = MagicMock()
        mock_response.read.return_value = b"<html>directory</html>"
        mock_response.headers = {
            "Content-Type": "text/html; charset=utf-8"}
        mock_response.__enter__ = lambda s: s
        mock_response.__exit__ = MagicMock(return_value=False)

        fetcher = prow.Fetcher()
        with patch("urllib.request.urlopen",
                   return_value=mock_response):
            result = fetcher.fetch_text("https://example.com/path")
        self.assertIsNone(result)

    def test_returns_text_for_plain_content(self):
        mock_response = MagicMock()
        mock_response.read.return_value = b"line1\nline2\n"
        mock_response.headers = {"Content-Type": "text/plain"}
        mock_response.__enter__ = lambda s: s
        mock_response.__exit__ = MagicMock(return_value=False)

        fetcher = prow.Fetcher()
        with patch("urllib.request.urlopen",
                   return_value=mock_response):
            result = fetcher.fetch_text("https://example.com/log.txt")
        self.assertEqual(result, "line1\nline2\n")

    def test_returns_none_on_error(self):
        fetcher = prow.Fetcher()
        with patch("urllib.request.urlopen",
                   side_effect=urllib.error.URLError("fail")):
            result = fetcher.fetch_text("https://example.com/bad")
        self.assertIsNone(result)


class TestResolve(unittest.TestCase):
    # (name, fetch_fn, env, expect_error_contains)
    ERROR_CASES = [
        ("unknown_env", None, "xxx", "Unknown env"),
        ("html_response",
         lambda url, t: b"<html>not found</html>",
         "int", "Could not fetch"),
        ("non_gs_path",
         lambda url, t: b"not a gs path",
         "int", "Expected gs://"),
        ("fetch_failure", None, "int", ""),
    ]

    def test_error_cases(self):
        for name, fetch_fn, env, err_msg in self.ERROR_CASES:
            with self.subTest(name):
                client = prow.ProwClient(MockFetcher(
                    fetch_fn=fetch_fn))
                with self.assertRaises(ValueError) as ctx:
                    client.resolve("1234567890123456789", env)
                if err_msg:
                    self.assertIn(err_msg, str(ctx.exception))

    def test_valid_env_constructs_url(self):
        fetcher = MockFetcher(
            fetch_fn=lambda url, timeout:
                b"gs://test-platform-results/pr-logs/pull/some/path/123"
        )
        client = prow.ProwClient(fetcher)
        result = client.resolve("1234567890123456789", "int")
        self.assertEqual(
            result,
            f"{prow.GCSWEB_BASE}/pr-logs/pull/some/path/123")


class TestListJobs(unittest.TestCase):

    def _make_periodic_client(self, job_ids, prowjob_fn):
        listing_html = " ".join(
            f'<a href="{jid}/">{jid}</a>' for jid in job_ids
        ).encode()
        fetcher = _periodic_fetcher(listing_html, prowjob_fn)
        return prow.ProwClient(fetcher)

    def test_invalid_env_raises(self):
        client = prow.ProwClient(MockFetcher())
        with self.assertRaises(ValueError):
            client.list_jobs("xxx", "periodic")

    def test_dev_periodic_raises(self):
        client = prow.ProwClient(MockFetcher())
        with self.assertRaises(ValueError) as ctx:
            client.list_jobs("dev", "periodic")
        self.assertIn("No periodic", str(ctx.exception))

    def test_empty_on_fetch_failure(self):
        client = prow.ProwClient(MockFetcher())
        self.assertEqual(
            client.list_jobs("int", "periodic", limit=1), [])

    def test_since_rejects_bad_format(self):
        client = prow.ProwClient(MockFetcher())
        with self.assertRaises(ValueError) as ctx:
            client.list_jobs("int", "periodic", since="2026-3-27")
        self.assertIn("ISO format", str(ctx.exception))

    def test_returns_jobs_sorted_newest_first(self):
        ids = ["1234567890123456789", "1234567890123456790",
               "1234567890123456791"]

        def prowjob(jid):
            return _make_prowjob(
                state="success" if jid.endswith("1") else "failure",
                started=f"2026-03-31T1{jid[-1]}:00:00Z",
            )

        client = self._make_periodic_client(ids, prowjob)
        result = client.list_jobs("int", "periodic", limit=10)
        self.assertEqual(len(result), 3)
        self.assertEqual(result[0]["job_id"], "1234567890123456791")
        self.assertEqual(result[2]["job_id"], "1234567890123456789")

    def test_limit_caps_results(self):
        ids = [str(1234567890123456789 + i) for i in range(5)]
        client = self._make_periodic_client(
            ids, lambda jid: _make_prowjob())
        result = client.list_jobs("int", "periodic", limit=2)
        self.assertEqual(len(result), 2)

    def test_since_filters_by_start_time(self):
        ids = ["1234567890123456789", "1234567890123456790"]

        def prowjob(jid):
            ts = ("2026-03-30T10:00:00Z"
                  if jid == "1234567890123456789"
                  else "2026-03-31T10:00:00Z")
            return _make_prowjob(started=ts)

        client = self._make_periodic_client(ids, prowjob)
        result = client.list_jobs(
            "int", "periodic", since="2026-03-31")
        self.assertEqual(len(result), 1)

    def test_skips_jobs_with_no_prowjob(self):
        ids = ["1234567890123456789", "1234567890123456790"]

        def prowjob(jid):
            if jid == "1234567890123456789":
                return None
            return _make_prowjob()

        client = self._make_periodic_client(ids, prowjob)
        result = client.list_jobs("int", "periodic", limit=10)
        self.assertEqual(len(result), 1)

    def test_periodic_populates_base_url(self):
        ids = ["1234567890123456789"]
        client = self._make_periodic_client(
            ids, lambda jid: _make_prowjob())
        result = client.list_jobs("int", "periodic", limit=1)
        job_name = prow.PERIODIC_JOBS["int"]
        self.assertIn(
            f"/logs/{job_name}/1234567890123456789",
            result[0]["base_url"])


class TestEnvHealth(unittest.TestCase):

    def _make_client(self, jobs):
        client = prow.ProwClient(MockFetcher())
        client.list_jobs = lambda *args, **kwargs: jobs
        return client

    @staticmethod
    def _job(job_id, state, started="2026-03-31T10:00:00",
             completed="2026-03-31T11:00:00", **kwargs):
        return {
            "job_id": job_id, "state": state,
            "started": started, "completed": completed,
            "base_url": f"https://example.com/{job_id}",
            **kwargs,
        }

    def test_empty_returns_clean(self):
        client = self._make_client([])
        result = client.env_health("dev", "presubmit")
        self.assertEqual(result["total"], 0)
        self.assertEqual(result["pass_rate"], 1.0)
        self.assertEqual(result["failed_jobs"], [])
        self.assertIsNone(result["window"])

    def test_all_passing(self):
        jobs = [self._job("1234567890123456789", "success")]
        client = self._make_client(jobs)
        result = client.env_health("dev", "presubmit")
        self.assertEqual(result["passed"], 1)
        self.assertEqual(result["failed"], 0)

    def test_mixed_results(self):
        jobs = [
            self._job("1234567890123456789", "success",
                      started="2026-03-31T12:00:00"),
            self._job("1234567890123456788", "failure",
                      started="2026-03-31T10:00:00"),
        ]
        client = self._make_client(jobs)
        result = client.env_health("dev", "presubmit")
        self.assertEqual(result["total"], 2)
        self.assertEqual(result["passed"], 1)
        self.assertEqual(result["failed"], 1)
        self.assertEqual(result["pass_rate"], 0.5)
        self.assertIsNotNone(result["last_success"])
        self.assertEqual(len(result["failed_jobs"]), 1)

    def test_failed_jobs_have_short_urls(self):
        jobs = [
            self._job("1234567890123456789", "failure",
                      started="2026-03-31T10:00:00", pr=42),
            self._job("1234567890123456790", "success",
                      started="2026-03-31T12:00:00"),
        ]
        client = self._make_client(jobs)
        result = client.env_health("dev", "presubmit")
        self.assertEqual(len(result["failed_jobs"]), 1)
        fj = result["failed_jobs"][0]
        self.assertIn("url", fj)
        self.assertIn("started", fj)
        self.assertNotIn("job_id", fj)
        self.assertNotIn("base_url", fj)
        self.assertEqual(fj["pr"], 42)

    def test_error_state_counted_as_failed(self):
        jobs = [self._job("1234567890123456789", "error")]
        client = self._make_client(jobs)
        result = client.env_health("dev", "presubmit")
        self.assertEqual(result["failed"], 1)
        self.assertEqual(result["pass_rate"], 0.0)

    def test_window_uses_first_and_last_job(self):
        jobs = [
            self._job("1234567890123456791", "success",
                      started="2026-03-31T14:00:00"),
            self._job("1234567890123456790", "failure",
                      started="2026-03-31T10:00:00"),
            self._job("1234567890123456789", "success",
                      started="2026-03-30T08:00:00"),
        ]
        client = self._make_client(jobs)
        result = client.env_health("dev", "presubmit")
        self.assertEqual(result["window"]["earliest"],
                         "2026-03-30T08:00:00")
        self.assertEqual(result["window"]["latest"],
                         "2026-03-31T14:00:00")

    def test_last_success_includes_pr_when_present(self):
        jobs = [self._job("1234567890123456789", "success", pr=42)]
        client = self._make_client(jobs)
        result = client.env_health("dev", "presubmit")
        self.assertEqual(result["last_success"]["pr"], 42)
        self.assertNotIn("job_id", result["last_success"])


class TestFetchStatus(unittest.TestCase):

    def test_periodic_path(self):
        def fetch_json(url, timeout):
            if "prowjob.json" in url:
                return _make_prowjob(state="failure")
            return None
        fetcher = MockFetcher(fetch_json_fn=fetch_json)
        client = prow.ProwClient(fetcher)
        result = client._fetch_status(
            "1234567890123456789", "int", "periodic")
        self.assertEqual(result["state"], "failure")
        self.assertNotIn("env", result)
        self.assertIn("integration-e2e-parallel",
                      result["base_url"])

    def test_presubmit_with_pr_fields(self):
        def fetch(url, timeout):
            if ".txt" in url:
                return (b"gs://test-platform-results/"
                        b"pr-logs/pull/path/123")
            return None

        def fetch_json(url, timeout):
            if "prowjob.json" in url:
                return _make_prowjob(
                    pulls=[{"number": 42, "title": "Fix",
                            "author": "alice"}])
            return None
        fetcher = MockFetcher(
            fetch_fn=fetch, fetch_json_fn=fetch_json)
        client = prow.ProwClient(fetcher)
        result = client._fetch_status(
            "1234567890123456789", "int", "presubmit")
        self.assertEqual(result["pr"], 42)
        self.assertNotIn("pr_title", result)
        self.assertNotIn("pr_author", result)

    def test_missing_prowjob_returns_none(self):
        fetcher = MockFetcher(
            fetch_json_fn=lambda url, timeout: None)
        client = prow.ProwClient(fetcher)
        self.assertIsNone(client._fetch_status(
            "1234567890123456789", "int", "periodic"))

    def test_timestamp_truncation(self):
        def fetch_json(url, timeout):
            if "prowjob.json" in url:
                return _make_prowjob(
                    started="2026-03-31T10:00:00.123456789Z",
                    completed="2026-03-31T11:00:00.987654321Z")
            return None
        fetcher = MockFetcher(fetch_json_fn=fetch_json)
        client = prow.ProwClient(fetcher)
        result = client._fetch_status(
            "1234567890123456789", "int", "periodic")
        self.assertEqual(result["started"], "2026-03-31T10:00:00")
        self.assertEqual(result["completed"], "2026-03-31T11:00:00")


class TestFetchFailures(unittest.TestCase):
    def test_returns_junit_failures(self):
        fetcher = MockFetcher(
            fetch_fn=lambda url, timeout: JUNIT_ONE_FAILURE)
        client = prow.ProwClient(fetcher)
        result = client.fetch_failures(
            "https://example.com/base", "stg")
        self.assertEqual(result["source"], "junit")
        self.assertEqual(len(result["failures"]), 1)
        self.assertEqual(result["failures"][0]["test"],
                         "TestFailing")
        self.assertIn("503", result["failures"][0]["message"])
        self.assertEqual(result["total_tests"], 3)

    def test_falls_back_to_step_failures(self):
        def fetch(url, timeout):
            if "junit_operator.xml" in url:
                return OPERATOR_ONE_FAILURE
            return None
        client = prow.ProwClient(MockFetcher(fetch_fn=fetch))
        result = client.fetch_failures(
            "https://example.com/base", "stg")
        self.assertEqual(result["source"], "junit_operator")
        self.assertEqual(len(result["failures"]), 1)
        self.assertIn("quota exceeded",
                      result["failures"][0]["message"])
        self.assertIsNone(result["total_tests"])

    def test_returns_none_when_no_artifacts(self):
        client = prow.ProwClient(MockFetcher())
        self.assertIsNone(client.fetch_failures(
            "https://example.com/base", "stg"))

    def test_unknown_env_raises(self):
        client = prow.ProwClient(MockFetcher())
        with self.assertRaises(ValueError):
            client.fetch_failures(
                "https://example.com/base", "xxx")

    def test_correct_step_per_env(self):
        # (env, expected_step, expected_container)
        cases = [
            ("int", "integration-e2e-parallel",
             "aro-hcp-test-persistent"),
            ("prod", "prod-e2e-parallel",
             "aro-hcp-test-persistent"),
            ("dev", "e2e-parallel", "aro-hcp-test-local"),
        ]
        for env, step, container in cases:
            with self.subTest(env=env):
                captured = {}

                def capture(url, timeout, _c=captured):
                    _c["url"] = url
                    return JUNIT_ONE_FAILURE
                fetcher = MockFetcher(fetch_fn=capture)
                client = prow.ProwClient(fetcher)
                client.fetch_failures("https://base", env)
                self.assertIn(step, captured["url"])
                self.assertIn(container, captured["url"])

    def test_includes_expected_fields(self):
        fetcher = MockFetcher(
            fetch_fn=lambda url, timeout: JUNIT_ONE_FAILURE)
        client = prow.ProwClient(fetcher)
        result = client.fetch_failures(
            "https://example.com/base", "stg")
        for key in ("source", "failures", "total_tests",
                     "total_time"):
            self.assertIn(key, result)
        f = result["failures"][0]
        self.assertNotIn("root_cause", f)


class TestFetchSteps(unittest.TestCase):
    def test_returns_step_failures(self):
        fetcher = MockFetcher(
            fetch_fn=lambda url, timeout: OPERATOR_ONE_FAILURE)
        client = prow.ProwClient(fetcher)
        result = client.fetch_steps("https://example.com/base")
        self.assertEqual(len(result), 1)
        self.assertIn("quota exceeded", result[0]["message"])

    def test_returns_none_when_not_found(self):
        client = prow.ProwClient(MockFetcher())
        self.assertIsNone(
            client.fetch_steps("https://example.com/base"))

    def test_returns_empty_when_no_failures(self):
        xml = (b'<testsuite><testcase name="step1">'
               b'</testcase></testsuite>')
        fetcher = MockFetcher(
            fetch_fn=lambda url, timeout: xml)
        client = prow.ProwClient(fetcher)
        self.assertEqual(
            client.fetch_steps("https://example.com/base"), [])

    def test_fetches_correct_url(self):
        captured = {}

        def capture(url, timeout):
            captured["url"] = url
            return None
        fetcher = MockFetcher(fetch_fn=capture)
        client = prow.ProwClient(fetcher)
        client.fetch_steps("https://example.com/base")
        self.assertEqual(
            captured["url"],
            "https://example.com/base/artifacts/"
            "junit_operator.xml")

    def test_strips_ansi_from_messages(self):
        ansi_xml = (
            b'<testsuite><testcase name="step">'
            b'<failure>\xef\xbf\xbd[37m[10:00:00]\xef\xbf\xbd[0m '
            b'ERROR: boom</failure>'
            b'</testcase></testsuite>'
        )
        fetcher = MockFetcher(
            fetch_fn=lambda url, timeout: ansi_xml)
        client = prow.ProwClient(fetcher)
        result = client.fetch_steps("https://example.com/base")
        self.assertNotIn("\ufffd", result[0]["message"])
        self.assertIn("ERROR: boom", result[0]["message"])


class TestFetchBuildLog(unittest.TestCase):

    BUILD_LOG = ("line1\nline2\nline3\n"
                 "\x1b[31mERROR: something failed\x1b[0m\n"
                 "line5\n")

    def test_returns_log_tail(self):
        fetcher = MockFetcher(
            fetch_fn=lambda url, timeout:
                self.BUILD_LOG.encode())
        client = prow.ProwClient(fetcher)
        result = client.build_log(
            "https://example.com/base", "int")
        self.assertEqual(result["step"],
                         "integration-e2e-parallel")
        self.assertEqual(result["container"],
                         "aro-hcp-test-persistent")
        self.assertEqual(result["total_lines"], 5)
        self.assertEqual(len(result["lines"]), 5)
        self.assertIn("ERROR: something failed",
                      result["lines"][3])
        self.assertNotIn("\x1b", result["lines"][3])

    def test_returns_none_when_not_found(self):
        client = prow.ProwClient(MockFetcher())
        self.assertIsNone(client.build_log(
            "https://example.com/base", "int"))

    def test_unknown_env_raises(self):
        client = prow.ProwClient(MockFetcher())
        with self.assertRaises(ValueError):
            client.build_log(
                "https://example.com/base", "xxx")

    def test_provision_step(self):
        captured = {}

        def capture(url, timeout):
            captured["url"] = url
            return b"provision output\n"
        fetcher = MockFetcher(fetch_fn=capture)
        client = prow.ProwClient(fetcher)
        result = client.build_log(
            "https://example.com/base", "dev",
            step="provision")
        self.assertEqual(result["container"],
                         "aro-hcp-provision-environment")
        self.assertIn("e2e-parallel", captured["url"])
        self.assertIn("aro-hcp-provision-environment",
                      captured["url"])

    def test_respects_lines_limit(self):
        log = "\n".join(f"line{i}" for i in range(200))
        fetcher = MockFetcher(
            fetch_fn=lambda url, timeout: log.encode())
        client = prow.ProwClient(fetcher)
        result = client.build_log(
            "https://example.com/base", "int", lines=10)
        self.assertEqual(len(result["lines"]), 10)
        self.assertEqual(result["total_lines"], 200)
        self.assertEqual(result["lines"][0], "line190")

    def test_fetches_correct_url(self):
        captured = {}

        def capture(url, timeout):
            captured["url"] = url
            return None
        fetcher = MockFetcher(fetch_fn=capture)
        client = prow.ProwClient(fetcher)
        client.build_log(
            "https://example.com/base", "stg")
        self.assertEqual(
            captured["url"],
            "https://example.com/base/artifacts/"
            "stage-e2e-parallel/"
            "aro-hcp-test-persistent/build-log.txt")

    def test_strips_ansi_codes(self):
        log = (b"\xef\xbf\xbd[37m[10:00:00]\xef\xbf\xbd[0m "
               b"ERROR: boom\n")
        fetcher = MockFetcher(
            fetch_fn=lambda url, timeout: log)
        client = prow.ProwClient(fetcher)
        result = client.build_log(
            "https://example.com/base", "int")
        self.assertNotIn("\ufffd", result["lines"][0])
        self.assertIn("ERROR: boom", result["lines"][0])

    def test_rejects_html_directory_listing(self):
        html = (b"<html><body><h1>test-platform-results"
                b"</h1></body></html>")
        fetcher = MockFetcher(
            fetch_fn=lambda url, timeout: html)
        client = prow.ProwClient(fetcher)
        self.assertIsNone(client.build_log(
            "https://example.com/base", "int"))


class TestNormalizeBaseUrl(unittest.TestCase):
    # (name, input_url, expected_output_or_checks)
    CASES = [
        ("prow_dashboard",
         "https://prow.ci.openshift.org/view/gs/"
         "test-platform-results/logs/some-job/1234567890123456789",
         "https://gcsweb-ci.apps.ci.l2s4.p1.openshiftapps.com"
         "/gcs/test-platform-results/logs/some-job/"
         "1234567890123456789"),
        ("prow_with_query",
         "https://prow.ci.openshift.org/view/gs/"
         "test-platform-results/logs/job/123?tab=artifacts",
         None),  # check: no "?", ends with "/123"
        ("prow_with_fragment",
         "https://prow.ci.openshift.org/view/gs/"
         "test-platform-results/logs/job/123#summary",
         None),  # check: no "#"
        ("prow_trailing_slash",
         "https://prow.ci.openshift.org/view/gs/"
         "test-platform-results/logs/job/123/",
         None),  # check: not endswith "/"
        ("gcsweb_passthrough",
         "https://gcsweb-ci.apps.ci.l2s4.p1.openshiftapps.com"
         "/gcs/test-platform-results/logs/job/123",
         "https://gcsweb-ci.apps.ci.l2s4.p1.openshiftapps.com"
         "/gcs/test-platform-results/logs/job/123"),
        ("gcsweb_strips_query",
         "https://gcsweb-ci.apps.ci.l2s4.p1.openshiftapps.com"
         "/gcs/test-platform-results/logs/job/123?foo=bar",
         None),  # check: no "?"
        ("gcsweb_strips_trailing_slash",
         "https://gcsweb-ci.apps.ci.l2s4.p1.openshiftapps.com"
         "/gcs/test-platform-results/logs/job/123/",
         None),  # check: not endswith "/"
        ("other_prow_with_view_gs",
         "https://other-prow.example.com/view/gs/bucket/path/123",
         "https://gcsweb-ci.apps.ci.l2s4.p1.openshiftapps.com"
         "/gcs/bucket/path/123"),
        ("short_path",
         "/logs/periodic-ci-Azure-ARO-HCP-main-periodic-"
         "integration-e2e-parallel/1234567890123456789",
         "https://gcsweb-ci.apps.ci.l2s4.p1.openshiftapps.com"
         "/gcs/test-platform-results/logs/periodic-ci-Azure-"
         "ARO-HCP-main-periodic-integration-e2e-parallel/"
         "1234567890123456789"),
    ]

    def test_normalize_base_url(self):
        for name, url, expected in self.CASES:
            with self.subTest(name):
                result = prow._normalize_base_url(url)
                if expected is not None:
                    self.assertEqual(result, expected)
                # Universal checks
                self.assertNotIn("?", result)
                self.assertNotIn("#", result)
                self.assertFalse(result.endswith("/"))

    def test_short_url_strips_prefix(self):
        full = (f"{prow.GCSWEB_BASE}/logs/job/123")
        self.assertEqual(prow._short_url(full), "/logs/job/123")

    def test_short_url_passthrough_non_gcsweb(self):
        url = "https://example.com/path"
        self.assertEqual(prow._short_url(url), url)


class TestGrepBuildLog(unittest.TestCase):

    LOG_CONTENT = "\n".join([
        "line 1: starting test",
        "line 2: running setup",
        "line 3: ERROR: quota exceeded in WestUS3",
        "line 4: cleaning up",
        "line 5: ERROR: timeout waiting for cluster",
        "line 6: done",
    ])

    def test_finds_matches(self):
        fetcher = MockFetcher(
            fetch_fn=lambda url, timeout:
                self.LOG_CONTENT.encode())
        client = prow.ProwClient(fetcher)
        result = client.build_log(
            "https://example.com/base", "int",
            grep="ERROR")
        self.assertEqual(result["total_matches"], 2)
        self.assertEqual(result["matches"][0]["line_number"], 3)
        self.assertIn("quota exceeded",
                      result["matches"][0]["line"])
        self.assertEqual(result["total_lines"], 6)

    def test_context_lines(self):
        fetcher = MockFetcher(
            fetch_fn=lambda url, timeout:
                self.LOG_CONTENT.encode())
        client = prow.ProwClient(fetcher)
        result = client.build_log(
            "https://example.com/base", "int",
            grep="quota", context=1)
        match = result["matches"][0]
        self.assertEqual(len(match["context_before"]), 1)
        self.assertIn("running setup",
                      match["context_before"][0])
        self.assertEqual(len(match["context_after"]), 1)
        self.assertIn("cleaning up",
                      match["context_after"][0])

    def test_grep_returns_none_when_not_found(self):
        client = prow.ProwClient(MockFetcher())
        self.assertIsNone(client.build_log(
            "https://example.com/base", "int",
            grep="error"))

    def test_invalid_regex_raises(self):
        fetcher = MockFetcher(
            fetch_fn=lambda url, timeout: b"text")
        client = prow.ProwClient(fetcher)
        with self.assertRaises(ValueError) as ctx:
            client.build_log(
                "https://example.com/base", "int",
                grep="[invalid")
        self.assertIn("Invalid regex", str(ctx.exception))

    def test_case_insensitive(self):
        fetcher = MockFetcher(
            fetch_fn=lambda url, timeout:
                self.LOG_CONTENT.encode())
        client = prow.ProwClient(fetcher)
        result = client.build_log(
            "https://example.com/base", "int",
            grep="error")
        self.assertEqual(result["total_matches"], 2)

    def test_truncates_at_50_matches(self):
        log = "\n".join(f"ERROR line {i}" for i in range(100))
        fetcher = MockFetcher(
            fetch_fn=lambda url, timeout: log.encode())
        client = prow.ProwClient(fetcher)
        result = client.build_log(
            "https://example.com/base", "int",
            grep="ERROR")
        self.assertEqual(result["total_matches"], 50)
        self.assertTrue(result["truncated"])

    def test_grep_provision_step(self):
        captured = {}

        def capture(url, timeout):
            captured["url"] = url
            return b"provision output\n"
        fetcher = MockFetcher(fetch_fn=capture)
        client = prow.ProwClient(fetcher)
        result = client.build_log(
            "https://example.com/base", "dev",
            grep="output", step="provision")
        self.assertIn("aro-hcp-provision-environment",
                      captured["url"])
        self.assertEqual(result["container"],
                         "aro-hcp-provision-environment")

    def test_grep_unknown_env_raises(self):
        client = prow.ProwClient(MockFetcher())
        with self.assertRaises(ValueError):
            client.build_log(
                "https://example.com/base", "xxx",
                grep="error")

    def test_strips_ansi(self):
        log = (b"\xef\xbf\xbd[31mERROR: boom\xef\xbf\xbd[0m\n"
               b"clean line\n")
        fetcher = MockFetcher(
            fetch_fn=lambda url, timeout: log)
        client = prow.ProwClient(fetcher)
        result = client.build_log(
            "https://example.com/base", "int",
            grep="boom")
        self.assertEqual(result["total_matches"], 1)
        self.assertNotIn("\ufffd", result["matches"][0]["line"])


class TestParseJunit(unittest.TestCase):

    def test_returns_none_for_invalid(self):
        for data in (None, b"not xml"):
            with self.subTest(data=data):
                self.assertIsNone(prow._parse_junit(data))

    def test_extracts_total_tests(self):
        result = prow._parse_junit(JUNIT_ONE_FAILURE)
        self.assertEqual(result["total_tests"], 3)

    def test_extracts_total_time(self):
        xml = (b'<testsuite tests="2" time="120.5">'
               b'<testcase name="T1" time="60.2"></testcase>'
               b'<testcase name="T2" time="60.3">'
               b'<failure>err</failure></testcase>'
               b'</testsuite>')
        result = prow._parse_junit(xml)
        self.assertEqual(result["total_tests"], 2)
        self.assertEqual(result["total_time"], 120.5)
        self.assertEqual(len(result["failures"]), 1)

    def test_failure_includes_duration(self):
        xml = (b'<testsuite tests="1">'
               b'<testcase name="T1" time="45.3">'
               b'<failure>err</failure></testcase>'
               b'</testsuite>')
        result = prow._parse_junit(xml)
        self.assertEqual(result["failures"][0]["duration"], 45.3)

    def test_no_time_attr_returns_none(self):
        xml = (b'<testsuite tests="1">'
               b'<testcase name="T1">'
               b'<failure>err</failure></testcase>'
               b'</testsuite>')
        result = prow._parse_junit(xml)
        self.assertIsNone(result["total_time"])
        self.assertNotIn("duration", result["failures"][0])

    def test_nested_testsuites_sum_tests(self):
        xml = b"""<testsuites>
            <testsuite tests="3" time="10.0">
                <testcase name="T1"><failure>e</failure></testcase>
            </testsuite>
            <testsuite tests="5" time="20.0">
                <testcase name="T2"><failure>e</failure></testcase>
            </testsuite>
        </testsuites>"""
        result = prow._parse_junit(xml)
        self.assertEqual(result["total_tests"], 8)
        self.assertEqual(result["total_time"], 30.0)
        self.assertEqual(len(result["failures"]), 2)

    def test_failures_list_from_parse_junit(self):
        result = prow._parse_junit(JUNIT_ONE_FAILURE)
        self.assertEqual(len(result["failures"]), 1)
        self.assertEqual(
            result["failures"][0]["name"], "TestFailing")


class TestCli(unittest.TestCase):

    def _run(self, *args):
        script = os.path.join(
            os.path.dirname(__file__), "..", "prow.py")
        return subprocess.run(
            [sys.executable, script, *args],
            capture_output=True, text=True, timeout=10,
        )

    # (name, args, expected_strings)
    HELP_CASES = [
        ("fetch_failures", ["fetch-failures"], ["base_url"]),
        ("build_log", ["build-log"],
         ["--step", "--lines", "--grep", "--context"]),
    ]

    def test_help_output(self):
        for name, args, expected_strings in self.HELP_CASES:
            with self.subTest(name):
                r = self._run(*args, "--help")
                self.assertEqual(r.returncode, 0)
                for s in expected_strings:
                    self.assertIn(s, r.stdout)

    # (name, args)
    ERROR_CASES = [
        ("no_args", []),
        ("unknown_command", ["bogus"]),
        ("build_log_bad_env",
         ["build-log", "https://example.com", "xxx"]),
        ("build_log_no_args", ["build-log"]),
    ]

    def test_error_cases(self):
        for name, args in self.ERROR_CASES:
            with self.subTest(name):
                r = self._run(*args)
                self.assertNotEqual(r.returncode, 0)

    def test_error_output_is_json(self):
        r = self._run("build-log", "https://example.com", "xxx")
        self.assertNotEqual(r.returncode, 0)
        err = json.loads(r.stderr)
        self.assertIn("error", err)

    def test_cut_commands_rejected(self):
        for cmd in ("pr-info", "pr-files", "fetch-step-failures",
                    "list-jobs", "lookup-job", "resolve-url",
                    "pr-comments", "pr-checks", "fetch-build-log",
                    "grep-build-log"):
            with self.subTest(cmd):
                r = self._run(cmd, "--help")
                self.assertNotEqual(r.returncode, 0)


if __name__ == "__main__":
    unittest.main()
