#!/usr/bin/env python3
"""Trusted, fixed-command publication and reconciliation for native Dependabot PRs.

The agent supplies patches, never shell commands. No repository program is run in
this credentialed process. Native Dependabot is deliberately not a robot-owned
PR kind in agentic_pr_maintenance.
"""

import base64
import hashlib
import json
import os
from pathlib import Path, PurePosixPath
import re
import subprocess
import sys
import tempfile
from urllib.parse import quote

import agentic_pr_maintenance as maintenance
from dependabot_guard import lock_versions

NATIVE = "dependabot[bot]"
ROBOT = maintenance.ROBOT
LABEL = "agentic-dependency-upgrade"
MARKER = "native-dependabot-maintenance:"
SHA = maintenance.SHA
ACTIONS = {"admit", "recover-retry", "release-hold", "close-superseded"}
VENDORS = ("vendor/", "image-sync/oc-mirror/acrauth/vendor/")
MAX_PATCH = 16 * 1024 * 1024


def require(condition, message):
    if not condition:
        raise ValueError(message)


def api(endpoint, *, pages=False, method=None, data=None, ci=False):
    command = ["gh", "api"]
    if pages:
        command += ["--paginate", "--slurp"]
    if method:
        command += ["--method", method]
    command.append(endpoint)
    if data is not None:
        command += ["--input", "-"]
    env = os.environ.copy()
    if ci and os.environ.get("CI_TOKEN"):
        env["GH_TOKEN"] = os.environ["CI_TOKEN"]
    result = subprocess.run(command, input=json.dumps(data) if data is not None else None,
                            capture_output=True, text=True, check=True, env=env)
    value = json.loads(result.stdout)
    if pages:
        require(isinstance(value, list) and bool(value), "missing paginated API evidence")
    return value


def rows(endpoint, *, ci=False):
    pages = api(endpoint, pages=True, ci=ci)
    require(all(isinstance(page, list) for page in pages), "malformed paginated list")
    result = [item for page in pages for item in page]
    require(all(isinstance(item, dict) for item in result), "malformed API row")
    return result


def unique(items, field):
    values = [item.get(field) for item in items]
    require(all(value is not None for value in values) and len(set(values)) == len(values),
            "missing or duplicate paginated evidence")
    return items


def validate_repo(repo):
    maintenance.validate_target(repo, 1, "0" * 40)


def identity(pull, repo, author, *, merged=False):
    require(isinstance(pull, dict) and type(pull.get("number")) is int and pull["number"] > 0,
            "missing pull request identity")
    require(pull.get("user", {}).get("login") == author and pull["user"].get("type") == "Bot",
            "foreign or non-bot pull request author")
    require(pull.get("base", {}).get("repo", {}).get("full_name") == repo
            and pull.get("head", {}).get("repo", {}).get("full_name") == repo
            and pull["base"].get("ref") == "main", "foreign repository, fork, or base")
    require(isinstance(pull["head"].get("sha"), str) and SHA.fullmatch(pull["head"]["sha"]),
            "missing pull request SHA")
    require(isinstance(pull["head"].get("ref"), str) and bool(pull["head"]["ref"]), "missing branch")
    require(isinstance(pull.get("labels"), list) and all(isinstance(label, dict)
            and isinstance(label.get("name"), str) for label in pull["labels"]), "missing labels")
    require(pull.get("state") == ("closed" if merged else "open")
            and pull.get("merged") is merged and pull.get("draft") is False,
            "pull request is not in the required ready/merged state")
    return pull


def main_sha(repo):
    value = api(f"repos/{repo}/git/ref/heads/main")
    sha = value.get("object", {}).get("sha") if isinstance(value, dict) else None
    require(isinstance(sha, str) and SHA.fullmatch(sha), "missing main SHA")
    return sha


def pull_detail(repo, number):
    require(type(number) is int and number > 0, "invalid pull request number")
    pull = api(f"repos/{repo}/pulls/{number}")
    require(isinstance(pull, dict) and pull.get("number") == number, "pull request number mismatch")
    return pull


def snapshot(repo, number, sha, *, author=NATIVE, merged=False):
    maintenance.validate_target(repo, number, sha)
    pull = identity(pull_detail(repo, number), repo, author, merged=merged)
    require(pull["head"]["sha"] == sha, "stale source head")
    commits = unique(rows(f"repos/{repo}/pulls/{number}/commits?per_page=100"), "sha")
    files = unique(rows(f"repos/{repo}/pulls/{number}/files?per_page=100"), "filename")
    comments = unique(rows(f"repos/{repo}/issues/{number}/comments?per_page=100"), "id")
    events = unique(rows(f"repos/{repo}/issues/{number}/events?per_page=100"), "id")
    require(type(pull.get("commits")) is int and len(commits) == pull["commits"]
            and bool(commits) and sha in {row["sha"] for row in commits}, "incomplete commit evidence")
    require(type(pull.get("changed_files")) is int and len(files) == pull["changed_files"]
            and bool(files), "incomplete changed-file evidence")
    require(type(pull.get("comments")) is int and len(comments) == pull["comments"],
            "incomplete comment evidence")
    require(all(isinstance(row["sha"], str) and SHA.fullmatch(row["sha"]) for row in commits),
            "invalid commit evidence")
    if author == NATIVE:
        require(all(isinstance(row.get("author"), dict) and row["author"].get("login") == NATIVE
                    and row["author"].get("type") == "Bot" for row in commits),
                "native branch contains a non-Dependabot or unattributed commit")
    statuses = unique(rows(f"repos/{repo}/commits/{sha}/statuses?per_page=100", ci=True), "id")
    check_pages = api(f"repos/{repo}/commits/{sha}/check-runs?filter=latest&per_page=100",
                      pages=True, ci=True)
    require(all(isinstance(page, dict) and isinstance(page.get("check_runs"), list)
                and type(page.get("total_count")) is int for page in check_pages),
            "incomplete check-run evidence")
    checks = unique([check for page in check_pages for check in page["check_runs"]], "id")
    require(len(checks) == check_pages[0]["total_count"], "truncated check-run evidence")
    latest = pull_detail(repo, number)
    require(latest == pull, "pull request changed during snapshot")
    holds, cancellation_pending = maintenance.active_holds(comments, {row["sha"] for row in commits})
    hold_events = [event for event in events if event.get("event") in ("labeled", "unlabeled")
                   and event.get("label", {}).get("name") == maintenance.HOLD_LABEL]
    held = maintenance.HOLD_LABEL in {label["name"] for label in pull["labels"]}
    trusted = maintenance.trusted_hold_label(hold_events)
    blocked_holds = (any(hold["kind"] != "retry-exhaustion" for hold in holds)
                     or held and (not trusted or not holds and not cancellation_pending))
    return {"pull": pull, "files": files, "comments": comments, "holds": holds,
            "held": held, "cancellation_pending": cancellation_pending,
            "blocked_holds": bool(blocked_holds),
            "ci": maintenance.ci_evidence(statuses, checks, sha)}


def content(repo, sha, path):
    value = api(f"repos/{repo}/contents/{quote(path, safe='/')}?ref={sha}")
    require(isinstance(value, dict) and value.get("type") == "file", "missing immutable regular file")
    if value.get("encoding") == "none":
        blob = value.get("sha")
        require(isinstance(blob, str) and SHA.fullmatch(blob), "missing large-file blob identity")
        value = api(f"repos/{repo}/git/blobs/{blob}")
        require(isinstance(value, dict) and value.get("sha") == blob, "large-file blob identity changed")
    require(value.get("encoding") == "base64" and isinstance(value.get("content"), str),
            "missing complete immutable file content")
    decoded = base64.b64decode(value["content"], validate=False)
    require(type(value.get("size")) is int and len(decoded) == value["size"], "truncated file content")
    return decoded.decode("utf-8")


def safe_path(path):
    require(isinstance(path, str) and path and not path.startswith("/")
            and all(part not in ("", ".", "..") for part in path.split("/"))
            and not re.search(r"[\s\\\x00-\x1f]", path), "unsafe patch path")
    return path


def go_versions(text):
    result = {}
    block = None
    for line in text.splitlines():
        line = line.split("//", 1)[0].strip()
        if re.fullmatch(r"(?:require|replace|exclude|retract|godebug)\s*\(", line):
            block = line.split("(")[0].strip()
            continue
        if line == ")":
            block = None
            continue
        match = re.fullmatch(r"(?:require\s+)?([A-Za-z0-9._~+/-]+)\s+(v[0-9][A-Za-z0-9.+_-]*)", line)
        if match and (block == "require" or line.startswith("require ")):
            require(match[1] not in result, "duplicate Go requirement")
            result[match[1]] = match[2]
    return result


