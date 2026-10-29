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


def open_pr(number=7689, title="Owned security dependency update"):
    return {"number": number, "title": title, "body": "", "base": "main"}


def go_item(manifest="service/go.mod", family="github.com/gofiber/fiber/v2"):
    return create_item(manifest, family, ecosystem="go")


def npm_manifest(dependencies=None, **other):
    return json.dumps({"dependencies": dependencies or {}, **other}, indent=2) + "\n"


def remediation_batch():
    fiber = go_item("internal/go.mod")
    fiber["data"]["manifests"] = ["admin/server/go.mod", "frontend/go.mod", "internal/go.mod"]
    fiber["data"]["target_version"] = "2.52.14"
    undici = create_item(family="undici")
    undici["data"]["target_version"] = "7.30.1"
    fast_uri = create_item(family="fast-uri")
    fast_uri["data"]["target_version"] = "3.1.9"
    return [fiber, undici, fast_uri]


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
    def setUp(self):
        # The overlap unit fixtures provide inventory first, then file responses.
        # Real API discovery and identity gates are exercised by the CLI tests.
        inventory = patch.object(
            guard, "owned_inventory",
            side_effect=lambda repo: guard.gh_lines("owned-security-inventory", ".[]"),
        )
        inventory.start()
        self.addCleanup(inventory.stop)

    def assert_deferred_noop(self, output):
        self.assertEqual(len(output["items"]), 1)
        noop = output["items"][0]
        self.assertEqual(noop["type"], "noop")
        self.assertIsInstance(noop["message"], str)
        self.assertTrue(noop["message"].strip())

    @patch.object(guard, "gh_lines", return_value=[])
    def test_short_display_title_does_not_block_valid_fiber_proposal(self, _gh):
        item = go_item("internal/go.mod")
        item["title"] = "gofiber/fiber/v2 to 2.52.14 (medium)"
        item["data"]["target_version"] = "2.52.14"
        output = {"items": [item]}
        guard.guard_create(output, "Azure/ARO-HCP")
        self.assertEqual(output["items"][0]["title"], "github.com/gofiber/fiber/v2 to 2.52.14")

    @patch.object(guard, "gh_lines")
    def test_go_directive_update_in_owned_pr_does_not_block_fiber(self, gh):
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
                self.assert_deferred_noop(output)

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

    @patch.object(guard, "gh_lines")
    def test_same_go_family_on_another_manifest_is_deferred(self, gh):
        before = "module example.com/other\n\nrequire go.opentelemetry.io/otel/trace v1.41.0\n"
        gh.side_effect = [
            [open_pr()],
            [changed_file("other/go.mod", before, before.replace("v1.41.0", "v1.42.0"))],
        ]
        output = {"items": [go_item(family="go.opentelemetry.io/otel/*")]}
        guard.guard_create(output, "Azure/ARO-HCP")
        self.assert_deferred_noop(output)

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
                self.assert_deferred_noop(output)

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
        self.assert_deferred_noop(output)

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
                    self.assert_deferred_noop(output)

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
        self.assert_deferred_noop(output)

    @patch.object(guard, "gh_lines")
    def test_api_failures_remain_failures(self, gh):
        for responses in (
            [subprocess.CalledProcessError(1, ["gh", "api"])],
            [[open_pr()], subprocess.CalledProcessError(1, ["gh", "api"])],
        ):
            with self.subTest(responses=responses):
                gh.side_effect = responses
                with tempfile.TemporaryDirectory() as tmp:
                    path = Path(tmp) / "agent_output.json"
                    original = json.dumps({"items": remediation_batch()})
                    path.write_text(original)
                    with patch.object(sys, "argv", ["dependabot_guard.py", "create", str(path), "Azure/ARO-HCP"]):
                        with self.assertRaises(SystemExit) as failure:
                            guard.main()
                    self.assertEqual(failure.exception.code, 1)
                    self.assertEqual(path.read_text(), original)

    @patch.object(guard, "gh_lines")
    def test_ingestion_errors_fail_without_noop_or_rewriting_output(self, gh):
        for items in ([], remediation_batch()):
            with self.subTest(has_proposals=bool(items)):
                with tempfile.TemporaryDirectory() as tmp:
                    path = Path(tmp) / "agent_output.json"
                    original = json.dumps({
                        "items": items,
                        "errors": ["Malformed safe-output record"],
                    })
                    path.write_text(original)
                    with patch.object(sys, "argv", ["dependabot_guard.py", "create", str(path), "Azure/ARO-HCP"]):
                        with self.assertRaises(SystemExit) as failure:
                            guard.main()
                    self.assertEqual(failure.exception.code, 1)
                    self.assertEqual(path.read_text(), original)

    @patch.object(guard, "gh_lines")
    def test_empty_ingestion_errors_allow_explicit_noop(self, gh):
        output = {"items": [], "errors": []}
        guard.guard_create(output, "Azure/ARO-HCP")
        self.assert_deferred_noop(output)
        self.assertEqual(output["errors"], [])

    @patch.object(guard, "gh_lines")
    def test_present_but_malformed_metadata_is_not_missing(self, gh):
        for data in (None, [], "typespec", {"ecosystem": "npm"}):
            with self.subTest(data=data):
                item = create_item()
                item["data"] = data
                with self.assertRaises(ValueError):
                    guard.guard_create({"items": [item]}, "Azure/ARO-HCP")

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

    @patch.object(guard, "gh_lines", return_value=[])
    def test_three_proposal_batch_selects_first_npm_manifest_owner(self, gh):
        for reverse_npm in (False, True):
            with self.subTest(reverse_npm=reverse_npm):
                fiber, undici, fast_uri = remediation_batch()
                first, deferred = (fast_uri, undici) if reverse_npm else (undici, fast_uri)
                output = {"items": [fiber, first, deferred], "other": "preserved"}
                with patch("sys.stderr", new_callable=io.StringIO) as stderr:
                    guard.guard_create(output, "Azure/ARO-HCP")
                self.assertEqual(output, {"items": [fiber, first], "other": "preserved"})
                self.assertEqual(fiber["title"], "github.com/gofiber/fiber/v2 to 2.52.14")
                self.assertEqual(
                    first["title"],
                    f'{first["data"]["package_family"]} to {first["data"]["target_version"]}',
                )
                self.assertIn(deferred["data"]["package_family"], stderr.getvalue())
                self.assertIn("api/package.json", stderr.getvalue())

    @patch.object(guard, "gh_lines")
    def test_open_overlap_does_not_reserve_manifest_from_next_candidate(self, gh):
        before = npm_manifest({"undici": "7.0.0", "fast-uri": "3.0.0"})
        gh.side_effect = [
            [open_pr()],
            [changed_file("api/package.json", before, before.replace("7.0.0", "7.30.1"))],
        ]
        fiber, undici, fast_uri = remediation_batch()
        output = {"items": [fiber, undici, fast_uri]}
        with patch("sys.stderr", new_callable=io.StringIO) as stderr:
            guard.guard_create(output, "Azure/ARO-HCP")
        self.assertEqual(output["items"], [fiber, fast_uri])
        self.assertIn("undici", stderr.getvalue())
        self.assertIn("#7689", stderr.getvalue())

    @patch.object(guard, "gh_lines")
    def test_all_creates_deferred_persists_explicit_noop(self, gh):
        go_before = "module example.com/service\n\nrequire github.com/gofiber/fiber/v2 v2.52.0\n"
        npm_before = npm_manifest({"undici": "7.0.0", "fast-uri": "3.0.0"})
        gh.side_effect = [
            [open_pr()],
            [
                changed_file("internal/go.mod", go_before, go_before.replace("v2.52.0", "v2.52.14")),
                changed_file(
                    "api/package.json", npm_before,
                    npm_before.replace("7.0.0", "7.30.1").replace("3.0.0", "3.1.9"),
                ),
            ],
        ]
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / "agent_output.json"
            path.write_text(json.dumps({"items": remediation_batch(), "other": "preserved"}))
            with patch.object(sys, "argv", ["dependabot_guard.py", "create", str(path), "Azure/ARO-HCP"]):
                with patch("sys.stderr", new_callable=io.StringIO) as stderr:
                    guard.main()
            written = json.loads(path.read_text())
        self.assert_deferred_noop(written)
        self.assertEqual(written["other"], "preserved")
        for family in ("github.com/gofiber/fiber/v2", "undici", "fast-uri"):
            self.assertIn(family, stderr.getvalue())
        self.assertIn("#7689", stderr.getvalue())

    @patch.object(guard, "gh_lines")
    def test_non_create_outputs_survive_all_create_deferrals_without_added_noop(self, gh):
        before = npm_manifest({"undici": "7.0.0", "fast-uri": "3.0.0"})
        for include_noop in (False, True):
            with self.subTest(include_noop=include_noop):
                gh.side_effect = [
                    [open_pr()],
                    [changed_file(
                        "api/package.json", before,
                        before.replace("7.0.0", "7.30.1").replace("3.0.0", "3.1.9"),
                    )],
                ]
                _, undici, fast_uri = remediation_batch()
                reconcile = {"type": "reconcile_owned_pr", "pull_request_number": "7187"}
                missing = {"type": "missing_data", "reason": "No safe patched version"}
                preserved = [reconcile, missing]
                if include_noop:
                    preserved.append({"type": "noop", "message": "Existing summary"})
                output = {
                    "items": [reconcile, undici, missing, fast_uri, *preserved[2:]],
                    "other": {"unchanged": True},
                }
                guard.guard_create(output, "Azure/ARO-HCP")
                self.assertEqual(output, {"items": preserved, "other": {"unchanged": True}})

    @patch.object(guard, "gh_lines")
    def test_malformed_later_shared_manifest_candidate_fails_before_partial_selection(self, gh):
        for invalid_data in ({"ecosystem": "npm"}, {"ecosystem": "invalid"}):
            with self.subTest(invalid_data=invalid_data):
                items = remediation_batch()
                items[-1]["data"] = invalid_data
                output = {"items": items}
                with tempfile.TemporaryDirectory() as tmp:
                    path = Path(tmp) / "agent_output.json"
                    original = json.dumps(output)
                    path.write_text(original)
                    with patch.object(sys, "argv", ["dependabot_guard.py", "create", str(path), "Azure/ARO-HCP"]):
                        with self.assertRaises(SystemExit) as failure:
                            guard.main()
                    self.assertEqual(failure.exception.code, 1)
                    self.assertEqual(path.read_text(), original)

    @patch.object(guard, "gh_lines")
    def test_two_requests_for_overlapping_family_patterns_are_rejected(self, gh):
        with self.assertRaisesRegex(ValueError, "multiple new PRs"):
            guard.guard_create(
                {"items": [go_item(family="go.opentelemetry.io/otel/*"), go_item("other/go.mod", "go.opentelemetry.io/otel/trace")]},
                "Azure/ARO-HCP",
            )

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

    @patch.object(guard, "gh_lines")
    def test_invalid_data_still_fails_closed(self, gh):
        item = create_item()
        item["data"]["manifests"] = ["../api/package.json"]
        with self.assertRaisesRegex(ValueError, "invalid dependency group"):
            guard.guard_create({"items": [item]}, "Azure/ARO-HCP")

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


