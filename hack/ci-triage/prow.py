"""Prow CI triage — data acquisition for agent-driven failure analysis.

Parallel GCS fetching, JUnit parsing, failure grouping with onset
detection and structural message deduplication. The agent does all
reasoning.

Commands:
    summary              — cross-env health overview
    failures  ENV        — evidence packet for one env
    build-log URL [ENV]  — build-log tail from a specific job
"""

import argparse
import json
import re
import sys
import urllib.parse
import xml.etree.ElementTree as ET
from concurrent.futures import ThreadPoolExecutor, as_completed
from datetime import datetime, timedelta, timezone

from fetcher import Fetcher, CachedFetcher


# --- Constants ---

GCSWEB_BASE = (
    "https://gcsweb-ci.apps.ci.l2s4.p1.openshiftapps.com"
    "/gcs/test-platform-results"
)

GCS_API = "https://storage.googleapis.com/storage/v1/b/test-platform-results/o"
GCS_DIRECT = "https://storage.googleapis.com/test-platform-results"
GCS_BUCKET = "test-platform-results"

# Prow/Deck Snowflake IDs use Twitter's epoch
_TWITTER_EPOCH_MS = 1288834974657

_ANSI_RE = re.compile(r"\x1b\[[0-9;]*m|\ufffd\[[0-9;]*m")

ENVS = {
    "dev": {
        "presubmit": "pull-ci-Azure-ARO-HCP-main-e2e-parallel",
        "step": "e2e-parallel",
        "container": "aro-hcp-test-local",
    },
    "int": {
        "periodic": "periodic-ci-Azure-ARO-HCP-main-periodic-integration-e2e-parallel",
        "presubmit": "pull-ci-Azure-ARO-HCP-main-integration-e2e-parallel",
        "step": "integration-e2e-parallel",
        "container": "aro-hcp-test-persistent",
    },
    "stg": {
        "periodic": "periodic-ci-Azure-ARO-HCP-main-periodic-stage-e2e-parallel",
        "presubmit": "pull-ci-Azure-ARO-HCP-main-stage-e2e-parallel",
        "step": "stage-e2e-parallel",
        "container": "aro-hcp-test-persistent",
    },
    "prod": {
        "periodic": "periodic-ci-Azure-ARO-HCP-main-periodic-prod-e2e-parallel",
        "presubmit": "pull-ci-Azure-ARO-HCP-main-prod-e2e-parallel",
        "step": "prod-e2e-parallel",
        "container": "aro-hcp-test-persistent",
    },
}
PROVISION_CONTAINER = "aro-hcp-provision-environment"

MAX_MESSAGE_CHARS = 4000

_ENV_URL_MARKERS = {
    "integration-e2e": "int",
    "stage-e2e": "stg",
    "prod-e2e": "prod",
}


# --- URL helpers ---

def _detect_env_from_url(base_url):
    for marker, marker_env in _ENV_URL_MARKERS.items():
        if marker in base_url:
            return marker_env
    if "e2e-parallel" in base_url:
        return "dev"
    return None


def _normalize_base_url(url):
    if url.startswith("/"):
        return f"{GCSWEB_BASE}{url}"
    if "/view/gs/" in url:
        gcs_path = url.split("/view/gs/", 1)[1]
        gcs_path = gcs_path.split("?")[0].split("#")[0].rstrip("/")
        return (f"https://gcsweb-ci.apps.ci.l2s4.p1.openshiftapps.com"
                f"/gcs/{gcs_path}")
    return url.split("?")[0].split("#")[0].rstrip("/")


def _short_url(url):
    if url.startswith(GCSWEB_BASE):
        return url[len(GCSWEB_BASE):]
    return url


# --- JUnit parsing ---

