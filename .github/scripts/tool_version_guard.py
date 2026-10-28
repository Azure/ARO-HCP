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

"""Defer tool-pin proposals that overlap fresh open-PR file inventories."""

import argparse
import json
import os
import re
import subprocess
import tempfile
from pathlib import Path


OWNER = "aro-hcp-robot[bot]"
LABEL = "agentic-tool-upgrade"
BINGO_HELPERS = {".bingo/Variables.mk", ".bingo/variables.env"}
CI_IMAGE_FILES = {
    "dev-infrastructure/openshift-ci/versions.mk",
    "dev-infrastructure/openshift-ci/Dockerfile",
}
ARTIFACT_DIRECTORY = Path("/tmp/gh-aw")


def gh_json(endpoint, *, paginate=False):
    command = ["gh", "api"]
    if paginate:
        command.extend(["--paginate", "--slurp"])
    result = subprocess.run(
        [*command, endpoint], check=True, stdout=subprocess.PIPE, text=True,
    )
    value = json.loads(result.stdout)
    if not paginate:
        return value
    if not isinstance(value, list) or not value or any(not isinstance(page, list) for page in value):
        raise ValueError(f"incomplete paginated response: {endpoint}")
    return [item for page in value for item in page]


def repository_path(value):
    if (
        not isinstance(value, str) or not value or "\0" in value
        or value.startswith("/") or any(part in ("", ".", "..") for part in value.split("/"))
    ):
        raise ValueError("invalid repository file path")
    return value


def artifact_component(value):
    """Match gh-aw v0.93.4 git_patch_utils.cjs filename sanitization."""
    return re.sub(r"-{2,}", "-", re.sub(r'[/\\:*?"<>|]', "-", value)).strip("-").lower()


def bundle_paths(bundle, branch, patch):
    """Inventory bundle trees in an isolated object database without checking out."""
    bases = set(re.findall(rb"^X-GH-AW-Base-Commit: ([0-9a-f]{40})\r?$", patch, re.MULTILINE))
    if len(bases) != 1:
        raise ValueError("bundle proposal requires its recorded patch base commit")
    base = bases.pop().decode("ascii")
    objects = subprocess.run(
        ["git", "rev-parse", "--path-format=absolute", "--git-path", "objects"],
        check=True, stdout=subprocess.PIPE, text=True,
    ).stdout.strip()
    shallow = Path(subprocess.run(
        ["git", "rev-parse", "--path-format=absolute", "--git-path", "shallow"],
        check=True, stdout=subprocess.PIPE, text=True,
    ).stdout.strip())
    environment = os.environ.copy()
    for key in ("GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_OBJECT_DIRECTORY"):
        environment.pop(key, None)
    environment.update({
        "GIT_ALTERNATE_OBJECT_DIRECTORIES": objects,
        "GIT_CONFIG_NOSYSTEM": "1",
        "GIT_CONFIG_GLOBAL": os.devnull,
        "GIT_NO_LAZY_FETCH": "1",
        "GIT_TERMINAL_PROMPT": "0",
    })
    with tempfile.TemporaryDirectory(prefix="tool-version-bundle-") as temporary:
        def git(*arguments):
            return subprocess.run(
                ["git", "-C", temporary, *arguments], env=environment,
                check=True, stdout=subprocess.PIPE,
            ).stdout

        git("init", "--bare", "--quiet", "--template=")
        # Alternates share objects, not shallow boundaries. Preserve the actual
        # checkout boundaries so ancestry checks do not walk unavailable parents.
        if shallow.exists():
            (Path(temporary) / "shallow").write_bytes(shallow.read_bytes())
        # unbundle imports objects only into the temporary repository and rejects
        # missing prerequisites or corrupt packs; it never fetches from a remote.
        heads = git("bundle", "unbundle", str(bundle.resolve())).decode("utf-8").splitlines()
        tips = [
            line.split(" ", 1)[0] for line in heads
            if line.endswith(f" refs/heads/{branch}")
        ]
        if len(tips) != 1 or not re.fullmatch(r"[0-9a-f]{40}", tips[0]):
            raise ValueError("bundle does not contain the proposal branch")
        git("merge-base", "--is-ancestor", base, tips[0])
        result = git(
            "diff", "--no-ext-diff", "--no-textconv", "--no-renames",
            "--name-only", "-z", base, tips[0], "--",
        )
        if not result or not result.endswith(b"\0"):
            raise ValueError("bundle has no complete changed-file inventory")
        return {repository_path(name.decode("utf-8")) for name in result[:-1].split(b"\0")}


