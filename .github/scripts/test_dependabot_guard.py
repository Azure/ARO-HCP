"""Focused safety checks for agentic dependency PR creation and retests."""

import base64
import difflib
import importlib.util
import io
import json
import os
import sys
import subprocess
import tempfile
from datetime import datetime, timezone
from pathlib import Path
from unittest import TestCase, main
from unittest.mock import patch


spec = importlib.util.spec_from_file_location(
    "dependabot_guard", Path(__file__).with_name("dependabot_guard.py")
)
guard = importlib.util.module_from_spec(spec)
spec.loader.exec_module(guard)


def create_item(manifest="api/package.json", family="typespec", patch_files=None, ecosystem="npm"):
    item = {
        "type": "create_pull_request",
        "title": f"{ecosystem} {family} family to 1.16.0 (high)",
        "data": {
            "ecosystem": ecosystem,
            "package_family": family,
            "target_version": "1.16.0",
            "manifests": [manifest],
        },
    }
    if patch_files:
        item["files"] = [{"path": f, "content": "..."} for f in patch_files]
    return item


def changed_file(filename, before, after, status="modified"):
    lines = list(difflib.unified_diff(before.splitlines(), after.splitlines(), lineterm=""))[2:]
    return {
        "filename": filename,
        "status": status,
        "patch": "\n".join(lines),
        "additions": sum(line.startswith("+") for line in lines),
        "deletions": sum(line.startswith("-") for line in lines),
    }


def open_pr(number=7689, title="Human toolchain update"):
    return {"number": number, "title": title, "body": "", "base": "main"}


def go_item(manifest="service/go.mod", family="github.com/gofiber/fiber/v2"):
    return create_item(manifest, family, ecosystem="go")


def npm_manifest(dependencies=None, **other):
    return json.dumps({"dependencies": dependencies or {}, **other}, indent=2) + "\n"


class MetadataPreflightTests(TestCase):
    def run_preflight(self, item):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "proposal.json"
            original = json.dumps(item)
            path.write_text(original)
            result = subprocess.run(
                [sys.executable, str(Path(__file__).with_name("dependabot_guard.py")), "validate", str(path)],
                env={**os.environ, "PATH": ""},
                capture_output=True, text=True,
            )
            self.assertEqual(path.read_text(), original)
            return result

    def test_combined_unrelated_npm_names_are_rejected(self):
        item = create_item(family="undici and fast-uri")
        item["data"]["target_version"] = "7.30.1"
        result = self.run_preflight(item)
        self.assertEqual(result.returncode, 1)
        self.assertEqual(result.stdout, "")

    def test_independent_npm_packages_keep_their_own_versions(self):
        for family, version in [("undici", "7.30.1"), ("fast-uri", "3.1.9")]:
            with self.subTest(family=family):
                item = create_item(family=family)
                item["data"]["target_version"] = version
                result = self.run_preflight(item)
                self.assertEqual(result.returncode, 0, result.stderr)
                validated = json.loads(result.stdout)
                self.assertEqual(validated["data"], item["data"])
                self.assertEqual(validated["title"], f"{family} to {version}")