def _parse_junit(data, name_field="name"):
    """Parse junit XML bytes into failures and passed test names."""
    if not data:
        return None
    try:
        root = ET.fromstring(data)
    except ET.ParseError:
        return None

    failures = []
    passed = []
    for tc in root.iter("testcase"):
        tc_name = tc.get("name", "")
        fail = tc.find("failure")
        if fail is not None:
            raw_msg = fail.get("message") or fail.text or ""
            failures.append({
                name_field: tc_name,
                "message": _strip_cert_bytes(
                    _strip_addresses(
                        raw_msg))[:MAX_MESSAGE_CHARS],
            })
        elif tc_name and tc.find("skipped") is None:
            passed.append(tc_name)
    return {"failures": failures, "passed": passed}


_GO_PTR_RE = re.compile(r"0x[0-9a-f]{8,}")


def _strip_addresses(msg):
    """Strip only Go pointer hex addresses — pure noise, no signal."""
    return _GO_PTR_RE.sub("0x...", msg)


_CERT_BYTES_RE = re.compile(
    r"\[(?:\d{1,3},\s*){10,}\d{1,3}(?:\]|(?:,\s*\d{1,3})*\s*$)")


def _strip_cert_bytes(msg):
    """Strip raw DER certificate byte arrays — pure noise.

    Matches both complete ([48, 130, ...]) and truncated
    arrays that hit the end of the message without a closing bracket.
    """
    return _CERT_BYTES_RE.sub("[<cert-bytes>]", msg)


# --- Message deduplication ---

# Patterns for ephemeral identifiers that vary across runs
# but don't change the semantic meaning of an error message.
_DEDUP_PATTERNS = [
    # UUIDs: 8-4-4-4-12 hex
    (re.compile(r"[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}"
                r"-[0-9a-f]{4}-[0-9a-f]{12}", re.I), "<uuid>"),
    # Azure resource groups (rg-xxx-yyy-randomsuffix)
    (re.compile(r"rg-[a-z0-9-]+"), "<rg>"),
    # Prow-style random suffixes on resource names (e.g.
    # e2e-cidr-connectivity-bqjn7p, cluster-abc12x)
    (re.compile(r"(?<=-)[a-z0-9]{5,8}(?=[\s/\]\)\"'\\,;:.]|$)"),
     "<id>"),
    # Hex addresses (already stripped by _strip_addresses,
    # but catch any remaining)
    (re.compile(r"0x[0-9a-f]{8,}"), "0x..."),
    # Go source file:line references (e.g. file.go:174)
    (re.compile(r"(?<=\.go:)\d+"), "<line>"),
    # Timestamps in various formats
    (re.compile(r"\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}:\d{2}"
                r"(?:\.\d+)?(?:Z|[+-]\d{2}:?\d{2})?"),
     "<timestamp>"),
    # Numeric IDs (build IDs, port numbers >1024, etc.)
    (re.compile(r"(?<![a-zA-Z])\d{5,}(?![a-zA-Z])"), "<num>"),
]


def _normalize_for_dedup(msg):
    """Normalize a message for deduplication.

    Replaces ephemeral identifiers with placeholders so that
    structurally identical messages (same error, different
    cluster/resource names) collapse to the same key.
    The original message is preserved verbatim for display.
    """
    normalized = msg
    for pattern, replacement in _DEDUP_PATTERNS:
        normalized = pattern.sub(replacement, normalized)
    return normalized


def _dedup_messages(messages):
    """Deduplicate messages by structural similarity.

    Returns list of {"msg": <verbatim representative>,
    "count": N} dicts, sorted by count descending.
    """
    seen = {}  # normalized_key -> (verbatim_msg, count)
    for msg in messages:
        key = _normalize_for_dedup(msg)
        if key in seen:
            seen[key] = (seen[key][0], seen[key][1] + 1)
        else:
            seen[key] = (msg, 1)
    result = [{"msg": v[0], "count": v[1]}
              for v in seen.values()]
    result.sort(key=lambda x: x["count"], reverse=True)
    return result


# --- Client ---

