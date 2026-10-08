#!/usr/bin/env python3
"""Fail closed on duplicate dependency PRs and unsupported automatic retests."""

import json
import os
import re
import subprocess
import sys
import tempfile
from datetime import datetime, timedelta, timezone


def gh_lines(path, expression, token=None):
    env = os.environ.copy()
    if token:
        env["GH_TOKEN"] = token
    result = subprocess.run(
        ["gh", "api", "--paginate", path, "--jq", expression],
        check=True,
        capture_output=True,
        text=True,
        env=env,
    )
    return [json.loads(line) for line in result.stdout.splitlines() if line]


def group(item):
    data = item.get("data")
    if not isinstance(data, dict):
        raise ValueError("create_pull_request requires dependency group data")
    ecosystem = data.get("ecosystem")
    family = data.get("package_family")
    version = data.get("target_version")
    manifests = data.get("manifests")
    if isinstance(manifests, list):
        manifests = [os.path.normpath(m) for m in manifests]
    if (
        ecosystem not in ("go", "npm")
        or not isinstance(family, str)
        or not re.fullmatch(r"[@a-zA-Z0-9._/+*-]{1,100}", family)
        or not isinstance(version, str)
        or not re.fullmatch(r"v?[0-9][a-zA-Z0-9.+_-]{0,50}", version)
        or not isinstance(manifests, list)
        or not manifests
        or any(
            not isinstance(path, str)
            or not re.fullmatch(r"[a-zA-Z0-9._/-]+", path)
            or path.startswith("/")
            or ".." in path.split("/")
            or path.rsplit("/", 1)[-1] not in ("go.mod", "package.json")
            for path in manifests
        )
    ):
        raise ValueError("invalid dependency group metadata")

    # Derive canonical changed manifests from each proposal's patch artifact.
    actual_files = set()
    if isinstance(item.get("files"), list):
        actual_files.update(os.path.normpath(f["path"]) for f in item["files"] if isinstance(f, dict) and "path" in f)
    elif isinstance(item.get("patch"), str):
        actual_files.update(os.path.normpath(f) for f in re.findall(r"^diff --git a/(.*) b/.*$", item["patch"], re.MULTILINE))
    actual_manifests = {f for f in actual_files if f.rsplit("/", 1)[-1] in ("go.mod", "package.json")}
    if actual_manifests and set(manifests) != actual_manifests:
        raise ValueError("manifests in data do not match actual manifests in patch")

    title = item.get("title", "").casefold()
    if family.casefold() not in title or version.casefold() not in title:
        raise ValueError("PR title must identify the dependency family and target version")
    return ecosystem, family.casefold(), version.casefold(), set(manifests)


def overlaps(candidate, existing, files):
    _, family, _, manifests = candidate
    title = (existing.get("title") or "").casefold()
    # Check for the canonical fix(deps): <family> to <version> pattern.
    if re.search(fr"fix\(deps\):\s+{re.escape(family)}\b", title):
        return True
    if not manifests.intersection(files):
        return False
    # An unrecognized PR changing the same manifest may fix this family.
    return True


def drop_creates_missing_data(output):
    """Drop creates that omitted `data` so a successful sweep is not failed.

    Invalid `data` still fails closed. Missing `data` is the agent forgetting
    the structured argument; stripping those items lets Process Safe Outputs
    skip them instead of opening an unguarded PR.
    """
    items = output.get("items")
    if not isinstance(items, list):
        raise ValueError("create_pull_request output items must be a list")
    kept = []
    skipped = 0
    for item in items:
        if item.get("type") == "create_pull_request" and not isinstance(item.get("data"), dict):
            title = item.get("title") or "untitled"
            print(
                f"::warning::Skipping create_pull_request without dependency group data: {title}",
                file=sys.stderr,
            )
            skipped += 1
            continue
        kept.append(item)
    if skipped:
        output["items"] = kept
        print(f"Dropped {skipped} create_pull_request item(s) missing dependency group data")
    return skipped


