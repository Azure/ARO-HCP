#!/usr/bin/env python3
"""Fail closed on duplicate dependency PRs and unsupported automatic retests."""

import base64
import fnmatch
import json
import os
import re
import subprocess
import sys
import tempfile
from datetime import datetime, timedelta, timezone
from urllib.parse import quote


def gh_lines(path, expression, token=None):
    env = os.environ.copy()
    if token:
        env["GH_TOKEN"] = token
    result = subprocess.run(
        ["gh", "api", "--paginate", path, "--jq", expression],
        check=True,
        capture_output=True,
        text=True,
        env=env,
    )
    return [json.loads(line) for line in result.stdout.splitlines() if line]


def group(item):
    """Validate dependency metadata and derive its publication title."""
    data = item.get("data")
    if not isinstance(data, dict):
        raise ValueError("create_pull_request requires dependency group data")
    ecosystem = data.get("ecosystem")
    family = data.get("package_family")
    version = data.get("target_version")
    manifests = data.get("manifests")
    if isinstance(manifests, list):
        manifests = [os.path.normpath(m) for m in manifests]
    if (
        ecosystem not in ("go", "npm")
        or not isinstance(family, str)
        or not re.fullmatch(r"[@a-zA-Z0-9._/+*-]{1,100}", family)
        or not isinstance(version, str)
        or not re.fullmatch(r"v?[0-9][a-zA-Z0-9.+_-]{0,50}", version)
        or not isinstance(manifests, list)
        or not manifests
        or any(
            not isinstance(path, str)
            or not re.fullmatch(r"[a-zA-Z0-9._/-]+", path)
            or path.startswith("/")
            or ".." in path.split("/")
            or path.rsplit("/", 1)[-1] not in ("go.mod", "package.json")
            for path in manifests
        )
    ):
        raise ValueError("invalid dependency group metadata")

    # Derive canonical changed manifests from each proposal's patch artifact.
    actual_files = set()
    if isinstance(item.get("files"), list):
        actual_files.update(os.path.normpath(f["path"]) for f in item["files"] if isinstance(f, dict) and "path" in f)
    elif isinstance(item.get("patch"), str):
        actual_files.update(os.path.normpath(f) for f in re.findall(r"^diff --git a/(.*) b/.*$", item["patch"], re.MULTILINE))
    actual_manifests = {f for f in actual_files if f.rsplit("/", 1)[-1] in ("go.mod", "package.json")}
    if actual_manifests and set(manifests) != actual_manifests:
        raise ValueError("manifests in data do not match actual manifests in patch")

    item["title"] = f"{family} to {version}"
    return ecosystem, family.casefold(), version.casefold(), set(manifests)


def family_matches(family, package):
    """Families may name a module prefix or use the prompt's '*' patterns."""
    package = package.casefold()
    return (
        fnmatch.fnmatchcase(package, family)
        or (family.endswith("/*") and package == family[:-2])
        or package.startswith(family.rstrip("/") + "/")
        or ("/" not in family and package.startswith("@" + family + "/"))
    )


def patch_hunks(file):
    """Validate unified-diff counts before trusting any dependency inference."""
    patch = file.get("patch")
    if not isinstance(patch, str) or not patch:
        raise ValueError("patch is unavailable")
    if file.get("status") not in ("added", "modified", "removed", "renamed", "copied"):
        raise ValueError("file status is unavailable or unsupported")
    hunks = []
    old_left = new_left = 0
    additions = deletions = 0
    for line in patch.splitlines():
        header = re.fullmatch(r"@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@.*", line)
        if header:
            if old_left or new_left:
                raise ValueError("patch is truncated")
            old_start, old_count, new_start, new_count = header.groups()
            old_left = int(old_count) if old_count is not None else 1
            new_left = int(new_count) if new_count is not None else 1
            hunks.append((int(old_start), int(new_start), []))
        elif line == r"\ No newline at end of file":
            continue
        elif hunks and line[:1] in (" ", "+", "-"):
            marker = line[0]
            old_left -= marker != "+"
            new_left -= marker != "-"
            additions += marker == "+"
            deletions += marker == "-"
            if old_left < 0 or new_left < 0:
                raise ValueError("patch has inconsistent hunk counts")
            hunks[-1][2].append((marker, line[1:]))
        else:
            raise ValueError("patch is not a unified diff")
    if not hunks or old_left or new_left:
        raise ValueError("patch is truncated")
    for field, actual in (("additions", additions), ("deletions", deletions)):
        if field in file and file[field] != actual:
            raise ValueError(f"patch {field} count is incomplete")
    return hunks


def changed_packages(entries):
    """Cancel semantically identical entries, including moves and formatting."""
    return {
        package
        for entry in entries["-"].keys() ^ entries["+"].keys()
        for package in entries["-"].get(entry, entries["+"].get(entry))
    }


