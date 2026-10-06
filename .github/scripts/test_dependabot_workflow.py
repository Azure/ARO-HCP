"""Catch silent gh-aw item drops in the compiled Dependabot workflow."""

import json
import re
from pathlib import Path
from unittest import TestCase, main

LOCK = Path(__file__).resolve().parents[1] / "workflows" / "dependabot-remediation.lock.yml"


def safe_outputs_config():
    match = re.search(r"GH_AW_SAFE_OUTPUTS_CONFIG: (\".+\")$", LOCK.read_text(), re.M)
    if not match:
        raise AssertionError("compiled workflow is missing GH_AW_SAFE_OUTPUTS_CONFIG")
    return json.loads(json.loads(match.group(1)))


class CompiledSafeOutputLimitsTests(TestCase):
    def setUp(self):
        self.config = safe_outputs_config()

    def test_reconcile_keeps_a_full_owned_pr_sweep(self):
        self.assertEqual(self.config["reconcile-owned-pr"]["max"], 20)

    def test_repair_stays_single_item(self):
        self.assertEqual(self.config["repair-owned-pr"]["max"], 1)

    def test_create_pull_request_limit_is_unchanged(self):
        self.assertEqual(self.config["create_pull_request"]["max"], 6)

    def test_tool_descriptions_advertise_the_limits(self):
        text = LOCK.read_text()
        self.assertIn("max 20 unique PRs per run", self.config["reconcile-owned-pr"]["description"])
        self.assertIn("max 1 per run", self.config["repair-owned-pr"]["description"])
        self.assertIn("unique_by(.pull_request_number)", text)


if __name__ == "__main__":
    main()