def npm_versions(text):
    document = json.loads(text)
    require(isinstance(document, dict), "invalid npm manifest")
    result = {}
    for section in ("dependencies", "devDependencies", "peerDependencies", "optionalDependencies", "overrides"):
        entries = document.get(section, {})
        require(isinstance(entries, dict), "unsupported npm dependency section")
        def visit(values, prefix):
            for package, version in values.items():
                key = prefix + (package,)
                if section == "overrides" and isinstance(version, dict):
                    visit(version, key)
                else:
                    require(isinstance(version, str), "unsupported npm dependency version")
                    result[key] = version
        visit(entries, (section,))
    return result


def action_versions(text):
    result = {}
    for index, line in enumerate(text.splitlines()):
        match = re.fullmatch(r"(\s*(?:-\s*)?uses:\s*['\"]?)([A-Za-z0-9_.-]+/[A-Za-z0-9_./-]+)@([A-Za-z0-9_.-]+)(['\"]?\s*(?:#.*)?)", line)
        if match:
            result[(index, match[2])] = match[3]
    return result


def version_map(path, text):
    name = PurePosixPath(path).name
    if name == "go.mod":
        return "go", go_versions(text)
    if name == "package.json":
        return "npm", npm_versions(text)
    if name in ("package-lock.json", "npm-shrinkwrap.json"):
        return "npm", {key: value[1] for key, value in lock_versions(json.loads(text)).items()}
    if path.startswith(".github/workflows/") and path.endswith((".md", ".yml", ".yaml")):
        return "actions", action_versions(text)
    return None, {}


def normalize_compiler_headers(old_lines, new_lines, pins):
    """Only compiler hashes and the intended action's descriptive pin may vary."""
    for prefix in ("# gh-aw-metadata: ", "# gh-aw-manifest: "):
        old_indexes = [index for index, line in enumerate(old_lines) if line.startswith(prefix)]
        new_indexes = [index for index, line in enumerate(new_lines) if line.startswith(prefix)]
        require(old_indexes == new_indexes and len(old_indexes) <= 1, "compiler header layout changed")
        for index in old_indexes:
            old = json.loads(old_lines[index][len(prefix):])
            new = json.loads(new_lines[index][len(prefix):])
            require(isinstance(old, dict) and isinstance(new, dict), "invalid compiler header")
            if prefix == "# gh-aw-metadata: ":
                for key in ("frontmatter_hash", "body_hash"):
                    require(all(isinstance(value.get(key), str) and re.fullmatch(r"[0-9a-f]{64}", value[key])
                                for value in (old, new)), "invalid compiler source hash")
                    old[key] = new[key] = "<HASH>"
            else:
                left, right = old.get("actions"), new.get("actions")
                require(isinstance(left, list) and isinstance(right, list) and len(left) == len(right),
                        "compiler action inventory changed")
                for before, after in zip(left, right):
                    require(isinstance(before, dict) and isinstance(after, dict)
                            and before.get("repo") == after.get("repo"), "compiler action identity changed")
                    if before != after:
                        repo = after["repo"]
                        require(set(before) == set(after) == {"repo", "sha", "version"}
                                and after["sha"] in pins.get(repo, set()), "unrelated compiler manifest pin")
                        before["sha"] = after["sha"]
                        if before["version"] != after["version"]:
                            official = api(f"repos/{upgrade_family('actions', repo)}/commits/{quote(after['version'], safe='')}")
                            require(isinstance(official, dict) and official.get("sha") == after["sha"],
                                    "compiler manifest release does not resolve to intended pin")
                            before["version"] = after["version"]
            require(old == new, "compiler metadata changes executable provenance or secrets")
            old_lines[index] = new_lines[index] = prefix + "<VALIDATED>"


def action_lock_changes(before, after, group):
    old, new = json.loads(before), json.loads(after)
    require(isinstance(old, dict) and isinstance(new, dict)
            and isinstance(old.get("entries"), dict) and isinstance(new.get("entries"), dict),
            "invalid compiler actions lock")
    old_entries, new_entries = old.pop("entries"), new.pop("entries")
    require(old == new, "compiler containers or non-action lock fields changed")
    allowed = {(item["package"], item["target"]) for source in group["sources"]
               for item in source["intents"] if item["ecosystem"] == "actions"}
    for key in old_entries.keys() | new_entries.keys():
        if old_entries.get(key) == new_entries.get(key):
            continue
        for entry in (old_entries.get(key), new_entries.get(key)):
            if entry is not None:
                require(isinstance(entry, dict) and set(entry) == {"repo", "version", "sha"}
                        and key == entry["repo"] + "@" + entry["version"]
                        and upgrade_family("actions", entry["repo"]) == group["family"],
                        "unrelated or malformed action lock entry")
        if key in new_entries:
            entry = new_entries[key]
            require((entry["repo"], entry["sha"]) in allowed, "lock pin is not the native intended upgrade")
            official = api(f"repos/{upgrade_family('actions', entry['repo'])}/commits/{quote(entry['version'], safe='')}")
            require(isinstance(official, dict) and official.get("sha") == entry["sha"],
                    "action lock release is not the official intended commit")


def changes(path, before, after, *, strict=True, cascade=False):
    ecosystem, old = version_map(path, before)
    _, new = version_map(path, after)
    if not ecosystem:
        return []
    changed = sorted((key for key in old.keys() | new.keys() if old.get(key) != new.get(key)), key=repr)
    lock_graph = ecosystem == "npm" and PurePosixPath(path).name in ("package-lock.json", "npm-shrinkwrap.json")
    require(cascade and (ecosystem == "go" or lock_graph)
            or all(key in old and key in new for key in changed),
            "added/removed dependency is not a version upgrade")
    if strict and ecosystem == "go":
        # No module, toolchain, replace, or other executable/configuration change.
        def mask(text):
            if cascade:
                preserved = []
                in_require = False
                entry = r"[A-Za-z0-9._~+/-]+\s+v[0-9][A-Za-z0-9.+_-]*(?:\s*//.*)?"
                for line in text.splitlines():
                    value = line.strip()
                    if re.fullmatch(r"require\s*\(", value):
                        require(not in_require, "nested Go require block")
                        in_require = True
                    elif in_require and value == ")":
                        in_require = False
                    elif in_require:
                        require(not value or value.startswith("//") or re.fullmatch(entry, value),
                                "invalid Go require entry")
                    elif not re.fullmatch(r"require\s+" + entry, value) and value:
                        preserved.append(value)
                require(not in_require, "unterminated Go require block")
                return preserved
            return re.sub(r"(?m)^(\s*(?:require\s+)?[A-Za-z0-9._~+/-]+\s+)v[0-9][A-Za-z0-9.+_-]*(\s*(?://.*)?)$", r"\1<VERSION>\2", text)
        require(mask(before) == mask(after), "non-version Go manifest change")
    elif strict and PurePosixPath(path).name == "package.json":
        old_doc, new_doc = json.loads(before), json.loads(after)
        for key in changed:
            for document in (old_doc, new_doc):
                node = document
                for part in key[:-1]:
                    node = node[part]
                node[key[-1]] = "<VERSION>"
        require(old_doc == new_doc, "non-dependency npm manifest change")
    elif strict and ecosystem == "actions":
        old_lines, new_lines = before.splitlines(), after.splitlines()
        require(len(old_lines) == len(new_lines), "action source layout changed")
        for index, package in changed:
            require(re.fullmatch(r"[0-9a-f]{40}", new[(index, package)]) is not None,
                    "Actions targets must be full immutable SHAs")
            old_lines[index] = old_lines[index].replace("@" + old[(index, package)], "@<PIN>", 1)
            new_lines[index] = new_lines[index].replace("@" + new[(index, package)], "@<PIN>", 1)
            # Version comments are metadata, not behavior.
            old_lines[index] = old_lines[index].split("#", 1)[0].rstrip()
            new_lines[index] = new_lines[index].split("#", 1)[0].rstrip()
        if path.endswith(".lock.yml"):
            pins = {}
            for (_, package), pin in new.items():
                pins.setdefault(package, set()).add(pin)
            normalize_compiler_headers(old_lines, new_lines, pins)
        require(old_lines == new_lines, "non-pin Actions source change")
    result = []
    for key in changed:
        package = (key[-1] if ecosystem == "npm" else key[1]) if isinstance(key, tuple) else key
        if ecosystem == "npm":
            package = re.sub(r"(?<!^)@.*$", "", package)
        if PurePosixPath(path).name in ("package-lock.json", "npm-shrinkwrap.json"):
            package = key.rsplit("node_modules/", 1)[1]
        direct = ecosystem != "go" or any(
            re.fullmatch(r"\s*(?:require\s+)?" + re.escape(package) + r"\s+v[0-9]\S*\s*", line)
            for line in (after if new.get(key) is not None else before).splitlines())
        result.append({"ecosystem": ecosystem, "package": package, "old": old.get(key),
                       "target": new.get(key), "manifest": path, "direct": direct,
                       "key": list(key) if isinstance(key, tuple) else key})
    return result


