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

import os
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

from tool_version_guard import defer_inflight_proposal

ROOT = Path(__file__).resolve().parents[2]


class ToolVersionGuardTests(unittest.TestCase):
    def test_new_proposal_waits_for_owned_tooling_pr(self):
        output = {"items": [{"type": "create_pull_request", "title": "bump bicep"}]}
        pr = {"number": 42, "user": {"login": "aro-hcp-robot[bot]"},
              "base": {"ref": "main"}, "labels": [{"name": "agentic-tool-upgrade"}]}
        defer_inflight_proposal(output, [pr])
        self.assertEqual([item["type"] for item in output["items"]], ["noop"])
        self.assertIn("#42", output["items"][0]["message"])

    def test_family_marker_serializes_before_label_is_applied(self):
        output = {"items": [{"type": "create_pull_request", "title": "bump bicep"}]}
        pr = {
            "number": 43,
            "user": {"login": "aro-hcp-robot[bot]"},
            "base": {"ref": "main"},
            "title": "chore(tools): bump bicep",
            "body": "<!-- agentic-tool-family: Azure/bicep -->",
            "labels": [],
        }
        defer_inflight_proposal(output, [pr])
        self.assertEqual([item["type"] for item in output["items"]], ["noop"])
        self.assertIn("#43", output["items"][0]["message"])

    def test_title_without_family_marker_does_not_serialize(self):
        proposal = {"type": "create_pull_request", "title": "bump bicep"}
        output = {"items": [proposal]}
        pr = {
            "number": 44,
            "user": {"login": "aro-hcp-robot[bot]"},
            "base": {"ref": "main"},
            "title": "chore(tools): ordinary maintenance",
            "body": "No tool family marker",
            "labels": [],
        }
        defer_inflight_proposal(output, [pr])
        self.assertEqual(output["items"], [proposal])

    def test_other_work_does_not_block_the_tooling_queue(self):
        for author, base, label in [
            ("human-maintainer", "main", "agentic-tool-upgrade"),
            ("aro-hcp-robot[bot]", "release", "agentic-tool-upgrade"),
            ("aro-hcp-robot[bot]", "main", "agentic-dependabot"),
        ]:
            with self.subTest(author=author, base=base, label=label):
                proposal = {"type": "create_pull_request", "title": "bump bicep"}
                output = {"items": [proposal]}
                pr = {"number": 42, "user": {"login": author},
                      "base": {"ref": base}, "labels": [{"name": label}]}
                defer_inflight_proposal(output, [pr])
                self.assertEqual(output["items"], [proposal])

    def test_api_failure_stderr_is_visible(self):
        with tempfile.TemporaryDirectory() as temporary:
            directory = Path(temporary)
            gh = directory / "gh"
            gh.write_text("#!/bin/sh\nprintf 'API denied\\n' >&2\nexit 7\n")
            gh.chmod(0o755)
            output = directory / "output.json"
            output.write_text('{"items": []}\n')
            environment = os.environ.copy()
            environment["PATH"] = f"{directory}:{environment['PATH']}"
            result = subprocess.run(
                [sys.executable, str(ROOT / ".github/scripts/tool_version_guard.py"),
                 str(output), "Azure/ARO-HCP"],
                env=environment, capture_output=True, text=True,
            )
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("API denied", result.stderr)


if __name__ == "__main__":
    unittest.main()
