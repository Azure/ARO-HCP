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

"""Keep shared tool-pin helpers on a single in-flight update branch."""

import argparse
import json
import subprocess
from pathlib import Path


OWNER = "aro-hcp-robot[bot]"
LABEL = "agentic-tool-upgrade"


def defer_inflight_proposal(output, prs):
    """Replace new proposals with a no-op while an owned tooling PR is open."""
    active = [
        pr["number"] for pr in prs
        if pr["user"]["login"] == OWNER
        and pr["base"]["ref"] == "main"
        and (
            any(label["name"] == LABEL for label in pr["labels"])
            or (
                pr.get("title", "").startswith("chore(tools): ")
                and "<!-- agentic-tool-family:" in pr.get("body", "")
            )
        )
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
    result = subprocess.run(
        ["gh", "api", "--paginate", "--slurp",
         f"repos/{args.repository}/pulls?state=open&per_page=100"],
        check=True, stdout=subprocess.PIPE, text=True,
    )
    prs = [pr for page in json.loads(result.stdout) for pr in page]
    defer_inflight_proposal(output, prs)
    args.output.write_text(json.dumps(output, indent=2) + "\n")


if __name__ == "__main__":
    main()
