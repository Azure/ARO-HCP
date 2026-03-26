#!/usr/bin/env python3
from __future__ import annotations

import json
import re
import sys
from pathlib import Path


MANIFEST_PATH_RE = re.compile(r"`([^`]+)`")
VALID_ROOTS = {"repo", "reviewer"}
VALID_KINDS = {"file", "glob"}


def load_json(path: Path) -> object:
    return json.loads(path.read_text(encoding="utf-8"))


def extract_manifest_paths(path: Path) -> set[str]:
    return set(MANIFEST_PATH_RE.findall(path.read_text(encoding="utf-8")))


def main() -> int:
    reviewer_root = Path(__file__).resolve().parents[2]
    repo_root = reviewer_root.parent.parent
    inventory_path = reviewer_root / "common/versioning/asset-inventory.json"
    manifest_path = reviewer_root / "MANIFEST.md"
    inventory = load_json(inventory_path)
    errors: list[str] = []

    def require(condition: bool, message: str) -> None:
        if not condition:
            errors.append(message)

    require(isinstance(inventory, dict), f"{inventory_path} must contain a JSON object")
    assets = inventory.get("assets") if isinstance(inventory, dict) else None
    require(isinstance(assets, list), f"{inventory_path} must contain an 'assets' list")

    inventory_paths: set[str] = set()
    if isinstance(assets, list):
        for index, entry in enumerate(assets):
            require(isinstance(entry, dict), f"asset entry {index} must be an object")
            if not isinstance(entry, dict):
                continue

            path = entry.get("path")
            root = entry.get("root")
            kind = entry.get("kind")
            section = entry.get("section")

            require(isinstance(path, str) and path, f"asset entry {index} has invalid path")
            require(root in VALID_ROOTS, f"asset entry {index} has invalid root: {root}")
            require(kind in VALID_KINDS, f"asset entry {index} has invalid kind: {kind}")
            require(isinstance(section, str) and section, f"asset entry {index} has invalid section")
            if not isinstance(path, str) or not path or root not in VALID_ROOTS or kind not in VALID_KINDS:
                continue

            require(path not in inventory_paths, f"asset inventory contains duplicate path: {path}")
            inventory_paths.add(path)

            base = reviewer_root if root == "reviewer" else repo_root
            if kind == "file":
                require((base / path).exists(), f"asset inventory file is missing: {path}")
            else:
                matches = sorted(base.glob(path))
                require(matches, f"asset inventory glob matched no files: {path}")

    manifest_paths = extract_manifest_paths(manifest_path)
    missing_from_manifest = sorted(inventory_paths - manifest_paths)
    missing_from_inventory = sorted(manifest_paths - inventory_paths)
    require(not missing_from_manifest, f"MANIFEST.md is missing asset inventory paths: {missing_from_manifest}")
    require(not missing_from_inventory, f"asset inventory is missing MANIFEST.md paths: {missing_from_inventory}")

    if errors:
        for error in errors:
            print(error, file=sys.stderr)
        return 1

    print(f"Validated {len(inventory_paths)} asset inventory entries and MANIFEST.md consistency.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