class ProwClient:
    """Client for Prow CI data acquisition and failure analysis."""

    def __init__(self, fetcher=None):
        self.fetcher = fetcher or Fetcher()

    def _build_log_url(self, base_url, env, step="test"):
        cfg = ENVS.get(env)
        if not cfg:
            raise ValueError(
                f"Unknown env: {env}. "
                f"Valid: {', '.join(ENVS)}")
        container = (PROVISION_CONTAINER if step == "provision"
                     else cfg["container"])
        return (f"{base_url}/artifacts/{cfg['step']}/{container}"
                f"/build-log.txt"), cfg["step"], container

    def fetch_failures(self, base_url, env):
        """Fetch test failures from junit.xml, fall back to step-level."""
        cfg = ENVS.get(env)
        if not cfg:
            raise ValueError(
                f"Unknown env: {env}. "
                f"Valid: {', '.join(ENVS)}")

        url = (f"{base_url}/artifacts/{cfg['step']}"
               f"/{cfg['container']}/artifacts/junit.xml")
        data = self.fetcher.fetch(url, timeout=20)
        result = _parse_junit(data, name_field="test")
        if result is not None:
            return result

        # Fallback: step-level failures from junit_operator.xml
        data = self.fetcher.fetch(
            f"{base_url}/artifacts/junit_operator.xml",
            timeout=20)
        result = _parse_junit(data, name_field="step")
        if result is not None:
            for f in result["failures"]:
                f["message"] = _ANSI_RE.sub(
                    "", f["message"])
            return result
        return None

    def build_log(self, base_url, env, step="test", lines=80):
        """Fetch build-log.txt tail."""
        url, test_step, container = self._build_log_url(
            base_url, env, step)
        text = self.fetcher.fetch_text(url, timeout=30)
        if not text:
            return None

        text = _ANSI_RE.sub("", text)
        text = (text.replace('\\"', '"')
                .replace('\\n', '\n')
                .replace('\\t', '\t'))
        all_lines = text.splitlines()
        total = len(all_lines)

        return {
            "step": test_step,
            "container": container,
            "lines": all_lines[-lines:],
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
                         start_bid=None, limit=1000):
        """List build IDs from GCS JSON API."""
        if job_type == "periodic":
            prefix = f"logs/{name}/"
            params = (f"prefix={urllib.parse.quote(prefix, safe='')}"
                      f"&delimiter=/&maxResults={limit}"
                      f"&fields=prefixes,nextPageToken")
            if start_bid:
                offset = f"{prefix}{start_bid}/"
                params += (f"&startOffset="
                           f"{urllib.parse.quote(offset, safe='')}")
            url = f"{GCS_API}?{params}"
            data = self.fetcher.fetch_json(url, timeout=15)
            if not data:
                return []
            prefixes = data.get("prefixes", [])
            return [p.rstrip("/").rsplit("/", 1)[-1]
                    for p in prefixes
                    if re.match(r"\d{19}$",
                                p.rstrip("/").rsplit("/", 1)[-1])]
        else:
            prefix = f"pr-logs/directory/{name}/"
            params = (f"prefix={urllib.parse.quote(prefix, safe='')}"
                      f"&maxResults={limit}"
                      f"&fields=items(name,metadata)")
            if start_bid:
                offset = f"{prefix}{start_bid}"
                params += (f"&startOffset="
                           f"{urllib.parse.quote(offset, safe='')}")
            url = f"{GCS_API}?{params}"
            data = self.fetcher.fetch_json(url, timeout=15)
            if not data:
                return []
            items = data.get("items", [])
            results = []
            for item in items:
                fname = item["name"].rsplit("/", 1)[-1]
                if (fname == "latest-build.txt"
                        or not fname.endswith(".txt")):
                    continue
                bid = fname.replace(".txt", "")
                if not re.match(r"\d{19}$", bid):
                    continue
                meta = item.get("metadata", {})
                gs_link = meta.get("x-goog-meta-link", "")
                results.append((bid, gs_link))
            return results

    def list_jobs(self, env, job_type, limit=20, since=None):
        """List recent jobs via the GCS JSON API.

        Uses the GCS storage API to list build IDs, Snowflake
        decode for start timestamps, and parallel finished.json
        fetches for results.
        """
        if since and not re.match(r"\d{4}-\d{2}-\d{2}", since):
            raise ValueError(
                f"--since must be ISO format (YYYY-MM-DD or "
                f"YYYY-MM-DDTHH:MM), got: {since}")

        cfg = ENVS.get(env)
        name = cfg.get(job_type) if cfg else None
        if not name:
            valid = [e for e, c in ENVS.items()
                     if job_type in c]
            raise ValueError(
                f"No {job_type} job for env '{env}'. "
                f"Valid: {', '.join(valid)}.")

        start_bid = self._iso_to_bid(since) if since else None

        listing = self._gcs_list_builds(
            name, job_type, start_bid=start_bid)
        if not listing:
            return []

        if job_type == "periodic":
            jobs_to_fetch = [
                (bid, f"{GCS_DIRECT}/logs/{name}/{bid}")
                for bid in listing]
        else:
            jobs_to_fetch = []
            for bid, gs_link in listing:
                if gs_link.startswith("gs://"):
                    rel = gs_link.replace(
                        f"gs://{GCS_BUCKET}/", "", 1)
                    gcs_url = f"{GCS_DIRECT}/{rel}"
                    jobs_to_fetch.append((bid, gcs_url))

        jobs_to_fetch.sort(key=lambda x: x[0], reverse=True)
        if not since:
            jobs_to_fetch = jobs_to_fetch[:limit]

        def _build_entry(bid_and_url):
            bid, gcs_url = bid_and_url
            started = self._bid_to_iso(bid)
            gcsweb_url = gcs_url.replace(
                GCS_DIRECT, GCSWEB_BASE)
            entry = {
                "state": "pending",
                "started": started,
                "job_id": bid,
                "base_url": gcsweb_url,
            }
            if job_type == "presubmit":
                parts = gcs_url.split("/")
                try:
                    idx = parts.index("pull") + 2
                    entry["pr"] = int(parts[idx])
                except (ValueError, IndexError):
                    pass

            finished = self.fetcher.fetch_json(
                f"{gcs_url}/finished.json", timeout=5)
            if finished:
                result = (finished.get("result") or "").lower()
                entry["state"] = result or "unknown"
                rev = finished.get("revision")
                if rev:
                    entry["revision"] = rev[:12]
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
        return results

    def failure_summary(self, env, job_type, since=None,
                        history=20, until=None):
        """Parallel junit fetch, failure grouping, onset detection."""
        effective_limit = None if since else history
        all_jobs = self.list_jobs(
            env, job_type, limit=effective_limit, since=since)
        if until and all_jobs:
            cutoff = (until + "T23:59:59"
                      if len(until) == 10 else until)
            all_jobs = [j for j in all_jobs
                        if j["started"] <= cutoff]
        if not all_jobs:
            return {
                "env": env, "type": job_type,
                "passed": 0, "failed": 0, "aborted": 0,
                "pass_rate": 1.0, "failure_groups": [],
                "per_job_tests": [],
            }

        passed = [j for j in all_jobs
                  if j["state"] == "success"]
        failed = [j for j in all_jobs
                  if j["state"] in ("failure", "error")]
        aborted = [j for j in all_jobs
                   if j["state"] == "aborted"]
        completed = len(passed) + len(failed)
        pass_rate = (round(len(passed) / completed, 2)
                     if completed else 0.0)

        def _fetch_one(job):
            return job, self.fetch_failures(
                job["base_url"], env)

        # Fetch junit from failed jobs
        job_results = []
        with ThreadPoolExecutor(max_workers=20) as pool:
            for f in as_completed(
                    pool.submit(_fetch_one, j)
                    for j in failed):
                try:
                    job, result = f.result()
                except Exception:
                    continue
                if result:
                    job_results.append((job, result))

        # Fetch junit from passed jobs for onset detection
        passed_tests = {}
        with ThreadPoolExecutor(max_workers=20) as pool:
            for f in as_completed(
                    pool.submit(_fetch_one, j)
                    for j in passed):
                try:
                    job, result = f.result()
                except Exception:
                    continue
                if result:
                    for t in result.get("passed", []):
                        prev = passed_tests.get(t)
                        if not prev or job["started"] > prev:
                            passed_tests[t] = job["started"]

        # Group failures by test name
        groups = {}
        per_job_tests = []
        for job, result in job_results:
            failures = result.get("failures", [])
            if not failures:
                continue
            short = _short_url(job["base_url"])
            for failure in failures:
                name = (failure.get("test")
                        or failure.get("step")
                        or "unknown")
                msg = failure.get("message")
                if name not in groups:
                    groups[name] = {
                        "count": 0, "messages": [],
                        "jobs": [], "timestamps": [],
                        "prs": set(),
                    }
                g = groups[name]
                g["count"] += 1
                g["jobs"].append(short)
                g["timestamps"].append(job["started"])
                if "pr" in job:
                    g["prs"].add(job["pr"])
                if msg:
                    g["messages"].append(msg)
            entry = {
                "job": short,
                "started": job["started"],
                "passed": len(result.get("passed", [])),
                "failed": len(failures),
            }
            if job.get("revision"):
                entry["revision"] = job["revision"]
            per_job_tests.append(entry)
            for t in result.get("passed", []):
                prev = passed_tests.get(t)
                if not prev or job["started"] > prev:
                    passed_tests[t] = job["started"]

        per_job_tests.sort(key=lambda x: x["started"])

        failure_groups = []
        for name, g in sorted(groups.items(),
                              key=lambda x: x[1]["count"],
                              reverse=True):
            ts = sorted(g["timestamps"])
            deduped = _dedup_messages(g["messages"])
            fg = {
                "test": name,
                "count": g["count"],
                "jobs": g["jobs"],
                "first_seen": ts[0] if ts else None,
                "last_seen": ts[-1] if ts else None,
                "messages": deduped,
                "prs": sorted(g["prs"]),
            }
            last_passed = passed_tests.get(name)
            if last_passed:
                fg["last_passed"] = last_passed
            failure_groups.append(fg)

        return {
            "env": env, "type": job_type,
            "passed": len(passed), "failed": len(failed),
            "aborted": len(aborted), "pass_rate": pass_rate,
            "failure_groups": failure_groups,
            "per_job_tests": per_job_tests,
        }

    def summary(self, since=None, until=None, envs=None):
        """Quick health scan across all envs.

        Returns pass/fail counts and top failure names per env/type
        without fetching full evidence packets.
        """
        target_envs = envs or list(ENVS.keys())
        combos = []
        for env in target_envs:
            cfg = ENVS.get(env)
            if not cfg:
                continue
            for jt in ("periodic", "presubmit"):
                if jt in cfg:
                    combos.append((env, jt))

        def _scan(env_jt):
            env, jt = env_jt
            effective_limit = None if since else 20
            jobs = self.list_jobs(env, jt, limit=effective_limit,
                                 since=since)
            if until and jobs:
                cutoff = (until + "T23:59:59"
                          if len(until) == 10 else until)
                jobs = [j for j in jobs if j["started"] <= cutoff]

            passed = [j for j in jobs if j["state"] == "success"]
            failed = [j for j in jobs
                      if j["state"] in ("failure", "error")]
            aborted = [j for j in jobs
                       if j["state"] == "aborted"]
            completed = len(passed) + len(failed)
            pass_rate = (round(len(passed) / completed, 2)
                         if completed else 0.0)

            # Lightweight top-failure sample from 3 most recent fails
            top_failures = []
            for job in failed[:3]:
                result = self.fetch_failures(job["base_url"], env)
                if result:
                    for f in result.get("failures", []):
                        name = (f.get("test") or f.get("step")
                                or "unknown")
                        if name not in top_failures:
                            top_failures.append(name)

            return {
                "env": env, "type": jt,
                "passed": len(passed), "failed": len(failed),
                "aborted": len(aborted), "pass_rate": pass_rate,
                "top_failures": top_failures[:5],
            }

        results = []
        with ThreadPoolExecutor(max_workers=len(combos)) as pool:
            for f in as_completed(
                    pool.submit(_scan, c) for c in combos):
                try:
                    results.append(f.result())
                except Exception:
                    continue

        results.sort(key=lambda r: (r["env"], r["type"]))
        return results

    def failures(self, env, since=None, until=None):
        """Run failure_summary for all job types in an env.

        Returns list of failure_summary results (one per job type).
        This is the main entry point — parallel fetch + analysis.
        """
        cfg = ENVS.get(env)
        if not cfg:
            raise ValueError(
                f"Unknown env: {env}. "
                f"Valid: {', '.join(ENVS)}")
        combos = [jt for jt in ("periodic", "presubmit")
                  if jt in cfg]

        results = []
        with ThreadPoolExecutor(
                max_workers=len(combos)) as pool:
            futures = {
                pool.submit(
                    self.failure_summary, env, jt,
                    since=since, history=500, until=until): jt
                for jt in combos}
            for f in as_completed(futures):
                try:
                    results.append(f.result())
                except Exception as ex:
                    print(f"error: {ex}", file=sys.stderr)

        results.sort(key=lambda r: r["pass_rate"])
        return results


