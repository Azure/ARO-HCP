#!/usr/bin/env python3
from __future__ import annotations

import sys
from pathlib import Path


REQUIRED_METADATA_PREFIXES = [
    "- **Title:**",
    "- **Merged:**",
    "- **Touched areas:**",
]

REQUIRED_SECTIONS = [
    ("## Why it mattered",),
    ("## Reusable lesson",),
]

OPTIONAL_CONTEXT_SECTIONS = (
    "## High-signal review moments",
    "## Review/issue context",
    "## Review/rollout signals",
)


def find_heading(lines: list[str], options: tuple[str, ...]) -> tuple[int, str] | None:
    for index, line in enumerate(lines):
        if line in options:
            return index, line
    return None


def find_headings(lines: list[str], options: tuple[str, ...]) -> list[tuple[int, str]]:
    return [(index, line) for index, line in enumerate(lines) if line in options]


def section_body(lines: list[str], heading_index: int) -> str:
    body_lines: list[str] = []
    for line in lines[heading_index + 1 :]:
        if line.startswith("## "):
            break
        body_lines.append(line)
    return "\n".join(body_lines).strip()


def validate_fixture(path: Path, lines: list[str]) -> list[str]:
    errors: list[str] = []
    if not lines or not lines[0].startswith("# PR #"):
        return [f"{path} is missing a '# PR #' title heading"]

    for prefix in REQUIRED_METADATA_PREFIXES:
        if not any(line.startswith(prefix) for line in lines):
            errors.append(f"{path} is missing required metadata line starting with: {prefix}")

    for section_options in REQUIRED_SECTIONS:
        heading = find_heading(lines, section_options)
        if heading is None:
            errors.append(f"{path} is missing one of the required headings: {section_options}")
            continue

        body = section_body(lines, heading[0])
        if not body:
            errors.append(f"{path} section {heading[1]} is empty")

    for heading_index, heading_name in find_headings(lines, OPTIONAL_CONTEXT_SECTIONS):
        body = section_body(lines, heading_index)
        if not body:
            errors.append(f"{path} section {heading_name} is empty")

    return errors


def main() -> int:
    reviewer_root = Path(__file__).resolve().parents[2]
    fixtures_dir = reviewer_root / "fixtures/historical-prs"
    fixture_paths = sorted(fixtures_dir.glob("*.md"))
    errors: list[str] = []

    if not fixture_paths:
        errors.append(f"no historical fixture files found under {fixtures_dir}")

    for path in fixture_paths:
        errors.extend(validate_fixture(path, path.read_text(encoding="utf-8").splitlines()))

    if errors:
        for error in errors:
            print(error, file=sys.stderr)
        return 1

    print(f"Validated {len(fixture_paths)} historical fixture documents.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
