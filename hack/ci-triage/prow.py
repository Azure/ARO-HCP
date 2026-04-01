"""Prow CI triage — data acquisition for agent-driven analysis.

Thin data layer for CI failure triage. Fetches job data, parses
junit artifacts, and returns raw results. The agent (SKILL.md)
handles investigation and reasoning.

Commands:
    env-health       — pass/fail ratio with failed job list
    failure-summary  — cross-job failure grouping with error samples
    fetch-failures   — per-test failures (auto-falls back to step-level)
    build-log        — build-log.txt tail or grep
"""

import argparse
import json
import re
import sys
import urllib.error
import urllib.parse
import urllib.request
import xml.etree.ElementTree as ET
from concurrent.futures import ThreadPoolExecutor, as_completed
from datetime import datetime, timezone

GCSWEB_BASE = (
    "https://gcsweb-ci.apps.ci.l2s4.p1.openshiftapps.com"
    "/gcs/test-platform-results"
)

GCS_API = "https://storage.googleapis.com/storage/v1/b/test-platform-results/o"
GCS_DIRECT = "https://storage.googleapis.com/test-platform-results"
GCS_BUCKET = "test-platform-results"

_TWITTER_EPOCH_MS = 1288834974657

_ANSI_RE = re.compile(r"\x1b\[[0-9;]*m|\ufffd\[[0-9;]*m")


def _url_encode(s):
    """URL-encode a string for use in query parameters."""
    return urllib.parse.quote(s, safe="")

PERIODIC_JOBS = {
    "int": "periodic-ci-Azure-ARO-HCP-main-periodic-integration-e2e-parallel",
    "stg": "periodic-ci-Azure-ARO-HCP-main-periodic-stage-e2e-parallel",
    "prod": "periodic-ci-Azure-ARO-HCP-main-periodic-prod-e2e-parallel",
}

PRESUBMIT_JOBS = {
    "dev": "pull-ci-Azure-ARO-HCP-main-e2e-parallel",
    "int": "pull-ci-Azure-ARO-HCP-main-integration-e2e-parallel",
    "stg": "pull-ci-Azure-ARO-HCP-main-stage-e2e-parallel",
    "prod": "pull-ci-Azure-ARO-HCP-main-prod-e2e-parallel",
}

TEST_STEPS = {
    "dev": "e2e-parallel",
    "int": "integration-e2e-parallel",
    "stg": "stage-e2e-parallel",
    "prod": "prod-e2e-parallel",
}

TEST_CONTAINERS = {
    "dev": "aro-hcp-test-local",
    "int": "aro-hcp-test-persistent",
    "stg": "aro-hcp-test-persistent",
    "prod": "aro-hcp-test-persistent",
}
PROVISION_CONTAINER = "aro-hcp-provision-environment"

MAX_MESSAGE_CHARS = 2000

_ENV_URL_MARKERS = {
    "integration-e2e": "int",
    "stage-e2e": "stg",
    "prod-e2e": "prod",
}

def _detect_env_from_url(base_url):
    """Detect environment from job URL based on known markers."""
    for marker, marker_env in _ENV_URL_MARKERS.items():
        if marker in base_url:
            return marker_env
    if "e2e-parallel" in base_url:
        return "dev"
    return None


def _normalize_base_url(url):
    """Convert a Prow dashboard URL, short path, or raw URL to GCSWEB."""
    if url.startswith("/"):
        return f"{GCSWEB_BASE}{url}"
    if "/view/gs/" in url:
        gcs_path = url.split("/view/gs/", 1)[1]
        gcs_path = gcs_path.split("?")[0].split("#")[0].rstrip("/")
        return (f"https://gcsweb-ci.apps.ci.l2s4.p1.openshiftapps.com"
                f"/gcs/{gcs_path}")
    return url.split("?")[0].split("#")[0].rstrip("/")


def _short_url(url):
    """Strip GCSWEB_BASE prefix from a URL for compact output."""
    if url.startswith(GCSWEB_BASE):
        return url[len(GCSWEB_BASE):]
    return url


# --- JUnit parsing ---

