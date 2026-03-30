#!/usr/bin/env python3
from __future__ import annotations

import argparse
import subprocess
import sys
from pathlib import Path


def resolve_fixture(repo_root: Path, sre_root: Path, raw: str) -> Path:
    candidate = Path(raw)
    if candidate.is_absolute():
        return candidate
    for base in (repo_root, sre_root):
        resolved = (base / raw).resolve()
        if resolved.exists():
            return resolved
    return (repo_root / raw).resolve()


def main() -> int:
    sre_root = Path(__file__).resolve().parents[2]
    repo_root = sre_root.parents[1]

    parser = argparse.ArgumentParser(
        description="Validate the SRE agent package and print a ready-to-run smoke prompt."
    )
    parser.add_argument(
        "--fixture",
        default="fixtures/historical-incidents/incident-002-kas-api-availability-burn.md",
        help="Fixture path relative to the repo root or the sre-agent root.",
    )
    args = parser.parse_args()

    fixture = resolve_fixture(repo_root, sre_root, args.fixture)
    if not fixture.exists():
        print(f"fixture not found: {fixture}", file=sys.stderr)
        return 1

    result = subprocess.run(
        [sys.executable, str(sre_root / "common/tools/validate_sre_assets.py")],
        cwd=repo_root,
        check=False,
    )
    if result.returncode != 0:
        return result.returncode

    fixture_rel = fixture.relative_to(repo_root)
    print("SRE agent smoke prompt")
    print()
    print(f"Repository: {repo_root}")
    print("Run Copilot CLI in that directory and use:")
    print()
    print(f"Use arohcp-sre-agent on {fixture_rel}")
    print()
    print("Expected kernel behavior:")
    print("- output starts with `# TSG:`")
    print("- metadata includes `Incident envelope`")
    print("- the fresh-session kube-apiserver child-agent flow is used")
    print("- the draft separates probe-path degradation from confirmed kube-apiserver failure")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
