#!/usr/bin/env python3
# Copyright 2026 Microsoft Corporation
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

"""Read pins and upstream releases once; emit one authoritative row per family.

No tracked files are written and no tools are installed. Exit zero means that
collection finished, not that every lookup succeeded: consumers must inspect
complete, errors, and each family's status before describing versions as current.
"""

import argparse
import json
import os
import re
import subprocess
from pathlib import Path


VERSION = re.compile(
    r"v?(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)"
    r"(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?"
    r"(?:\+([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?"
)


def version_key(value):
    """SemVer precedence, including numeric prerelease identifiers and build tags."""
    match = VERSION.fullmatch(value) if isinstance(value, str) else None
    if not match:
        raise ValueError(f"invalid semantic version: {value!r}")
    major, minor, patch, prerelease, _ = match.groups()
    identifiers = []
    for item in prerelease.split(".") if prerelease else []:
        if item.isdigit():
            if len(item) > 1 and item.startswith("0"):
                raise ValueError(f"invalid numeric prerelease identifier: {value!r}")
            identifiers.append((0, int(item)))
        else:
            identifiers.append((1, item))
    return (int(major), int(minor), int(patch), not prerelease, tuple(identifiers))


def command_json(command, root):
    result = subprocess.run(
        command, cwd=root, env={**os.environ, "GOWORK": "off", "GOTOOLCHAIN": "local"},
        capture_output=True, text=True, timeout=120, check=False,
    )
    if result.returncode:
        raise ValueError(f"{command[0]} failed ({result.returncode}): {result.stderr.strip()[:1000]}")
    value = json.loads(result.stdout)
    if isinstance(value, dict) and value.get("Error"):
        raise ValueError(f"upstream lookup failed: {value['Error']}")
    return value


def add_pin(families, family, kind, source_url, pin):
    row = families.setdefault(family, {
        "family": family, "kind": kind, "sourceURL": source_url, "pins": [],
    })
    row["pins"].append(pin)


def collect(root):
    families = {}
    errors = []
    ci = root / "dev-infrastructure/openshift-ci"
    try:
        metadata = command_json(["bash", str(ci / "update-tool-versions.sh"), "--metadata-json"], root)
        if not isinstance(metadata, list) or not metadata:
            raise ValueError("empty CI tool mapping")
        versions = (ci / "versions.mk").read_text()
        for tool in metadata:
            variable, repository = tool["variable"], tool["repository"]
            matches = re.findall(rf"^{re.escape(variable)}\s*\?=\s*(\S+)\s*$", versions, re.MULTILINE)
            pin = {"toolName": variable.removesuffix("_VERSION").lower(),
                   "source": "dev-infrastructure/openshift-ci/versions.mk", "variable": variable,
                   "current": matches[0] if len(matches) == 1 else None}
            add_pin(families, repository, "github-release",
                    f"https://api.github.com/repos/{repository}/releases/latest", pin)
    except (OSError, ValueError, KeyError, TypeError, subprocess.SubprocessError) as error:
        errors.append(f"CI pin discovery: {error}")

    modules = sorted(path for path in (root / ".bingo").glob("*.mod") if path.name != "go.mod")
    if not modules:
        errors.append("Bingo pin discovery: no tool module files found")
    for path in modules:
        source = str(path.relative_to(root))
        try:
            metadata = command_json([
                "go", "-C", str(root / ".bingo"), "mod", "edit", "-json", f"-modfile={path.name}",
            ], root)
            direct = [item for item in metadata.get("Require", []) if not item.get("Indirect")]
            if len(direct) != 1 or metadata.get("Replace") or metadata.get("Exclude"):
                raise ValueError("expected one direct upstream requirement without replace/exclude")
            module, current = direct[0]["Path"], direct[0]["Version"]
            if not isinstance(module, str) or not module:
                raise ValueError("missing upstream module path")
            add_pin(families, module, "go-module", f"https://pkg.go.dev/{module}",
                    {"toolName": path.stem, "source": source, "current": current})
        except (OSError, ValueError, KeyError, TypeError, AttributeError, subprocess.SubprocessError) as error:
            add_pin(families, f"pin:{source}", "go-module", None,
                    {"toolName": path.stem, "source": source, "current": None, "error": str(error)})

    rows = []
    for family, row in sorted(families.items()):
        row["pins"].sort(key=lambda pin: pin["toolName"])
        row["toolNames"] = [pin["toolName"] for pin in row["pins"]]
        currents = {pin["current"] for pin in row["pins"]}
        row.update(current=next(iter(currents)) if len(currents) == 1 else None,
                   latest=None, status="error", reason=None)
        try:
            for pin in row["pins"]:
                if pin.get("error"):
                    raise ValueError(pin["error"])
                version_key(pin["current"])
            if row["kind"] == "github-release":
                release = command_json(["gh", "api", row["sourceURL"]], root)
                if release.get("draft") is not False or release.get("prerelease") is not False:
                    raise ValueError("latest release metadata is missing or not stable")
                latest = release["tag_name"]
                if not isinstance(latest, str):
                    raise ValueError("missing latest release tag")
                latest = latest.removeprefix("v")
            else:
                release = command_json([
                    "go", "-C", str(root / ".bingo"), "list", "-m", "-json", f"{family}@latest",
                ], root)
                if release.get("Path") != family:
                    raise ValueError("latest module response does not match upstream family")
                latest = release["Version"]
            latest_key = version_key(latest)
            row["latest"] = latest
            if not latest_key[3]:
                row.update(status="blocked", reason="upstream @latest is not a stable release")
            elif len(currents) != 1:
                row.update(status="blocked", reason="family pins disagree; see per-tool current versions")
            else:
                current_key = version_key(row["current"])
                row["status"] = ("outdated" if current_key < latest_key
                                 else "ahead" if current_key > latest_key else "current")
        except (OSError, ValueError, KeyError, TypeError, AttributeError, subprocess.SubprocessError) as error:
            row.update(status="error", reason=str(error))
        rows.append(row)
    return {"schemaVersion": 1, "complete": not errors and all(row["status"] != "error" for row in rows),
            "errors": errors, "families": rows}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--root", type=Path, default=Path(__file__).resolve().parents[2])
    args = parser.parse_args()
    result = collect(args.root.resolve())
    print(json.dumps(result, indent=2, sort_keys=True))
    summary = os.environ.get("GITHUB_STEP_SUMMARY")
    if summary:
        def cell(value):
            return str(value if value is not None else "unknown").replace("|", "\\|").replace("\n", " ")

        lines = ["## Tool version inventory", "",
                 "| Family | Pinned | Latest stable | Status |",
                 "| --- | --- | --- | --- |"]
        for row in result["families"]:
            lines.append("| " + " | ".join(cell(row[key]) for key in
                         ("family", "current", "latest", "status")) + " |")
        if not result["complete"]:
            lines.extend(["", "Inventory is incomplete. Unknown or failed lookups are not current."])
        with open(summary, "a", encoding="utf-8") as destination:
            destination.write("\n".join(lines) + "\n")


if __name__ == "__main__":
    main()
