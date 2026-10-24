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

import unittest

from tool_version_guard import defer_inflight_proposal


class ToolVersionGuardTests(unittest.TestCase):
    def test_new_proposal_waits_for_owned_tooling_pr(self):
        output = {"items": [{"type": "create_pull_request", "title": "bump bicep"}]}
        pr = {"number": 42, "user": {"login": "aro-hcp-robot[bot]"},
              "base": {"ref": "main"}, "labels": [{"name": "agentic-tool-upgrade"}]}
        defer_inflight_proposal(output, [pr])
        self.assertEqual([item["type"] for item in output["items"]], ["noop"])
        self.assertIn("#42", output["items"][0]["message"])

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


if __name__ == "__main__":
    unittest.main()
