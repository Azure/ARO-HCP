"""Prow CI triage — data acquisition for agent-driven analysis.

Thin data layer for CI failure triage. Fetches job data, parses
junit artifacts, and returns raw results. The agent (SKILL.md)
handles investigation and reasoning.

Commands:
    env-health       — pass/fail ratio with failed job list
    fetch-failures   — per-test failures (auto-falls back to step-level)
    build-log        — build-log.txt tail or grep
"""

import argparse
import json
import re
import sys
import urllib.error
import urllib.request
import xml.etree.ElementTree as ET
from concurrent.futures import ThreadPoolExecutor, as_completed

GCSWEB_BASE = (
    "https://gcsweb-ci.apps.ci.l2s4.p1.openshiftapps.com"
    "/gcs/test-platform-results"
)

_ANSI_RE = re.compile(r"\x1b\[[0-9;]*m|\ufffd\[[0-9;]*m")

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

    def resolve(self, job_id, env):
        """Resolve presubmit base URL via GCS indirection."""
        job_name = PRESUBMIT_JOBS.get(env)
        if not job_name:
            raise ValueError(
                f"Unknown env: {env}. Valid: {', '.join(PRESUBMIT_JOBS)}")

        txt_url = (f"{GCSWEB_BASE}/pr-logs/directory/"
                   f"{job_name}/{job_id}.txt")
        text = self.fetcher.fetch_text(txt_url, timeout=10)
        if not text:
            raise ValueError(f"Could not fetch {txt_url}")

        gs_path = text.strip()
        if not gs_path.startswith("gs://"):
            raise ValueError(f"Expected gs:// path, got: {gs_path[:100]}")

        relative = gs_path.replace("gs://test-platform-results/", "", 1)
        return f"{GCSWEB_BASE}/{relative}"

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

    def _fetch_status(self, job_id, env, job_type):
        """Fetch status for one job. Returns dict or None."""
        try:
            if job_type == "periodic":
                name = PERIODIC_JOBS.get(env)
                base = (f"{GCSWEB_BASE}/logs/{name}/{job_id}"
                        if name else None)
            else:
                base = self.resolve(job_id, env)
        except ValueError:
            return None
        if not base:
            return None

        pj = self.fetcher.fetch_json(f"{base}/prowjob.json", timeout=10)
        if not pj:
            return None

        status = pj.get("status", {})
        entry = {
            "state": status.get("state", "unknown"),
            "started": (status.get("startTime") or "?")[:19],
            "completed": (status.get("completionTime") or "running")[:19],
            "job_id": job_id,
            "base_url": base,
        }

        if job_type == "presubmit":
            pulls = pj.get("spec", {}).get("refs", {}).get("pulls", [])
            if pulls:
                entry["pr"] = pulls[0].get("number")

        return entry

    def list_jobs(self, env, job_type, limit=20, since=None):
        """List recent jobs with status via parallel fetching."""
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

        if job_type == "periodic":
            listing_url = f"{GCSWEB_BASE}/logs/{name}/"
        else:
            listing_url = f"{GCSWEB_BASE}/pr-logs/directory/{name}/"

        html = self.fetcher.fetch(listing_url)
        if not html:
            return []

        ids = re.findall(
            r"\b\d{19}\b", html.decode("utf-8", errors="replace"))
        unique = list(dict.fromkeys(ids))
        fetch_limit = max(limit, 100) if since else limit
        job_ids = sorted(unique, reverse=True)[:fetch_limit]

        results = []
        with ThreadPoolExecutor(max_workers=8) as pool:
            futures = {
                pool.submit(self._fetch_status, jid, env, job_type): jid
                for jid in job_ids}
            for f in as_completed(futures):
                try:
                    entry = f.result()
                except Exception as e:
                    print(json.dumps({"debug": f"fetch_status failed "
                                      f"for {futures[f]}: {e}"}),
                          file=sys.stderr)
                    continue
                if entry is None:
                    continue
                if since and entry["started"] < since:
                    continue
                results.append(entry)

        results.sort(key=lambda e: e["job_id"], reverse=True)
        return results[:limit]

    def env_health(self, env, job_type, since=None, history=20):
        """Environment health with root-cause grouped failures."""
        all_jobs = self.list_jobs(env, job_type, limit=history,
                                  since=since)
        if not all_jobs:
            return {
                "env": env, "type": job_type, "window": None,
                "total": 0, "passed": 0, "failed": 0, "pass_rate": 1.0,
                "last_success": None, "failed_jobs": [],
            }

        passed = [j for j in all_jobs if j["state"] == "success"]
        failed = [j for j in all_jobs
                  if j["state"] in ("failure", "error")]

        last_success = None
        if passed:
            ls = passed[0]
            last_success = {"completed": ls["completed"]}
            if "pr" in ls:
                last_success["pr"] = ls["pr"]

        window = {
            "earliest": all_jobs[-1]["started"],
            "latest": all_jobs[0]["started"],
        }

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
            "pass_rate": round(len(passed) / len(all_jobs), 2),
            "last_success": last_success,
            "failed_jobs": failed_jobs_out,
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