class OwnedInventoryCliTests(TestCase):
    """Exercise both actual CLIs with a deny-by-default, offline gh fixture."""

    REPO = "Azure/ARO-HCP"
    ISSUES = f"repos/{REPO}/issues?state=open&creator=aro-hcp-robot%5Bbot%5D&per_page=100"
    PULLS = f"repos/{REPO}/pulls"
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
if "--jq" in arguments:
    endpoint = arguments[arguments.index("--jq") - 1]
response = responses.get(endpoint)
if response is None:
    sys.exit("Unexpected API endpoint: " + endpoint)
if endpoint.endswith("/files?per_page=100"):
    expected = ["api", "--paginate", endpoint, "--jq",
                ".[] | {filename, previous_filename, status, patch, additions, deletions}"]
elif "/issues?state=open&creator=" in endpoint:
    expected = ["api", "--paginate", "--slurp", endpoint]
else:
    expected = ["api", endpoint]
if arguments != expected:
    sys.exit("Unexpected API arguments: " + repr(arguments))
if response.get("returncode"):
    sys.exit(response["returncode"])
if "raw" in response:
    sys.stdout.write(response["raw"])
elif "pages" in response:
    print(json.dumps(response["pages"]))
elif endpoint.endswith("/files?per_page=100"):
    for item in response["body"]:
        print(json.dumps(item))
