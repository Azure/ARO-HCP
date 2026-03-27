#!/usr/bin/env python3
from __future__ import annotations

import re
import sys
from pathlib import Path


COMMAND_SPECS = {
    ".claude/commands/arohcp/review.md": {
        "title": "# ARO-HCP Review",
        "required_frontmatter": ["description", "argument-hint", "allowed-tools"],
        "expected_allowed_tools": [
            "Read",
            "Glob",
            "Grep",
            "Bash(git:*)",
            "Bash(gh pr view:*)",
            "Bash(gh pr diff:*)",
            "Bash(make:*)",
            "Bash(go:*)",
            "Bash(python3:*)",
        ],
        "required_sections": ["## Process", "## Your Task"],
        "required_strings": [
            "tooling/pr-reviewer/SKILL.md",
            "tooling/pr-reviewer/MANIFEST.md",
            "tooling/pr-reviewer/common/validation/command-policy.md",
            "common/baseline/no-findings.md",
        ],
        "required_section_strings": {
            "## Process": ["$ARGUMENTS"],
            "## Your Task": ["$ARGUMENTS"],
        },
    },
    ".claude/commands/arohcp/eval.md": {
        "title": "# ARO-HCP Eval",
        "required_frontmatter": ["description", "argument-hint", "allowed-tools"],
        "expected_allowed_tools": [
            "Read",
            "Glob",
            "Grep",
            "Bash(git:*)",
            "Bash(make:*)",
            "Bash(python3:*)",
        ],
        "required_sections": ["## Process", "## Your Task"],
        "required_strings": [
            "tooling/pr-reviewer/evals/evals.json",
            "tooling/pr-reviewer/SKILL.md",
            "MANIFEST.md",
            "tooling/pr-reviewer/common/tools/run_reviewer_evals.py",
            "make -C tooling/pr-reviewer evalcheck",
        ],
        "required_section_strings": {
            "## Process": ["$ARGUMENTS"],
            "## Your Task": ["$ARGUMENTS"],
        },
    },
}


def parse_frontmatter(path: Path, errors: list[str]) -> tuple[dict[str, str], str]:
    lines = path.read_text(encoding="utf-8").splitlines()
    if not lines or lines[0].strip() != "---":
        errors.append(f"{path} is missing opening frontmatter delimiter")
        return {}, ""

    closing_index = None
    for index, line in enumerate(lines[1:], start=1):
        if line.strip() == "---":
            closing_index = index
            break
    if closing_index is None:
        errors.append(f"{path} is missing closing frontmatter delimiter")
        return {}, ""

    frontmatter: dict[str, str] = {}
    for line_number, line in enumerate(lines[1:closing_index], start=2):
        if not line.strip():
            continue
        if ":" not in line:
            errors.append(f"{path}:{line_number} has invalid frontmatter line: {line}")
            continue
        key, value = line.split(":", 1)
        key = key.strip()
        value = value.strip()
        if not key or not value:
            errors.append(f"{path}:{line_number} has empty frontmatter key or value")
            continue
        if key in frontmatter:
            errors.append(f"{path}:{line_number} duplicates frontmatter key {key}")
            continue
        frontmatter[key] = value

    body = "\n".join(lines[closing_index + 1 :]).strip()
    if not body:
        errors.append(f"{path} has no body after frontmatter")
    return frontmatter, body


def parse_allowed_tools(value: str) -> set[str]:
    return {tool.strip() for tool in value.split(",") if tool.strip()}


def extract_section(body: str, heading: str) -> str | None:
    pattern = re.compile(rf"(?ms)^{re.escape(heading)}\s*$\n(.*?)(?=^##\s|\Z)")
    match = pattern.search(body)
    if not match:
        return None
    return match.group(1).strip()


def main() -> int:
    reviewer_root = Path(__file__).resolve().parents[2]
    repo_root = reviewer_root.parent.parent
    errors: list[str] = []

    for rel_path, spec in COMMAND_SPECS.items():
        path = repo_root / rel_path
        if not path.exists():
            errors.append(f"command entrypoint missing: {path}")
            continue

        frontmatter, body = parse_frontmatter(path, errors)
        for key in spec["required_frontmatter"]:
            if not frontmatter.get(key):
                errors.append(f"{path} is missing required frontmatter key: {key}")

        allowed_tools = parse_allowed_tools(frontmatter.get("allowed-tools", ""))
        expected_allowed_tools = set(spec["expected_allowed_tools"])
        for tool in expected_allowed_tools - allowed_tools:
            errors.append(f"{path} is missing required allowed-tools entry: {tool}")
        for tool in allowed_tools - expected_allowed_tools:
            errors.append(f"{path} has unexpected allowed-tools entry: {tool}")

        if spec["title"] not in body:
            errors.append(f"{path} is missing required title heading: {spec['title']}")

        for heading in spec["required_sections"]:
            section = extract_section(body, heading)
            if section is None:
                errors.append(f"{path} is missing required section: {heading}")
                continue
            if not section:
                errors.append(f"{path} has empty section: {heading}")
                continue
            for needle in spec["required_section_strings"].get(heading, []):
                if needle not in section:
                    errors.append(f"{path} section {heading} is missing required content: {needle}")

        for needle in spec["required_strings"]:
            if needle not in body:
                errors.append(f"{path} is missing required body content: {needle}")

    if errors:
        for error in errors:
            print(error, file=sys.stderr)
        return 1

    print(f"Validated {len(COMMAND_SPECS)} Claude command entrypoints.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
