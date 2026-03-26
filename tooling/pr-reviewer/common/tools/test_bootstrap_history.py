#!/usr/bin/env python3
from __future__ import annotations

import sys
import unittest
from pathlib import Path

TOOLS_DIR = Path(__file__).resolve().parent
sys.path.insert(0, str(TOOLS_DIR))

import bootstrap_history
import classify_paths


class BootstrapHistoryTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls) -> None:
        cls.routing = classify_paths.load_routing()

    def assert_same_domains(self, *paths: str) -> None:
        expected = [
            item["domain"]
            for item in classify_paths.classify(list(paths), self.routing)["domains"]
        ]
        actual = bootstrap_history.classify(list(paths), self.routing)
        self.assertEqual(expected, actual)

    def test_hidden_command_and_directory_inputs_match_live_classifier(self) -> None:
        self.assert_same_domains("./.claude/commands/arohcp/review.md", "tooling/pr-reviewer")

    def test_github_instruction_path_uses_same_routing_logic(self) -> None:
        self.assert_same_domains(".github/instructions/arohcp-reviewer.instructions.md")

    def test_unmatched_paths_stay_unmatched(self) -> None:
        self.assert_same_domains("not-a-real-reviewer-prefix/example.txt")


if __name__ == "__main__":
    unittest.main()
