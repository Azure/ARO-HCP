#!/usr/bin/env python3
"""Trusted, fixed-command publication and reconciliation for native Dependabot PRs.

The agent supplies patches, never shell commands. No candidate repository program
is run in this credentialed process. Native Dependabot uses depctl for shared
hold, CI, replacement tree, coverage and admission policy; Python owns lifecycle writes.
"""

import base64
import hashlib
import json
import os
from pathlib import Path, PurePosixPath
import re
import stat
import subprocess
import sys
from urllib.parse import quote


NATIVE = "dependabot[bot]"
ROBOT = "aro-hcp-robot[bot]"
LABEL = "agentic-dependency-upgrade"
MARKER = "native-dependabot-maintenance:"
SHA = re.compile(r"[0-9a-f]{40}")
HOLD_LABEL = "do-not-merge/hold"
RECOVERY_MARKER = "gh-aw-recompile:recovery:"
ACTIONS = {"admit", "recover-retry", "release-hold", "close-superseded"}
VENDORS = ("vendor/", "image-sync/oc-mirror/acrauth/vendor/")
MAX_PATCH = 16 * 1024 * 1024
# JSON can escape each patch byte as six ASCII bytes. Leave room for the
# bounded publication text, 100 source identities, and JSON structure.
MAX_REQUEST = 6 * MAX_PATCH + 256 * 1024
REQUEST_FILE = "native-replacement-request.json"
DETECTION_PATCH = "aw-native-replacement.patch"



def lock_versions(document):
    """Read resolved package identities from npm lockfile versions 1 through 3."""
    result = {}
    if not isinstance(document, dict) or document.get("lockfileVersion") not in (1, 2, 3):
        raise ValueError("unsupported npm lockfile format")
    if document["lockfileVersion"] in (2, 3):
        packages = document.get("packages")
        if not isinstance(packages, dict):
            raise ValueError("npm lockfile packages are unavailable")
        for path, entry in packages.items():
            if not path:
                continue
            if not isinstance(entry, dict) or "node_modules/" not in path:
                raise ValueError("cannot identify npm lockfile package")
            package = path.rsplit("node_modules/", 1)[1]
            if entry.get("link") is True:
                raise ValueError("npm workspace link requires conservative deferral")
            if not isinstance(entry.get("version"), str):
                raise ValueError("npm resolved version is unavailable")
            result[path] = (package, entry["version"], entry.get("resolved"), entry.get("integrity"))
    else:
        def visit(dependencies, parent=""):
            if not isinstance(dependencies, dict):
                raise ValueError("invalid npm lockfile dependencies")
            for package, entry in dependencies.items():
                if not isinstance(entry, dict) or not isinstance(entry.get("version"), str):
                    raise ValueError("npm resolved version is unavailable")
                path = parent + "node_modules/" + package
                result[path] = (package, entry["version"], entry.get("resolved"), entry.get("integrity"))
                visit(entry.get("dependencies", {}), path + "/")
        visit(document.get("dependencies", {}))
    return result



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


def validate_target(repo, number, expected_head):
    require(isinstance(repo, str) and re.fullmatch(
        r"[A-Za-z0-9][A-Za-z0-9_.-]*/[A-Za-z0-9][A-Za-z0-9_.-]*", repo),
        "repository must be OWNER/REPO")
    require(not isinstance(number, bool) and re.fullmatch(r"[1-9][0-9]*", str(number)),
            "pull request number must be a positive integer")
    require(isinstance(expected_head, str) and SHA.fullmatch(expected_head),
            "expected head must be a full lowercase commit SHA")
    return int(number)


def snapshot_evidence(comments, commits, hold_events, statuses, checks, head):
    """Evaluate shared hold and CI policy with the trusted depctl binary."""
    result = subprocess.run(
        [os.environ.get("DEPCTL_BINARY", "depctl"), "maintenance", "snapshot-evidence"],
        input=json.dumps({"comments": comments, "commits": commits, "hold_events": hold_events,
                          "statuses": statuses, "checks": checks, "head": head}),
        capture_output=True, text=True, check=True,
    )
    return json.loads(result.stdout)