def source_intent(repo, snap):
    pull = snap["pull"]
    comparison = api(f"repos/{repo}/compare/{pull['base']['sha']}...{pull['head']['sha']}")
    base = comparison.get("merge_base_commit", {}).get("sha") if isinstance(comparison, dict) else None
    require(isinstance(base, str) and SHA.fullmatch(base), "missing immutable merge base")
    intents = []
    ancillary = []
    documents = {}
    for file in snap["files"]:
        path = safe_path(file.get("filename"))
        require(file.get("status") == "modified" and not file.get("previous_filename"),
                "native file additions/removals/renames need manual diagnosis")
        if PurePosixPath(path).name in ("go.sum", "go.work.sum"):
            ancillary.append(("go", path))
            continue
        ecosystem, _ = version_map(path, "{}" if path.endswith("package.json") else "") if not path.endswith(("package-lock.json", "npm-shrinkwrap.json")) else ("npm", {})
        require(ecosystem is not None, "unsupported/protected native source path: " + path)
        before, after = content(repo, base, path), content(repo, pull["head"]["sha"], path)
        documents[path] = (before, after)
        intents += changes(path, before, after,
                           cascade=ecosystem == "go" or PurePosixPath(path).name in ("package-lock.json", "npm-shrinkwrap.json"))
    require(bool(intents), "no verifiable dependency version upgrade")
    ecosystems = {item["ecosystem"] for item in intents}
    require(len(ecosystems) == 1 and all(kind in ecosystems for kind, _ in ancillary), "mixed dependency ecosystems")
    for _, path in ancillary:
        before = content(repo, base, path).splitlines()
        after = content(repo, pull["head"]["sha"], path).splitlines()
        require(all(re.fullmatch(r"\S+ v[0-9]\S* h1:[A-Za-z0-9+/]+=*", line)
                    for line in before + after), "malformed Go checksum evidence")
    primary = [item for item in intents if item["direct"] and not item["manifest"].endswith(
        ("package-lock.json", "npm-shrinkwrap.json", ".lock.yml"))]
    if ecosystems == {"npm"}:
        lock_paths = sorted({item["manifest"] for item in intents if item["manifest"].endswith(
            ("package-lock.json", "npm-shrinkwrap.json"))})
        if primary:
            roots = {PurePosixPath(item["manifest"]).parent for item in primary}
            require(all(PurePosixPath(path).parent in roots for path in lock_paths),
                    "native lock graph belongs to an unrelated manifest consumer")
            # A regenerated transitive graph is solver output, not additional
            # native upgrade intent. Retain primary family floors and require
            # compatible resolved consumers, never historical graph equality.
            for item in primary:
                item["lock_manifests"] = [path for path in lock_paths
                                          if PurePosixPath(path).parent == PurePosixPath(item["manifest"]).parent]
            intents = primary
        else:
            require(all(item["old"] is not None and item["target"] is not None for item in intents),
                    "lock-only graph additions/removals lack primary upgrade evidence")
    ecosystem = next(iter(ecosystems))
    families = {upgrade_family(ecosystem, item["package"]) for item in primary or intents}
    require(len(families) == 1, "multiple upgrade families require manual diagnosis")
    family = next(iter(families))
    if ecosystem == "npm" and primary:
        primary_packages = {item["package"] for item in primary}
        for lock_path in lock_paths:
            manifest_path = str(PurePosixPath(lock_path).with_name("package.json"))
            require(manifest_path in documents, "native lock lacks immutable root manifest evidence")
            root = npm_versions(documents[manifest_path][1])
            old_lock, new_lock = (lock_versions(json.loads(text)) for text in documents[lock_path])
            root_packages = {key[1] for key in root if len(key) == 2 and key[0] != "overrides"}
            for package in sorted(root_packages - primary_packages):
                key = "node_modules/" + package
                old, new = old_lock.get(key), new_lock.get(key)
                if old is not None and new is not None and old[1] != new[1] and upgrade_family("npm", package) == family:
                    intents.append({"ecosystem": "npm", "package": package, "old": old[1], "target": new[1],
                                    "manifest": lock_path, "direct": False, "key": key})
    targets = {}
    for item in primary or intents:
        require(item["package"] not in targets or targets[item["package"]] == item["target"],
                "conflicting native dependency targets")
        targets[item["package"]] = item["target"]
    target = next(iter(set(targets.values()))) if len(set(targets.values())) == 1 else json.dumps(targets, sort_keys=True)
    group = hashlib.sha256(f"{ecosystem}:{family}".encode()).hexdigest()[:24]
    if ecosystem == "actions":
        for package, pin in sorted(targets.items()):
            require(isinstance(pin, str) and SHA.fullmatch(pin), "Actions target is not immutable")
            official_action_pin(upgrade_family("actions", package), pin)
    return {"group": group, "ecosystem": ecosystem, "family": family, "target": target,
            "intents": intents, "number": pull["number"], "head_sha": pull["head"]["sha"]}


def official_action_pin(repo, pin):
    tags = unique(rows(f"repos/{repo}/tags?per_page=100"), "name")
    require(all(isinstance(tag.get("name"), str) and isinstance(tag.get("commit"), dict)
                and isinstance(tag["commit"].get("sha"), str)
                and SHA.fullmatch(tag["commit"]["sha"]) for tag in tags),
            "malformed official action tag inventory")
    require(any(tag["commit"]["sha"] == pin
                and re.fullmatch(r"v?\d+\.\d+\.\d+(?:[-+][A-Za-z0-9_.-]+)?", tag["name"])
                for tag in tags), "Actions target has no official immutable release-tag evidence")


def upgrade_family(ecosystem, package):
    if ecosystem == "actions":
        return "/".join(package.split("/")[:2])
    if ecosystem == "npm" and (package.startswith("@typespec/") or package.startswith("@azure-tools/typespec-")):
        return "typespec"
    if ecosystem == "go" and package.startswith("github.com/Azure/azure-sdk-for-go/"):
        return "github.com/Azure/azure-sdk-for-go"
    return package


def metadata(body):
    require(isinstance(body, str), "missing replacement body")
    matches = re.findall(r"^<!-- " + MARKER + r"(\{[^\n]*\}) -->$", body, re.MULTILINE)
    require(len(matches) == 1, "replacement must have exactly one ownership marker")
    value = json.loads(matches[0])
    require(isinstance(value, dict) and set(value) == {"group", "sources"}
            and isinstance(value["group"], str) and re.fullmatch(r"[0-9a-f]{24}", value["group"]),
            "malformed replacement ownership marker")
    validate_sources(value["sources"])
    return value


def validate_sources(sources):
    require(isinstance(sources, list) and 0 < len(sources) <= 100, "missing source PRs")
    numbers = set()
    for source in sources:
        require(isinstance(source, dict) and set(source) == {"number", "head_sha"}, "invalid source schema")
        require(type(source["number"]) is int, "source number must be integer")
        maintenance.validate_target("Azure/ARO-HCP", source["number"], source["head_sha"])
        require(source["number"] not in numbers, "duplicate source PR")
        numbers.add(source["number"])
    require(sources == sorted(sources, key=lambda row: row["number"]), "source PRs must be sorted")


def replacement(pull, repo, *, merged=False, publication_window=False):
    identity(pull, repo, ROBOT, merged=merged)
    meta = metadata(pull.get("body"))
    require((publication_window or LABEL in {label["name"] for label in pull["labels"]})
            and pull.get("title", "").startswith("chore(deps): ")
            and pull["head"]["ref"] == "native-dependabot-" + meta["group"], "unverified replacement identity")
    return meta


def creator_inventory(repo, creator, state="open"):
    require(state in ("open", "all"), "unsupported inventory state")
    issues = unique(rows(f"repos/{repo}/issues?state={state}&creator={quote(creator, safe='')}&per_page=100"), "number")
    result = []
    for issue in issues:
        require(issue.get("user", {}).get("login") == creator and issue.get("state") in (("open", "closed") if state == "all" else ("open",)),
                "creator-filtered inventory returned foreign evidence")
        if "pull_request" not in issue:
            continue
        if creator == ROBOT:
            body, labels = issue.get("body"), issue.get("labels")
            require((body is None or isinstance(body, str)) and isinstance(labels, list),
                    "incomplete robot publication metadata")
            if MARKER not in (body or "") and not any(
                isinstance(label, dict) and label.get("name") == LABEL for label in labels
            ):
                continue
        result.append(pull_detail(repo, issue["number"]))
    return result


