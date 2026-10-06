#!/usr/bin/env python3
"""Fail closed on duplicate dependency PRs and unsupported automatic retests."""

import json
import os
import re
import subprocess
import sys
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
    if (
        ecosystem not in ("go", "npm")
        or not isinstance(family, str)
        or not re.fullmatch(r"[@a-zA-Z0-9._/+*-]{3,100}", family)
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
    title = item.get("title", "").casefold()
    if family.casefold() not in title or version.casefold() not in title:
        raise ValueError("PR title must identify the dependency family and target version")
    return ecosystem, family.casefold(), version.casefold(), set(manifests)


def overlaps(candidate, existing, files):
    _, family, _, manifests = candidate
    text = ((existing.get("title") or "") + "\n" + (existing.get("body") or "")).casefold()
    if family in text:
        return True
    if not manifests.intersection(files):
        return False
    # An unrecognized PR changing the same manifest may fix this family.
    return True


def guard_create(output, repo):
    items = [item for item in output["items"] if item.get("type") == "create_pull_request"]
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
        status["target_url"]
        for status in latest.values()
        if status["state"] in ("failure", "error") and status.get("target_url")
    }
    failed_urls.update(
        check["details_url"]
        for check in checks
        if check["conclusion"] in ("failure", "timed_out", "cancelled", "action_required", "startup_failure")
        and check.get("details_url")
    )
    if not any(url in evidence for url in failed_urls):
        raise ValueError("retest needs a current failing check URL and proven transient-failure evidence")
    links = set(re.findall(r"https://[^\s)]+", evidence))
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
            with open(sys.argv[2], encoding="utf-8") as source:
                guard_create(json.load(source), sys.argv[3])
        elif sys.argv[1] == "retest" and len(sys.argv) == 6:
            guard_retest(*sys.argv[2:])
        else:
            raise ValueError("usage: dependabot_guard.py create OUTPUT REPO | retest REPO PR SHA EVIDENCE")
    except (OSError, KeyError, TypeError, ValueError, subprocess.CalledProcessError) as error:
        print(f"Dependabot guard refused action: {error}", file=sys.stderr)
        sys.exit(1)


if __name__ == "__main__":
    main()