def validate_repo(repo):
    validate_target(repo, 1, "0" * 40)


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
    validate_target(repo, number, sha)
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
    hold_events = [event for event in events if event.get("event") in ("labeled", "unlabeled")
                   and event.get("label", {}).get("name") == HOLD_LABEL]
    evidence = snapshot_evidence(comments, [row["sha"] for row in commits],
                                 hold_events, statuses, checks, sha)
    holds = evidence["active_holds"]
    cancellation_pending = evidence["cancellation_pending"]
    held = HOLD_LABEL in {label["name"] for label in pull["labels"]}
    trusted = evidence["trusted_hold_label"]
    blocked_holds = (any(hold["kind"] != "retry-exhaustion" for hold in holds)
                     or held and (not trusted or not holds and not cancellation_pending))
    return {"pull": pull, "files": files, "comments": comments, "holds": holds,
            "held": held, "cancellation_pending": cancellation_pending,
            "blocked_holds": bool(blocked_holds),
            "ci": evidence["ci"]}


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
        validate_target("Azure/ARO-HCP", source["number"], source["head_sha"])
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


def closure_requests(snap):
    """Read trusted current-head closure commands, not copied or stale markers."""
    pattern = r"^<!-- " + re.escape(MARKER) + "close:" + snap["pull"]["head"]["sha"] + r":([1-9][0-9]*) -->\n"
    requested = set()
    for comment in snap["comments"]:
        if comment.get("user", {}).get("login") != ROBOT:
            continue
        match = re.match(pattern, comment["body"])
        if match and re.search(r"(?m)^/close\s*\Z", comment["body"]):
            requested.add(int(match.group(1)))
    return sorted(requested)


def next_action(group, replacements):
    """Select work from verified identities, never from native-source CI."""
    if group["blockers"]:
        return {"action": "blocked", "reason": "; ".join(sorted(group["blockers"]))}
    matching = sorted((row for row in replacements if row["group"] == group["group"]),
                      key=lambda row: row["number"])
    opened = [row for row in matching if not row["merged"]]
    if len(opened) > 1:
        return {"action": "blocked", "reason": "ambiguous open matching replacements"}
    if opened and opened[0]["blocked_holds"]:
        return {"action": "blocked", "reason": "replacement has human/unknown hold",
                "replacement_pr": opened[0]["number"]}
    sources = {(row["number"], row["head_sha"]) for row in group["sources"]}
    pending = set()
    for row in matching:
        if row["merged"]:
            mapped = {(source["number"], source["head_sha"]) for source in row["sources"]}
            pending.update((source["number"], source["head_sha"]) for source in group["sources"]
                           if row["number"] in source.get("closure_requests", [])
                           and (source["number"], source["head_sha"]) in mapped)
    for row in reversed(matching):
        if not row["merged"]:
            continue
        mapped = (sources - pending) & {(source["number"], source["head_sha"]) for source in row["sources"]}
        if mapped:
            return {"action": "close_superseded",
                    "reason": "merged replacement maps exact source heads; closure guard must verify coverage",
                    "replacement_pr": row["number"],
                    "source_prs": [{"number": number, "head_sha": sha} for number, sha in sorted(mapped)]}
    if pending == sources and pending:
        return {"action": "wait_for_closure",
                "reason": "trusted current-head closure commands already requested; wait for observed Prow closure",
                "source_prs": [{"number": number, "head_sha": sha} for number, sha in sorted(pending)]}
    if opened:
        return {"action": "maintain_replacement",
                "reason": "reuse existing replacement; its current-head CI governs further progress",
                "replacement_pr": opened[0]["number"]}
    return {"action": "prepare_replacement",
            "reason": "no open matching replacement or merged exact-source mapping; native CI is evidence only"}