def patch_paths(item, repository, artifact_directory=ARTIFACT_DIRECTORY):
    """Read the same branch-derived patch namespace as the pinned PR handler."""
    for field in ("branch", "title", "body"):
        if not isinstance(item.get(field), str) or not item[field].strip():
            raise ValueError(f"create_pull_request requires {field}")
    if item.get("repo", repository).lower() != repository.lower():
        raise ValueError("tooling proposals must target the current repository")
    if item.get("base", "main") not in ("", "main"):
        raise ValueError("tooling proposals must target main")
    branch = artifact_component(item["branch"])
    if not branch:
        raise ValueError("invalid proposal branch")
    # resolve_transport_paths.cjs rejects artifacts in both namespaces.
    namespaces = [
        artifact_directory / f"aw-{artifact_component(repository)}-{branch}",
        artifact_directory / f"aw-{branch}",
    ]
    present = [
        prefix for prefix in namespaces
        if any(Path(f"{prefix}.{suffix}").exists() for suffix in ("patch", "bundle"))
    ]
    if len(present) != 1:
        raise ValueError("missing or ambiguous proposal patch artifacts")
    patch = Path(f"{present[0]}.patch")
    if not patch.is_file():
        raise ValueError("a complete proposal patch artifact is required")
    content = patch.read_bytes()
    paths = set()
    # Git validates complete hunks and decodes quoted paths. Reversing also
    # inventories the source side of renames without parsing diff path quoting.
    for direction in ([], ["--reverse"]):
        result = subprocess.run(
            ["git", "apply", "--numstat", "-z", *direction, "-"],
            input=content, check=True, stdout=subprocess.PIPE,
        )
        records = result.stdout.split(b"\0")
        if not records or records.pop() != b"":
            raise ValueError("incomplete proposal patch file inventory")
        index = 0
        while index < len(records):
            fields = records[index].split(b"\t", 2)
            index += 1
            if len(fields) != 3:
                raise ValueError("malformed proposal patch file inventory")
            names = [fields[2]]
            if not names[0]:
                names = records[index:index + 2]
                index += 2
                if len(names) != 2:
                    raise ValueError("incomplete proposal rename inventory")
            paths.update(repository_path(name.decode("utf-8")) for name in names)
    if not paths:
        raise ValueError("proposal patch has no changed files")
    bundle = Path(f"{present[0]}.bundle")
    if bundle.exists():
        paths.update(bundle_paths(bundle, item["branch"], content))
    return paths


def overlaps(candidate, files):
    """Treat generated Bingo helpers and the CI image pair as coupled files."""
    return bool(
        candidate.intersection(files)
        or (CI_IMAGE_FILES.intersection(candidate) and CI_IMAGE_FILES.intersection(files))
        or (BINGO_HELPERS.intersection(candidate) and any(path.startswith(".bingo/") for path in files))
        or (BINGO_HELPERS.intersection(files) and any(path.startswith(".bingo/") for path in candidate))
    )


def open_prs(repository):
    prs = gh_json(f"repos/{repository}/pulls?state=open&base=main&per_page=100", paginate=True)
    numbers = set()
    for pr in prs:
        if (
            not isinstance(pr, dict)
            or type(pr.get("number")) is not int or pr["number"] <= 0
            or pr["number"] in numbers
            or not isinstance(pr.get("base"), dict)
            or not isinstance(pr["base"].get("ref"), str)
            or not isinstance(pr.get("user"), dict)
            or not isinstance(pr["user"].get("login"), str)
            or not isinstance(pr.get("labels"), list)
            or any(not isinstance(label, dict) or not isinstance(label.get("name"), str)
                   for label in pr["labels"])
            or pr.get("state", "open") != "open"
        ):
            raise ValueError("incomplete open pull-request inventory")
        numbers.add(pr["number"])
    return [pr for pr in prs if pr["base"]["ref"] == "main"]