def go_dependencies(hunks):
    entries = {"-": {}, "+": {}}
    module = r'(?:"[a-zA-Z0-9._~+/-]+"|[a-zA-Z0-9._~+/-]+)'
    version = r"v[0-9][a-zA-Z0-9.+_-]*"
    for _, _, lines in hunks:
        for side in ("-", "+"):
            block = None
            for marker, text in lines:
                if marker not in (" ", side):
                    continue
                text = text.split("//", 1)[0].strip()
                opener = re.fullmatch(r"(require|replace|exclude|retract|godebug)\s*\(", text)
                if opener:
                    block = opener[1]
                    continue
                if text == ")":
                    block = None
                    continue
                if marker != side or not text:
                    continue
                if re.match(r"^(go|toolchain|module|godebug|retract)\s+", text):
                    continue
                if block in ("retract", "godebug"):
                    continue
                directive = re.match(r"^(require|replace|exclude)\s+", text)
                directive = directive[1] if directive else block or "require"
                text = re.sub(r"^(require|replace|exclude)\s+", "", text)
                if "=>" in text:
                    before, after = text.split("=>", 1)
                    match = re.fullmatch(fr"({module})(?:\s+({version}))?", before.strip())
                    replacement = re.fullmatch(fr"({module})\s+({version})", after.strip())
                    if not match or (not replacement and not re.fullmatch(r'(?:"[^"]+"|[./][^\s]+)', after.strip())):
                        raise ValueError("cannot classify Go replace change")
                    package = match[1].strip('"')
                    target = replacement[1].strip('"') if replacement else after.strip().strip('"')
                    entry = ("replace", package, match[2], target, replacement[2] if replacement else None)
                    entries[side][entry] = {package, target} if replacement else {package}
                else:
                    match = re.fullmatch(fr"({module})\s+({version})", text)
                    if not match:
                        raise ValueError("cannot classify Go manifest change")
                    package = match[1].strip('"')
                    entries[side][(directive, package, match[2])] = {package}
    return changed_packages(entries)


def npm_dependencies(hunks):
    sections = {"dependencies", "devDependencies", "peerDependencies", "optionalDependencies", "overrides"}
    entries = {"-": {}, "+": {}}
    token = re.compile(r'"(?:[^"\\]|\\.)*"|[{}\[\]:,]|-?\d+(?:\.\d+)?(?:[eE][+-]?\d+)?|true|false|null')
    for old_start, new_start, lines in hunks:
        for side, start in (("-", old_start), ("+", new_start)):
            # A hunk may start inside a dependency object. Its enclosing section
            # is unknown until context establishes it; do not guess from a key.
            stack = [(None,)] if start > 1 else []
            key = None
            side_lines = [(marker, text) for marker, text in lines if marker in (" ", side)]
            text = "\n".join(line for _, line in side_lines)
            if not text:
                continue
            # Complete manifests also get JSON syntax validation.
            if start <= 1 and text.lstrip().startswith("{"):
                depth = 0
                for match in token.finditer(text):
                    if match[0] in ("{", "["):
                        depth += 1
                    elif match[0] in ("}", "]"):
                        depth -= 1
                if depth == 0:
                    try:
                        json.loads(text)
                    except ValueError as error:
                        raise ValueError("cannot parse npm manifest JSON") from error
            for marker, line in side_lines:
                matches = list(token.finditer(line))
                position = 0
                for index, match in enumerate(matches):
                    if line[position:match.start()].strip():
                        raise ValueError("cannot tokenize npm manifest change")
                    position = match.end()
                    value = match[0]
                    path = stack[-1] if stack else ()
                    is_key = value.startswith('"') and index + 1 < len(matches) and matches[index + 1][0] == ":"
                    if is_key:
                        key = json.loads(value)
                    elif value in ("{", "["):
                        stack.append(path + (key,) if key is not None else path)
                        key = None
                    elif value in ("}", "]"):
                        if stack and stack[-1] != (None,):
                            stack.pop()
                        key = None
                    elif value == ",":
                        key = None
                    if value in ("{", "}", "[", "]", ":", ","):
                        continue
                    dependency_path = path + (key,) if key is not None else path
                    section = next((i for i, part in enumerate(dependency_path) if part in sections), None)
                    if section is not None:
                        names = dependency_path[section + 1:]
                        if names:
                            # Nested overrides name both the parent and target.
                            if dependency_path[section] != "overrides":
                                names = names[:1]
                            if not is_key:
                                entry = (tuple(dependency_path[section:]), json.dumps(json.loads(value)))
                                entries[side][entry] = {
                                    re.sub(r"(?<!^)@.*$", "", name) for name in names
                                }
                        elif not is_key and marker == side:
                            raise ValueError("cannot classify npm dependency entry")
                    elif (
                        marker == side
                        and dependency_path
                        and dependency_path[0] is None
                        and len(dependency_path) < 3
                        and not (
                            is_key
                            and index + 2 < len(matches)
                            and matches[index + 2][0] in ("{", "[")
                        )
                    ):
                        raise ValueError("npm dependency section is outside diff context")
                if line[position:].strip():
                    raise ValueError("cannot tokenize npm manifest change")
    return changed_packages(entries)


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