def inventory(repo):
    validate_repo(repo)
    base = main_sha(repo)
    groups = {}
    current = creator_inventory(repo, NATIVE)
    source_evidence = {}
    for pull in current:
        row = {"number": pull["number"], "head_sha": pull.get("head", {}).get("sha")}
        evidence = source_evidence[row["number"]] = {}
        try:
            snap = snapshot(repo, row["number"], row["head_sha"])
            evidence["snapshot"] = snap
            intent = source_intent(repo, snap)
            evidence["intent"] = intent
            row.update(intent)
            row["ci"] = snap["ci"]
            row["held"] = snap["held"]
            row["blocked_holds"] = snap["blocked_holds"]
            row["closure_requests"] = closure_requests(snap)
            key = intent["group"] + ":" + intent["target"]
            group = groups.setdefault(key, {key: intent[key] for key in ("group", "ecosystem", "family", "target")})
            group.setdefault("sources", []).append(row)
            group.setdefault("blockers", [])
            if snap["blocked_holds"]:
                group["blockers"].append(f"PR #{row['number']}: human/unknown hold")
        except ValueError as error:
            evidence["error"] = error
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
    for group in groups.values():
        group["sources"].sort(key=lambda row: row["number"])
        decision = next_action(group, replacements)
        if decision["action"] == "prepare_replacement":
            try:
                validate_source_siblings(repo, group["sources"], current, evidence=source_evidence)
            except ValueError as error:
                group["blockers"].append(str(error))
                decision = next_action(group, replacements)
        group["next_action"] = decision
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
    field = "request_sha256" if kind == "native_replacement" else "request"
    require(set(item) in ({"type", field}, {"type", "data"}), "ambiguous native output envelope")
    data = item.get("data") if "data" in item else {key: value for key, value in item.items() if key != "type"}
    require(isinstance(data, dict) and set(data) == {field} and isinstance(data[field], str),
            "invalid native safe-output envelope")
    if kind == "native_replacement":
        digest = data[field]
        require(re.fullmatch(r"[0-9a-f]{64}", digest), "replacement request digest must be lowercase SHA256")
        directory = os.open(Path(path).parent / "agent", os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW)
        try:
            descriptor = os.open(REQUEST_FILE, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK, dir_fd=directory)
            with os.fdopen(descriptor, "rb") as payload_file:
                info = os.fstat(payload_file.fileno())
                require(stat.S_ISREG(info.st_mode) and 0 < info.st_size <= MAX_REQUEST,
                        "replacement request artifact must be a bounded regular file")
                payload = payload_file.read(MAX_REQUEST + 1)
        finally:
            os.close(directory)
        require(0 < len(payload) <= MAX_REQUEST, "replacement request artifact exceeds size limit")
        require(hashlib.sha256(payload).hexdigest() == digest, "replacement request artifact digest mismatch")
        request = replacement_json(payload.decode("utf-8"))
        validate_replacement_request(request)
    else:
        request = json.loads(data[field])
        require(isinstance(request, dict), "native request must be JSON object")
    return request


def stage_detection(repo, output_file, detection_dir):
    """Expand only the prepared detector copy with digest-verified publication data.

    Run after prepare_threat_detection_files.sh, before the detector. Its pinned
    consumer reads agent_output.json and top-level aw-*.patch files, not arbitrary
    artifact JSON. The publisher still reads the untouched original digest record.
    """
    validate_repo(repo)
    output_path = Path(output_file)
    original = output_path.read_bytes()
    request = read_request(output_file, "native_replacement")
    require(output_path.read_bytes() == original, "agent output changed during detection staging")
    if request is None:
        return {"status": "no-request"}
    output = json.loads(original)
    item = next(item for item in output["items"] if item["type"] == "native_replacement")
    item.update({"title": safe_text(request["title"], title=True),
                 "body": safe_text(request["body"]),
                 "source_prs": request["source_prs"],
                 "expected_base_sha": request["expected_base_sha"]})
    expanded = json.dumps(output, ensure_ascii=False, sort_keys=True).encode("utf-8")
    directory = os.open(detection_dir, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW)
    try:
        descriptor = os.open("agent_output.json", os.O_RDWR | os.O_NOFOLLOW | os.O_NONBLOCK,
                             dir_fd=directory)
        with os.fdopen(descriptor, "r+b") as detector_output:
            info = os.fstat(detector_output.fileno())
            require(stat.S_ISREG(info.st_mode) and info.st_nlink == 1,
                    "detector output must be a singly linked regular file")
            original_info = output_path.stat()
            require((info.st_dev, info.st_ino) != (original_info.st_dev, original_info.st_ino),
                    "detector output must be a separate copy of original agent output")
            require(json.loads(detector_output.read()) == json.loads(original),
                    "prepared detector output does not match original agent output")
            descriptor = os.open(DETECTION_PATCH, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW,
                                 0o600, dir_fd=directory)
            with os.fdopen(descriptor, "wb") as detector_patch:
                detector_patch.write(request["patch"].encode("utf-8"))
            detector_output.seek(0)
            detector_output.truncate(0)
            detector_output.write(expanded)
    finally:
        os.close(directory)
    return {"status": "staged", "patch": DETECTION_PATCH}