# --- Evidence rendering ---


def _render_summary(results):
    """Render summary scan as compact markdown table."""
    lines = []
    now_str = datetime.now(tz=timezone.utc).strftime(
        "%Y-%m-%d %H:%M UTC")
    lines.append(f"# CI Summary — {now_str}")
    lines.append("")
    lines.append(
        "| Env | Type | Passed | Failed"
        " | Pass Rate | Top Failures |")
    lines.append(
        "|-----|------|--------|-------"
        "|-----------|--------------|")
    for r in results:
        completed = r["passed"] + r["failed"]
        if not completed:
            continue
        pct = f"{r['pass_rate']:.0%}"
        top = ", ".join(r["top_failures"][:3]) or "—"
        lines.append(
            f"| {r['env']} | {r['type']} "
            f"| {r['passed']} | {r['failed']} "
            f"| {pct} | {top} |")
    lines.append("")
    return "\n".join(lines)


def _render_evidence(env_results):
    """Render failure_summary results as markdown for agent analysis."""
    lines = []
    now_str = datetime.now(tz=timezone.utc).strftime(
        "%Y-%m-%d %H:%M UTC")
    lines.append(f"# CI Evidence Packet — {now_str}")
    lines.append("")

    # Job summary table
    lines.append("## Jobs")
    lines.append(
        "| Env | Type | Passed | Failed"
        " | Aborted | Pass Rate |")
    lines.append(
        "|-----|------|--------|-------"
        "|---------|-----------|")
    for er in env_results:
        completed = er['passed'] + er['failed']
        if not completed:
            continue
        pct = f"{er['pass_rate']:.0%}"
        lines.append(
            f"| {er['env']} | {er['type']} "
            f"| {er['passed']} | {er['failed']} "
            f"| {er['aborted']} | {pct} |")
    lines.append("")

    # Per-job test results
    for er in env_results:
        pjt = er.get("per_job_tests", [])
        if not pjt:
            continue
        lines.append(
            f"## Per-Job — {er['env']}/{er['type']}")
        for job in pjt:
            rev = (f" @{job['revision']}"
                   if job.get("revision") else "")
            lines.append(
                f"  {job['started']} "
                f"{job['passed']}P/{job['failed']}F"
                f"{rev} {job['job']}")
    lines.append("")

    # Failure groups
    for er in env_results:
        groups = er.get("failure_groups", [])
        if not groups:
            continue
        total_runs = er["passed"] + er["failed"]
        lines.append(
            f"## Failures — {er['env']}/{er['type']} "
            f"({er['passed']}/{total_runs} passed)")
        lines.append("")

        for fg in groups:
            rate = (f" ({fg['count']}/{total_runs})"
                    if total_runs else "")
            lines.append(
                f"**{fg['test']}** — "
                f"{fg['count']}x{rate}")

            parts = []
            if fg.get("first_seen"):
                parts.append(
                    f"since {fg['first_seen']}")
            if fg.get("last_passed"):
                parts.append(
                    f"last pass {fg['last_passed']}")
            elif fg.get("first_seen"):
                parts.append("no pass in window")
            if parts:
                lines.append(
                    f"  {' | '.join(parts)}")

            for entry in fg.get("messages", []):
                msg = entry["msg"]
                count = entry["count"]
                suffix = (f" (x{count})"
                          if count > 1 else "")
                msg_lines = msg.split('\n')
                lines.append(
                    f"  msg{suffix}: {msg_lines[0]}")
                for ml in msg_lines[1:]:
                    if ml.strip():
                        lines.append(f"       {ml}")

            prs = fg.get("prs", [])
            if prs:
                lines.append(
                    f"  prs: "
                    f"{', '.join(f'#{p}' for p in prs)}")

            jobs = fg.get("jobs", [])
            if jobs:
                lines.append(
                    f"  jobs: {', '.join(jobs)}")
            lines.append("")

    return "\n".join(lines)


