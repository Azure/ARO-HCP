"""Focused safety checks for agentic dependency PR creation and retests."""

import importlib.util
import os
from datetime import datetime, timezone
from pathlib import Path
from unittest import TestCase, main
from unittest.mock import patch


spec = importlib.util.spec_from_file_location(
    "dependabot_guard", Path(__file__).with_name("dependabot_guard.py")
)
guard = importlib.util.module_from_spec(spec)
spec.loader.exec_module(guard)


def create_item(manifest="api/package.json", family="typespec"):
    return {
        "type": "create_pull_request",
        "title": f"api npm {family} family to 1.16.0 (high)",
        "data": {
            "ecosystem": "npm",
            "package_family": family,
            "target_version": "1.16.0",
            "manifests": [manifest],
        },
    }


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


if __name__ == "__main__":
    main()