def patch_paths(patch):
    require(isinstance(patch, str) and 0 < len(patch.encode()) <= MAX_PATCH and "\x00" not in patch,
            "patch must be nonempty UTF-8 and at most 16 MiB")
    headers = re.findall(r"^diff --git a/(\S+) b/(\S+)$", patch, re.MULTILINE)
    require(bool(headers) and patch.startswith("diff --git ") and len(headers) == patch.count("\ndiff --git ") + 1,
            "malformed patch file headers")
    require(re.search(r"^@@ -\d+(?:,\d+)? \+\d+(?:,\d+)? @@", patch, re.MULTILINE),
            "patch must contain a meaningful text diff")
    require(all(old == new for old, new in headers), "renamed paths not allowed")
    paths = [safe_path(old) for old, _ in headers]
    require(len(set(paths)) == len(paths), "duplicate patch paths")
    require(all(mode == "100644" for mode in re.findall(r"^(?:new|deleted) file mode (\d+)$", patch, re.MULTILINE)),
            "only regular patch files are allowed")
    require(not re.search(r"^(?:old mode|new mode|rename |copy |GIT binary patch|Binary files)", patch, re.MULTILINE),
            "mode changes, renames and binary patches are forbidden")
    return paths


def native_policy(operation, evidence, *flags):
    """Send fixed policy evidence to the trusted depctl; never fall back to Python."""
    result = subprocess.run(
        [os.environ.get("DEPCTL_BINARY", "depctl"), "dependabot", "native", operation, *flags],
        input=json.dumps(evidence), capture_output=True, text=True,
    )
    require(result.returncode == 0, result.stderr.strip() or "native policy check failed")
    return json.loads(result.stdout) if result.stdout.strip() else None


def coverage_paths(intents):
    return set(native_policy("check-coverage", {"intents": intents}, "--paths-only"))


def verify_coverage(intents, reader, available):
    wanted = coverage_paths(intents) & set(available)
    native_policy("check-coverage", {"intents": intents, "available": sorted(available),
                                    "contents": {path: reader(path) for path in sorted(wanted)}})


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
    """Gather immutable consumer evidence; depctl alone decides native eligibility."""
    head = snap["pull"]["head"]["sha"]
    wanted = coverage_paths(intent["intents"])
    available = tree_paths(repo, head, wanted)
    live = tree_paths(repo, main_sha(repo), wanted) if intent["ecosystem"] == "actions" else set()
    native_policy("check-admission", {
        "group": intent, "paths": [file["filename"] for file in snap["files"]],
        "available": sorted(available), "live": sorted(live),
        "contents": {path: content(repo, head, path) for path in sorted(available & wanted)},
    })


def safe_text(text, *, title=False):
    require(isinstance(text, str), "invalid publication text")
    # Normalize every literal mention delimiter, regardless of Markdown quoting,
    # escaping, or code fences. Scoped package names remain readable without
    # allowing user/team notifications. Never apply this to the patch.
    text = text.replace("@", "\uff20")
    require(0 < len(text) <= (200 if title else 20000)
            and bool(text.strip()) and not re.search(r"[\x00-\x08\x0b\x0c\x0e-\x1f\x7f]", text),
            "invalid publication text")
    text.encode("utf-8")
    require(not re.search(r"(?m)^\s*/|(?i:\b(?:close[sd]?|fix(?:e[sd])?|resolve[sd]?)\s+#\d+)", text)
            and MARKER not in text and "<!-- gh-aw-workflow-id:" not in text,
            "publication text cannot contain commands, auto-close directives or ownership markers")
    if title:
        require(text.startswith("chore(deps): ") and bool(text[len("chore(deps): "):].strip())
                and "\n" not in text and "\r" not in text, "replacement title must be chore(deps): ...")
    return text


def replacement_json(text):
    def unique_fields(pairs):
        result = {}
        for key, value in pairs:
            require(key not in result, "duplicate replacement JSON field")
            result[key] = value
        return result
    try:
        return json.loads(text, object_pairs_hook=unique_fields)
    except RecursionError as error:
        raise ValueError("replacement JSON nesting is too deep") from error


def validate_replacement_request(request):
    require(isinstance(request, dict)
            and set(request) == {"source_prs", "expected_base_sha", "patch", "title", "body"},
            "invalid replacement schema")
    validate_sources(request["source_prs"])
    validate_target("Azure/ARO-HCP", 1, request["expected_base_sha"])
    safe_text(request["title"], title=True)
    safe_text(request["body"])
    return patch_paths(request["patch"])