def _parse_junit(data, name_field="name"):
    """Parse junit XML bytes into failures and suite metadata.

    Returns None if data is None or unparseable.
    Otherwise returns {failures, total_tests, total_time}.
    """
    if not data:
        return None
    try:
        root = ET.fromstring(data)
    except ET.ParseError:
        return None

    total_tests = 0
    total_time = 0.0
    has_time = False
    if root.tag == "testsuite":
        suites = [root]
    else:
        suites = root.findall("testsuite")
    for suite in suites:
        tests_attr = suite.get("tests")
        if tests_attr:
            try:
                total_tests += int(tests_attr)
            except ValueError:
                pass
        time_attr = suite.get("time")
        if time_attr:
            try:
                total_time += float(time_attr)
                has_time = True
            except ValueError:
                pass

    failures = []
    for tc in root.iter("testcase"):
        fail = tc.find("failure")
        if fail is not None:
            msg = fail.get("message") or fail.text or ""
            entry = {
                name_field: tc.get("name", ""),
                "message": msg[:MAX_MESSAGE_CHARS],
            }
            tc_time = tc.get("time")
            if tc_time:
                try:
                    entry["duration"] = round(float(tc_time), 1)
                except ValueError:
                    pass
            failures.append(entry)

    return {
        "failures": failures,
        "total_tests": total_tests,
        "total_time": round(total_time, 1) if has_time else None,
    }


# --- HTTP ---

class Fetcher:
    """HTTP fetcher for GCS and JSON endpoints."""

    def fetch(self, url, timeout=15):
        try:
            with urllib.request.urlopen(url, timeout=timeout) as r:
                return r.read()
        except (urllib.error.URLError, urllib.error.HTTPError,
                TimeoutError, OSError):
            return None

    def fetch_text(self, url, timeout=15):
        try:
            with urllib.request.urlopen(url, timeout=timeout) as r:
                ct = r.headers.get("Content-Type", "")
                if "text/html" in ct:
                    return None
                return r.read().decode("utf-8", errors="replace")
        except urllib.error.HTTPError as e:
            if e.code == 404:
                return None
            return None
        except (urllib.error.URLError, TimeoutError, OSError):
            return None

    def fetch_json(self, url, timeout=15):
        try:
            with urllib.request.urlopen(url, timeout=timeout) as r:
                if "text/html" in r.headers.get("Content-Type", ""):
                    return None
                return json.loads(r.read())
        except (urllib.error.URLError, urllib.error.HTTPError,
                TimeoutError, OSError, json.JSONDecodeError):
            return None


# --- Client ---