def inventory(repo):
    validate_repo(repo)
    base = main_sha(repo)
    groups = {}
    for pull in creator_inventory(repo, NATIVE):
        row = {"number": pull["number"], "head_sha": pull.get("head", {}).get("sha")}
        try:
            snap = snapshot(repo, row["number"], row["head_sha"])
            intent = source_intent(repo, snap)
            row.update(intent)
            row["ci"] = snap["ci"]
            row["held"] = snap["held"]
            row["blocked_holds"] = snap["blocked_holds"]
            key = intent["group"] + ":" + intent["target"]
            group = groups.setdefault(key, {key: intent[key] for key in ("group", "ecosystem", "family", "target")})
            group.setdefault("sources", []).append(row)
            group.setdefault("blockers", [])
            if snap["blocked_holds"]:
                group["blockers"].append(f"PR #{row['number']}: human/unknown hold")
        except ValueError as error:
            groups[f"blocked:{pull['number']}"] = {"group": None, "sources": [row], "blockers": [str(error)]}
    replacements = []
    for pull in creator_inventory(repo, ROBOT, state="all"):
        body = pull.get("body") or ""
        labels = {label.get("name") for label in pull.get("labels", [])}
        if MARKER not in body and LABEL not in labels:
            continue
        if pull.get("state") == "closed" and pull.get("merged") is False:
            continue
        merged = pull.get("merged") is True
        meta = replacement(pull, repo, merged=merged, publication_window=not merged)
        row = {"number": pull["number"], "head_sha": pull["head"]["sha"], "merged": merged,
               "group": meta["group"], "sources": meta["sources"], "url": pull.get("html_url")}
        row["publication_pending"] = LABEL not in {label["name"] for label in pull["labels"]}
        if not merged:
            snap = snapshot(repo, pull["number"], pull["head"]["sha"], author=ROBOT)
            row.update({key: snap[key] for key in ("ci", "held", "blocked_holds")})
        replacements.append(row)
    require(main_sha(repo) == base, "main changed during inventory")
    return {"repository": repo, "base_sha": base, "groups": list(groups.values()), "replacements": replacements}


def read_request(path, kind):
    output = json.loads(Path(path).read_text())
    require(isinstance(output, dict) and isinstance(output.get("items"), list), "invalid global agent output")
    require(isinstance(output.get("errors", []), list) and not output.get("errors", []),
            "agent output contains errors")
    items = output["items"]
    require(all(isinstance(item, dict) and isinstance(item.get("type"), str) for item in items), "malformed output item")
    native_items = [item for item in items if item["type"] in ("native_replacement", "native_action")]
    require(len(native_items) <= 1, "only one native output kind/item may run per workflow")
    selected = [item for item in items if item["type"] == kind]
    require(len(selected) <= 1, "native safe output exceeds max 1")
    if not selected:
        return None
    item = selected[0]
    require(set(item) in ({"type", "request"}, {"type", "data"}), "ambiguous native output envelope")
    data = item.get("data") if "data" in item else {key: value for key, value in item.items() if key != "type"}
    require(isinstance(data, dict) and set(data) == {"request"} and isinstance(data["request"], str), "invalid native safe-output envelope")
    request = json.loads(data["request"])
    require(isinstance(request, dict), "native request must be JSON object")
    return request


def git(cwd, *args, input=None, author_email=None):
    env = os.environ.copy()
    env.update({"GIT_CONFIG_NOSYSTEM": "1", "GIT_CONFIG_GLOBAL": os.devnull,
                "GIT_TERMINAL_PROMPT": "0",
                "GIT_AUTHOR_NAME": "aro-hcp-robot[bot]", "GIT_AUTHOR_EMAIL": "aro-hcp-robot[bot]@users.noreply.github.com",
                "GIT_COMMITTER_NAME": "aro-hcp-robot[bot]", "GIT_COMMITTER_EMAIL": "aro-hcp-robot[bot]@users.noreply.github.com"})
    if author_email:
        env["GIT_AUTHOR_EMAIL"] = env["GIT_COMMITTER_EMAIL"] = author_email
    return subprocess.run(["git", "-c", "core.hooksPath=/dev/null", "-c", "core.fsmonitor=false",
                           "-c", "credential.helper=", "-c", "credential.https://github.com.helper=!gh auth git-credential", *args],
                          cwd=cwd, input=input, text=True, capture_output=True, check=True, env=env).stdout.strip()


def remote_url(repo):
    url = git(None, "remote", "get-url", "origin")
    require(url in (f"https://github.com/{repo}", f"https://github.com/{repo}.git",
                    f"git@github.com:{repo}.git"), "publication origin is not the exact repository")
    return f"https://github.com/{repo}.git"


def patch_paths(patch):
    require(isinstance(patch, str) and 0 < len(patch.encode()) <= MAX_PATCH and "\x00" not in patch,
            "patch must be nonempty UTF-8 and at most 16 MiB")
    headers = re.findall(r"^diff --git a/(\S+) b/(\S+)$", patch, re.MULTILINE)
    require(bool(headers) and patch.startswith("diff --git ") and len(headers) == patch.count("\ndiff --git ") + 1,
            "malformed patch file headers")
    require(all(old == new for old, new in headers), "renamed paths not allowed")
    paths = [safe_path(old) for old, _ in headers]
    require(len(set(paths)) == len(paths), "duplicate patch paths")
    require(not re.search(r"^(?:old mode|new mode|rename |copy |GIT binary patch|Binary files)", patch, re.MULTILINE),
            "mode changes, renames and binary patches are forbidden")
    return paths


def sdk_consumer(path):
    return re.fullmatch(r"test/sdk/v[0-9]+(?:preview)?/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp/.+", path) is not None


def validate_sdk_module(path, before, after, workspace_go):
    expected = "github.com/Azure/ARO-HCP/" + str(PurePosixPath(path).parent)
    allowed = {"github.com/Azure/azure-sdk-for-go/sdk/azcore",
               "github.com/Azure/azure-sdk-for-go/sdk/internal", "golang.org/x/net", "golang.org/x/text"}
    def parse(text):
        module = []
        versions = []
        in_require = False
        for raw in text.splitlines():
            line = raw.split("//", 1)[0].strip()
            if not line:
                continue
            if line.startswith("module "):
                module.append(line[7:].strip())
            elif line.startswith("go "):
                versions.append(line[3:].strip())
            elif line == "require (":
                require(not in_require, "nested SDK requirements")
                in_require = True
            elif line == ")":
                require(in_require, "unmatched SDK requirement block")
                in_require = False
            else:
                require(re.fullmatch(r"(?:require\s+)?[A-Za-z0-9._~+/-]+\s+v[0-9][A-Za-z0-9.+_-]*", line)
                        and (in_require or line.startswith("require ")),
                        "SDK module contains unsupported source/toolchain/replace configuration")
        require(not in_require and module == [expected] and len(versions) == 1,
                "SDK generated module identity is not exact")
        require(re.fullmatch(r"1\.\d+(?:\.\d+)?", versions[0]), "unsupported generated SDK Go version")
        return go_versions(text), tuple(map(int, (versions[0] + ".0").split(".")[:3]))
    new, version = parse(after)
    limit = tuple(map(int, (workspace_go + ".0").split(".")[:3]))
    require(version <= limit, "SDK generator requires an unvalidated newer Go toolchain")
    old, old_version = parse(before) if before else ({}, version)
    require(version >= old_version, "SDK generator downgrades its Go requirement")
    require(all(package in allowed or old.get(package) == target for package, target in new.items()),
            "SDK metadata introduces an unrelated dependency upgrade")
    require(all(package not in new or version_covers(new[package], target)
                for package, target in old.items()), "SDK metadata downgrades an existing dependency")



