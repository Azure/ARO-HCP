#!/usr/bin/env python3
from __future__ import annotations

import sys
import unittest
from pathlib import Path

TOOLS_DIR = Path(__file__).resolve().parent
sys.path.insert(0, str(TOOLS_DIR))

import validate_fixture_quality


class ValidateFixtureQualityTest(unittest.TestCase):
    def validate(self, *lines: str) -> list[str]:
        return validate_fixture_quality.validate_fixture(Path("fixture.md"), list(lines))

    def test_empty_later_optional_context_section_is_rejected(self) -> None:
        errors = self.validate(
            "# PR #1: Example fixture",
            "",
            "- **Title:** Example",
            "- **Merged:** 2026-03-26",
            "- **Touched areas:** tooling/pr-reviewer",
            "",
            "## High-signal review moments",
            "",
            "- The first optional section has real content.",
            "",
            "## Review/issue context",
            "",
            "## Why it mattered",
            "",
            "Because the fixture should fail when a later optional section is empty.",
            "",
            "## Reusable lesson",
            "",
            "- Validate every optional context heading independently.",
        )

        self.assertIn("fixture.md section ## Review/issue context is empty", errors)

    def test_multiple_optional_context_sections_with_content_pass(self) -> None:
        errors = self.validate(
            "# PR #1: Example fixture",
            "",
            "- **Title:** Example",
            "- **Merged:** 2026-03-26",
            "- **Touched areas:** tooling/pr-reviewer",
            "",
            "## High-signal review moments",
            "",
            "- First optional section content.",
            "",
            "## Review/rollout signals",
            "",
            "- Second optional section content.",
            "",
            "## Why it mattered",
            "",
            "Because fixtures can include more than one optional context section.",
            "",
            "## Reusable lesson",
            "",
            "- Keep optional-section validation exhaustive.",
        )

        self.assertEqual([], errors)


if __name__ == "__main__":
    unittest.main()