def validate_source_siblings(repo, intents, current, *, evidence=None):
    """Require complete family membership or immutable proof of disjointness."""
    selected = {source["number"] for source in intents}
    require(selected <= {pull["number"] for pull in current},
            "native source inventory changed during authorization")
    for pull in current:
        if pull["number"] in selected:
            continue
        cached = evidence.get(pull["number"], {}) if evidence is not None else {}
        try:
            if "snapshot" not in cached and "error" in cached:
                raise cached["error"]
            snap = cached.get("snapshot")
            if snap is None:
                snap = snapshot(repo, pull["number"], pull["head"]["sha"])
            # Only complete, unambiguous changed-file evidence can prove a sibling
            # belongs to another ecosystem. Titles and branch names are untrusted.
            ecosystems = set()
            for file in snap["files"]:
                path = safe_path(file.get("filename"))
                name = PurePosixPath(path).name
                ecosystem = {"go.mod": "go", "go.sum": "go", "go.work.sum": "go",
                             "package.json": "npm", "package-lock.json": "npm",
                             "npm-shrinkwrap.json": "npm", "Dockerfile": "docker"}.get(name)
                if path.startswith(".github/workflows/") and path.endswith((".md", ".yml", ".yaml")):
                    ecosystem = "actions"
                if file.get("status") != "modified" or file.get("previous_filename"):
                    ecosystem = None
                ecosystems.add(ecosystem)
            if ecosystems and None not in ecosystems and intents[0]["ecosystem"] not in ecosystems:
                continue
            if "error" in cached:
                raise cached["error"]
            candidate = cached.get("intent")
            if candidate is None:
                candidate = source_intent(repo, snap)
            require((candidate["group"], candidate["target"]) != (intents[0]["group"], intents[0]["target"]),
                    f"replacement omits current same-family/target native source #{pull['number']}")
        except ValueError as error:
            raise ValueError(f"PR #{pull['number']}: {error}") from error


def post_comment(repo, number, body):
    response = api(f"repos/{repo}/issues/{number}/comments", method="POST", data={"body": body})
    require(isinstance(response, dict) and type(response.get("id")) is int and response.get("body") == body,
            "comment response incomplete; inspect before retry")
    return {"number": number, "comment_id": response["id"]}


def recovery_body(snap, sha, evidence):
    require(isinstance(evidence, str) and 40 <= len(evidence) <= 4000, "diagnosed recovery evidence required")
    require(not re.search(r"^\s*/", evidence, re.MULTILINE) and MARKER not in evidence
            and RECOVERY_MARKER not in evidence and not re.search(r"@[A-Za-z0-9]", evidence),
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
    validate_target(repo, number, sha)
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
            if request["replacement_pr"] in closure_requests(snap):
                return {"number": number, "status": "close-already-requested"}
            body = marker + f"\nCovered by merged replacement https://github.com/{repo}/pull/{request['replacement_pr']}.\n\n/close"
    return {**post_comment(repo, number, body), "action": action, "status": "requested"}


def main(argv=None):
    argv = sys.argv[1:] if argv is None else argv
    try:
        require(bool(argv) and argv[0] in ("inventory", "stage-detection", "reconcile")
                and len(argv) >= 2,
                "usage: native_dependabot_maintenance.py inventory REPO | stage-detection REPO OUTPUT_FILE DETECTION_DIR | reconcile REPO OUTPUT_FILE")
        mode, repo = argv[:2]
        validate_repo(repo)
        if mode == "inventory":
            require(len(argv) == 2, "inventory takes only REPO")
            result = inventory(repo)
        elif mode == "stage-detection":
            require(len(argv) == 4, "stage-detection requires REPO OUTPUT_FILE DETECTION_DIR")
            result = stage_detection(repo, *argv[2:])
        else:
            require(len(argv) == 3, "output file required")
            request = read_request(argv[2], "native_action")
            result = {"status": "no-request"} if request is None else reconcile(repo, request)
        print(json.dumps(result, sort_keys=True))
        return 0
    except (ValueError, KeyError, TypeError, OSError, subprocess.CalledProcessError) as error:
        print(f"native Dependabot maintenance blocked: {error}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    sys.exit(main())
