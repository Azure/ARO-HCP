#!/usr/bin/env python3
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

"""Serialize agent-owned tool updates without inspecting unrelated PR files."""

import argparse
import json
import os
import subprocess
import tempfile
from pathlib import Path


OWNER = "aro-hcp-robot[bot]"
LABEL = "agentic-tool-upgrade"


def gh_json(endpoint, *, paginate=False):
    command = ["gh", "api"]
    if paginate:
        command.extend(["--paginate", "--slurp"])
    result = subprocess.run(
        [*command, endpoint], check=True, stdout=subprocess.PIPE, text=True,
    )
    value = json.loads(result.stdout)
    if not paginate:
        return value
    if not isinstance(value, list) or not value or any(not isinstance(page, list) for page in value):
        raise ValueError(f"incomplete paginated response: {endpoint}")
    return [item for page in value for item in page]


def tooling_pr(pr):
    """Recognize this workflow's PRs, including the pre-label publication window."""
    if (
        not isinstance(pr.get("labels"), list)
        or any(not isinstance(label, dict) or not isinstance(label.get("name"), str)
               for label in pr["labels"])
        or not isinstance(pr.get("title"), str)
        or (pr.get("body") is not None and not isinstance(pr["body"], str))
    ):
        raise ValueError("incomplete tooling pull-request metadata")
    return (
        any(label["name"] == LABEL for label in pr["labels"])
        or (
            pr["title"].startswith("chore(tools): ")
            and "<!-- agentic-tool-family:" in (pr.get("body") or "")
        )
    )


def validate_pr_identity(pr):
    if (
        not isinstance(pr, dict)
        or type(pr.get("number")) is not int or pr["number"] <= 0
        or not isinstance(pr.get("user"), dict)
        or not isinstance(pr["user"].get("login"), str) or not pr["user"]["login"]
        or pr.get("state") not in ("open", "closed")
    ):
        raise ValueError("incomplete pull-request identity")


def open_prs(repository):
    """Fetch fresh creator-filtered issues; inspect only owned tooling PR metadata."""
    issues = gh_json(
        f"repos/{repository}/issues?state=open&creator=aro-hcp-robot%5Bbot%5D&per_page=100",
        paginate=True,
    )
    numbers = set()
    prs = []
    for issue in issues:
        validate_pr_identity(issue)
        if issue["number"] in numbers:
            raise ValueError("duplicate open pull-request inventory")
        numbers.add(issue["number"])
        if (
            issue["user"]["login"] != OWNER or issue["state"] != "open"
            or "pull_request" not in issue
        ):
            continue
        if not isinstance(issue["pull_request"], dict):
            raise ValueError("incomplete pull-request issue metadata")
        if not tooling_pr(issue):
            continue
        pr = gh_json(f"repos/{repository}/pulls/{issue['number']}")
        validate_pr_identity(pr)
        if (
            pr["number"] != issue["number"]
            or not isinstance(pr.get("base"), dict)
            or not isinstance(pr["base"].get("ref"), str) or not pr["base"]["ref"]
        ):
            raise ValueError("incomplete tooling pull-request metadata")
        if (
            pr["user"]["login"] == OWNER and pr["state"] == "open"
            and pr["base"]["ref"] == "main" and tooling_pr(pr)
        ):
            prs.append(pr)
    return prs


def guard_create(output, repository):
    """Recheck the owned tooling queue immediately before safe-output publication."""
    if (
        not isinstance(output, dict) or output.get("errors")
        or not isinstance(output.get("items"), list)
        or any(not isinstance(item, dict) or not isinstance(item.get("type"), str)
               for item in output["items"])
    ):
        raise ValueError("invalid safe-output proposal inventory")
    for item in output["items"]:
        if item["type"] != "create_pull_request":
            continue
        for field in ("branch", "title", "body"):
            if not isinstance(item.get(field), str) or not item[field].strip():
                raise ValueError(f"create_pull_request requires {field}")
        target = item.get("repo", repository)
        if not isinstance(target, str) or target.lower() != repository.lower():
            raise ValueError("tooling proposals must target the current repository")
        if item.get("base", "main") not in ("", "main"):
            raise ValueError("tooling proposals must target main")
    defer_inflight_proposal(output, open_prs(repository))


def rewrite_output(path, output):
    """Leave the original artifact intact on failed validation or serialization."""
    descriptor, temporary = tempfile.mkstemp(prefix="tool-version-guard-", dir=path.parent)
    try:
        with os.fdopen(descriptor, "w", encoding="utf-8") as destination:
            json.dump(output, destination, indent=2)
            destination.write("\n")
        os.replace(temporary, path)
    finally:
        if os.path.exists(temporary):
            os.unlink(temporary)


def defer_inflight_proposal(output, prs):
    """Replace new proposals with a no-op while an owned tooling PR is open."""
    active = [
        pr["number"] for pr in prs
        if pr["user"]["login"] == OWNER
        and pr["state"] == "open"
        and pr["base"]["ref"] == "main"
        and tooling_pr(pr)
    ]
    if not active or not any(item["type"] == "create_pull_request" for item in output["items"]):
        return
    output["items"] = [item for item in output["items"] if item["type"] != "create_pull_request"]
    if not output["items"]:
        output["items"].append({
            "type": "noop",
            "message": "Waiting for existing tooling PR(s): " + ", ".join(f"#{n}" for n in active),
        })


def main():
    """Recheck open PRs with the publication token before applying safe outputs."""
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("output", type=Path)
    parser.add_argument("repository")
    args = parser.parse_args()
    output = json.loads(args.output.read_text())
    guard_create(output, args.repository)
    rewrite_output(args.output, output)


if __name__ == "__main__":
    main()