def validate_tree(cwd, base, tree, paths, group):
    actual = git(cwd, "diff", "--name-only", base, tree).splitlines()
    require(sorted(actual) == sorted(paths), "patch path list does not match resulting tree")
    ecosystem = group["ecosystem"]
    manifests = []
    for path in paths:
        modes = [git(cwd, "ls-tree", ref, "--", path).split(" ", 1)[0] for ref in (base, tree)]
        generated = (ecosystem == "go" and path.startswith(VENDORS)
                     or ecosystem == "npm" and group["family"] == "typespec"
                     and (path.startswith("internal/azureapi/") and "/generated/" in path
                          or sdk_consumer(path)))
        require(all(mode in ("100644", "") for mode in modes) and "100644" in modes
                and (all(modes) or generated),
                "only regular files; additions/removals limited to generated consumers")
        name = PurePosixPath(path).name
        allowed = False
        if ecosystem == "go":
            allowed = name in ("go.mod", "go.sum", "go.work.sum") or path.startswith(VENDORS)
        elif ecosystem == "npm":
            allowed = name in ("package.json", "package-lock.json", "npm-shrinkwrap.json")
            allowed |= group["family"] == "typespec" and (path == "api/Makefile"
                or path.startswith("api/redhatopenshift/") and path.endswith((".tsp", "/openapi.json"))
                or path.startswith("internal/azureapi/") and "/generated/" in path and path.endswith(".go")
                or sdk_consumer(path) and (path.endswith(".go") or path.endswith("/testdata/_metadata.json")
                                           or name in ("go.mod", "go.sum", "LICENSE", "LICENSE.txt")))
        elif ecosystem == "actions":
            allowed = (path.startswith(".github/workflows/") and path.endswith((".md", ".yml", ".yaml"))
                       or path == ".github/aw/actions-lock.json")
        require(allowed, "protected or unrelated replacement path: " + path)
        if path == ".github/aw/actions-lock.json":
            action_lock_changes(git(cwd, "show", f"{base}:{path}"), git(cwd, "show", f"{tree}:{path}"), group)
        elif ecosystem == "npm" and group["family"] == "typespec" and sdk_consumer(path) and name == "go.mod":
            require(modes[1] == "100644", "generated SDK module metadata cannot be deleted")
            before = git(cwd, "show", f"{base}:{path}") if modes[0] else ""
            workspace = git(cwd, "show", f"{tree}:go.work")
            go = re.findall(r"(?m)^go (1\.\d+(?:\.\d+)?)$", workspace)
            require(len(go) == 1, "missing workspace Go toolchain")
            validate_sdk_module(path, before, git(cwd, "show", f"{tree}:{path}"), go[0])
        elif ecosystem == "npm" and sdk_consumer(path) and name == "go.sum" and modes[1]:
            require(all(re.fullmatch(r"\S+ v[0-9]\S* h1:[A-Za-z0-9+/]+=*", line)
                        for line in git(cwd, "show", f"{tree}:{path}").splitlines()),
                    "malformed generated SDK checksum metadata")
        elif name in ("go.mod", "package.json", "package-lock.json", "npm-shrinkwrap.json") or ecosystem == "actions":
            before, after = (git(cwd, "show", f"{ref}:{path}") for ref in (base, tree))
            entries = changes(path, before, after, strict=True,
                              cascade=ecosystem == "go" or name in ("package-lock.json", "npm-shrinkwrap.json"))
            if ecosystem == "go":
                require(all(item["target"] is not None and (item["old"] is None
                            or version_covers(item["target"], item["old"]))
                            for item in entries if upgrade_family("go", item["package"]) == group["family"]),
                        "Go replacement downgrades/removes a preserved current-base family requirement")
            if ecosystem in ("actions", "npm") and name not in ("package-lock.json", "npm-shrinkwrap.json"):
                require(all(upgrade_family(ecosystem, item["package"]) == group["family"] for item in entries),
                        "replacement changes an unrelated dependency family")
            manifests += entries
    require(any(upgrade_family(ecosystem, item["package"]) == group["family"] for item in manifests),
            "replacement lacks the actual source upgrade family/target")
    if ecosystem == "npm":
        roots = {str(PurePosixPath(item["manifest"]).parent) for item in manifests}
        require(all(str(PurePosixPath(path).parent) in roots for path in paths
                    if PurePosixPath(path).name in ("package.json", "package-lock.json", "npm-shrinkwrap.json")),
                "npm patch changes unrelated consumer roots")
        for root in roots:
            manifest_path = root + "/package.json" if root != "." else "package.json"
            lock_path = root + "/package-lock.json" if root != "." else "package-lock.json"
            base_files = set(git(cwd, "ls-tree", "-r", "--name-only", base).splitlines())
            if lock_path in base_files:
                old_lock = lock_versions(json.loads(git(cwd, "show", f"{base}:{lock_path}")))
                new_lock = lock_versions(json.loads(git(cwd, "show", f"{tree}:{lock_path}")))
                preserve_npm_resolutions(old_lock, new_lock)
                if manifest_path in base_files:
                    validate_npm_overrides(git(cwd, "show", f"{tree}:{manifest_path}"),
                                           json.loads(git(cwd, "show", f"{tree}:{lock_path}")))
    available = set(git(cwd, "ls-tree", "-r", "--name-only", tree).splitlines())
    if ecosystem == "go":
        family_targets = {item["package"]: item["target"] for source in group["sources"]
                          for item in source["intents"] if item["target"] is not None
                          and upgrade_family("go", item["package"]) == group["family"]}
        for path in sorted(available):
            if path.endswith("go.mod") and not path.startswith(VENDORS):
                versions = go_versions(git(cwd, "show", f"{tree}:{path}"))
                require(all(package not in versions or version_covers(versions[package], target)
                            for package, target in family_targets.items()),
                        "Go consumer still selects an older family version: " + path)
        for prefix in VENDORS:
            if not any(path.startswith(prefix) for path in available):
                continue
            module_file = prefix + "modules.txt"
            require(module_file in available, "vendor consumer lacks module provenance")
            text = git(cwd, "show", f"{tree}:{module_file}")
            versions = dict(re.findall(r"(?m)^# (\S+) (v[0-9]\S*)(?: => .*)?$", text))
            require(all(package not in versions or version_covers(versions[package], target)
                        for package, target in family_targets.items()),
                    "vendor consumer still selects an older family version: " + module_file)
            if git(cwd, "ls-tree", base, "--", module_file):
                old_text = git(cwd, "show", f"{base}:{module_file}")
                old_versions = dict(re.findall(r"(?m)^# (\S+) (v[0-9]\S*)(?: => .*)?$", old_text))
                require(all(package in versions and version_covers(versions[package], target)
                            for package, target in old_versions.items()
                            if upgrade_family("go", package) == group["family"]),
                        "vendor replacement downgrades/removes a current-base family resolution")
    for source in group["sources"]:
        verify_coverage(source["intents"], lambda path: git(cwd, "show", f"{tree}:{path}"), available)


def version_covers(actual, target):
    if actual is None or target is None:
        return actual is None and target is None
    if not isinstance(actual, str) or not isinstance(target, str):
        return False
    pattern = r"v(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?(?:\+[0-9A-Za-z.-]+)?"
    left, right = re.fullmatch(pattern, actual), re.fullmatch(pattern, target)
    if left is None or right is None:
        return False
    if any(part.isdigit() and len(part) > 1 and part.startswith("0")
           for match in (left, right) for part in (match[4] or "").split(".")):
        return False
    left_release, right_release = tuple(map(int, left.groups()[:3])), tuple(map(int, right.groups()[:3]))
    if left_release != right_release:
        return left_release > right_release
    left_pre, right_pre = left[4], right[4]
    if right_pre is None:
        return left_pre is None
    if left_pre is None:
        return True
    left_parts, right_parts = left_pre.split("."), right_pre.split(".")
    for a, b in zip(left_parts, right_parts):
        if a == b:
            continue
        if a.isdigit() and b.isdigit():
            return int(a) > int(b)
        if a.isdigit() != b.isdigit():
            return not a.isdigit()
        return a > b
    return len(left_parts) >= len(right_parts)


def npm_version(value):
    if not isinstance(value, str):
        return None
    match = re.fullmatch(r"([~^]?)(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)", value)
    return (match[1], tuple(map(int, match.groups()[1:]))) if match else None


def npm_covers(actual, target, *, resolved=False):
    left, right = npm_version(actual), npm_version(target)
    if left is None or right is None:
        return False
    op, version = left
    target_op, floor = right
    if version[0] != floor[0] or version < floor:
        return False
    if floor[0] == 0 and version[1] != floor[1]:
        return False
    if target_op == "~" and version[1] != floor[1]:
        return False
    return (not op if resolved else op == target_op or not op or target_op == "^" and op == "~")