def load_lockfiles(repo, pr, files):
    """Compare merge-base/head resolutions without including unrelated base changes."""
    locks = [file for file in files if any(
        path and os.path.basename(path) in ("package-lock.json", "npm-shrinkwrap.json")
        for path in (file["filename"], file.get("previous_filename")))]
    if not locks:
        return
    detail = gh_lines(f"repos/{repo}/pulls/{pr['number']}",
                      '{base: .base.sha, head: .head.sha}')[0]
    comparison = gh_lines(f"repos/{repo}/compare/{detail['base']}...{detail['head']}",
                          '{base: .merge_base_commit.sha}')[0]
    detail["base"] = comparison["base"]
    for file in locks:
        documents = []
        for side in ("base", "head"):
            if file.get("status") == ("added" if side == "base" else "removed"):
                documents.append({})
                continue
            path = (file.get("previous_filename") or file["filename"]) if side == "base" else file["filename"]
            if os.path.basename(path) not in ("package-lock.json", "npm-shrinkwrap.json"):
                documents.append({})
                continue
            content = gh_lines(f"repos/{repo}/contents/{quote(path, safe='/')}?ref={detail[side]}",
                               '. | {encoding, content}')[0]
            try:
                if content.get("encoding") != "base64":
                    raise ValueError("complete lockfile content unavailable")
                documents.append(lock_versions(json.loads(base64.b64decode(content["content"]))))
            except (ValueError, KeyError, TypeError):
                documents = None
                break
        file["lock_versions"] = documents


def overlaps(candidate, files):
    ecosystem, family, _, manifests = candidate
    manifest_names = {"go.mod"} if ecosystem == "go" else {"package.json", "package-lock.json", "npm-shrinkwrap.json"}
    for file in files:
        filename = file["filename"]
        paths = {filename, file.get("previous_filename")}
        if not any(path and os.path.basename(path) in manifest_names for path in paths):
            continue
        lockfile = any(path and os.path.basename(path) in ("package-lock.json", "npm-shrinkwrap.json")
                       for path in paths)
        if lockfile:
            paths.update(os.path.join(os.path.dirname(path), "package.json") for path in list(paths) if path)
        try:
            if lockfile:
                versions = file.get("lock_versions")
                if not versions:
                    raise ValueError("complete npm lockfile comparison unavailable")
                before, after = versions
                packages = {entry[0] for path in before.keys() | after.keys()
                            if before.get(path) != after.get(path)
                            for entry in (before.get(path), after.get(path)) if entry}
            else:
                hunks = patch_hunks(file)
                packages = go_dependencies(hunks) if ecosystem == "go" else npm_dependencies(hunks)
        except ValueError as error:
            # An uncertain diff can still establish relevance in another
            # manifest when its changed lines mention this family. This is
            # only a reason to defer, never evidence that creation is safe.
            patch = file.get("patch")
            changed_tokens = (
                token
                for line in (patch.splitlines() if isinstance(patch, str) else [])
                if line.startswith(("+", "-"))
                for token in re.findall(r"[@a-zA-Z0-9._/+*-]+", line[1:])
            )
            if manifests.intersection(paths) or any(family_matches(family, token) for token in changed_tokens):
                return f"{filename}: {error}"
            continue
        matching = sorted(package for package in packages if family_matches(family, package))
        if matching:
            return f"{filename} changes dependency {', '.join(matching)}"
    return None


def drop_creates_missing_data(output):
    """Drop creates that omitted `data` so a successful sweep is not failed.

    Invalid `data` still fails closed. Missing `data` is the agent forgetting
    the structured argument; stripping those items lets Process Safe Outputs
    skip them instead of opening an unguarded PR.
    """
    items = output.get("items")
    if not isinstance(items, list):
        raise ValueError("create_pull_request output items must be a list")
    kept = []
    skipped = 0
    for item in items:
        if item.get("type") == "create_pull_request" and "data" not in item:
            title = item.get("title") or "untitled"
            print(
                f"::warning::Skipping create_pull_request without dependency group data: {title}",
                file=sys.stderr,
            )
            skipped += 1
            continue
        kept.append(item)
    if skipped:
        output["items"] = kept
        print(f"Dropped {skipped} create_pull_request item(s) missing dependency group data")
    return skipped