class ProwClient:
    """Client for Prow CI data acquisition and failure analysis."""

    def __init__(self, fetcher=None):
        self.fetcher = fetcher or Fetcher()

    def _junit_url(self, base_url, env):
        """Build junit.xml URL for an env."""
        step = TEST_STEPS.get(env)
        if not step:
            return None
        container = TEST_CONTAINERS[env]
        return (f"{base_url}/artifacts/{step}/{container}"
                f"/artifacts/junit.xml")

    def _build_log_url(self, base_url, env, step="test"):
        """Build build-log.txt URL for an env and step type."""
        test_step = TEST_STEPS.get(env)
        if not test_step:
            raise ValueError(
                f"Unknown env: {env}. Valid: {', '.join(TEST_STEPS)}")
        container = (PROVISION_CONTAINER if step == "provision"
                     else TEST_CONTAINERS[env])
        return (f"{base_url}/artifacts/{test_step}/{container}"
                f"/build-log.txt"), test_step, container

    def fetch_failures(self, base_url, env):
        """Fetch test failures, auto-falling back to step-level.

        Tries junit.xml first (per-test). If not found, falls back
        to junit_operator.xml (per-step). Returns None only if both
        are missing.
        """
        step = TEST_STEPS.get(env)
        if not step:
            raise ValueError(
                f"Unknown env: {env}. "
                f"Valid: {', '.join(TEST_STEPS)}")

        url = self._junit_url(base_url, env)
        data = self.fetcher.fetch(url, timeout=20)
        parsed = _parse_junit(data, name_field="test")
        if parsed is not None:
            parsed["source"] = "junit"
            return parsed

        # Fallback to step-level failures
        steps = self.fetch_steps(base_url)
        if steps is not None:
            return {
                "source": "junit_operator",
                "failures": steps,
                "total_tests": None,
                "total_time": None,
            }

        return None

    def fetch_steps(self, base_url, strip_ansi=True):
        """Fetch step-level failures from junit_operator.xml."""
        url = f"{base_url}/artifacts/junit_operator.xml"
        data = self.fetcher.fetch(url, timeout=20)
        result = _parse_junit(data, name_field="step")
        if result is None:
            return None
        failures = result["failures"]
        if strip_ansi:
            for f in failures:
                f["message"] = _ANSI_RE.sub("", f["message"])
        return failures

    def build_log(self, base_url, env, step="test", lines=80,
                  grep=None, context=3):
        """Fetch build-log.txt — tail or grep mode.

        Without grep: return last N lines (tail mode).
        With grep: search full log by regex (grep mode).
        """
        url, test_step, container = self._build_log_url(
            base_url, env, step)
        text = self.fetcher.fetch_text(url, timeout=30)
        if not text:
            return None

        text = _ANSI_RE.sub("", text)
        all_lines = text.splitlines()
        total = len(all_lines)

        if grep is None:
            # Tail mode
            return {
                "step": test_step,
                "container": container,
                "lines": all_lines[-lines:],
                "total_lines": total,
            }

        # Grep mode
        try:
            pat = re.compile(grep, re.IGNORECASE)
        except re.error as e:
            raise ValueError(f"Invalid regex: {e}")

        matches = []
        for i, line in enumerate(all_lines):
            if pat.search(line):
                before = all_lines[max(0, i - context):i]
                after = all_lines[i + 1:i + 1 + context]
                matches.append({
                    "line_number": i + 1,
                    "line": line,
                    "context_before": before,
                    "context_after": after,
                })
                if len(matches) >= 50:
                    break

        return {
            "step": test_step,
            "container": container,
            "pattern": grep,
            "matches": matches,
            "total_matches": len(matches),
            "truncated": len(matches) >= 50,
            "total_lines": total,
        }

    @staticmethod
    def _bid_to_iso(bid):
        """Decode a Snowflake build ID to ISO timestamp."""
        ts_ms = (int(bid) >> 22) + _TWITTER_EPOCH_MS
        dt = datetime.fromtimestamp(ts_ms / 1000, tz=timezone.utc)
        return dt.strftime("%Y-%m-%dT%H:%M:%S")

    @staticmethod
    def _iso_to_bid(iso):
        """Encode an ISO date/datetime to the nearest Snowflake ID."""
        if len(iso) == 10:
            iso += "T00:00:00"
        dt = datetime.fromisoformat(iso).replace(tzinfo=timezone.utc)
        ts_ms = int(dt.timestamp() * 1000)
        return str((ts_ms - _TWITTER_EPOCH_MS) << 22)

    def _gcs_list_builds(self, name, job_type,
                         start_bid=None, limit=None):
        """List build IDs from GCS JSON API with pagination.

        For presubmit jobs, returns [(bid, gs_link), ...].
        For periodic jobs, returns [bid, ...].
        Paginates automatically; limit caps the total returned.
        """
        page_size = 1000
        if job_type == "periodic":
            prefix = f"logs/{name}/"
            base_params = (f"prefix={_url_encode(prefix)}"
                           f"&delimiter=/&maxResults={page_size}"
                           f"&fields=prefixes,nextPageToken")
            if start_bid:
                offset = f"{prefix}{start_bid}/"
                base_params += (
                    f"&startOffset={_url_encode(offset)}")
            all_bids = []
            page_token = None
            while True:
                params = base_params
                if page_token:
                    params += (
                        f"&pageToken={_url_encode(page_token)}")
                data = self.fetcher.fetch_json(
                    f"{GCS_API}?{params}", timeout=15)
                if not data:
                    break
                for p in data.get("prefixes", []):
                    bid = p.rstrip("/").rsplit("/", 1)[-1]
                    if re.match(r"\d{19}$", bid):
                        all_bids.append(bid)
                page_token = data.get("nextPageToken")
                if not page_token:
                    break
                if limit and len(all_bids) >= limit:
                    break
            return all_bids[:limit] if limit else all_bids
        else:
            prefix = f"pr-logs/directory/{name}/"
            base_params = (f"prefix={_url_encode(prefix)}"
                           f"&maxResults={page_size}"
                           f"&fields=items(name,metadata)"
                           f",nextPageToken")
            if start_bid:
                offset = f"{prefix}{start_bid}"
                base_params += (
                    f"&startOffset={_url_encode(offset)}")
            results = []
            page_token = None
            while True:
                params = base_params
                if page_token:
                    params += (
                        f"&pageToken={_url_encode(page_token)}")
                data = self.fetcher.fetch_json(
                    f"{GCS_API}?{params}", timeout=15)
                if not data:
                    break
                for item in data.get("items", []):
                    fname = item["name"].rsplit("/", 1)[-1]
                    if (fname == "latest-build.txt"
                            or not fname.endswith(".txt")):
                        continue
                    bid = fname.replace(".txt", "")
                    if not re.match(r"\d{19}$", bid):
                        continue
                    meta = item.get("metadata", {})
                    gs_link = meta.get(
                        "x-goog-meta-link", "")
                    results.append((bid, gs_link))
                page_token = data.get("nextPageToken")
                if not page_token:
                    break
                if limit and len(results) >= limit:
                    break
            return results[:limit] if limit else results

    def _fetch_finished(self, gcs_url):
        """Fetch finished.json from a GCS direct URL."""
        return self.fetcher.fetch_json(
            f"{gcs_url}/finished.json", timeout=5)

    def list_jobs(self, env, job_type, limit=20, since=None):
        """List recent jobs via the GCS JSON API.

        Uses the GCS storage API to list build IDs, Snowflake
        decode for start timestamps, and parallel finished.json
        fetches for results. Typically completes in ~1s for 100+
        jobs.
        """
        if since and not re.match(r"\d{4}-\d{2}-\d{2}", since):
            raise ValueError(
                f"--since must be ISO format (YYYY-MM-DD or "
                f"YYYY-MM-DDTHH:MM), got: {since}")

        name = (PERIODIC_JOBS.get(env) if job_type == "periodic"
                else PRESUBMIT_JOBS.get(env))
        if not name:
            valid = ("periodic: int, stg, prod" if job_type == "periodic"
                     else "presubmit: dev, int, stg, prod")
            raise ValueError(
                f"No {job_type} job for env '{env}'. Valid: {valid}.")

        # Compute startOffset from --since using Snowflake encoding
        start_bid = self._iso_to_bid(since) if since else None

        listing = self._gcs_list_builds(
            name, job_type, start_bid=start_bid)
        if not listing:
            return []

        if job_type == "periodic":
            # listing is [bid, bid, ...] — build base URLs directly
            jobs_to_fetch = [
                (bid, f"{GCS_DIRECT}/logs/{name}/{bid}")
                for bid in listing]
        else:
            # listing is [(bid, gs_link), ...] — convert gs:// to URL
            jobs_to_fetch = []
            for bid, gs_link in listing:
                if gs_link.startswith("gs://"):
                    rel = gs_link.replace(
                        f"gs://{GCS_BUCKET}/", "", 1)
                    gcs_url = f"{GCS_DIRECT}/{rel}"
                    jobs_to_fetch.append((bid, gcs_url))

        # Sort newest-first, cap fetch count
        jobs_to_fetch.sort(key=lambda x: x[0], reverse=True)
        if not since:
            jobs_to_fetch = jobs_to_fetch[:limit]

        # Parallel fetch finished.json for result + completion
        def _build_entry(bid_and_url):
            bid, gcs_url = bid_and_url
            started = self._bid_to_iso(bid)
            gcsweb_url = gcs_url.replace(
                GCS_DIRECT, GCSWEB_BASE)
            entry = {
                "state": "pending",
                "started": started,
                "completed": "running",
                "job_id": bid,
                "base_url": gcsweb_url,
            }
            # Extract PR number from path for presubmits
            if job_type == "presubmit":
                parts = gcs_url.split("/")
                try:
                    idx = parts.index("pull") + 2
                    entry["pr"] = int(parts[idx])
                except (ValueError, IndexError):
                    pass

            finished = self._fetch_finished(gcs_url)
            if finished:
                result = (finished.get("result") or "").lower()
                entry["state"] = result or "unknown"
                ts = finished.get("timestamp")
                if ts:
                    dt = datetime.fromtimestamp(
                        ts, tz=timezone.utc)
                    entry["completed"] = dt.strftime(
                        "%Y-%m-%dT%H:%M:%S")
            return entry

        results = []
        with ThreadPoolExecutor(max_workers=20) as pool:
            futures = {pool.submit(_build_entry, jf): jf
                       for jf in jobs_to_fetch}
            for f in as_completed(futures):
                try:
                    entry = f.result()
                except Exception:
                    continue
                results.append(entry)

        results.sort(key=lambda e: e["job_id"], reverse=True)
        return results[:limit] if limit else results

    @staticmethod
    def _summarize_jobs(all_jobs):
        """Partition jobs by state and compute pass rate."""
        passed = [j for j in all_jobs
                  if j["state"] == "success"]
        failed = [j for j in all_jobs
                  if j["state"] in ("failure", "error")]
        aborted = [j for j in all_jobs
                   if j["state"] == "aborted"]
        completed = len(passed) + len(failed)
        pass_rate = (round(len(passed) / completed, 2)
                     if completed else 0.0)
        window = {
            "earliest": all_jobs[-1]["started"],
            "latest": all_jobs[0]["started"],
        }
        return passed, failed, aborted, pass_rate, window

    def _fetch_jobs(self, env, job_type, since, history):
        """Fetch job list with appropriate limit."""
        effective_limit = None if since else history
        return self.list_jobs(env, job_type,
                              limit=effective_limit, since=since)

    def env_health(self, env, job_type, since=None, history=20):
        """Environment health with root-cause grouped failures."""
        all_jobs = self._fetch_jobs(env, job_type, since, history)
        if not all_jobs:
            return {
                "env": env, "type": job_type, "window": None,
                "total": 0, "passed": 0, "failed": 0,
                "aborted": 0, "pass_rate": 1.0,
                "last_success": None, "failed_jobs": [],
            }

        passed, failed, aborted, pass_rate, window = (
            self._summarize_jobs(all_jobs))

        last_success = None
        if passed:
            ls = passed[0]
            last_success = {"completed": ls["completed"]}
            if "pr" in ls:
                last_success["pr"] = ls["pr"]

        failed_jobs_out = []
        for j in failed:
            entry = {"url": _short_url(j["base_url"]),
                     "started": j["started"]}
            if "pr" in j:
                entry["pr"] = j["pr"]
            failed_jobs_out.append(entry)

        return {
            "env": env,
            "type": job_type,
            "window": window,
            "total": len(all_jobs),
            "passed": len(passed),
            "failed": len(failed),
            "aborted": len(aborted),
            "pass_rate": pass_rate,
            "last_success": last_success,
            "failed_jobs": failed_jobs_out,
        }

    def failure_summary(self, env, job_type, since=None,
                        history=20, sample=3):
        """Fetch failures from all failed jobs and group by test.

        Returns a compact cross-job summary: which tests fail most
        often, with representative error messages and job URLs.
        Uses list_jobs directly and fetches junit.xml in parallel.
        """
        all_jobs = self._fetch_jobs(env, job_type, since, history)
        if not all_jobs:
            return {
                "env": env, "type": job_type, "window": None,
                "total": 0, "passed": 0, "failed": 0,
                "aborted": 0, "pass_rate": 1.0,
                "jobs_analyzed": 0, "failure_groups": [],
            }

        passed, failed, aborted, pass_rate, window = (
            self._summarize_jobs(all_jobs))

        # Parallel fetch junit from all failed jobs
        def _fetch_one(job):
            result = self.fetch_failures(job["base_url"], env)
            return job, result

        job_results = []
        with ThreadPoolExecutor(max_workers=20) as pool:
            futures = {pool.submit(_fetch_one, j): j
                       for j in failed}
            for f in as_completed(futures):
                try:
                    job, result = f.result()
                except Exception:
                    continue
                if result and result.get("failures"):
                    job_results.append((job, result))

        # Group failures by test name
        groups = {}
        for job, result in job_results:
            short = _short_url(job["base_url"])
            for failure in result["failures"]:
                name = (failure.get("test")
                        or failure.get("step")
                        or "unknown")
                if name not in groups:
                    groups[name] = {
                        "count": 0, "messages": [],
                        "jobs": [],
                    }
                g = groups[name]
                g["count"] += 1
                g["jobs"].append(short)
                msg = failure.get("message", "")
                if (msg and len(g["messages"]) < sample
                        and msg not in g["messages"]):
                    g["messages"].append(msg)

        # Sort by frequency
        failure_groups = []
        for name, g in sorted(groups.items(),
                              key=lambda x: x[1]["count"],
                              reverse=True):
            failure_groups.append({
                "test": name,
                "count": g["count"],
                "jobs_hit": len(g["jobs"]),
                "sample_messages": g["messages"],
            })

        return {
            "env": env,
            "type": job_type,
            "window": window,
            "total": len(all_jobs),
            "passed": len(passed),
            "failed": len(failed),
            "aborted": len(aborted),
            "pass_rate": pass_rate,
            "jobs_analyzed": len(job_results),
            "failure_groups": failure_groups,
        }


