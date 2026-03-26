#!/usr/bin/env python3
from __future__ import annotations

import sys
from pathlib import Path


REPO_WIDE_INSTRUCTION_PATH = Path(".github/copilot-instructions.md")
PATH_SCOPED_INSTRUCTION_PATH = Path(".github/instructions/arohcp-reviewer.instructions.md")

REPO_WIDE_REQUIRED_STRINGS = [
    "tooling/pr-reviewer/SKILL.md",
    "tooling/pr-reviewer/MANIFEST.md",
    "tooling/pr-reviewer/common/validation/command-policy.md",
    "tooling/pr-reviewer/common/tools/classify_paths.py",
    "tooling/pr-reviewer/common/domain-routing/path-routing.json",
    "history_fixtures",
    "`pass`, `fail`, `blocked`, or `not applicable`",
    "make -C tooling/pr-reviewer validate",
]

PATH_SCOPED_REQUIRED_GLOBS = [
    "tooling/pr-reviewer/**",
    ".claude/commands/arohcp/**",
    ".github/copilot-instructions.md",
    ".github/instructions/arohcp-reviewer.instructions.md",
]

PATH_SCOPED_REQUIRED_STRINGS = [
    "tooling/pr-reviewer/SKILL.md",
    "tooling/pr-reviewer/MANIFEST.md",
    "tooling/pr-reviewer/common/validation/command-policy.md",
    "make -C tooling/pr-reviewer validate",
]


def require(condition: bool, message: str, errors: list[str]) -> None:
    if not condition:
        errors.append(message)


def load_text(path: Path, errors: list[str]) -> str:
    if not path.exists():
        errors.append(f"instruction file missing: {path}")
        return ""
    return path.read_text(encoding="utf-8")


def parse_apply_to(path: Path, errors: list[str]) -> tuple[list[str], str]:
    text = load_text(path, errors)
    if not text:
        return [], ""

    lines = text.splitlines()
    require(bool(lines) and lines[0].strip() == "---", f"{path} is missing opening frontmatter delimiter", errors)
    if not lines or lines[0].strip() != "---":
        return [], ""

    closing_index = None
    for index, line in enumerate(lines[1:], start=1):
        if line.strip() == "---":
            closing_index = index
            break
    require(closing_index is not None, f"{path} is missing closing frontmatter delimiter", errors)
    if closing_index is None:
        return [], ""

    apply_to: list[str] = []
    in_apply_to = False
    for line_number, line in enumerate(lines[1:closing_index], start=2):
        stripped = line.strip()
        if not stripped:
            continue
        if stripped == "applyTo:":
            in_apply_to = True
            continue
        if in_apply_to and stripped.startswith("- "):
            value = stripped[2:].strip().strip('"').strip("'")
            if value:
                apply_to.append(value)
            else:
                errors.append(f"{path}:{line_number} has empty applyTo entry")
            continue
        errors.append(f"{path}:{line_number} has unsupported frontmatter content: {line}")

    body = "\n".join(lines[closing_index + 1 :]).strip()
    require(bool(body), f"{path} has no body after frontmatter", errors)
    return apply_to, body


def main() -> int:
    reviewer_root = Path(__file__).resolve().parents[2]
    repo_root = reviewer_root.parent.parent
    errors: list[str] = []

    repo_wide_text = load_text(repo_root / REPO_WIDE_INSTRUCTION_PATH, errors)
    for needle in REPO_WIDE_REQUIRED_STRINGS:
        require(
            needle in repo_wide_text,
            f"{repo_root / REPO_WIDE_INSTRUCTION_PATH} is missing required content: {needle}",
            errors,
        )

    apply_to, path_scoped_body = parse_apply_to(repo_root / PATH_SCOPED_INSTRUCTION_PATH, errors)
    for glob_pattern in PATH_SCOPED_REQUIRED_GLOBS:
        require(
            glob_pattern in apply_to,
            f"{repo_root / PATH_SCOPED_INSTRUCTION_PATH} is missing required applyTo entry: {glob_pattern}",
            errors,
        )
    for needle in PATH_SCOPED_REQUIRED_STRINGS:
        require(
            needle in path_scoped_body,
            f"{repo_root / PATH_SCOPED_INSTRUCTION_PATH} is missing required content: {needle}",
            errors,
        )

    if errors:
        for error in errors:
            print(error, file=sys.stderr)
        return 1

    print("Validated Copilot instruction entrypoints.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