def guard_create(output, repo):
    drop_creates_missing_data(output)
    items = [item for item in output.get("items", []) if item.get("type") == "create_pull_request"]
    if not items:
        return
    groups = [group(item) for item in items]
    for index, (_, family, _, manifests) in enumerate(groups):
        for _, other_family, _, other_manifests in groups[:index]:
            if (
                family_matches(family, other_family)
                or family_matches(other_family, family)
                or manifests.intersection(other_manifests)
            ):
                raise ValueError("multiple new PRs for the same family or manifest")
    prs = gh_lines(
        f"repos/{repo}/pulls?state=open&per_page=100",
        '.[] | {number, title, body, base: .base.ref}',
    )
    deferred = set()
    for pr in prs:
        if pr["base"] != "main":
            continue
        files = gh_lines(
            f"repos/{repo}/pulls/{pr['number']}/files?per_page=100",
            ".[] | {filename, previous_filename, status, patch, additions, deletions}",
        )
        if any(candidate[0] == "npm" for candidate in groups):
            load_lockfiles(repo, pr, files)
        for index, candidate in enumerate(groups):
            reason = overlaps(candidate, files)
            if reason:
                deferred.add(id(items[index]))
                print(
                    f"::warning::Skipping create_pull_request for {candidate[1]}: "
                    f"open PR #{pr['number']}: {reason}",
                    file=sys.stderr,
                )
    output["items"] = [item for item in output["items"] if id(item) not in deferred]
    print(
        f"Checked {len(groups)} dependency groups against {len(prs)} open PRs; "
        f"deferred {len(deferred)} overlapping create(s)"
    )


def rewrite_output(path, output):
    directory = os.path.dirname(path) or "."
    fd, tmp = tempfile.mkstemp(prefix="dependabot-guard-", suffix=".json", dir=directory)
    try:
        with os.fdopen(fd, "w", encoding="utf-8") as dest:
            json.dump(output, dest)
            dest.write("\n")
        os.replace(tmp, path)
    except Exception:
        try:
            os.unlink(tmp)
        except OSError:
            pass
        raise


def guard_retest(repo, number, sha, evidence, now=None):
    statuses = gh_lines(
        f"repos/{repo}/commits/{sha}/statuses?per_page=100",
        '.[] | {context, state, target_url, created_at}',
        os.environ["CI_TOKEN"],
    )
    checks = gh_lines(
        f"repos/{repo}/commits/{sha}/check-runs?per_page=100&filter=latest",
        '.check_runs[] | {name, status, conclusion, details_url}',
        os.environ["CI_TOKEN"],
    )
    latest = {}
    for status in statuses:
        if status["context"].casefold() != "tide":
            context = status["context"]
            if context not in latest or status["created_at"] > latest[context]["created_at"]:
                latest[context] = status
    failed_urls = {
        status["target_url"].rstrip(".;,")
        for status in latest.values()
        if status["state"] in ("failure", "error") and status.get("target_url")
    }
    failed_urls.update(
        check["details_url"].rstrip(".;,")
        for check in checks
        if check["conclusion"] in ("failure", "timed_out", "cancelled", "action_required", "startup_failure")
        and check.get("details_url")
    )
    links = {url.rstrip(".;,") for url in re.findall(r"https://[^\s)]+", evidence)}
    if not any(url in links for url in failed_urls):
        raise ValueError("retest needs a current failing check URL and proven transient-failure evidence")
    if not links.difference(failed_urls):
        raise ValueError("retest also needs a comparison run URL")
    comments = gh_lines(
        f"repos/{repo}/issues/{number}/comments?per_page=100",
        '.[] | {body, created_at}',
    )
    now = now or datetime.now(timezone.utc)
    for comment in comments:
        if "/retest-required" in comment["body"]:
            posted = datetime.fromisoformat(comment["created_at"].replace("Z", "+00:00"))
            if posted > now - timedelta(hours=24):
                raise ValueError(f"PR #{number} already requested a retest in the last 24 hours")
    print(f"PR #{number}: current failing check and comparison run verified; retest cooldown clear")


def main():
    try:
        if sys.argv[1] == "create" and len(sys.argv) == 4:
            path = sys.argv[2]
            with open(path, encoding="utf-8") as source:
                output = json.load(source)
            guard_create(output, sys.argv[3])
            rewrite_output(path, output)
        elif sys.argv[1] == "retest" and len(sys.argv) == 6:
            guard_retest(*sys.argv[2:])
        else:
            raise ValueError("usage: dependabot_guard.py create OUTPUT REPO | retest REPO PR SHA EVIDENCE")
    except (OSError, KeyError, TypeError, ValueError, subprocess.CalledProcessError) as error:
        print(f"Dependabot guard refused action: {error}", file=sys.stderr)
        sys.exit(1)


if __name__ == "__main__":
    main()