# --- CLI ---

def _build_parser():
    parser = argparse.ArgumentParser(
        prog="prow.py",
        description="Prow CI triage — data acquisition for "
                    "agent-driven analysis.",
    )
    sub = parser.add_subparsers(dest="command", required=True)

    eh = sub.add_parser(
        "env-health",
        help="Environment health — pass/fail ratio and job list")
    eh.add_argument("env", help="Environment (dev, int, stg, prod)")
    eh.add_argument("type", help="Job type (periodic, presubmit)")
    eh.add_argument("--since", help="ISO date/datetime filter")
    eh.add_argument("--history", type=int, default=20,
                    help="Number of jobs to analyze (default: 20)")

    fs = sub.add_parser(
        "failure-summary",
        help="Cross-job failure summary — groups errors "
             "across all failed jobs")
    fs.add_argument("env", help="Environment (dev, int, stg, prod)")
    fs.add_argument("type", help="Job type (periodic, presubmit)")
    fs.add_argument("--since", help="ISO date/datetime filter")
    fs.add_argument("--history", type=int, default=20,
                    help="Number of jobs to analyze (default: 20)")

    ff = sub.add_parser(
        "fetch-failures",
        help="Per-test failures from junit.xml")
    ff.add_argument("base_url",
                    help="Job base URL (or Prow dashboard URL)")
    ff.add_argument("env", nargs="?", default=None,
                    help="Environment (auto-detected from URL "
                    "if omitted)")

    bl = sub.add_parser(
        "build-log",
        help="Build-log.txt tail or grep")
    bl.add_argument("base_url",
                    help="Job base URL (or Prow dashboard URL)")
    bl.add_argument("env", nargs="?", default=None,
                    help="Environment (auto-detected from URL "
                    "if omitted)")
    bl.add_argument("--step", choices=["test", "provision"],
                    default="test",
                    help="Step type: test (default) or provision")
    bl.add_argument("--lines", type=int, default=80,
                    help="Tail lines to return (default: 80)")
    bl.add_argument("--grep",
                    help="Regex pattern — switches to grep mode")
    bl.add_argument("--context", type=int, default=3,
                    help="Context lines around grep matches "
                    "(default: 3)")

    return parser