# --- CLI ---

def _parse_since(value):
    """Resolve relative date shorthand to ISO format.

    7d, 24h, 2w → ISO date/datetime. Passthrough for ISO input.
    """
    if not value or not value.strip():
        return None
    v = value.strip().lower()
    m = re.fullmatch(r"(\d+)([dhw])", v)
    if m:
        n, unit = int(m.group(1)), m.group(2)
        if unit == "w":
            delta = timedelta(weeks=n)
        elif unit == "h":
            delta = timedelta(hours=n)
        else:
            delta = timedelta(days=n)
        dt = datetime.now(tz=timezone.utc) - delta
        if unit == "h":
            return dt.strftime("%Y-%m-%dT%H:%M:%S")
        return dt.strftime("%Y-%m-%d")
    return value


def _die(msg):
    print(json.dumps({"error": msg}), file=sys.stderr)
    sys.exit(1)


def main(argv=None):
    parser = argparse.ArgumentParser(
        prog="prow.py",
        description="Prow CI triage data acquisition")
    sub = parser.add_subparsers(dest="command", required=True)

    su = sub.add_parser(
        "summary", help="Quick health scan across all envs")
    su.add_argument("--since",
                    help="ISO date or relative (7d, 24h, 2w)")
    su.add_argument("--until",
                    help="ISO date end filter")
    su.add_argument("--cache-dir")
    su.add_argument("--no-cache", action="store_true")

    fa = sub.add_parser(
        "failures", help="Evidence packet for one env")
    fa.add_argument("env", help="Environment (dev/int/stg/prod)")
    fa.add_argument("--since",
                    help="ISO date or relative (7d, 24h, 2w)")
    fa.add_argument("--until",
                    help="ISO date end filter")
    fa.add_argument("--cache-dir")
    fa.add_argument("--no-cache", action="store_true")

    bl = sub.add_parser(
        "build-log", help="Build-log tail")
    bl.add_argument("base_url")
    bl.add_argument("env", nargs="?", default=None)
    bl.add_argument("--step", choices=["test", "provision"],
                    default="test")
    bl.add_argument("--lines", type=int, default=80)

    args = parser.parse_args(argv)

    fetcher = None
    if (args.command in ("failures", "summary")
            and not getattr(args, "no_cache", False)):
        cache_dir = getattr(args, "cache_dir", None)
        fetcher = CachedFetcher(cache_dir=cache_dir)
    client = ProwClient(fetcher=fetcher)

    try:
        if args.command == "summary":
            since = _parse_since(args.since) or (
                datetime.now(tz=timezone.utc)
                - timedelta(days=7)
            ).strftime("%Y-%m-%d")
            results = client.summary(
                since=since, until=args.until)
            print(_render_summary(results))

        elif args.command == "failures":
            since = _parse_since(args.since) or (
                datetime.now(tz=timezone.utc)
                - timedelta(days=7)
            ).strftime("%Y-%m-%d")
            results = client.failures(
                args.env, since=since, until=args.until)
            print(_render_evidence(results))

        elif args.command == "build-log":
            base_url = _normalize_base_url(args.base_url)
            env = args.env or _detect_env_from_url(base_url)
            if not env:
                _die("Cannot detect env from URL. "
                     "Specify env explicitly.")
            result = client.build_log(
                base_url, env,
                step=args.step, lines=args.lines)
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
