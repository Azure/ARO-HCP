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


class PublicationInventoryTests(unittest.TestCase):
    """Exercise the public CLI with gh-aw v0.93.4 proposal and patch artifacts."""

    REPOSITORY = "Azure/ARO-HCP"
    PULLS = f"repos/{REPOSITORY}/pulls"
    GH = r'''
import json
import os
import sys
from pathlib import Path
from urllib.parse import urlsplit

arguments = sys.argv[1:]
with Path(os.environ["GUARD_GH_CALLS"]).open("a") as calls:
    calls.write(json.dumps(arguments) + "\n")
if not arguments or arguments[0] != "api":
    sys.exit("Only offline gh api fixtures are supported")
endpoint = next((argument for argument in arguments if argument.startswith("repos/")), "")
responses = json.loads(Path(os.environ["GUARD_GH_RESPONSES"]).read_text())
response = responses.get(urlsplit(endpoint).path)
if response is None:
    sys.exit("Unexpected API endpoint: " + endpoint)
if response.get("returncode"):
    print(response["stderr"], file=sys.stderr)
    sys.exit(response["returncode"])
if "raw" in response:
    sys.stdout.write(response["raw"])
elif "pages" in response:
    pages = response["pages"]
    if "--paginate" not in arguments:
        pages = pages[:1]
    if "--slurp" in arguments:
        print(json.dumps(pages))
    else:
        for page in pages:
            print(json.dumps(page))
else:
    print(json.dumps(response["body"]))
'''

    def setUp(self):
        temporary = tempfile.TemporaryDirectory(prefix="tool-version-guard-")
        self.addCleanup(temporary.cleanup)
        self.directory = Path(temporary.name)
        self.checkout = self.directory
        self.branch = f"guard-tests/{self.directory.name}"
        self.proposal = {
            "type": "create_pull_request",
            "branch": self.branch,
            "title": "bump yq",
            "body": "<!-- agentic-tool-family: mikefarah/yq -->",
            "base": "main",
        }
        self.output = {"items": [self.proposal]}
        self.responses = {self.PULLS: {"pages": [[]]}}
        self.patch_directory = Path("/tmp/gh-aw")
        self.patch_directory.mkdir(parents=True, exist_ok=True)
        branch_filename = self.branch.replace("/", "-")
        self.qualified_patch = self.patch_directory / f"aw-azure-aro-hcp-{branch_filename}.patch"
        self.legacy_patch = self.patch_directory / f"aw-{branch_filename}.patch"
        self.qualified_bundle = self.qualified_patch.with_suffix(".bundle")
        self.legacy_bundle = self.legacy_patch.with_suffix(".bundle")
        for path in (
            self.qualified_patch, self.legacy_patch,
            self.qualified_bundle, self.legacy_bundle,
        ):
            self.addCleanup(path.unlink, missing_ok=True)
        self.write_patch(".bingo/yq.mod")
        gh = self.directory / "gh"
        gh.write_text(f"#!{sys.executable}\n{self.GH}")
        gh.chmod(0o755)

    def write_patch(self, *filenames, content=None, legacy=False):
        # generate_git_patch.cjs emits format-patch mail; the handler derives its
        # canonical artifact filename from branch/repo, not item.files metadata.
        if content is None:
            header = (
                f"From {'1' * 40} Mon Sep 17 00:00:00 2001\n"
                f"X-GH-AW-Base-Commit: {'2' * 40}\n"
                "From: Tool Updater <tool-updater@example.com>\n"
                "Date: Tue, 27 Oct 2026 12:00:00 +0000\n"
                "Subject: [PATCH] bump tool\n\n---\n\n"
            )
            diffs = [
                f"diff --git a/{filename} b/{filename}\n"
                "index 1111111..2222222 100644\n"
                f"--- a/{filename}\n"
                f"+++ b/{filename}\n"
                "@@ -1 +1 @@\n"
                "-v1.0.0\n"
                "+v1.1.0\n"
                for filename in filenames
            ]
            content = header + "".join(diffs) + "-- \n2.43.0\n"
        path = self.legacy_patch if legacy else self.qualified_patch
        path.write_text(content)

    def git(self, *arguments, cwd=None):
        environment = {
            key: value for key, value in os.environ.items()
            if not key.startswith("GIT_")
        }
        environment.update({
            "GIT_CONFIG_GLOBAL": os.devnull,
            "GIT_CONFIG_NOSYSTEM": "1",
            "GIT_AUTHOR_DATE": "2026-10-27T12:00:00+00:00",
            "GIT_COMMITTER_DATE": "2026-10-27T12:00:00+00:00",
            "GIT_TERMINAL_PROMPT": "0",
        })
        result = subprocess.run(
            ["git", "-c", "user.name=Tool Updater",
             "-c", "user.email=tool-updater@example.com",
             "-c", "commit.gpgsign=false", "-c", f"core.hooksPath={os.devnull}",
             *arguments],
            cwd=cwd or self.checkout, env=environment,
            check=True, capture_output=True, text=True,
        )
        return result.stdout

    def write_bundle(self, *filenames, merge_only_path=None, shallow=False):
        """Generate the default paired transport with real, local Git objects."""
        producer = self.directory / "producer"
        self.git("init", "--quiet", "--initial-branch=main", str(producer))
        if shallow:
            self.git("commit", "--quiet", "--allow-empty", "-m", "older history", cwd=producer)
        base_files = [*filenames]
        if merge_only_path is not None:
            base_files.append(merge_only_path)
        for filename in base_files:
            path = producer / filename
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text("v1.0.0\n")
        self.git("add", "--", *base_files, cwd=producer)
        self.git("commit", "--quiet", "-m", "base tool pins", cwd=producer)
        base = self.git("rev-parse", "HEAD", cwd=producer).strip()
        self.git("checkout", "--quiet", "-b", self.branch, cwd=producer)
        for filename in filenames:
            (producer / filename).write_text("v1.1.0\n")
        self.git("commit", "--quiet", "-am", "bump tool", cwd=producer)
        if merge_only_path is not None:
            self.git("checkout", "--quiet", "-b", "side-topology", base, cwd=producer)
            self.git("commit", "--quiet", "--allow-empty", "-m", "side topology", cwd=producer)
            self.git("checkout", "--quiet", self.branch, cwd=producer)
            self.git("merge", "--no-commit", "--no-ff", "side-topology", cwd=producer)
            (producer / merge_only_path).write_text("v1.1.0\n")
            self.git("add", "--", merge_only_path, cwd=producer)
            self.git("commit", "--quiet", "-m", "merge-only tool pin", cwd=producer)
        # Match generate_git_patch.cjs embedBaseCommit and the normal
        # generate_git_bundle.cjs base..branch transport range.
        patch = self.git("format-patch", f"{base}..{self.branch}", "--stdout", cwd=producer)
        patch = patch.replace("\n", f"\nX-GH-AW-Base-Commit: {base}\n", 1)
        self.qualified_patch.write_text(patch)
        self.git(
            "bundle", "create", str(self.qualified_bundle),
            f"{base}..{self.branch}", cwd=producer,
        )
        self.proposal["base_commit"] = base
        # The publication checkout has the prerequisite base, not the candidate
        # branch or its objects. Only the bundle carries the proposed commits.
        checkout = self.directory / "checkout"
        self.git(
            "clone", "--quiet", "--no-local", "--single-branch", "--branch", "main",
            *(["--depth=1"] if shallow else []), str(producer), str(checkout),
        )
        self.checkout = checkout

    def open_pr(self, number, *filenames, author="human-maintainer"):
        pr = {
            "number": number,
            "state": "open",
            "title": "Independent maintenance",
            "body": "",
            "user": {"login": author},
            "base": {"ref": "main"},
            "labels": [],
        }
        files = [
            {"filename": filename, "status": "modified"}
            if isinstance(filename, str) else filename
            for filename in filenames
        ]
        self.responses[f"{self.PULLS}/{number}"] = {
            "body": {**pr, "changed_files": len(files)},
        }
        self.responses[f"{self.PULLS}/{number}/files"] = {"pages": [files]}
        return pr

    def inventory(self, *prs):
        self.responses[self.PULLS] = {"pages": [list(prs)]}

    def run_guard(self, original=None):
        path = self.directory / "output.json"
        if original is None:
            original = json.dumps(self.output, indent=1) + "\n"
        path.write_text(original)
        # This is the earlier agent inventory, not publication-time authority.
        (self.directory / "tool-version-open-prs.json").write_text("[]\n")
        responses = self.directory / "responses.json"
        responses.write_text(json.dumps(self.responses))
        calls = self.directory / "calls.jsonl"
        calls.write_text("")
        environment = {
            key: value for key, value in os.environ.items()
            if not key.startswith(("GH_AW_", "GIT_"))
        }
        environment.update({
            "PATH": f"{self.directory}:{os.environ['PATH']}",
            "GUARD_GH_RESPONSES": str(responses),
            "GUARD_GH_CALLS": str(calls),
            "GIT_CONFIG_GLOBAL": os.devnull,
            "GIT_CONFIG_NOSYSTEM": "1",
            "GIT_TERMINAL_PROMPT": "0",
        })
        result = subprocess.run(
            [sys.executable, str(ROOT / ".github/scripts/tool_version_guard.py"),
             str(path), self.REPOSITORY],
            cwd=self.checkout, env=environment, capture_output=True, text=True,
        )
        recorded = [json.loads(line) for line in calls.read_text().splitlines()]
        return result, path.read_text(), original, recorded

    def assert_deferred(self, result, written, number):
        self.assertEqual(result.returncode, 0, result.stderr)
        output = json.loads(written)
        self.assertEqual([item["type"] for item in output["items"]], ["noop"])
        self.assertIn(f"#{number}", output["items"][0]["message"])
        return output

    def assert_failed_closed(self):
        result, written, original, _ = self.run_guard()
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(written, original)
        self.assertTrue(result.stderr.strip())
        return result

    def test_human_and_other_bot_arriving_at_publication_defer_new_proposal(self):
        for author, label in (
            ("human-maintainer", "agentic-tool-upgrade"),
            ("dependabot[bot]", "agentic-dependabot"),
        ):
            with self.subTest(author=author, label=label):
                pr = self.open_pr(101, ".bingo/yq.mod", author=author)
                pr["labels"].append({"name": label})
                self.inventory(pr)
                result, written, _, calls = self.run_guard()
                self.assert_deferred(result, written, 101)
                inventory_call = next(
                    call for call in calls
                    if any(argument.split("?")[0] == self.PULLS for argument in call)
                )
                self.assertIn("--paginate", inventory_call)
                self.assertIn("--slurp", inventory_call)
                endpoint = next(argument for argument in inventory_call if argument.startswith("repos/"))
                self.assertIn("state=open", endpoint)
                self.assertIn("base=main", endpoint)

    def test_disjoint_prs_allow_create_and_preserve_other_outputs(self):
        other_outputs = [
            {"type": "noop", "message": "Other work is unchanged"},
            {"type": "report_missing_data", "reason": "No release notes"},
        ]
        self.output = {"items": [self.proposal, *other_outputs], "version": 1}
        self.inventory(
            self.open_pr(102, ".bingo/helm.mod"),
            self.open_pr(103, "dev-infrastructure/openshift-ci/versions.mk", author="renovate[bot]"),
            self.open_pr(104, "frontend/README.md"),
        )
        result, written, _, calls = self.run_guard()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(json.loads(written), self.output)
        for number in (102, 103, 104):
            self.assertTrue(any(
                f"{self.PULLS}/{number}/files?per_page=100" in call for call in calls
            ))

    def test_overlap_preserves_non_create_outputs_and_envelope(self):
        other_outputs = [
            {"type": "noop", "message": "Existing explanation"},
            {"type": "report_missing_data", "reason": "No release notes"},
        ]
        self.output = {"items": [self.proposal, *other_outputs], "version": 1}
        self.inventory(self.open_pr(105, ".bingo/yq.mod"))
        result, written, _, _ = self.run_guard()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(json.loads(written), {"items": other_outputs, "version": 1})

    def test_ci_version_and_dockerfile_paths_are_coupled(self):
        versions = "dev-infrastructure/openshift-ci/versions.mk"
        dockerfile = "dev-infrastructure/openshift-ci/Dockerfile"
        for candidate, existing in ((versions, dockerfile), (dockerfile, versions)):
            with self.subTest(candidate=candidate, existing=existing):
                self.write_patch(candidate)
                self.inventory(self.open_pr(106, existing))
                result, written, _, _ = self.run_guard()
                self.assert_deferred(result, written, 106)

    def test_shared_bingo_helpers_conflict_with_other_bingo_candidate_paths(self):
        for helper in (".bingo/Variables.mk", ".bingo/variables.env"):
            for candidate, existing in ((helper, ".bingo/yq.mod"), (".bingo/yq.mod", helper)):
                with self.subTest(candidate=candidate, existing=existing):
                    self.write_patch(candidate)
                    self.inventory(self.open_pr(107, existing))
                    result, written, _, _ = self.run_guard()
                    self.assert_deferred(result, written, 107)

    def test_later_inventory_and_file_pages_include_renamed_previous_path(self):
        unrelated = self.open_pr(108, "frontend/README.md")
        renamed = {
            "filename": ".bingo/renamed-yq.mod",
            "previous_filename": ".bingo/yq.mod",
            "status": "renamed",
        }
        overlapping = self.open_pr(109, "Makefile", renamed)
        self.responses[self.PULLS] = {"pages": [[unrelated], [overlapping]]}
        self.responses[f"{self.PULLS}/109/files"] = {
            "pages": [[{"filename": "Makefile", "status": "modified"}], [renamed]],
        }
        result, written, _, calls = self.run_guard()
        self.assert_deferred(result, written, 109)
        for call in calls:
            if any("/files?" in argument or argument.split("?")[0] == self.PULLS for argument in call):
                self.assertIn("--paginate", call)
                self.assertIn("--slurp", call)

    def test_renamed_candidate_conflicts_at_both_original_and_destination_paths(self):
        self.write_patch(content=(
            f"From {'1' * 40} Mon Sep 17 00:00:00 2001\n"
            f"X-GH-AW-Base-Commit: {'2' * 40}\n"
            "From: Tool Updater <tool-updater@example.com>\n"
            "Date: Tue, 27 Oct 2026 12:00:00 +0000\n"
            "Subject: [PATCH] rename tool pin\n\n---\n\n"
            "diff --git a/.bingo/yq.mod b/.bingo/renamed-yq.mod\n"
            "similarity index 100%\n"
            "rename from .bingo/yq.mod\n"
            "rename to .bingo/renamed-yq.mod\n"
            "-- \n2.43.0\n"
        ))
        for filename in (".bingo/yq.mod", ".bingo/renamed-yq.mod"):
            with self.subTest(filename=filename):
                self.inventory(self.open_pr(110, filename))
                result, written, _, _ = self.run_guard()
                self.assert_deferred(result, written, 110)

    def test_legacy_branch_named_patch_is_also_inspected(self):
        self.qualified_patch.unlink()
        self.write_patch(".bingo/yq.mod", legacy=True)
        self.inventory(self.open_pr(111, ".bingo/yq.mod"))
        result, written, _, _ = self.run_guard()
        self.assert_deferred(result, written, 111)

    def test_api_errors_fail_closed_and_retain_diagnostic(self):
        pr = self.open_pr(112, ".bingo/yq.mod")
        baseline = dict(self.responses)
        for endpoint in (self.PULLS, f"{self.PULLS}/112", f"{self.PULLS}/112/files"):
            with self.subTest(endpoint=endpoint):
                self.responses = dict(baseline)
                self.inventory(pr)
                self.responses[endpoint] = {"returncode": 7, "stderr": "API denied at publication"}
                result = self.assert_failed_closed()
                self.assertIn("API denied at publication", result.stderr)

    def test_later_pr_api_failure_is_not_hidden_by_an_earlier_overlap(self):
        self.inventory(
            self.open_pr(115, ".bingo/yq.mod"),
            self.open_pr(116, "Makefile", author="dependabot[bot]"),
        )
        self.responses[f"{self.PULLS}/116/files"] = {
            "returncode": 7, "stderr": "Later PR file inventory denied",
        }
        result = self.assert_failed_closed()
        self.assertIn("Later PR file inventory denied", result.stderr)

    def test_malformed_or_incomplete_api_responses_fail_closed(self):
        pr = self.open_pr(113, ".bingo/yq.mod")
        baseline = dict(self.responses)
        cases = [
            (self.PULLS, {"raw": "{invalid json"}),
            (self.PULLS, {"pages": [{"not": "a PR page"}]}),
            (self.PULLS, {"pages": [[{"number": 113}]]}),
            (f"{self.PULLS}/113", {"body": pr}),
            (f"{self.PULLS}/113", {"body": {**pr, "changed_files": 2}}),
            (f"{self.PULLS}/113", {"body": {**pr, "changed_files": 3001}}),
            (f"{self.PULLS}/113/files", {"raw": "{invalid json"}),
            (f"{self.PULLS}/113/files", {"pages": [{"not": "a file page"}]}),
            (f"{self.PULLS}/113/files", {"pages": [[{"status": "modified"}]]}),
            (f"{self.PULLS}/113/files", {"pages": [[{
                "filename": ".bingo/renamed-yq.mod", "status": "renamed",
            }]]}),
        ]
        for endpoint, response in cases:
            with self.subTest(endpoint=endpoint, response=response):
                self.responses = dict(baseline)
                self.inventory(pr)
                self.responses[endpoint] = response
                self.assert_failed_closed()

    def test_missing_or_malformed_patch_fails_closed(self):
        self.inventory(self.open_pr(114, "frontend/README.md"))
        self.qualified_patch.unlink()
        self.assert_failed_closed()
        for content in (
            "",
            "not a git patch\n",
            'diff --git "unterminated b/.bingo/yq.mod\n',
            "diff --git a/.bingo/yq.mod b/.bingo/yq.mod\n"
            "--- a/.bingo/yq.mod\n+++ b/.bingo/yq.mod\n"
            "@@ -1 +1 @@\n-v1.0.0\n",
        ):
            with self.subTest(content=content):
                self.write_patch(content=content)
                self.assert_failed_closed()

    def test_ambiguous_patch_namespaces_fail_closed(self):
        self.write_patch(".bingo/yq.mod", legacy=True)
        self.assert_failed_closed()

    def test_missing_branch_fails_closed_without_invented_file_metadata(self):
        del self.proposal["branch"]
        self.assert_failed_closed()

    def test_malformed_safe_output_fails_closed(self):
        for original in (
            "{invalid json\n",
            '{"items": {}}\n',
            '{"items": [null]}\n',
            '{"items": [], "errors": ["Agent output failed"]}\n',
        ):
            with self.subTest(original=original):
                result, written, _, _ = self.run_guard(original=original)
                self.assertNotEqual(result.returncode, 0)
                self.assertEqual(written, original)
                self.assertTrue(result.stderr.strip())

    def test_non_create_outputs_do_not_require_patch_artifacts(self):
        self.output = {
            "items": [{"type": "noop", "message": "No version updates"}],
            "version": 1,
        }
        self.qualified_patch.unlink()
        result, written, _, _ = self.run_guard()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(json.loads(written), self.output)

    def test_default_paired_bundle_transport_defers_in_either_namespace(self):
        self.write_bundle(".bingo/yq.mod")
        self.inventory(self.open_pr(117, ".bingo/yq.mod", author="dependabot[bot]"))
        result, written, _, _ = self.run_guard()
        self.assert_deferred(result, written, 117)
        self.qualified_patch.rename(self.legacy_patch)
        self.qualified_bundle.rename(self.legacy_bundle)
        result, written, _, _ = self.run_guard()
        self.assert_deferred(result, written, 117)

    def test_default_paired_bundle_transport_allows_disjoint_publication(self):
        self.write_bundle(".bingo/yq.mod")
        self.inventory(self.open_pr(118, ".bingo/helm.mod"))
        result, written, _, _ = self.run_guard()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(json.loads(written), self.output)

    def test_depth_one_checkout_preserves_boundaries_and_detects_bundle_overlap(self):
        self.write_bundle(".bingo/yq.mod", merge_only_path=".bingo/helm.mod", shallow=True)
        git_directory = self.checkout / ".git"
        before = {
            path.relative_to(git_directory): path.read_bytes()
            for path in git_directory.rglob("*") if path.is_file()
        }
        base = self.proposal["base_commit"]
        parent = self.git("rev-parse", f"{base}^", cwd=self.directory / "producer").strip()
        self.assertEqual((git_directory / "shallow").read_text().strip(), base)
        with self.assertRaises(subprocess.CalledProcessError):
            self.git("cat-file", "-e", parent)
        # Reproduce the original object-only alternate database failure. This
        # must not pass merely because this fixture happens to retain history.
        verifier = self.directory / "without-shallow-boundaries"
        self.git("init", "--quiet", "--bare", str(verifier))
        alternates = verifier / "objects/info/alternates"
        alternates.write_text(str(git_directory / "objects") + "\n")
        heads = self.git("bundle", "unbundle", str(self.qualified_bundle), cwd=verifier)
        tip = next(line.split()[0] for line in heads.splitlines()
                   if line.endswith(f" refs/heads/{self.branch}"))
        with self.assertRaises(subprocess.CalledProcessError) as failure:
            self.git("merge-base", "--is-ancestor", base, tip, cwd=verifier)
        self.assertEqual(failure.exception.returncode, 128)
        self.assertIn(parent, failure.exception.stderr)
        # The checkout cannot fetch missing parents from its now-absent remote.
        (self.directory / "producer").rename(self.directory / "offline-producer")
        self.inventory(self.open_pr(120, ".bingo/helm.mod"))
        result, written, _, _ = self.run_guard()
        self.assert_deferred(result, written, 120)
        self.inventory(self.open_pr(121, ".bingo/kustomize.mod"))
        result, written, _, _ = self.run_guard()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(json.loads(written), self.output)
        after = {
            path.relative_to(git_directory): path.read_bytes()
            for path in git_directory.rglob("*") if path.is_file()
        }
        self.assertEqual(after, before)

    def test_depth_one_checkout_does_not_bypass_missing_base_or_ancestry(self):
        self.write_bundle(".bingo/yq.mod", shallow=True)
        patch = self.qualified_patch.read_text()
        base = self.proposal["base_commit"]
        parent = self.git("rev-parse", f"{base}^", cwd=self.directory / "producer").strip()
        unrelated = self.git("commit-tree", f"{base}^{{tree}}", "-m", "unrelated root").strip()
        for recorded_base in (parent, unrelated):
            with self.subTest(recorded_base=recorded_base):
                self.qualified_patch.write_text(patch.replace(
                    f"X-GH-AW-Base-Commit: {base}\n",
                    f"X-GH-AW-Base-Commit: {recorded_base}\n",
                ))
                self.assert_failed_closed()

    def test_merge_only_bundle_change_is_included_in_overlap_inventory(self):
        self.write_bundle(".bingo/yq.mod", merge_only_path=".bingo/helm.mod")
        self.assertNotIn("diff --git a/.bingo/helm.mod", self.qualified_patch.read_text())
        self.inventory(self.open_pr(119, ".bingo/helm.mod"))
        result, written, _, _ = self.run_guard()
        self.assert_deferred(result, written, 119)

    def test_bundle_without_companion_policy_patch_fails_closed(self):
        self.write_bundle(".bingo/yq.mod")
        self.qualified_patch.unlink()
        self.assert_failed_closed()

    def test_corrupt_or_truncated_bundle_fails_closed(self):
        self.write_bundle(".bingo/yq.mod")
        bundle = self.qualified_bundle.read_bytes()
        for content in (b"", b"not a git bundle\n", bundle[:len(bundle) // 2]):
            with self.subTest(content_length=len(content)):
                self.qualified_bundle.write_bytes(content)
                self.assert_failed_closed()

    def test_bundle_missing_recorded_or_available_base_fails_closed(self):
        self.write_bundle(".bingo/yq.mod")
        patch = self.qualified_patch.read_text()
        header = f"X-GH-AW-Base-Commit: {self.proposal['base_commit']}\n"
        self.qualified_patch.write_text(patch.replace(header, ""))
        self.assert_failed_closed()
        self.qualified_patch.write_text(patch)
        self.checkout = self.directory
        self.assert_failed_closed()

    def test_split_patch_and_bundle_namespaces_fail_closed(self):
        self.write_bundle(".bingo/yq.mod")
        self.qualified_bundle.rename(self.legacy_bundle)
        self.assert_failed_closed()
        self.legacy_bundle.rename(self.qualified_bundle)
        self.qualified_patch.rename(self.legacy_patch)
        self.assert_failed_closed()

    def test_duplicate_paired_bundle_namespaces_fail_closed(self):
        self.write_bundle(".bingo/yq.mod")
        self.legacy_patch.write_bytes(self.qualified_patch.read_bytes())
        self.legacy_bundle.write_bytes(self.qualified_bundle.read_bytes())
        self.assert_failed_closed()


if __name__ == "__main__":
    unittest.main()