def _die(msg):
    print(json.dumps({"error": msg}), file=sys.stderr)
    sys.exit(1)


def main(argv=None):
    parser = _build_parser()
    args = parser.parse_args(argv)
    client = ProwClient()

    try:
        if args.command == "env-health":
            result = client.env_health(
                args.env, args.type, since=args.since,
                history=args.history)
            print(json.dumps(result, indent=2))

        elif args.command == "failure-summary":
            result = client.failure_summary(
                args.env, args.type, since=args.since,
                history=args.history)
            print(json.dumps(result, indent=2))

        elif args.command == "fetch-failures":
            base_url = _normalize_base_url(args.base_url)
            env = args.env or _detect_env_from_url(base_url)
            if not env:
                _die("Cannot detect env from URL. "
                     "Specify env explicitly.")
            result = client.fetch_failures(base_url, env)
            if result is None:
                print(json.dumps({"status": "no_artifacts",
                                  "message": "No junit.xml or "
                                  "junit_operator.xml found"}))
            else:
                print(json.dumps(result, indent=2))

        elif args.command == "build-log":
            base_url = _normalize_base_url(args.base_url)
            env = args.env or _detect_env_from_url(base_url)
            if not env:
                _die("Cannot detect env from URL. "
                     "Specify env explicitly.")
            result = client.build_log(
                base_url, env,
                step=args.step, lines=args.lines,
                grep=args.grep, context=args.context)
            if result is None:
                print(json.dumps({
                    "status": "not_found",
                    "message": (f"build-log.txt not found "
                                f"for {args.step} step")}))
            else:
                print(json.dumps(result, indent=2))

    except (ValueError, RuntimeError) as e:
        _die(str(e))


if __name__ == "__main__":
    main()