def pr_paths(repository, pr):
    endpoint = f"repos/{repository}/pulls/{pr['number']}"
    detail = gh_json(endpoint)
    if (
        not isinstance(detail, dict) or detail.get("number") != pr["number"]
        or detail.get("state") != "open" or detail.get("base", {}).get("ref") != "main"
        or type(detail.get("changed_files")) is not int
        or not 0 <= detail["changed_files"] <= 3000
    ):
        raise ValueError(f"complete open-PR file count unavailable: #{pr['number']}")
    files = gh_json(f"{endpoint}/files?per_page=100", paginate=True)
    names = set()
    paths = set()
    for file in files:
        if not isinstance(file, dict):
            raise ValueError("invalid open-PR file inventory")
        if file.get("status") not in {"added", "removed", "modified", "renamed", "copied", "changed", "unchanged"}:
            raise ValueError("missing open-PR file status")
        filename = repository_path(file.get("filename"))
        if filename in names:
            raise ValueError("duplicate open-PR file inventory")
        names.add(filename)
        paths.add(filename)
        previous = file.get("previous_filename")
        if previous is not None:
            paths.add(repository_path(previous))
        if file.get("status") == "renamed" and previous is None:
            raise ValueError("missing open-PR rename source")
    if len(names) != detail["changed_files"]:
        raise ValueError(f"incomplete open-PR file inventory: #{pr['number']}")
    return paths


def guard_create(output, repository):
    """Requery all authors immediately before publication, preserving other outputs."""
    if (
        not isinstance(output, dict) or output.get("errors")
        or not isinstance(output.get("items"), list)
        or any(not isinstance(item, dict) or not isinstance(item.get("type"), str)
               for item in output["items"])
    ):
        raise ValueError("invalid safe-output proposal inventory")
    prs = open_prs(repository)
    candidates = [
        (item, patch_paths(item, repository))
        for item in output["items"] if item["type"] == "create_pull_request"
    ]
    if not candidates:
        return
    inventory = [(pr["number"], pr_paths(repository, pr)) for pr in prs]
    defer_inflight_proposal(output, prs)
    retained = []
    deferred = []
    for item in output["items"]:
        if item["type"] != "create_pull_request":
            retained.append(item)
            continue
        paths = next(paths for candidate, paths in candidates if candidate is item)
        conflicts = [number for number, files in inventory if overlaps(paths, files)]
        if conflicts:
            deferred.extend(conflicts)
        else:
            retained.append(item)
    if not retained:
        retained.append({
            "type": "noop",
            "message": "Waiting for overlapping open PR(s): "
                       + ", ".join(f"#{number}" for number in sorted(set(deferred))),
        })
    output["items"] = retained


def rewrite_output(path, output):
    """Leave the original artifact intact on failed validation or serialization."""
    descriptor, temporary = tempfile.mkstemp(prefix="tool-version-guard-", dir=path.parent)
    try:
        with os.fdopen(descriptor, "w", encoding="utf-8") as destination:
            json.dump(output, destination, indent=2)
            destination.write("\n")
        os.replace(temporary, path)
    finally:
        if os.path.exists(temporary):
            os.unlink(temporary)


def defer_inflight_proposal(output, prs):
    """Replace new proposals with a no-op while an owned tooling PR is open."""
    active = [
        pr["number"] for pr in prs
        if pr["user"]["login"] == OWNER
        and pr["base"]["ref"] == "main"
        and (
            any(label["name"] == LABEL for label in pr["labels"])
            or (
                (pr.get("title") or "").startswith("chore(tools): ")
                and "<!-- agentic-tool-family:" in (pr.get("body") or "")
            )
        )
    ]
    if not active or not any(item["type"] == "create_pull_request" for item in output["items"]):
        return
    output["items"] = [item for item in output["items"] if item["type"] != "create_pull_request"]
    if not output["items"]:
        output["items"].append({
            "type": "noop",
            "message": "Waiting for existing tooling PR(s): " + ", ".join(f"#{n}" for n in active),
        })


def main():
    """Recheck open PRs with the publication token before applying safe outputs."""
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("output", type=Path)
    parser.add_argument("repository")
    args = parser.parse_args()
    output = json.loads(args.output.read_text())
    guard_create(output, args.repository)
    rewrite_output(args.output, output)


if __name__ == "__main__":
    main()