def override_selector(value, version):
    match = re.fullmatch(r"(@[^/]+/[^@]+|[^@]+)(?:@(.+))?", value)
    require(match is not None, "invalid npm override selector")
    package, selector = match.groups()
    if selector is None:
        return package, True
    actual = npm_version(version)
    require(actual is not None and not actual[0], "unverifiable overridden npm version")
    constraint = re.fullmatch(r"(\^|~|>=)?(\d+)(?:\.(\d+|x|\*))?(?:\.(\d+|x|\*))?(?:-[A-Za-z0-9_.-]+)?", selector)
    require(constraint is not None, "unsupported npm override selector range")
    operator, major, minor, patch = constraint.groups()
    floor = (int(major), int(minor) if minor not in (None, "x", "*") else 0,
             int(patch) if patch not in (None, "x", "*") else 0)
    current = actual[1]
    if operator == ">=":
        return package, current >= floor
    if operator == "^":
        return package, current >= floor and current[0] == floor[0] and (floor[0] != 0 or current[1] == floor[1])
    if operator == "~":
        return package, current >= floor and current[:2] == floor[:2]
    return package, current[0] == floor[0] and (minor in (None, "x", "*") or current[1] == floor[1]) \
        and (patch in (None, "x", "*") or current[2] == floor[2])


def validate_npm_overrides(manifest_text, lock_document):
    """Apply each preserved override only to its selected package/parent scope."""
    manifest = npm_versions(manifest_text)
    resolved = lock_versions(lock_document)
    def child(parent, package):
        scope = parent
        while scope:
            candidate = scope + "/node_modules/" + package
            if candidate in resolved:
                return candidate
            scope = scope.rsplit("/node_modules/", 1)[0] if "/node_modules/" in scope else ""
        candidate = "node_modules/" + package
        return candidate if candidate in resolved else None
    for key, floor in manifest.items():
        if key[0] != "overrides":
            continue
        if floor.startswith("$"):
            reference = floor[1:]
            choices = [manifest[(section, reference)] for section in
                       ("dependencies", "devDependencies", "optionalDependencies")
                       if (section, reference) in manifest]
            require(bool(choices) and len(set(choices)) == 1, "unresolved npm override dependency reference")
            floor = choices[0]
        contexts = [None]
        for selector in key[1:]:
            selected = set()
            for parent in contexts:
                if selector == ".":
                    require(parent is not None, "root self override has no parent package")
                    selected.add(parent)
                    continue
                package = re.split(r"(?<!^)@", selector, maxsplit=1)[0]
                candidates = [path for path, entry in resolved.items() if entry[0] == package] if parent is None \
                    else [child(parent, package)]
                for path in candidates:
                    if path is not None and override_selector(selector, resolved[path][1])[1]:
                        selected.add(path)
            contexts = sorted(selected)
        def satisfies(path):
            actual = resolved[path][1]
            if len(key) > 2:
                return npm_covers(actual, floor, resolved=True)
            left, right = npm_version(actual), npm_version(floor)
            # Global security floors do not force every existing transitive
            # major into one compatibility range (e.g. minimatch v3 and v10).
            return bool(left and right and not left[0] and left[1] >= right[1])
        require(all(satisfies(path) for path in contexts),
                "npm lock violates preserved security override scope: " + "/".join(key[1:]))


def preserve_npm_resolutions(old, new):
    for path in old.keys() & new.keys():
        old_package, old_version, _, _ = old[path]
        new_package, new_version, _, _ = new[path]
        require(old_package == new_package, "npm lock path changed package identity")
        if old_version == new_version:
            continue
        left, right = npm_version(old_version), npm_version(new_version)
        require(left is not None and right is not None and right[1] >= left[1],
                "npm regeneration downgrades an existing resolution or has unverifiable versions")


def verify_coverage(intents, reader, available=None):
    require(bool(intents), "missing coverage intent")
    for item in intents:
        paths = [item["manifest"]]
        if item["ecosystem"] == "actions" and available is not None and paths[0].endswith(".md"):
            lock = paths[0][:-3] + ".lock.yml"
            require(lock in available, "live action source lacks compiled consumer coverage")
            paths.append(lock)
        if item["ecosystem"] == "actions" and available is not None and paths[0] not in available:
            stem = re.sub(r"\.(?:yml|yaml)$", "", paths[0])
            paths = [stem + ".md", stem + ".lock.yml"]
            require(all(path in available for path in paths),
                    "obsolete workflow has no live source and compiled replacement")
        for path in paths:
            _, versions = version_map(path, reader(path))
            key = tuple(item["key"]) if isinstance(item["key"], list) else item["key"]
            if item["ecosystem"] == "actions":
                pins = [value for (_, package), value in versions.items() if package == item["package"]]
                require(bool(pins) and all(pin == item["target"] for pin in pins),
                        "live Actions source/lock does not cover target: " + path)
            else:
                require(version_covers(versions.get(key), item["target"]) if item["ecosystem"] == "go"
                        else npm_covers(versions.get(key), item["target"],
                                        resolved=PurePosixPath(path).name != "package.json"),
                        "upgrade intent is not covered: " + path + " " + item["package"])
        if item["ecosystem"] == "npm" and PurePosixPath(item["manifest"]).name == "package.json":
            lock_paths = set(item.get("lock_manifests", []))
            if available is not None:
                parent = PurePosixPath(item["manifest"]).parent
                lock_paths.update(str(parent / name) for name in ("package-lock.json", "npm-shrinkwrap.json")
                                  if str(parent / name) in available)
            for lock_path in sorted(lock_paths):
                resolutions = lock_versions(json.loads(reader(lock_path)))
                resolved = resolutions.get("node_modules/" + item["package"])
                require(resolved is not None and npm_covers(resolved[1], item["target"], resolved=True),
                        "npm primary family lacks compatible lock resolution: " + lock_path + " " + item["package"])


def coverage_paths(intents):
    paths = set()
    for item in intents:
        path = item["manifest"]
        paths.add(path)
        paths.update(item.get("lock_manifests", []))
        if item["ecosystem"] == "npm" and PurePosixPath(path).name == "package.json":
            paths.update(str(PurePosixPath(path).with_name(name))
                         for name in ("package-lock.json", "npm-shrinkwrap.json"))
        if item["ecosystem"] == "actions":
            stem = path[:-9] if path.endswith(".lock.yml") else re.sub(r"\.(?:md|yml|yaml)$", "", path)
            paths.update((stem + ".md", stem + ".lock.yml"))
    return paths


def tree_paths(repo, sha, wanted, cache=None):
    """Prove only requested paths through complete non-recursive directory trees."""
    cache = {} if cache is None else cache
    found = set()
    for path in sorted(wanted):
        parts = safe_path(path).split("/")
        tree_sha = sha
        for index, part in enumerate(parts):
            key = (repo, tree_sha)
            if key not in cache:
                tree = api(f"repos/{repo}/git/trees/{tree_sha}")
                require(isinstance(tree, dict) and tree.get("truncated") is False
                        and isinstance(tree.get("tree"), list), "incomplete targeted immutable tree")
                entries = unique(tree["tree"], "path")
                require(all(isinstance(item.get("path"), str) and "/" not in item["path"]
                            and isinstance(item.get("sha"), str) and SHA.fullmatch(item["sha"])
                            for item in entries), "malformed targeted immutable tree")
                cache[key] = {item["path"]: item for item in entries}
            entry = cache[key].get(part)
            if entry is None:
                break
            if index == len(parts) - 1:
                require(entry.get("type") == "blob" and entry.get("mode") == "100644",
                        "coverage manifest is not a regular immutable file")
                found.add(path)
            else:
                require(entry.get("type") == "tree" and entry.get("mode") == "040000",
                        "coverage path crosses a non-directory")
                tree_sha = entry["sha"]
    return found


def admission_safe(repo, snap, intent):
    """Admission is narrower than discovery; incomplete consumers need replacement."""
    require(intent["ecosystem"] != "go", "native Go workspace/vendor completeness requires a validated replacement")
    require(intent["family"] != "typespec", "TypeSpec generated consumers require a validated replacement")
    paths = {file["filename"] for file in snap["files"]}
    head = snap["pull"]["head"]["sha"]
    wanted = coverage_paths(intent["intents"])
    available = tree_paths(repo, head, wanted)
    if intent["ecosystem"] == "actions":
        live = tree_paths(repo, main_sha(repo), wanted)
        require(all(item["manifest"] in live for item in intent["intents"]),
                "obsolete native workflow cannot be admitted")
        for path in paths:
            if path.endswith(".md"):
                lock = path[:-3] + ".lock.yml"
                require(lock in paths, "native workflow source lacks regenerated compiler lock")
        verify_coverage(intent["intents"], lambda path: content(repo, head, path), available)
    else:
        for item in intent["intents"]:
            if PurePosixPath(item["manifest"]).name == "package.json":
                lock = str(PurePosixPath(item["manifest"]).with_name("package-lock.json"))
                require(lock in paths, "native npm manifest lacks updated lock consumer")
                resolved = lock_versions(json.loads(content(repo, head, lock)))
                resolution = resolved.get("node_modules/" + item["package"])
                require(resolution is not None and npm_covers(resolution[1], item["target"], resolved=True),
                        "native npm lock does not resolve intended target")