class CreateGuardTests(TestCase):
    @patch.object(guard, "gh_lines", return_value=[])
    def test_short_display_title_does_not_block_valid_fiber_proposal(self, _gh):
        item = go_item("internal/go.mod")
        item["title"] = "gofiber/fiber/v2 to 2.52.14 (medium)"
        item["data"]["target_version"] = "2.52.14"
        output = {"items": [item]}
        guard.guard_create(output, "Azure/ARO-HCP")
        self.assertEqual(output["items"][0]["title"], "github.com/gofiber/fiber/v2 to 2.52.14")

    @patch.object(guard, "gh_lines")
    def test_7689_go_directive_update_does_not_block_fiber(self, gh):
        before = "module example.com/service\n\ngo 1.26.0\n\nrequire github.com/gofiber/fiber/v2 v2.52.0\n"
        gh.side_effect = [
            [open_pr()],
            [changed_file("service/go.mod", before, before.replace("go 1.26.0", "go 1.26.10"))],
        ]
        item = go_item()
        output = {"items": [item]}
        guard.guard_create(output, "Azure/ARO-HCP")
        self.assertEqual(output["items"], [item])

    @patch.object(guard, "gh_lines")
    def test_unrelated_go_dependency_in_shared_manifest_is_allowed(self, gh):
        before = "module example.com/service\n\ngo 1.26.10\n\nrequire (\n\tgithub.com/gofiber/fiber/v2 v2.52.0\n\tgolang.org/x/net v0.48.0\n)\n"
        gh.side_effect = [
            [open_pr()],
            [changed_file("service/go.mod", before, before.replace("v0.48.0", "v0.49.0"))],
        ]
        item = go_item()
        output = {"items": [item]}
        guard.guard_create(output, "Azure/ARO-HCP")
        self.assertEqual(output["items"], [item])

    @patch.object(guard, "gh_lines")
    def test_go_require_formatting_and_indirect_comments_do_not_overlap(self, gh):
        prefix = "module example.com/service\n\ngo 1.26.10\n\n"
        before = prefix + "require github.com/gofiber/fiber/v2 v2.52.0 // indirect\n"
        for after in (
            before.replace(" // indirect", ""),
            before.replace(" v2.52.0", "\t\tv2.52.0"),
            prefix + 'require (\n\t"github.com/gofiber/fiber/v2" v2.52.0\n)\n',
        ):
            with self.subTest(after=after):
                gh.side_effect = [[open_pr()], [changed_file("service/go.mod", before, after)]]
                item = go_item()
                output = {"items": [item]}
                guard.guard_create(output, "Azure/ARO-HCP")
                self.assertEqual(output["items"], [item])

    @patch.object(guard, "gh_lines")
    def test_go_replacement_formatting_does_not_overlap(self, gh):
        before = "module example.com/service\n\nreplace github.com/gofiber/fiber/v2 => github.com/gofiber/fiber/v2 v2.52.0\n"
        after = "module example.com/service\n\nreplace (\n\tgithub.com/gofiber/fiber/v2\t=>\tgithub.com/gofiber/fiber/v2 v2.52.0 // equivalent\n)\n"
        gh.side_effect = [[open_pr()], [changed_file("service/go.mod", before, after)]]
        item = go_item()
        output = {"items": [item]}
        guard.guard_create(output, "Azure/ARO-HCP")
        self.assertEqual(output["items"], [item])

    @patch.object(guard, "gh_lines")
    def test_go_dependency_additions_and_removals_still_overlap(self, gh):
        before = "module example.com/service\n\ngo 1.26.10\n"
        after = before + "\nrequire github.com/gofiber/fiber/v2 v2.52.0\n"
        for old, new in ((before, after), (after, before)):
            with self.subTest(old=old, new=new):
                gh.side_effect = [[open_pr()], [changed_file("other/go.mod", old, new)]]
                output = {"items": [go_item()]}
                guard.guard_create(output, "Azure/ARO-HCP")
                self.assertEqual(output["items"], [])

    @patch.object(guard, "gh_lines")
    def test_real_go_overlap_defers_only_candidate_and_preserves_other_outputs(self, gh):
        before = "module example.com/service\n\nrequire (\n\tgithub.com/gofiber/fiber/v2 v2.52.0\n)\n"
        gh.side_effect = [
            [open_pr()],
            [changed_file("service/go.mod", before, before.replace("v2.52.0", "v2.53.0"))],
        ]
        independent = create_item()
        reconcile = {"type": "reconcile_owned_pr", "pull_request_number": "7187"}
        noop = {"type": "noop", "message": "Other work preserved"}
        output = {"items": [go_item(), independent, reconcile, noop], "other": "preserved"}
        with patch("sys.stderr", new_callable=io.StringIO) as stderr:
            guard.guard_create(output, "Azure/ARO-HCP")
        self.assertEqual(output, {"items": [independent, reconcile, noop], "other": "preserved"})
        self.assertIn("#7689", stderr.getvalue())
        self.assertIn("github.com/gofiber/fiber/v2", stderr.getvalue())
        self.assertIn("changes dependency", stderr.getvalue())
        self.assertEqual(gh.call_count, 2)  # Read-only listing; no PR/issue mutations.

    @patch.object(guard, "gh_lines")
    def test_same_go_family_on_another_manifest_is_deferred(self, gh):
        before = "module example.com/other\n\nrequire go.opentelemetry.io/otel/trace v1.41.0\n"
        gh.side_effect = [
            [open_pr()],
            [changed_file("other/go.mod", before, before.replace("v1.41.0", "v1.42.0"))],
        ]
        output = {"items": [go_item(family="go.opentelemetry.io/otel/*")]}
        guard.guard_create(output, "Azure/ARO-HCP")
        self.assertEqual(output["items"], [])

    @patch.object(guard, "gh_lines")
    def test_go_replace_changes_are_dependency_changes(self, gh):
        for directive in (
            "replace github.com/gofiber/fiber/v2 => github.com/gofiber/fiber/v2 v2.52.0",
            "replace (\n\tgithub.com/gofiber/fiber/v2 => github.com/gofiber/fiber/v2 v2.52.0\n)",
        ):
            with self.subTest(directive=directive):
                before = "module example.com/service\n\n" + directive + "\n"
                gh.side_effect = [
                    [open_pr()],
                    [changed_file("other/go.mod", before, before.replace("v2.52.0", "v2.53.0"))],
                ]
                output = {"items": [go_item()]}
                guard.guard_create(output, "Azure/ARO-HCP")
                self.assertEqual(output["items"], [])

    @patch.object(guard, "gh_lines")
    def test_npm_engines_and_other_dependencies_do_not_overlap(self, gh):
        before = npm_manifest({"@typespec/compiler": "1.15.0", "undici": "7.0.0"}, engines={"node": "22"})
        for after in (before.replace('"22"', '"24"'), before.replace('"7.0.0"', '"7.1.0"')):
            with self.subTest(after=after):
                gh.side_effect = [
                    [open_pr(title="fix(deps): typespec to 1.16.0")],
                    [changed_file("api/package.json", before, after)],
                ]
                item = create_item()
                output = {"items": [item]}
                guard.guard_create(output, "Azure/ARO-HCP")
                self.assertEqual(output["items"], [item])

    @patch.object(guard, "gh_lines")
    def test_partial_npm_engines_context_is_not_a_dependency_change(self, gh):
        gh.side_effect = [
            [open_pr()],
            [{
                "filename": "api/package.json", "status": "modified",
                "patch": '@@ -20,4 +20,4 @@\n   "engines": {\n-    "node": "22"\n+    "node": "24"\n   }\n }',
            }],
        ]
        item = create_item()
        output = {"items": [item]}
        guard.guard_create(output, "Azure/ARO-HCP")
        self.assertEqual(output["items"], [item])

    @patch.object(guard, "gh_lines")
    def test_npm_indentation_and_dependency_order_do_not_overlap(self, gh):
        data = {
            "dependencies": {"@typespec/compiler": "1.15.0", "undici": "7.0.0"},
            "overrides": {"parent": {"@typespec/compiler": "1.15.0"}},
        }
        before = json.dumps(data, indent=2)
        reordered = {
            "overrides": data["overrides"],
            "dependencies": dict(reversed(list(data["dependencies"].items()))),
        }
        for after in (json.dumps(data, indent=4), json.dumps(reordered, indent=2), json.dumps(data)):
            with self.subTest(after=after):
                gh.side_effect = [[open_pr()], [changed_file("api/package.json", before, after)]]
                item = create_item()
                output = {"items": [item]}
                guard.guard_create(output, "Azure/ARO-HCP")
                self.assertEqual(output["items"], [item])

    @patch.object(guard, "gh_lines")
    def test_nested_npm_override_target_is_matched(self, gh):
        before = json.dumps({"overrides": {"some-parent": {"@typespec/compiler": "1.15.0"}}}, indent=2)
        gh.side_effect = [
            [open_pr()],
            [changed_file("other/package.json", before, before.replace("1.15.0", "1.16.0"))],
        ]
        output = {"items": [create_item()]}
        guard.guard_create(output, "Azure/ARO-HCP")
        self.assertEqual(output["items"], [])

    @patch.object(guard, "gh_lines")
    def test_actual_npm_changes_overlap_globally_and_support_family_patterns(self, gh):
        for section in ("dependencies", "devDependencies", "peerDependencies", "optionalDependencies", "overrides"):
            for family in ("@typespec/*", "typespec"):
                with self.subTest(section=section, family=family):
                    before = json.dumps({section: {"@typespec/compiler": "1.15.0"}}, indent=2)
                    gh.side_effect = [
                        [open_pr(title="Unclassified PR")],
                        [changed_file("other/package.json", before, before.replace("1.15.0", "1.16.0"))],
                    ]
                    output = {"items": [create_item(family=family)]}
                    guard.guard_create(output, "Azure/ARO-HCP")
                    self.assertEqual(output["items"], [])

    @patch.object(guard, "gh_lines")
    def test_missing_or_unreliable_patch_defers_only_relevant_candidate(self, gh):
        before = npm_manifest({"@typespec/compiler": "1.15.0"})
        valid = changed_file("api/package.json", before, before.replace("1.15.0", "1.16.0"))
        bad_files = [
            {"filename": "api/package.json", "status": "modified"},
            {**valid, "patch": valid["patch"] + "\n..."},
            {**valid, "additions": valid["additions"] + 1},
            {**valid, "patch": "@@ -4,2 +4,2 @@\n-  old\n+  new"},
            changed_file("api/package.json", before, before.replace('"1.15.0"', "not-json")),
            {
                "filename": "api/package.json", "status": "modified",
                "patch": '@@ -20 +20 @@\n-    "@typespec/compiler": "1.15.0"\n+    "@typespec/compiler": "1.16.0"',
            },
        ]
        for file in bad_files:
            with self.subTest(file=file):
                gh.side_effect = [[open_pr()], [file]]
                independent = go_item()
                output = {"items": [create_item(), independent, {"type": "noop"}]}
                with patch("sys.stderr", new_callable=io.StringIO) as stderr:
                    guard.guard_create(output, "Azure/ARO-HCP")
                self.assertEqual(output["items"], [independent, {"type": "noop"}])
                self.assertIn("#7689", stderr.getvalue())
                self.assertIn("api/package.json", stderr.getvalue())

    @patch.object(guard, "gh_lines")
    def test_unclassifiable_go_change_defers_relevant_candidate(self, gh):
        gh.side_effect = [
            [open_pr()],
            [{"filename": "service/go.mod", "status": "modified", "patch": "@@ -20 +20 @@\n-not a module\n+still not a module"}],
        ]
        output = {"items": [go_item(), create_item()]}
        guard.guard_create(output, "Azure/ARO-HCP")
        self.assertEqual(
            [item["data"]["package_family"] for item in output["items"]],
            ["typespec"],
        )

    @patch.object(guard, "gh_lines")
    def test_unavailable_diff_in_unrelated_manifest_does_not_block(self, gh):
        gh.side_effect = [
            [open_pr(title="fix(deps): typespec to 1.16.0")],
            [{"filename": "other/package.json", "status": "modified"}],
        ]
        item = create_item()
        output = {"items": [item]}
        guard.guard_create(output, "Azure/ARO-HCP")
        self.assertEqual(output["items"], [item])

    @patch.object(guard, "gh_lines")
    def test_uncertain_family_change_is_relevant_across_manifests(self, gh):
        gh.side_effect = [
            [open_pr()],
            [{
                "filename": "other/package.json", "status": "modified",
                "patch": '@@ -20 +20 @@\n-    "@typespec/compiler": "1.15.0"\n+    "@typespec/compiler": "1.16.0"',
            }],
        ]
        independent = go_item()
        output = {"items": [create_item(), independent]}
        guard.guard_create(output, "Azure/ARO-HCP")
        self.assertEqual(output["items"], [independent])

    @patch.object(guard, "gh_lines")
    def test_unavailable_renamed_manifest_diff_is_relevant_to_previous_path(self, gh):
        gh.side_effect = [
            [open_pr()],
            [{"filename": "other/package.json", "previous_filename": "api/package.json", "status": "renamed"}],
        ]
        output = {"items": [create_item()]}
        guard.guard_create(output, "Azure/ARO-HCP")
        self.assertEqual(output["items"], [])

    @patch.object(guard, "gh_lines")
    def test_non_main_pr_is_ignored(self, gh):
        gh.return_value = [{**open_pr(), "base": "release"}]
        item = create_item()
        output = {"items": [item]}
        guard.guard_create(output, "Azure/ARO-HCP")
        self.assertEqual(output["items"], [item])
        gh.assert_called_once()

    @patch.object(guard, "gh_lines")
    def test_api_failures_remain_failures(self, gh):
        for responses in (
            [subprocess.CalledProcessError(1, ["gh", "api"])],
            [[open_pr()], subprocess.CalledProcessError(1, ["gh", "api"])],
        ):
            with self.subTest(responses=responses):
                gh.side_effect = responses
                with self.assertRaises(subprocess.CalledProcessError):
                    guard.guard_create({"items": [create_item()]}, "Azure/ARO-HCP")

    @patch.object(guard, "gh_lines")
    def test_present_but_malformed_metadata_is_not_missing(self, gh):
        for data in (None, [], "typespec", {"ecosystem": "npm"}):
            with self.subTest(data=data):
                item = create_item()
                item["data"] = data
                with self.assertRaises(ValueError):
                    guard.guard_create({"items": [item]}, "Azure/ARO-HCP")
        gh.assert_not_called()

    @patch.object(guard, "gh_lines")
    def test_two_character_package_is_allowed(self, gh):
        gh.side_effect = [[], []]
        guard.guard_create({"items": [create_item(family="ms")]}, "Azure/ARO-HCP")

    @patch.object(guard, "gh_lines")
    def test_patch_mismatch_is_rejected(self, gh):
        item = create_item(manifest="api/package.json", patch_files=["other/package.json"])
        with self.assertRaisesRegex(ValueError, "manifests in data do not match"):
            guard.guard_create({"items": [item]}, "Azure/ARO-HCP")

    @patch.object(guard, "gh_lines")
    def test_path_normalization_succeeds(self, gh):
        gh.side_effect = [[], []]
        item = create_item(manifest="./api/package.json", patch_files=["api/package.json"])
        guard.guard_create({"items": [item]}, "Azure/ARO-HCP")

    @patch.object(guard, "gh_lines")
    def test_two_requests_for_same_family_are_rejected_before_api(self, gh):
        with self.assertRaisesRegex(ValueError, "multiple new PRs"):
            guard.guard_create({"items": [create_item(), create_item()]}, "Azure/ARO-HCP")
        gh.assert_not_called()

    @patch.object(guard, "gh_lines")
    def test_two_requests_for_one_manifest_are_rejected_before_api(self, gh):
        with self.assertRaisesRegex(ValueError, "multiple new PRs"):
            guard.guard_create(
                {"items": [create_item(), create_item(family="other")]},
                "Azure/ARO-HCP",
            )
        gh.assert_not_called()

    @patch.object(guard, "gh_lines")
    def test_two_requests_for_overlapping_family_patterns_are_rejected(self, gh):
        with self.assertRaisesRegex(ValueError, "multiple new PRs"):
            guard.guard_create(
                {"items": [go_item(family="go.opentelemetry.io/otel/*"), go_item("other/go.mod", "go.opentelemetry.io/otel/trace")]},
                "Azure/ARO-HCP",
            )
        gh.assert_not_called()

    def test_unchecked_group_data_is_rejected(self):
        item = create_item()
        item["data"]["manifests"] = ["../api/package.json"]
        with self.assertRaisesRegex(ValueError, "invalid dependency group"):
            guard.group(item)

    @patch.object(guard, "gh_lines")
    def test_missing_data_is_dropped_instead_of_failing_the_job(self, gh):
        output = {
            "items": [
                {"type": "create_pull_request", "title": "api npm undici to 7.29.1+ (high)"},
                {"type": "missing_data", "reason": "sprintf-js has no patch"},
            ]
        }
        guard.guard_create(output, "Azure/ARO-HCP")
        gh.assert_not_called()
        self.assertEqual(
            [item["type"] for item in output["items"]],
            ["missing_data"],
        )

    @patch.object(guard, "gh_lines")
    def test_mixed_creates_drop_only_the_item_missing_data(self, gh):
        gh.side_effect = [[], []]
        output = {
            "items": [
                {"type": "create_pull_request", "title": "api npm undici to 7.29.1+ (high)"},
                create_item(),
            ]
        }
        guard.guard_create(output, "Azure/ARO-HCP")
        remaining = [item for item in output["items"] if item.get("type") == "create_pull_request"]
        self.assertEqual(len(remaining), 1)
        self.assertEqual(remaining[0]["data"]["package_family"], "typespec")
        gh.assert_called()

    @patch.object(guard, "gh_lines")
    def test_invalid_data_still_fails_closed(self, gh):
        item = create_item()
        item["data"]["manifests"] = ["../api/package.json"]
        with self.assertRaisesRegex(ValueError, "invalid dependency group"):
            guard.guard_create({"items": [item]}, "Azure/ARO-HCP")
        gh.assert_not_called()

    def test_main_rewrites_output_after_dropping_creates_missing_metadata(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / "agent_output.json"
            path.write_text(json.dumps({
                "items": [
                    {"type": "create_pull_request", "title": "api npm undici to 7.29.1+ (high)"},
                    {"type": "reconcile_owned_pr", "pull_request_number": "7187"},
                ]
            }))
            with patch.object(sys, "argv", ["dependabot_guard.py", "create", str(path), "Azure/ARO-HCP"]):
                guard.main()
            written = json.loads(path.read_text())
            self.assertEqual(
                [item["type"] for item in written["items"]],
                ["reconcile_owned_pr"],
            )

    @patch.object(guard, "gh_lines")
    def test_main_persists_per_candidate_deferral(self, gh):
        before = "module example.com/service\n\nrequire github.com/gofiber/fiber/v2 v2.52.0\n"
        gh.side_effect = [
            [open_pr()],
            [changed_file("service/go.mod", before, before.replace("v2.52.0", "v2.53.0"))],
        ]
        independent = create_item()
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / "agent_output.json"
            path.write_text(json.dumps({"items": [go_item(), independent, {"type": "noop"}]}))
            with patch.object(sys, "argv", ["dependabot_guard.py", "create", str(path), "Azure/ARO-HCP"]):
                guard.main()
            written = json.loads(path.read_text())["items"]
            self.assertEqual([item["type"] for item in written], ["create_pull_request", "noop"])
            self.assertEqual(written[0]["data"]["package_family"], "typespec")


class RetestGuardTests(TestCase):
    def setUp(self):
        self.token = patch.dict(os.environ, {"CI_TOKEN": "test-ci-token"})
        self.token.start()
        self.addCleanup(self.token.stop)

    @patch.object(guard, "gh_lines")
    def test_failed_e2e_needs_current_failure_and_comparison(self, gh):
        gh.side_effect = [
            [{"context": "e2e-parallel", "state": "failure", "target_url": "https://prow/fail", "created_at": "2026-10-06T12:00:00Z"}],
            [],
        ]
        with self.assertRaisesRegex(ValueError, "comparison run URL"):
            guard.guard_retest("Azure/ARO-HCP", "7355", "a" * 40, "https://prow/fail timed out")

    @patch.object(guard, "gh_lines")
    def test_latest_success_overrules_older_failed_status(self, gh):
        gh.side_effect = [
            [
                {"context": "e2e-parallel", "state": "failure", "target_url": "https://prow/fail", "created_at": "2026-10-06T12:00:00Z"},
                {"context": "e2e-parallel", "state": "success", "target_url": "https://prow/pass", "created_at": "2026-10-06T13:00:00Z"},
            ],
            [],
        ]
        with self.assertRaisesRegex(ValueError, "current failing check"):
            guard.guard_retest("Azure/ARO-HCP", "7355", "a" * 40, "https://prow/fail https://prow/other")

    @patch.object(guard, "gh_lines")
    def test_cooldown_blocks_duplicate_retest(self, gh):
        gh.side_effect = [
            [{"context": "e2e-parallel", "state": "failure", "target_url": "https://prow/fail", "created_at": "2026-10-06T12:00:00Z"}],
            [],
            [{"body": "Evidence\n\n/retest-required", "created_at": "2026-10-06T13:00:00Z"}],
        ]
        with self.assertRaisesRegex(ValueError, "last 24 hours"):
            guard.guard_retest(
                "Azure/ARO-HCP", "7355", "a" * 40,
                "https://prow/fail; same job passes at https://prow/pass",
                datetime(2026, 10, 6, 14, tzinfo=timezone.utc),
            )

    @patch.object(guard, "gh_lines")
    def test_transient_evidence_after_cooldown_passes(self, gh):
        gh.side_effect = [
            [{"context": "e2e-parallel", "state": "failure", "target_url": "https://prow/fail", "created_at": "2026-10-06T12:00:00Z"}],
            [],
            [{"body": "/retest-required", "created_at": "2026-10-05T12:00:00Z"}],
        ]
        guard.guard_retest(
            "Azure/ARO-HCP", "7355", "a" * 40,
            "https://prow/fail was transient; same job passes at https://prow/pass",
            datetime(2026, 10, 6, 14, tzinfo=timezone.utc),
        )

    @patch.object(guard, "gh_lines")
    def test_url_normalization_with_punctuation_passes(self, gh):
        gh.side_effect = [
            [{"context": "e2e-parallel", "state": "failure", "target_url": "https://prow/fail", "created_at": "2026-10-06T12:00:00Z"}],
            [],
            [],
        ]
        guard.guard_retest(
            "Azure/ARO-HCP", "7355", "a" * 40,
            "Failed check https://prow/fail; comparison at https://prow/other.",
            datetime(2026, 10, 6, 14, tzinfo=timezone.utc),
        )


class NpmLockOverlapTests(TestCase):
    def test_null_and_renamed_previous_paths_preserve_dependency_comparison(self):
        for previous in (None, "legacy/package-lock.json"):
            with self.subTest(previous=previous):
                base_path = previous or "api/package-lock.json"
                documents = {
                    f"repos/owner/repo/contents/{base_path}?ref=base": "1.0.0",
                    "repos/owner/repo/contents/api/package-lock.json?ref=head": "2.0.0",
                }
                def api(endpoint, expression):
                    if endpoint.endswith("/pulls/123"):
                        return [{"base": "base", "head": "head"}]
                    if "/compare/" in endpoint:
                        return [{"base": "base"}]
                    content = {"lockfileVersion": 3, "packages": {
                        "node_modules/undici": {"version": documents[endpoint]}}}
                    return [{"encoding": "base64", "content":
                             base64.b64encode(json.dumps(content).encode()).decode()}]
                files = [{"filename": "api/package-lock.json", "previous_filename": previous,
                          "status": "renamed" if previous else "modified"}]
                with patch.object(guard, "gh_lines", side_effect=api):
                    guard.load_lockfiles("owner/repo", {"number": 123}, files)
                self.assertIsNotNone(guard.overlaps(
                    ("npm", "undici", "2.0.0", {"api/package.json"}), files))
                self.assertIsNone(guard.overlaps(
                    ("npm", "unrelated", "2.0.0", {"api/package.json"}), files))

    def test_deleted_fork_still_detects_lockfile_overlap(self):
        def api(endpoint, expression):
            if endpoint.endswith("/pulls/123"):
                return [{"base": "base", "head": "head", "head_repo": None}]
            if "/compare/" in endpoint:
                return [{"base": "base"}]
            if not endpoint.startswith("repos/owner/repo/contents/"):
                raise subprocess.CalledProcessError(1, ["gh", "api", endpoint])
            version = "1.0.0" if endpoint.endswith("ref=base") else "2.0.0"
            content = {"lockfileVersion": 3, "packages": {
                "node_modules/undici": {"version": version}}}
            return [{"encoding": "base64", "content": base64.b64encode(json.dumps(content).encode()).decode()}]
        files = [{"filename": "api/package-lock.json", "status": "modified"}]
        with patch.object(guard, "gh_lines", side_effect=api):
            guard.load_lockfiles("owner/repo", {"number": 123}, files)
        self.assertIsNotNone(guard.overlaps(
            ("npm", "undici", "2.0.0", {"api/package.json"}), files))

    def test_base_branch_only_updates_do_not_overlap(self):
        documents = {
            "base-tip": {"undici": "2.0.0", "other": "1.0.0"},
            "ancestor": {"undici": "1.0.0", "other": "1.0.0"},
            "head": {"undici": "1.0.0", "other": "2.0.0"},
        }
        def api(endpoint, expression):
            if endpoint.endswith("/pulls/123"):
                return [{"base": "base-tip", "head": "head", "head_repo": "fork/repo"}]
            if "/compare/" in endpoint:
                return [{"base": "ancestor"}]
            versions = documents[endpoint.split("?ref=")[1]]
            content = {"lockfileVersion": 3, "packages": {
                "node_modules/" + name: {"version": version} for name, version in versions.items()}}
            return [{"encoding": "base64", "content": base64.b64encode(json.dumps(content).encode()).decode()}]
        files = [{"filename": "api/package-lock.json", "status": "modified"}]
        with patch.object(guard, "gh_lines", side_effect=api):
            guard.load_lockfiles("owner/repo", {"number": 123}, files)
        self.assertIsNone(guard.overlaps(("npm", "undici", "2.0.0", {"api/package.json"}), files))
        self.assertIsNotNone(guard.overlaps(("npm", "other", "2.0.0", {"api/package.json"}), files))

    def test_renamed_lockfile_without_content_still_defers(self):
        file = {"filename": "api/archived.json", "previous_filename": "api/package-lock.json"}
        self.assertIsNotNone(guard.overlaps(
            ("npm", "undici", "2.0.0", {"api/package.json"}), [file]))

    def test_resolved_upgrade_matches_nested_and_scoped_packages(self):
        for name in ("undici", "@scope/package"):
            for format_version in (1, 2, 3):
                def document(version):
                    entry = {"version": version}
                    if format_version == 1:
                        return {"lockfileVersion": 1, "dependencies": {
                            "parent": {"version": "1.0.0", "dependencies": {name: entry}}}}
                    return {"lockfileVersion": format_version, "packages": {
                        "node_modules/parent/node_modules/" + name: entry}}
                for filename in ("api/package-lock.json", "api/npm-shrinkwrap.json"):
                    with self.subTest(name=name, format=format_version, file=filename):
                        file = {"filename": filename, "lock_versions": [
                            guard.lock_versions(document("1.0.0")),
                            guard.lock_versions(document("2.0.0"))]}
                        candidate = ("npm", name, "2.0.0", {"api/package.json"})
                        self.assertIn(name, guard.overlaps(candidate, [file]))
                        other = ("npm", "unrelated", "2.0.0", {"api/package.json"})
                        self.assertIsNone(guard.overlaps(other, [file]))

    def test_uncertain_lockfile_defers_only_relevant_manifest(self):
        candidate = ("npm", "undici", "2.0.0", {"api/package.json"})
        file = {"filename": "api/package-lock.json"}
        self.assertIn("unavailable", guard.overlaps(candidate, [file]))
        other = ("npm", "different", "2.0.0", {"other/package.json"})
        self.assertIsNone(guard.overlaps(other, [file]))

    def test_formatting_and_metadata_do_not_change_resolutions(self):
        before = {"lockfileVersion": 3, "packages": {
            "node_modules/undici": {"version": "1.0.0", "dev": True}}}
        after = {"packages": {"node_modules/undici": {"dev": False, "version": "1.0.0"}},
                 "lockfileVersion": 3}
        file = {"filename": "api/package-lock.json",
                "lock_versions": [guard.lock_versions(before), guard.lock_versions(after)]}
        self.assertIsNone(guard.overlaps(("npm", "undici", "2.0.0", {"api/package.json"}), [file]))

    def test_added_and_removed_resolutions_overlap(self):
        versions = guard.lock_versions({"lockfileVersion": 3, "packages": {
            "node_modules/undici": {"version": "1.0.0"}}})
        for pair in ([{}, versions], [versions, {}]):
            self.assertIsNotNone(guard.overlaps(
                ("npm", "undici", "2.0.0", {"api/package.json"}),
                [{"filename": "api/package-lock.json", "lock_versions": pair}]))


if __name__ == "__main__":
    main()
