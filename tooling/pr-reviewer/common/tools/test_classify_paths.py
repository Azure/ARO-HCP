#!/usr/bin/env python3
from __future__ import annotations

import sys
import unittest
from pathlib import Path

TOOLS_DIR = Path(__file__).resolve().parent
sys.path.insert(0, str(TOOLS_DIR))

import classify_paths


class ClassifyPathsTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls) -> None:
        cls.routing = classify_paths.load_routing()

    def classify(self, *paths: str) -> dict:
        normalized = [classify_paths.normalize_path(path) for path in paths]
        return classify_paths.classify(normalized, self.routing)

    def test_hidden_command_path_routes(self) -> None:
        result = self.classify("./.claude/commands/arohcp/review.md")
        self.assertEqual([], result["unmatched_paths"])
        self.assertIn("sub-reviewers/cross-cutting.md", result["always_load"])
        domains = {item["domain"]: item for item in result["domains"]}
        self.assertIn("observability-testing-tooling", domains)

    def test_bare_directory_and_trailing_slash_route_equally(self) -> None:
        bare = self.classify("tooling/pr-reviewer")
        with_slash = self.classify("tooling/pr-reviewer/")
        self.assertEqual(bare, with_slash)
        domains = {item["domain"]: item for item in bare["domains"]}
        self.assertIn("observability-testing-tooling", domains)

    def test_upgrade_controller_path_routes_to_backend_state_with_upgrade_fixture(self) -> None:
        result = self.classify("backend/pkg/controllers/upgradecontrollers/control_plane_desired_version_controller.go")
        self.assertEqual([], result["unmatched_paths"])
        domains = {item["domain"]: item for item in result["domains"]}
        backend = domains["backend-state"]
        self.assertEqual(
            ["backend/pkg/controllers/upgradecontrollers/control_plane_desired_version_controller.go"],
            backend["high_risk_paths"],
        )
        self.assertIn(
            "fixtures/historical-prs/pr-3954-control-plane-upgrade-controller-flow.md",
            backend["history_fixtures"],
        )

    def test_config_yaml_routes_as_high_risk_config_pipeline(self) -> None:
        result = self.classify("config/config.yaml")
        self.assertEqual([], result["unmatched_paths"])
        domains = {item["domain"]: item for item in result["domains"]}
        config = domains["config-pipelines"]
        self.assertEqual(["config/config.yaml"], config["high_risk_paths"])

    def test_multi_domain_path_set_loads_all_expected_domains(self) -> None:
        result = self.classify(
            "frontend/pkg/frontend/cluster.go",
            "backend/pkg/controllers/upgradecontrollers/control_plane_active_version_controller.go",
            ".claude/commands/arohcp/review.md",
        )
        self.assertEqual([], result["unmatched_paths"])
        domains = {item["domain"] for item in result["domains"]}
        self.assertTrue(
            {"resource-provider-api", "backend-state", "observability-testing-tooling"}.issubset(domains)
        )


if __name__ == "__main__":
    unittest.main()