def safe_text(text, *, title=False):
    require(isinstance(text, str) and 0 < len(text) <= (200 if title else 20000), "invalid publication text")
    require(not re.search(r"(?m)^\s*/|@[A-Za-z0-9_-]+(?![A-Za-z0-9_/-])|(?i:\b(?:close[sd]?|fix(?:e[sd])?|resolve[sd]?)\s+#\d+)", text)
            and MARKER not in text and "<!-- gh-aw-workflow-id:" not in text,
            "publication text cannot contain commands, mentions, auto-close directives or ownership markers")
    if title:
        require(text.startswith("chore(deps): ") and "\n" not in text, "replacement title must be chore(deps): ...")


def sources_group(repo, sources):
    validate_sources(sources)
    intents = []
    for source in sources:
        snap = snapshot(repo, source["number"], source["head_sha"])
        require(not snap["blocked_holds"], "human/unknown native hold blocks publication")
        intents.append(source_intent(repo, snap))
    require(len({(row["group"], row["target"]) for row in intents}) == 1,
            "sources do not share one actual upgrade family/target")
    selected = {source["number"] for source in sources}
    current = creator_inventory(repo, NATIVE)
    require(selected <= {pull["number"] for pull in current},
            "native source inventory changed during authorization")
    for pull in current:
        if pull["number"] in selected:
            continue
        snap = snapshot(repo, pull["number"], pull["head"]["sha"])
        candidate = source_intent(repo, snap)
        require((candidate["group"], candidate["target"]) != (intents[0]["group"], intents[0]["target"]),
                f"replacement omits current same-family/target native source #{pull['number']}")
    return {**{key: intents[0][key] for key in ("group", "ecosystem", "family", "target")}, "sources": intents}


def post_comment(repo, number, body):
    response = api(f"repos/{repo}/issues/{number}/comments", method="POST", data={"body": body})
    require(isinstance(response, dict) and type(response.get("id")) is int and response.get("body") == body,
            "comment response incomplete; inspect before retry")
    return {"number": number, "comment_id": response["id"]}


def ensure_label(repo):
    """Create only this fixed repository label; tolerate a concurrent creator."""
    endpoint = f"repos/{repo}/labels/{LABEL}"
    try:
        value = api(endpoint)
    except subprocess.CalledProcessError as error:
        if "(HTTP 404)" not in (error.stderr or ""):
            raise
        try:
            api(f"repos/{repo}/labels", method="POST",
                data={"name": LABEL, "color": "0366d6",
                      "description": "Validated native Dependabot replacement maintained by gh-aw-recompile"})
        except subprocess.CalledProcessError as conflict:
            if "(HTTP 422)" not in (conflict.stderr or ""):
                raise
        value = api(endpoint)
    require(isinstance(value, dict) and value.get("name") == LABEL
            and type(value.get("id")) is int and value["id"] > 0,
            "fixed dependency-upgrade label could not be verified")


def publish(repo, request):
    require(set(request) == {"source_prs", "expected_base_sha", "patch", "title", "body"}, "invalid replacement schema")
    validate_repo(repo)
    maintenance.validate_target(repo, 1, request["expected_base_sha"])
    base = request["expected_base_sha"]
    require(main_sha(repo) == base, "main moved")
    group = sources_group(repo, request["source_prs"])
    safe_text(request["title"], title=True)
    safe_text(request["body"])
    paths = patch_paths(request["patch"])
    candidates = []
    merged_history = []
    closed_unmerged = []
    branch = "native-dependabot-" + group["group"]
    for pull in creator_inventory(repo, ROBOT, state="all"):
        if MARKER not in (pull.get("body") or "") and LABEL not in {label.get("name") for label in pull.get("labels", [])}:
            continue
        if pull.get("state") == "closed" and pull.get("merged") is False:
            if pull.get("head", {}).get("ref") == branch:
                closed_unmerged.append(pull)
            continue
        merged = pull.get("merged") is True
        meta = replacement(pull, repo, merged=merged, publication_window=not merged)
        if meta["group"] == group["group"]:
            (merged_history if merged else candidates).append(pull)
    require(len(candidates) <= 1, "multiple replacement PRs for group")
    existing = candidates[0] if candidates else None
    if existing:
        snap = snapshot(repo, existing["number"], existing["head"]["sha"], author=ROBOT)
        require(not snap["blocked_holds"], "human/unknown replacement hold blocks publication")
    marker = "<!-- " + MARKER + json.dumps({"group": group["group"], "sources": request["source_prs"]}, sort_keys=True, separators=(",", ":")) + " -->"
    body = request["body"] + "\n\n" + marker
    def merged_predecessor(head):
        require(not any(pull["head"]["sha"] == head for pull in closed_unmerged),
                "closed-unmerged replacement branch must not be revived")
        matches = [pull for pull in merged_history if pull["head"]["sha"] == head]
        require(len(matches) <= 1, "ambiguous merged replacement branch ownership")
        if not matches:
            return None
        prior = matches[0]
        replacement(prior, repo, merged=True)
        merge = prior.get("merge_commit_sha")
        require(isinstance(merge, str) and SHA.fullmatch(merge), "missing predecessor merge commit")
        comparison = api(f"repos/{repo}/compare/{merge}...{base}")
        require(isinstance(comparison, dict) and comparison.get("status") in ("ahead", "identical")
                and comparison.get("merge_base_commit", {}).get("sha") == merge,
                "previous replacement merge is not an ancestor of current main")
        return prior
    previous = None
    with tempfile.TemporaryDirectory(prefix="native-dependabot-") as directory:
        git(directory, "init", "--quiet")
        git(directory, "remote", "add", "origin", remote_url(repo))
        git(directory, "fetch", "--no-tags", "origin", "+refs/heads/main:refs/remotes/origin/main")
        require(git(directory, "rev-parse", "refs/remotes/origin/main") == base, "main moved")
        remote = git(directory, "ls-remote", "--heads", "origin", "refs/heads/" + branch)
        require(not existing or bool(remote), "verified replacement branch is missing")
        old = None
        if remote:
            old = remote.split()[0]
            require(not existing or old == existing["head"]["sha"], "replacement head moved")
            git(directory, "fetch", "--no-tags", "origin", "refs/heads/" + branch)
        git(directory, "read-tree", base)
        git(directory, "checkout-index", "--all", "--index")
        git(directory, "apply", "--index", "--whitespace=error", "-", input=request["patch"])
        tree = git(directory, "write-tree")
        validate_tree(directory, base, tree, paths, group)
        if old and not existing:
            previous = merged_predecessor(old)
        if previous:
            try:
                for source in group["sources"]:
                    verify_coverage(source["intents"], lambda path: git(directory, "show", f"{base}:{path}"),
                                    set(git(directory, "ls-tree", "-r", "--name-only", base).splitlines()))
            except ValueError:
                pass
            else:
                raise ValueError("merged replacement already covers current native intent; do not create another PR")
        if old and not existing and not previous:
            detail = api(f"repos/{repo}/commits/{old}")
            require(isinstance(detail, dict) and isinstance(detail.get("author"), dict)
                    and detail["author"].get("login") == ROBOT and detail["author"].get("type") == "Bot",
                    "orphan publication commit is not attributed to the robot")
            message = git(directory, "show", "-s", "--format=%B", old)
            parents = git(directory, "show", "-s", "--format=%P", old).split()
            if len(parents) == 2 and parents[1] == base:
                previous = merged_predecessor(parents[0])
            require(metadata(message) == {"group": group["group"], "sources": request["source_prs"]}
                    and f"native-dependabot-base: {base}" in message.splitlines()
                    and (parents == [base] or len(parents) == 2 and parents[1] == base and previous is not None)
                    and git(directory, "rev-parse", old + "^{tree}") == tree,
                    "orphan branch does not match exact authorized publication")
        unchanged = old and git(directory, "rev-parse", old + "^{tree}") == tree
        if unchanged:
            head = old
        else:
            parents = ["-p", old] if old else []
            if old != base:
                parents += ["-p", base]
            bot = api(f"users/{quote(ROBOT, safe='')}")
            require(isinstance(bot, dict) and bot.get("login") == ROBOT and bot.get("type") == "Bot"
                    and type(bot.get("id")) is int and bot["id"] > 0, "missing publishing App bot identity")
            message = request["title"] + "\n\n" + marker + f"\nnative-dependabot-base: {base}\n"
            head = git(directory, "commit-tree", tree, *parents, input=message,
                       author_email=f"{bot['id']}+{ROBOT}@users.noreply.github.com")
        ensure_label(repo)
        # Re-read the entire authorization after patch application, before publication.
        require(main_sha(repo) == base, "main moved before publication")
        require(sources_group(repo, request["source_prs"]) == group, "source intent changed")
        if existing:
            fresh = snapshot(repo, existing["number"], old, author=ROBOT)
            require(not fresh["blocked_holds"] and replacement(fresh["pull"], repo, publication_window=True)["group"] == group["group"],
                    "replacement authorization changed")
        if previous:
            fresh_previous = pull_detail(repo, previous["number"])
            require(fresh_previous == previous, "merged predecessor authorization changed")
            replacement(fresh_previous, repo, merged=True)
        require(git(directory, "ls-remote", "--heads", "origin", "refs/heads/main").split()[0] == base, "remote main moved")
        if not unchanged:
            # Normal push only: a concurrent update cannot be overwritten.
            git(directory, "push", "origin", head + ":refs/heads/" + branch)
    require(main_sha(repo) == base and sources_group(repo, request["source_prs"]) == group,
            "source or main changed before PR publication")
    if existing:
        response = api(f"repos/{repo}/pulls/{existing['number']}", method="PATCH",
                       data={"title": request["title"], "body": body})
    else:
        response = api(f"repos/{repo}/pulls", method="POST",
                       data={"head": branch, "base": "main", "title": request["title"], "body": body, "draft": False})
        require(isinstance(response, dict) and type(response.get("number")) is int, "missing created PR identity")
    number = response.get("number")
    require(type(number) is int, "missing published PR identity")
    if not existing or LABEL not in {label["name"] for label in existing["labels"]}:
        api(f"repos/{repo}/issues/{number}/labels", method="POST", data={"labels": [LABEL]})
    final = pull_detail(repo, number)
    require(final["head"]["sha"] == head and replacement(final, repo)["sources"] == request["source_prs"], "published replacement verification failed")
    # Prow may automatically request repository reviewers; this helper never requests any.
    return {"number": number, "head_sha": head, "group": group["group"], "reused": bool(existing), "unchanged": bool(unchanged)}