else:
    print(json.dumps(response["body"]))
'''

    def setUp(self):
        temporary = tempfile.TemporaryDirectory(prefix="security-owned-guard-")
        self.addCleanup(temporary.cleanup)
        self.directory = Path(temporary.name)
        # Flat safe-output record shape and structured data from run 40594645177.
        self.proposal = {
            "type": "create_pull_request", "branch": "fix/undici-7.30.1",
            "title": "undici to 7.30.1",
            "body": "Update the transitive undici override and lockfile.",
            "data": {
                "ecosystem": "npm", "manifests": ["api/package.json"],
                "package_family": "undici", "target_version": "7.30.1",
            },
        }
        self.responses = {self.ISSUES: {"pages": [[]]}}
        gh = self.directory / "gh"
        gh.write_text(f"#!{sys.executable}\n{self.GH}")
        gh.chmod(0o755)

    def pr(self, number=42, **overrides):
        pr = {
            "number": number, "state": "open", "draft": False,
            "title": "fix(deps): undici to 7.30.1", "body": "",
            "user": {"login": "aro-hcp-robot[bot]"},
            "head": {"ref": "fix/undici-7.30.1", "sha": "a" * 40,
                     "repo": {"full_name": self.REPO}},
            "base": {"ref": "main", "repo": {"full_name": self.REPO}},
            "labels": [{"name": "agentic-dependabot"}],
            "pull_request": {"url": f"https://api.github.com/{self.PULLS}/{number}"},
        }
        pr.update(overrides)
        return pr

    def inventory(self, *prs):
        self.responses[self.ISSUES] = {"pages": [list(prs)]}

    def detail(self, pr, **overrides):
        self.responses[f"{self.PULLS}/{pr['number']}"] = {"body": {**pr, **overrides}}

    def run_cli(self, command="create", output=None):
        path = self.directory / "agent_output.json"
        original = json.dumps(output or {"items": [self.proposal]}, indent=1) + "\n"
        path.write_text(original)
        responses = self.directory / "responses.json"
        responses.write_text(json.dumps(self.responses))
        calls = self.directory / "calls.jsonl"
        calls.write_text("")
        environment = {
            **os.environ, "PATH": f"{self.directory}:{os.environ['PATH']}",
            "GUARD_GH_RESPONSES": str(responses), "GUARD_GH_CALLS": str(calls),
        }
        arguments = [command, self.REPO] if command == "inventory" else [command, str(path), self.REPO]
        result = subprocess.run(
            [sys.executable, str(Path(__file__).with_name("dependabot_guard.py")), *arguments],
            env=environment, cwd=self.directory, capture_output=True, text=True,
        )
        recorded = [json.loads(line) for line in calls.read_text().splitlines()]
        return result, path.read_text(), original, recorded

    def assert_allowed(self):
        result, written, _, calls = self.run_cli()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(json.loads(written), {"items": [self.proposal]})
        return calls

    def assert_failed_closed(self, command="create"):
        result, written, original, calls = self.run_cli(command)
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(written, original)
        if command == "inventory":
            self.assertEqual(result.stdout, "")
        return calls

    def test_human_prettier_spectral_and_typespec_never_fetch_detail_or_files(self):
        # Their shared-manifest diffs include both uncertain npm context and real
        # undici changes. Neither can veto publication because ownership is absent.
        self.inventory(*[
            self.pr(number, user={"login": "human"}, title=title,
                    labels=[{"name": "agentic-dependabot"}])
            for number, title in (
                (7593, "chore(deps): bump prettier"),
                (7347, "chore(deps): bump spectral"),
                (7037, "chore(deps): bump spectral"),
                (7460, "fix(deps): typespec with transitive undici"),
            )
        ])
        self.assertEqual(self.assert_allowed(), [
            ["api", "--paginate", "--slurp", self.ISSUES],
        ])

    def test_native_dependabot_unrelated_bot_workflow_and_non_pr_are_ignored(self):
        issue = self.pr(46)
        del issue["pull_request"]
        self.inventory(
            self.pr(42, user={"login": "dependabot[bot]"}),
            self.pr(43, labels=[], title="Automated - Update component image digests"),
            self.pr(44, labels=[], body="<!-- gh-aw-workflow-id: tooling-update -->"),
            self.pr(45, labels=[], body=guard.WORKFLOW_MARKER, title="Other workflow"),
            issue,
        )
        self.assertEqual(self.assert_allowed(), [
            ["api", "--paginate", "--slurp", self.ISSUES],
        ])

    def test_inventory_emits_exact_preflight_shape_and_is_read_only(self):
        pr = self.pr(body=None, draft=True)
        self.inventory(pr)
        self.detail(pr)
        result, written, original, calls = self.run_cli("inventory")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(written, original)
        self.assertEqual(json.loads(result.stdout), [{
            "number": 42, "title": pr["title"], "body": "",
            "head": "fix/undici-7.30.1", "head_repo": self.REPO, "base": "main",
            "sha": "a" * 40, "draft": True, "author": "aro-hcp-robot[bot]",
            "labels": ["agentic-dependabot"],
        }])
        self.assertEqual(calls, [
            ["api", "--paginate", "--slurp", self.ISSUES], ["api", f"{self.PULLS}/42"],
        ])

    def test_exact_runtime_marker_recognizes_prelabel_owned_pr(self):
        pr = self.pr(labels=[], body=f"Remediation\n{guard.WORKFLOW_MARKER}\n")
        self.inventory(pr)
        self.detail(pr)
        result, _, _, _ = self.run_cli("inventory")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual([pr["number"] for pr in json.loads(result.stdout)], [42])
        for body in (
            "<!-- gh-aw-workflow-id: dependabot-remediation-extra -->",
            "<!-- gh-aw-workflow-id: another-workflow -->",
            f"quoted {guard.WORKFLOW_MARKER}",
        ):
            with self.subTest(body=body):
                self.inventory(self.pr(labels=[], body=body))
                self.assertEqual(self.assert_allowed(), [
                    ["api", "--paginate", "--slurp", self.ISSUES],
                ])

    def test_owned_real_overlap_defers_but_unrelated_package_does_not(self):
        pr = self.pr()
        self.inventory(pr)
        self.detail(pr)
        before = npm_manifest({"undici": "7.24.4", "fast-uri": "3.1.8"})
        files = f"{self.PULLS}/42/files?per_page=100"
        for package, version, deferred in (("undici", "7.24.4", True), ("fast-uri", "3.1.8", False)):
            with self.subTest(package=package):
                self.responses[files] = {"body": [
                    changed_file("api/package.json", before, before.replace(version, "9.0.0")),
                ]}
                result, written, _, calls = self.run_cli()
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertEqual(json.loads(written)["items"][0]["type"],
                                 "noop" if deferred else "create_pull_request")
                self.assertEqual([call[2] for call in calls if "--jq" in call], [files])

    def test_owned_partial_npm_context_defers_without_vetoing_other_groups(self):
        pr = self.pr()
        self.inventory(pr)
        self.detail(pr)
        self.responses[f"{self.PULLS}/42/files?per_page=100"] = {"body": [{
            "filename": "api/package.json", "status": "modified",
            "patch": '@@ -20 +20 @@\n-    "prettier": "3.6.2"\n+    "prettier": "3.6.3"',
        }]}
        result, written, _, _ = self.run_cli(output={"items": [self.proposal, go_item()]})
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual([item["data"]["ecosystem"] for item in json.loads(written)["items"]], ["go"])

    def test_closed_other_base_and_fresh_unowned_prs_are_ignored(self):
        for overrides in (
            {"state": "closed"}, {"base": {"ref": "release"}},
            {"user": {"login": "human"}}, {"labels": [], "body": ""},
        ):
            with self.subTest(overrides=overrides):
                pr = self.pr()
                self.inventory(pr)
                self.detail(pr, **overrides)
                calls = self.assert_allowed()
                self.assertEqual(len(calls), 2)
        self.inventory(self.pr(state="closed"))
        self.assertEqual(len(self.assert_allowed()), 1)

    def test_recorded_flat_proposal_serializes_same_batch_manifest(self):
        sibling = create_item(family="fast-uri")
        sibling["data"]["target_version"] = "3.1.9"
        result, written, _, calls = self.run_cli(output={"items": [self.proposal, sibling]})
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(json.loads(written), {"items": [self.proposal]})
        self.assertIn("same-batch manifest conflict", result.stderr)
        self.assertEqual(calls, [["api", "--paginate", "--slurp", self.ISSUES]])

    def test_malformed_owned_metadata_fails_closed_for_both_clis(self):
        for command in ("create", "inventory"):
            for overrides in (
                {"number": 43}, {"draft": None}, {"labels": None}, {"body": []},
                {"head": None}, {"head": {"ref": "fix/x", "sha": "bad", "repo": {"full_name": self.REPO}}},
                {"head": {"ref": "fix/x", "sha": "a" * 40, "repo": {"full_name": "fork/ARO-HCP"}}},
                {"base": {"ref": "main", "repo": {"full_name": "fork/ARO-HCP"}}},
            ):
                with self.subTest(command=command, overrides=overrides):
                    pr = self.pr()
                    self.inventory(pr)
                    self.detail(pr, **overrides)
                    self.assert_failed_closed(command)

    def test_inventory_pages_api_errors_and_invalid_json_fail_closed(self):
        for command in ("create", "inventory"):
            for response in (
                {"returncode": 1}, {"raw": "not json"}, {"pages": []},
                {"pages": [None]}, {"pages": [[self.pr(), self.pr()]]},
                {"pages": [[self.pr(labels=None)]]},
            ):
                with self.subTest(command=command, response=response):
                    self.responses = {self.ISSUES: response}
                    self.assert_failed_closed(command)
            pr = self.pr()
            self.inventory(pr)
            self.responses[f"{self.PULLS}/42"] = {"returncode": 1}
            self.assert_failed_closed(command)

    def test_paginated_inventory_keeps_every_owned_pr(self):
        first, second = self.pr(42), self.pr(43)
        self.responses[self.ISSUES] = {"pages": [[first], [second]]}
        self.detail(first)
        self.detail(second)
        result, _, _, _ = self.run_cli("inventory")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual([pr["number"] for pr in json.loads(result.stdout)], [42, 43])


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
