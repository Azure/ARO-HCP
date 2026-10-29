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

import json
import os
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

from tool_version_guard import rewrite_output

ROOT = Path(__file__).resolve().parents[2]


class ToolVersionGuardTests(unittest.TestCase):
    """Exercise the actual CLI with a deny-by-default, offline gh API fixture."""

    REPOSITORY = "Azure/ARO-HCP"
    PULLS = f"repos/{REPOSITORY}/pulls"
    ISSUES = (
        f"repos/{REPOSITORY}/issues?state=open"
        "&creator=aro-hcp-robot%5Bbot%5D&per_page=100"
    )
    GH = r'''
import json
import os
import sys
from pathlib import Path

arguments = sys.argv[1:]
with Path(os.environ["GUARD_GH_CALLS"]).open("a") as calls:
    calls.write(json.dumps(arguments) + "\n")
responses = json.loads(Path(os.environ["GUARD_GH_RESPONSES"]).read_text())
endpoint = arguments[-1] if arguments else ""
response = responses.get(endpoint)
if response is None:
    sys.exit("Unexpected API endpoint: " + endpoint)
expected = ["api"]
if "pages" in response or response.get("paginate"):
    expected.extend(["--paginate", "--slurp"])
expected.append(endpoint)
if arguments != expected:
    sys.exit("Unexpected API arguments: " + repr(arguments))
if response.get("returncode"):
    print(response["stderr"], file=sys.stderr)
    sys.exit(response["returncode"])
if "raw" in response:
    sys.stdout.write(response["raw"])
elif "pages" in response:
    print(json.dumps(response["pages"]))
else:
    print(json.dumps(response["body"]))
'''

    def setUp(self):
        temporary = tempfile.TemporaryDirectory(prefix="tool-version-guard-")
        self.addCleanup(temporary.cleanup)
        self.directory = Path(temporary.name)
        self.proposal = {
            "type": "create_pull_request",
            "branch": "guard-tests/tool-update",
            "title": "chore(tools): bump yq",
            "body": "<!-- agentic-tool-family: mikefarah/yq -->",
            "base": "main",
        }
        self.output = {"items": [self.proposal]}
        self.responses = {self.ISSUES: {"pages": [[]]}}
        gh = self.directory / "gh"
        gh.write_text(f"#!{sys.executable}\n{self.GH}")
        gh.chmod(0o755)

    def open_pr(self, number, *, author="aro-hcp-robot[bot]", owned=True, **overrides):
        pr = {
            "number": number,
            "state": "open",
            "title": "Independent maintenance",
            "body": "",
            "user": {"login": author},
            "base": {"ref": "main"},
            "labels": [{"name": "agentic-tool-upgrade"}] if owned else [],
            "pull_request": {"url": f"https://api.github.com/{self.PULLS}/{number}"},
        }
        pr.update(overrides)
        return pr

    def inventory(self, *prs):
        self.responses[self.ISSUES] = {"pages": [list(prs)]}

    def detail(self, pr, **overrides):
        self.responses[f"{self.PULLS}/{pr['number']}"] = {"body": {**pr, **overrides}}

    def run_guard(self, original=None):
        path = self.directory / "output.json"
        if original is None:
            original = json.dumps(self.output, indent=1) + "\n"
        path.write_text(original)
        # An earlier workflow snapshot must never override publication-time data.
        (self.directory / "tool-version-open-prs.json").write_text("[]\n")
        responses = self.directory / "responses.json"
        responses.write_text(json.dumps(self.responses))
        calls = self.directory / "calls.jsonl"
        calls.write_text("")
        environment = os.environ.copy()
        environment.update({
            "PATH": f"{self.directory}:{os.environ['PATH']}",
            "GUARD_GH_RESPONSES": str(responses),
            "GUARD_GH_CALLS": str(calls),
        })
        result = subprocess.run(
            [sys.executable, str(ROOT / ".github/scripts/tool_version_guard.py"),
             str(path), self.REPOSITORY],
            cwd=self.directory, env=environment, capture_output=True, text=True,
        )
        recorded = [json.loads(line) for line in calls.read_text().splitlines()]
        return result, path.read_text(), original, recorded

    def assert_deferred(self, number):
        result, written, _, calls = self.run_guard()
        self.assertEqual(result.returncode, 0, result.stderr)
        output = json.loads(written)
        self.assertEqual([item["type"] for item in output["items"]], ["noop"])
        self.assertIn(f"#{number}", output["items"][0]["message"])
        return calls

    def assert_allowed(self):
        result, written, _, calls = self.run_guard()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(json.loads(written), self.output)
        return calls

    def assert_failed_closed(self, original=None):
        result, written, original, _ = self.run_guard(original=original)
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(written, original)
        self.assertTrue(result.stderr.strip())
        return result

    def test_owned_tooling_pr_blocks_new_proposal(self):
        pr = self.open_pr(42)
        self.inventory(pr)
        self.detail(pr)
        calls = self.assert_deferred(42)
        self.assertEqual(calls, [
            ["api", "--paginate", "--slurp", self.ISSUES],
            ["api", f"{self.PULLS}/42"],
        ])

    def test_family_marker_serializes_before_label_is_applied(self):
        pr = self.open_pr(
            43, owned=False, title="chore(tools): bump bicep",
            body="<!-- agentic-tool-family: Azure/bicep -->",
        )
        self.inventory(pr)
        self.detail(pr)
        self.assert_deferred(43)

    def test_unrelated_bot_prs_and_issues_do_not_block(self):
        unrelated = [
            self.open_pr(44, owned=False, title="chore(tools): ordinary maintenance"),
            self.open_pr(45, owned=False, body="<!-- agentic-tool-family: Azure/bicep -->"),
            self.open_pr(46, owned=False, title="Automated - Update component image digests"),
            self.open_pr(47, labels=[{"name": "agentic-dependabot"}]),
            self.open_pr(48, author="dependabot[bot]"),
            self.open_pr(49),
        ]
        del unrelated[-1]["pull_request"]
        self.inventory(*unrelated)
        self.assertEqual(self.assert_allowed(), [["api", "--paginate", "--slurp", self.ISSUES]])

    def test_human_6590_bingo_and_7676_43846_files_never_block_or_fetch_files(self):
        # Even if the creator filter unexpectedly returns humans, ownership wins.
        # No fixtures exist for PR detail, files, compare, or tree endpoints: any
        # such request fails the actual guard CLI instead of permitting a mock.
        self.inventory(
            self.open_pr(6590, author="human-maintainer", files=[
                ".bingo/Variables.mk", ".bingo/variables.env", ".bingo/yq.mod",
            ]),
            self.open_pr(7676, author="human-maintainer", changed_files=43846),
        )
        self.assertEqual(self.assert_allowed(), [["api", "--paginate", "--slurp", self.ISSUES]])

    def test_pagination_finds_owned_tooling_pr_on_later_page(self):
        unrelated = self.open_pr(50, owned=False)
        active = self.open_pr(51)
        self.responses[self.ISSUES] = {"pages": [[unrelated], [active]]}
        self.detail(active)
        calls = self.assert_deferred(51)
        self.assertEqual(len(calls), 2)
        self.assertEqual(calls[0], ["api", "--paginate", "--slurp", self.ISSUES])

    def test_closed_inventory_entry_is_ignored_without_detail_request(self):
        self.inventory(self.open_pr(52, state="closed"))
        self.assertEqual(len(self.assert_allowed()), 1)

    def test_closed_non_main_or_no_longer_owned_details_do_not_block(self):
        pr = self.open_pr(53)
        self.inventory(pr)
        for overrides in (
            {"state": "closed"},
            {"base": {"ref": "release"}},
            {"user": {"login": "human-maintainer"}},
            {"labels": [], "title": "Independent maintenance", "body": ""},
        ):
            with self.subTest(overrides=overrides):
                self.detail(pr, **overrides)
                self.assert_allowed()

    def test_owned_queue_preserves_other_outputs_and_envelope(self):
        other_outputs = [
            {"type": "noop", "message": "Other work is unchanged"},
            {"type": "report_missing_data", "reason": "No release notes"},
        ]
        self.output = {"items": [self.proposal, *other_outputs, dict(self.proposal)], "version": 1}
        pr = self.open_pr(54)
        self.inventory(pr)
        self.detail(pr)
        result, written, _, _ = self.run_guard()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(json.loads(written), {"items": other_outputs, "version": 1})

    def test_empty_queue_preserves_proposal_other_outputs_and_envelope(self):
        self.output = {
            "items": [self.proposal, {"type": "report_missing_data", "reason": "No release notes"}],
            "version": 1,
        }
        self.assert_allowed()

    def test_non_create_outputs_are_preserved_without_patch_artifacts(self):
        self.output = {"items": [{"type": "noop", "message": "No version updates"}], "version": 1}
        pr = self.open_pr(55)
        self.inventory(pr)
        self.detail(pr)
        self.assert_allowed()

    def test_api_errors_fail_closed_preserve_output_and_retain_stderr(self):
        pr = self.open_pr(56)
        for endpoint in (self.ISSUES, f"{self.PULLS}/56"):
            with self.subTest(endpoint=endpoint):
                self.inventory(pr)
                self.detail(pr)
                self.responses[endpoint] = {
                    "returncode": 7, "stderr": "API denied at publication",
                    "paginate": endpoint == self.ISSUES,
                }
                result = self.assert_failed_closed()
                self.assertIn("API denied at publication", result.stderr)

    def test_later_metadata_failure_is_not_hidden_by_active_owned_pr(self):
        first, second = self.open_pr(57), self.open_pr(58)
        self.inventory(first, second)
        self.detail(first)
        self.responses[f"{self.PULLS}/58"] = {
            "returncode": 7, "stderr": "Later tooling metadata denied",
        }
        result = self.assert_failed_closed()
        self.assertIn("Later tooling metadata denied", result.stderr)

    def test_malformed_or_incomplete_paginated_inventory_fails_closed(self):
        pr = self.open_pr(59)
        malformed = [
            {"raw": "{invalid json", "paginate": True},
            {"raw": "{}", "paginate": True},
            {"pages": []},
            {"pages": [{"not": "an issue page"}]},
            {"pages": [[None]]},
            {"pages": [[{"number": 59}]]},
            {"pages": [[pr], [pr]]},
        ]
        for field, value in (
            ("number", True), ("number", 0), ("user", {}), ("state", None),
            ("labels", None), ("labels", [{"not": "a label"}]),
            ("title", None), ("body", []), ("pull_request", None),
        ):
            malformed.append({"pages": [[{**pr, field: value}]]})
        self.detail(pr)
        for response in malformed:
            with self.subTest(response=response):
                self.responses[self.ISSUES] = response
                self.assert_failed_closed()

    def test_malformed_owned_pr_details_fail_closed(self):
        pr = self.open_pr(60)
        self.inventory(pr)
        malformed = [
            {"raw": "{invalid json"},
            {"body": []},
            {"body": {"number": 60}},
        ]
        for field, value in (
            ("number", 61), ("number", True), ("user", {}), ("state", None),
            ("base", {}), ("base", None), ("base", {"ref": ""}),
            ("labels", None), ("title", None), ("body", []),
        ):
            malformed.append({"body": {**pr, field: value}})
        for response in malformed:
            with self.subTest(response=response):
                self.responses[f"{self.PULLS}/60"] = response
                self.assert_failed_closed()

    def test_malformed_safe_output_fails_closed(self):
        for original in (
            "{invalid json\n", "null\n", '{"items": {}}\n',
            '{"items": [null]}\n', '{"items": [{"type": null}]}\n',
            '{"items": [], "errors": ["Agent output failed"]}\n',
        ):
            with self.subTest(original=original):
                self.assert_failed_closed(original=original)

    def test_invalid_proposal_metadata_fails_closed(self):
        baseline = dict(self.proposal)
        for field, value in (
            ("branch", None), ("title", ""), ("body", " "),
            ("repo", "another/repository"), ("repo", None), ("base", "release"),
        ):
            with self.subTest(field=field, value=value):
                self.output = {"items": [{**baseline, field: value}]}
                self.assert_failed_closed()


class AtomicOutputTests(unittest.TestCase):
    def test_serialization_failure_keeps_original_and_removes_temporary(self):
        with tempfile.TemporaryDirectory() as temporary:
            path = Path(temporary) / "output.json"
            original = '{"items": []}\n'
            path.write_text(original)
            with self.assertRaises(TypeError):
                rewrite_output(path, {"not_json": object()})
            self.assertEqual(path.read_text(), original)
            self.assertEqual(list(path.parent.iterdir()), [path])

    def test_replacement_failure_keeps_original_and_removes_temporary(self):
        with tempfile.TemporaryDirectory() as temporary:
            path = Path(temporary) / "output.json"
            original = '{"items": []}\n'
            path.write_text(original)
            with patch("tool_version_guard.os.replace", side_effect=OSError("replacement denied")):
                with self.assertRaisesRegex(OSError, "replacement denied"):
                    rewrite_output(path, {"items": [{"type": "noop"}]})
            self.assertEqual(path.read_text(), original)
            self.assertEqual(list(path.parent.iterdir()), [path])


if __name__ == "__main__":
    unittest.main()