def recovery_body(snap, sha, evidence):
    require(isinstance(evidence, str) and 40 <= len(evidence) <= 4000, "diagnosed recovery evidence required")
    require(not re.search(r"^\s*/", evidence, re.MULTILINE) and MARKER not in evidence
            and maintenance.RECOVERY_MARKER not in evidence and not re.search(r"@[A-Za-z0-9]", evidence),
            "recovery evidence contains commands, mentions or markers")
    links = {url.rstrip(".;,") for url in re.findall(r"https://[^\s)<>]+", evidence)}
    failed = {item["url"] for item in snap["ci"]["blocked"] if item["url"]}
    trusted = re.compile(r"https://(?:prow\.ci\.openshift\.org|gcsweb-ci\.apps\.ci\.l2s4\.p1\.openshiftapps\.com|gcs\.ci\.openshift\.org|storage\.googleapis\.com|github\.com)/")
    require(bool(links & failed) and any(trusted.match(url) for url in links - failed),
            "recovery needs current failure and trusted comparison run URLs")
    marker = f"<!-- {MARKER}recovery:{sha} -->"
    require(not any(marker in row["body"] and row.get("user", {}).get("login") == ROBOT for row in snap["comments"]),
            "single recovery budget for head already consumed")
    require(not any(item["pending"] for item in snap["ci"]["blocked"]), "current CI pending")
    require(any(item["state"] in ("failure", "error", "timed_out", "cancelled", "action_required", "startup_failure") and item["url"] for item in snap["ci"]["blocked"]), "no evidenced current failure")
    require(all(item["state"] in ("failure", "error", "timed_out", "cancelled", "action_required", "startup_failure") for item in snap["ci"]["blocked"]), "unknown CI state")
    commands = "/hold cancel\n/retest-required" if snap["held"] else "/retest-required"
    return marker + "\n" + evidence + "\n\n" + commands


def reconcile(repo, request):
    require(set(request) <= {"number", "expected_head_sha", "action", "evidence", "replacement_pr"}
            and {"number", "expected_head_sha", "action"} <= set(request), "invalid action schema")
    number, sha, action = request["number"], request["expected_head_sha"], request["action"]
    maintenance.validate_target(repo, number, sha)
    require(type(number) is int and action in ACTIONS, "unsupported native action")
    if action == "close-superseded":
        require(type(request.get("replacement_pr")) is int and request["replacement_pr"] > 0
                and request["replacement_pr"] != number, "merged replacement PR required")
    else:
        require("replacement_pr" not in request, "replacement is only supported for closure")
    for _ in range(2):
        pull = pull_detail(repo, number)
        author = pull.get("user", {}).get("login")
        require(author in (NATIVE, ROBOT), "foreign action target")
        require(author == NATIVE or action in ("recover-retry", "release-hold"),
                "only recovery actions support verified robot replacements")
        snap = snapshot(repo, number, sha, author=author)
        if author == ROBOT:
            replacement(snap["pull"], repo)
            intent = None
        else:
            intent = source_intent(repo, snap)
        require(not snap["blocked_holds"], "human/unknown hold blocks native action")
        if action == "admit":
            require(not snap["held"] and not snap["holds"], "held native PR cannot be admitted")
            admission_safe(repo, snap, intent)
            marker = f"<!-- {MARKER}admit:{sha} -->"
            if any(marker in row["body"] and row.get("user", {}).get("login") == ROBOT for row in snap["comments"]):
                return {"number": number, "status": "already-admitted"}
            body = marker + "\n/ok-to-test"
        elif action == "recover-retry":
            require(not snap["cancellation_pending"], "hold cancellation pending")
            body = recovery_body(snap, sha, request.get("evidence"))
        elif action == "release-hold":
            if not snap["held"] or snap["cancellation_pending"]:
                return {"number": number, "status": "not-held-or-cancel-pending"}
            require(bool(snap["holds"]) and not snap["ci"]["blocked"]
                    and {"ci/pipeline-gate", "ci/prow/e2e-parallel"} <= set(snap["ci"]["positive"]),
                    "required current-head CI must be green")
            body = "/hold cancel"
        else:
            target = pull_detail(repo, request["replacement_pr"])
            meta = replacement(target, repo, merged=True)
            require({"number": number, "head_sha": sha} in meta["sources"]
                    and meta["group"] == intent["group"], "replacement does not identify exact native source")
            merge_sha = target.get("merge_commit_sha")
            require(isinstance(merge_sha, str) and SHA.fullmatch(merge_sha), "missing merged replacement commit")
            base = main_sha(repo)
            comparison = api(f"repos/{repo}/compare/{merge_sha}...{base}")
            require(isinstance(comparison, dict) and comparison.get("status") in ("ahead", "identical")
                    and comparison.get("merge_base_commit", {}).get("sha") == merge_sha,
                    "replacement merge is not on current main")
            wanted = coverage_paths(intent["intents"])
            tree_cache = {}
            for ref in (target["head"]["sha"], merge_sha, base):
                verify_coverage(intent["intents"], lambda path: content(repo, ref, path),
                                tree_paths(repo, ref, wanted, tree_cache))
            require(main_sha(repo) == base and pull_detail(repo, request["replacement_pr"]) == target,
                    "merged coverage evidence changed")
            marker = f"<!-- {MARKER}close:{sha}:{request['replacement_pr']} -->"
            if any(marker in row["body"] and row.get("user", {}).get("login") == ROBOT for row in snap["comments"]):
                return {"number": number, "status": "close-already-requested"}
            body = marker + f"\nCovered by merged replacement https://github.com/{repo}/pull/{request['replacement_pr']}.\n\n/close"
    return {**post_comment(repo, number, body), "action": action, "status": "requested"}


def main(argv=None):
    argv = sys.argv[1:] if argv is None else argv
    try:
        require(len(argv) in (2, 3) and argv[0] in ("inventory", "publish", "reconcile"), "usage: native_dependabot_maintenance.py inventory REPO | publish/reconcile REPO OUTPUT_FILE")
        mode, repo = argv[:2]
        validate_repo(repo)
        if mode == "inventory":
            require(len(argv) == 2, "inventory takes only REPO")
            result = inventory(repo)
        else:
            require(len(argv) == 3, "output file required")
            request = read_request(argv[2], "native_replacement" if mode == "publish" else "native_action")
            result = {"status": "no-request"} if request is None else (publish if mode == "publish" else reconcile)(repo, request)
        print(json.dumps(result, sort_keys=True))
        return 0
    except (ValueError, KeyError, TypeError, OSError, subprocess.CalledProcessError) as error:
        print(f"native Dependabot maintenance blocked: {error}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    sys.exit(main())
