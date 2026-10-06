"""Focused safety checks for agentic dependency PR creation and retests."""

import importlib.util
import json
import os
import sys
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


def create_item(manifest="api/package.json", family="typespec", patch_files=None):
    item = {
        "type": "create_pull_request",
        "title": f"api npm {family} family to 1.16.0 (high)",
        "data": {
            "ecosystem": "npm",
            "package_family": family,
            "target_version": "1.16.0",
            "manifests": [manifest],
        },
    }
    if patch_files:
        item["files"] = [{"path": f, "content": "..."} for f in patch_files]
    return item


class CreateGuardTests(TestCase):
    @patch.object(guard, "gh_lines")
    def test_existing_typespec_pr_blocks_create_even_if_patch_differs(self, gh):
        gh.side_effect = [
            [{"number": 7355, "title": "fix(deps): api npm typespec family", "body": "", "base": "main"}],
            [{"filename": name} for name in ("api/Makefile", "api/package.json", "api/package-lock.json")],
        ]
        with self.assertRaisesRegex(ValueError, "#7355"):
            guard.guard_create({"items": [create_item()]}, "Azure/ARO-HCP")

    @patch.object(guard, "gh_lines")
    def test_unclassified_pr_on_same_manifest_blocks_create(self, gh):
        gh.side_effect = [
            [{"number": 400, "title": "Other dependency", "body": "", "base": "main"}],
            [{"filename": "api/package.json"}],
        ]
        with self.assertRaisesRegex(ValueError, "#400"):
            guard.guard_create({"items": [create_item()]}, "Azure/ARO-HCP")

    @patch.object(guard, "gh_lines")
    def test_unrelated_manifest_is_allowed(self, gh):
        gh.side_effect = [
            [{"number": 400, "title": "Other dependency", "body": "", "base": "main"}],
            [{"filename": "service/go.mod"}],
        ]
        guard.guard_create({"items": [create_item()]}, "Azure/ARO-HCP")

    @patch.object(guard, "gh_lines")
    def test_same_family_on_another_manifest_is_blocked(self, gh):
        gh.side_effect = [
            [{"number": 401, "title": "fix(deps): typespec to 1.16.0", "body": "", "base": "main"}],
            [{"filename": "other/package.json"}],
        ]
        with self.assertRaisesRegex(ValueError, "#401"):
            guard.guard_create({"items": [create_item()]}, "Azure/ARO-HCP")

    @patch.object(guard, "gh_lines")
    def test_prose_match_without_deps_is_allowed(self, gh):
        gh.side_effect = [
            [{"number": 402, "title": "Documentation for typespec", "body": "See typespec family docs", "base": "main"}],
            [{"filename": "docs/readme.md"}],
        ]
        guard.guard_create({"items": [create_item()]}, "Azure/ARO-HCP")

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

    def test_main_rewrites_output_after_dropping_malformed_creates(self):
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


if __name__ == "__main__":
    main()