def guard_create(output, repo):
    drop_creates_missing_data(output)
    items = [item for item in output.get("items", []) if item.get("type") == "create_pull_request"]
    if not items:
        return
    groups = [group(item) for item in items]
    for index, (_, family, _, manifests) in enumerate(groups):
        for _, other_family, _, other_manifests in groups[:index]:
            if family == other_family or manifests.intersection(other_manifests):
                raise ValueError("multiple new PRs for the same family or manifest")
    prs = gh_lines(
        f"repos/{repo}/pulls?state=open&per_page=100",
        '.[] | {number, title, body, base: .base.ref}',
    )
    for pr in prs:
        if pr["base"] != "main":
            continue
        files = {
            file["filename"]
            for file in gh_lines(
                f"repos/{repo}/pulls/{pr['number']}/files?per_page=100",
                ".[] | {filename}",
            )
        }
        for candidate in groups:
            if overlaps(candidate, pr, files):
                raise ValueError(
                    f"open PR #{pr['number']} changes a manifest for {candidate[1]}; "
                    "reconcile that PR instead of creating a replacement"
                )
    print(f"Checked {len(groups)} dependency groups against {len(prs)} open PRs")


def rewrite_output(path, output):
    directory = os.path.dirname(path) or "."
    fd, tmp = tempfile.mkstemp(prefix="dependabot-guard-", suffix=".json", dir=directory)
    try:
        with os.fdopen(fd, "w", encoding="utf-8") as dest:
            json.dump(output, dest)
            dest.write("\n")
        os.replace(tmp, path)
    except Exception:
        try:
            os.unlink(tmp)
        except OSError:
            pass
        raise


def guard_retest(repo, number, sha, evidence, now=None):
    statuses = gh_lines(
        f"repos/{repo}/commits/{sha}/statuses?per_page=100",
        '.[] | {context, state, target_url, created_at}',
        os.environ["CI_TOKEN"],
    )
    checks = gh_lines(
        f"repos/{repo}/commits/{sha}/check-runs?per_page=100&filter=latest",
        '.check_runs[] | {name, status, conclusion, details_url}',
        os.environ["CI_TOKEN"],
    )
    latest = {}
    for status in statuses:
        if status["context"].casefold() != "tide":
            context = status["context"]
            if context not in latest or status["created_at"] > latest[context]["created_at"]:
                latest[context] = status
    failed_urls = {
        status["target_url"].rstrip(".;,")
        for status in latest.values()
        if status["state"] in ("failure", "error") and status.get("target_url")
    }
    failed_urls.update(
        check["details_url"].rstrip(".;,")
        for check in checks
        if check["conclusion"] in ("failure", "timed_out", "cancelled", "action_required", "startup_failure")
        and check.get("details_url")
    )
    links = {url.rstrip(".;,") for url in re.findall(r"https://[^\s)]+", evidence)}
    if not any(url in links for url in failed_urls):
        raise ValueError("retest needs a current failing check URL and proven transient-failure evidence")
    if not links.difference(failed_urls):
        raise ValueError("retest also needs a comparison run URL")
    comments = gh_lines(
        f"repos/{repo}/issues/{number}/comments?per_page=100",
        '.[] | {body, created_at}',
    )
    now = now or datetime.now(timezone.utc)
    for comment in comments:
        if "/retest-required" in comment["body"]:
            posted = datetime.fromisoformat(comment["created_at"].replace("Z", "+00:00"))
            if posted > now - timedelta(hours=24):
                raise ValueError(f"PR #{number} already requested a retest in the last 24 hours")
    print(f"PR #{number}: current failing check and comparison run verified; retest cooldown clear")


def main():
    try:
        if sys.argv[1] == "create" and len(sys.argv) == 4:
            path = sys.argv[2]
            with open(path, encoding="utf-8") as source:
                output = json.load(source)
            guard_create(output, sys.argv[3])
            rewrite_output(path, output)
        elif sys.argv[1] == "retest" and len(sys.argv) == 6:
            guard_retest(*sys.argv[2:])
        else:
            raise ValueError("usage: dependabot_guard.py create OUTPUT REPO | retest REPO PR SHA EVIDENCE")
    except (OSError, KeyError, TypeError, ValueError, subprocess.CalledProcessError) as error:
        print(f"Dependabot guard refused action: {error}", file=sys.stderr)
        sys.exit(1)


if __name__ == "__main__":
    main()
