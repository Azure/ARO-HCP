#!/usr/bin/env python3
"""Run repository-owned native dependency consumers, then prepare an offline request.

Run inside the read-only agent sandbox, in the candidate worktree, without App
credentials or GitHub writes. This is not a sandbox launcher and never fetches,
checks out, or executes a source PR. Logs never use safe-output stdout.
"""

import argparse
from contextlib import contextmanager
import hashlib
import json
import os
from pathlib import Path, PurePosixPath
import re
import shlex
import signal
import stat
import subprocess
import sys
import tempfile
import time

import native_dependabot_maintenance as native


CONSUMER_TIMEOUT = 1800
RESULTS_FILE = "native-validation-results.json"
API_TARGETS = ("fmt", "swagger", "lint-openapi", "models", "testsdk", "validate-examples")
API_GENERATORS = ("fmt", "swagger", "models", "testsdk")
COMMON_GATES = (("make", "verify-yamlfmt"), ("make", "-C", "config", "detect-change"))
require = native.require


def git(root, *args, index=None):
    """Local-only Git, with no hooks, external diffs, or executable filters."""
    env = os.environ.copy()
    env.update({"GIT_CONFIG_NOSYSTEM": "1", "GIT_CONFIG_GLOBAL": os.devnull,
                "GIT_TERMINAL_PROMPT": "0", "GIT_OPTIONAL_LOCKS": "0",
                "GIT_AUTHOR_NAME": "aro-hcp-robot[bot]",
                "GIT_AUTHOR_EMAIL": "aro-hcp-robot[bot]@users.noreply.github.com",
                "GIT_COMMITTER_NAME": "aro-hcp-robot[bot]",
                "GIT_COMMITTER_EMAIL": "aro-hcp-robot[bot]@users.noreply.github.com"})
    # Do not let caller-supplied Git plumbing redirect the candidate or its index.
    for key in ("GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_COMMON_DIR",
                "GIT_OBJECT_DIRECTORY", "GIT_ALTERNATE_OBJECT_DIRECTORIES"):
        env.pop(key, None)
    if index is not None:
        env["GIT_INDEX_FILE"] = str(index)
    command = ["git", "-c", "core.hooksPath=/dev/null", "-c", "core.fsmonitor=false",
               "-c", "credential.helper=", "-c", "core.quotePath=false"]
    settings = subprocess.run(command + ["config", "--null", "--list", "--name-only"],
                              cwd=root, env=env, capture_output=True, check=True).stdout.decode().split("\0")
    command += [value for key in sorted(set(settings)) if re.fullmatch(r"filter\..+\.(clean|process)", key)
                for value in ("-c", key + "=")]
    return subprocess.run(command + list(args), cwd=root, env=env, capture_output=True,
                          check=True).stdout.decode("utf-8").strip()


def untracked_paths(root):
    ordinary = git(root, "ls-files", "--others", "--exclude-standard", "-z").split("\0")
    # Generated vendor additions are ignored by repository policy and must be
    # published, unlike ignored node_modules, caches, or scratch elsewhere.
    vendor = git(root, "ls-files", "--others", "--ignored", "--exclude-standard", "-z",
                 "--", *native.VENDORS).split("\0")
    return [path for path in ordinary + vendor if path]


def paths(root, base):
    changed = git(root, "diff", "--no-ext-diff", "--no-textconv", "--no-renames", "--name-only", "-z", base).split("\0")
    return sorted({native.safe_path(path) for path in changed + untracked_paths(root) if path})


def stage_candidate(root, base, *, index=None):
    # The index starts at HEAD, which may already contain a committed candidate.
    # Include HEAD-relative restorations even when their bytes now equal base.
    changed = sorted(set(paths(root, base)) | set(paths(root, "HEAD")))
    vendor = [path for path in changed if path.startswith(native.VENDORS)]
    ordinary = [path for path in changed if not path.startswith(native.VENDORS)]
    if ordinary:
        git(root, "add", "-A", "--", *ordinary, index=index)
    if vendor:
        git(root, "add", "-A", "--force", "--", *vendor, index=index)


def file_state(root, path, *, strict=True):
    target = Path(root) / path
    if not target.exists() and not target.is_symlink():
        return None
    info = target.lstat()
    if not strict and stat.S_ISLNK(info.st_mode):
        return ("symlink", os.readlink(target))
    require(stat.S_ISREG(info.st_mode) and (not strict or info.st_nlink == 1),
            "candidate output is not a singly linked regular file: " + path)
    return (bool(info.st_mode & 0o111), hashlib.sha256(target.read_bytes()).hexdigest())


def worktree_state(root):
    tracked = git(root, "ls-files", "-z").split("\0")
    untracked = untracked_paths(root)
    # Unchanged tracked names (including vendor notices with spaces) and symlinks
    # are legitimate. Strict patch path/mode rules apply only to changed scope.
    return {path: file_state(root, path, strict=False) for path in sorted(set(tracked + untracked)) if path}


def clean(root):
    require(not git(root, "status", "--porcelain=v1", "--untracked-files=all") and not untracked_paths(root),
            "validation left candidate dirty; inspect full logs and git status (no request prepared)")


def generated_api_path(path):
    return (path.startswith("internal/azureapi/") and "/generated/" in path and path.endswith(".go")
            or native.sdk_consumer(path)
            or path.startswith("api/redhatopenshift/") and path.endswith((".tsp", "/openapi.json")))


def load_group(repo, inventory_file, group_id):
    native.validate_repo(repo)
    observed = native.replacement_json(Path(inventory_file).read_text(encoding="utf-8"))
    require(isinstance(observed, dict) and observed.get("repository") == repo
            and isinstance(observed.get("groups"), list), "inventory repository or groups are invalid")
    native.maintenance.validate_target(repo, 1, observed.get("base_sha"))
    require(isinstance(group_id, str) and re.fullmatch(r"[0-9a-f]{24}", group_id), "invalid inventory group")
    require(all(isinstance(group, dict) for group in observed["groups"]), "invalid inventory group entries")
    matches = [group for group in observed["groups"] if group.get("group") == group_id]
    require(len(matches) == 1, "inventory must contain exactly one matching group")
    group = matches[0]
    require(group.get("next_action", {}).get("action") in ("prepare_replacement", "maintain_replacement")
            and group.get("blockers") == [], "inventory group is not eligible for replacement")
    require(group.get("ecosystem") in ("go", "npm", "actions") and isinstance(group.get("family"), str)
            and bool(group["family"]), "unsupported inventory ecosystem or family")
    require(isinstance(group.get("sources"), list) and all(isinstance(source, dict) for source in group["sources"]),
            "invalid inventory source group")
    native.validate_sources([{"number": source.get("number"), "head_sha": source.get("head_sha")}
                             for source in group["sources"]])
    manifests = set()
    for source in group["sources"]:
        require(source.get("group") == group_id and source.get("ecosystem") == group["ecosystem"]
                and source.get("family") == group["family"] and isinstance(source.get("intents"), list)
                and bool(source["intents"]), "incomplete or inconsistent source intent group")
        for intent in source["intents"]:
            require(isinstance(intent, dict) and intent.get("ecosystem") == group["ecosystem"]
                    and isinstance(intent.get("package"), str),
                    "source intent does not belong to selected ecosystem")
            manifests.add(native.safe_path(intent.get("manifest")))
            locks = intent.get("lock_manifests", [])
            require(isinstance(locks, list), "invalid source lock manifests")
            manifests.update(native.safe_path(path) for path in locks)
        require(any(native.upgrade_family(group["ecosystem"], intent["package"]) == group["family"]
                    for intent in source["intents"]), "source lacks selected upgrade family")
    return observed["base_sha"], group, manifests


def make_targets(text):
    # Discover explicit Make targets, never recipes or agent-supplied command text.
    return {target for match in re.finditer(r"(?m)^([A-Za-z0-9_. /-]+):(?!=)", text)
            for target in match[1].split() if "%" not in target}


def consumer_plan(root, base, group, manifests, compiler):
    ecosystem = group["ecosystem"]
    available = set(git(root, "ls-tree", "-r", "--name-only", base).splitlines())
    if ecosystem == "npm":
        roots = sorted({str(PurePosixPath(path).parent) for path in manifests})
        plan = []
        for directory in roots:
            prefix = "" if directory == "." else directory + "/"
            require(prefix + "package.json" in available, "unsupported npm consumer: missing package.json in " + directory)
            require(prefix + "package-lock.json" in available or prefix + "npm-shrinkwrap.json" in available,
                    "unsupported npm consumer: no committed lock in " + directory)
            makefile = prefix + "Makefile"
            require(makefile in available, "unsupported npm consumer: no repository Makefile in " + directory)
            targets = make_targets(git(root, "show", base + ":" + makefile))
            selected = API_TARGETS if directory == "api" else ("fmt", "lint", "test")
            require(set(selected) <= targets, "unsupported npm consumer " + directory
                    + ": missing fixed Make consumers " + ", ".join(sorted(set(selected) - targets)))
            plan.append(("npm-" + directory.replace("/", "-"), ("npm", "ci"), directory))
            plan.extend(("npm-" + directory.replace("/", "-") + "-" + target,
                         ("make", "-C", directory, target), ".") for target in selected)
        return plan
    if ecosystem == "go":
        require(all(PurePosixPath(path).name == "go.mod" for path in manifests),
                "unsupported Go source manifest; expected go.mod consumers")
        require("go.work" in available and "image-sync/oc-mirror/acrauth/go.mod" in available,
                "unsupported Go workspace: missing workspace or independent acrauth consumer")
        targets = make_targets(git(root, "show", base + ":Makefile"))
        wanted = {"all-tidy", "vendor", "verify-vendor", "test-compile", "test-unit", "lint", "verify"}
        require(wanted <= targets, "unsupported Go workspace: missing fixed Make consumers "
                + ", ".join(sorted(wanted - targets)))
        return [("go-" + target, ("make", target), ".")
                for target in ("verify-vendor", "test-compile", "test-unit", "lint", "verify")]
    require(compiler is not None, "Actions validation requires --compiler PATH to the repository-pinned compiler")
    compiler = Path(compiler).resolve()
    require(compiler.is_file() and os.access(compiler, os.X_OK), "Actions compiler is not executable")
    affected = set(manifests) | set(paths(root, base))
    sources = set()
    for path in affected:
        if path.startswith(".github/workflows/") and path.endswith(".md"):
            sources.add(path)
        elif path.endswith(".lock.yml"):
            source = path.removesuffix(".lock.yml") + ".md"
            require(source in available, "unsupported Actions consumer: generated lock has no live source: " + path)
            sources.add(source)
        elif path.startswith(".github/workflows/") and path.endswith((".yml", ".yaml")) and path not in available:
            stem = re.sub(r"\.(?:yml|yaml)$", "", path)
            source = stem + ".md"
            require(source in available and stem + ".lock.yml" in available,
                    "unsupported Actions consumer: obsolete workflow has no live source: " + path)
            sources.add(source)
    # actions-lock.json can select a pin used by any agentic source. Compile all
    # live sources to propagate it; unchanged locks must retain their provenance.
    if ".github/aw/actions-lock.json" in affected:
        sources.update(path for path in available if path.startswith(".github/workflows/") and path.endswith(".md"))
    plan = [("actions-" + PurePosixPath(source).stem,
             (str(compiler), "compile", PurePosixPath(source).stem, "--strict"), ".") for source in sorted(sources)]
    yaml_files = {path for path in affected if path in available and path.startswith(".github/workflows/")
                  and path.endswith((".yml", ".yaml"))}
    yaml_files.update(source.removesuffix(".md") + ".lock.yml" for source in sources)
    require(bool(yaml_files), "unsupported Actions consumer: no live YAML workflow to validate")
    require(".yamllint.yml" in available, "unsupported Actions consumer: missing repository yamllint policy")
    # Use the repository's existing MegaLinter YAML_YAMLLINT policy, including
    # for ordinary (non-agentic) workflows. A missing linter is a real failure.
    plan.append(("actions-yaml-lint", ("yamllint", "-c", ".yamllint.yml", *sorted(yaml_files)), "."))
    return plan


class Validator:
    def __init__(self, root, artifacts):
        self.root = Path(root).resolve()
        self.artifacts = Path(artifacts).resolve()
        require(not self.artifacts.is_relative_to(self.root), "ARTIFACT_DIR must be outside the candidate checkout")
        self.artifacts.mkdir(parents=True, exist_ok=True)
        # This fixed file belongs to the runner: stale success must not survive
        # a failed rerun. Never delete any other existing artifact.
        (self.artifacts / native.REQUEST_FILE).unlink(missing_ok=True)
        self.attempt = Path(tempfile.mkdtemp(prefix="native-validation-", dir=self.artifacts))
        self.results = {"status": "running", "steps": [], "attempt_directory": str(self.attempt)}
        self.save()

    def save(self):
        text = json.dumps(self.results, indent=2, sort_keys=True) + "\n"
        (self.attempt / RESULTS_FILE).write_text(text)
        (self.artifacts / RESULTS_FILE).write_text(text)

    def run(self, name, argv, *, cwd=None, env_overrides=None):
        cwd = self.root if cwd is None else Path(cwd)
        log = self.attempt / (f"{len(self.results['steps']) + 1:02d}-" + name + ".log")
        result = {"name": name, "argv": list(argv), "cwd": str(cwd), "log": str(log),
                  "environment": dict(env_overrides or {}),
                  "timeout_seconds": CONSUMER_TIMEOUT, "status": "running"}
        self.results["steps"].append(result)
        self.save()
        started = time.monotonic()
        env = os.environ.copy()
        env["CI"] = "true"
        env.update(env_overrides or {})
        try:
            with log.open("wb") as output:
                process = subprocess.Popen(list(argv), cwd=cwd, env=env, stdout=output,
                                           stderr=subprocess.STDOUT, start_new_session=True)
                try:
                    result["returncode"] = process.wait(timeout=CONSUMER_TIMEOUT)
                except subprocess.TimeoutExpired:
                    # Kill the whole session, including make's grandchildren.
                    os.killpg(process.pid, signal.SIGKILL)
                    result.update(status="timeout", returncode=process.wait())
                    raise ValueError(f"{name} exceeded {CONSUMER_TIMEOUT}s; full log: {log}")
            result["status"] = "passed" if result["returncode"] == 0 else "failed"
            require(result["returncode"] == 0, f"{name} exited {result['returncode']}; full log: {log}")
        except OSError as error:
            result.update(status="failed", error=str(error))
            raise ValueError(f"{name} could not start: {error}; full log: {log}") from error
        finally:
            result["duration_seconds"] = round(time.monotonic() - started, 3)
            self.save()
        return log

    def validated_body(self, proposed_body, base, head):
        steps = self.results["steps"]
        require(bool(steps) and all(step.get("status") == "passed" and step.get("returncode") == 0
                                    for step in steps), "cannot record validation evidence for incomplete consumers")
        summary = ["", "", "### Repository-owned candidate validation", "",
                   f"- Inventory base: `{base}`",
                   f"- Validated local candidate: `{head}`",
                   "- Dependency scope and complete source coverage: passed.",
                   "- These are local consumer results, not source-PR CI or deployed-state evidence.",
                   "- Full per-step logs and machine results are retained in the validation artifacts.", ""]
        for step in steps:
            arguments = [key + "=" + value for key, value in sorted(step.get("environment", {}).items())]
            command = shlex.join(arguments + step["argv"]).replace("`", "\\`")
            summary.append(f"- **{step['name']}**: passed (exit 0), `{command}`")
        excluded = self.results.get("excluded_baseline_identical_generation", [])
        if excluded:
            summary += ["", "Excluded only initially unchanged generated files whose bytes exactly matched "
                        "the isolated inventory-base generation:"]
            summary.extend("- `" + path + "`" for path in excluded)
        text = Path(proposed_body).read_text(encoding="utf-8").rstrip() + "\n".join(summary) + "\n"
        native.safe_text(text)
        destination = self.attempt / "native-validated-body.txt"
        destination.write_text(text, encoding="utf-8")
        return destination

    @contextmanager
    def baseline(self, base):
        with tempfile.TemporaryDirectory(prefix="native-base-", dir=self.attempt) as directory:
            checkout = Path(directory) / "checkout"
            git(self.root, "worktree", "add", "--detach", str(checkout), base)
            try:
                yield checkout
            finally:
                # Only this runner-created throwaway checkout is removed. The
                # caller's candidate, including failed generator output, remains.
                git(self.root, "worktree", "remove", "--force", str(checkout))

    def workspace_modules(self, step_name):
        log = self.run(step_name, ("go", "work", "edit", "-json"))
        workspace = json.loads(log.read_text())
        require(isinstance(workspace.get("Use"), list) and workspace["Use"],
                "Go workspace discovery returned no modules")
        modules = []
        for entry in workspace["Use"]:
            directory = entry.get("DiskPath") if isinstance(entry, dict) else None
            require(isinstance(directory, str) and directory.startswith("./"),
                    "unsupported Go workspace module path")
            relative = native.safe_path(directory.removeprefix("./"))
            require((self.root / relative / "go.mod").is_file(), "missing Go workspace module: " + relative)
            modules.append(relative)
        return modules

    def test_generated_go(self, changed, tree):
        internal = any(path.startswith("internal/azureapi/") and path.endswith(".go") for path in changed)
        sdk_paths = [path for path in changed if native.sdk_consumer(path)]
        sdk_go_paths = [path for path in sdk_paths
                        if path.endswith(".go") or PurePosixPath(path).name in ("go.mod", "go.sum")]
        if not internal and not sdk_paths:
            return
        workspace = (set(self.workspace_modules("npm-generated-workspace"))
                     if internal or sdk_go_paths else set())
        if internal:
            require("internal" in workspace, "unsupported generated API consumer: internal is outside the workspace")
            self.run("npm-generated-internal-test", ("go", "test", "-timeout", "1200s", "./internal/azureapi/..."))
            clean(self.root)
        available = set(git(self.root, "ls-tree", "-r", "--name-only", tree).splitlines())
        modules = set()
        for path in sdk_go_paths:
            directory = PurePosixPath(path).parent
            while str(directory) != "." and str(directory / "go.mod") not in available:
                directory = directory.parent
            require(native.sdk_consumer(str(directory / "go.mod")),
                    "unsupported generated SDK consumer: no generated module boundary for " + path)
            modules.add(str(directory))
        for index, directory in enumerate(sorted(modules), 1):
            self.run("npm-generated-sdk-test-" + str(index),
                     ("go", "test", "-mod=readonly", "-timeout", "1200s", "./..."),
                     cwd=self.root / directory,
                     env_overrides={} if directory in workspace else {"GOWORK": "off"})
            clean(self.root)
        if sdk_paths:
            # Root consumers use vendored copies of these local SDK modules.
            # Passing standalone SDK tests does not prove those copies are fresh.
            self.run("npm-generated-verify-vendor", ("make", "verify-vendor"))
            clean(self.root)

    def snapshot(self, base, group, *, complete=True):
        with tempfile.TemporaryDirectory(prefix="native-index-", dir=self.attempt) as directory:
            index = Path(directory) / "index"
            git(self.root, "read-tree", "HEAD", index=index)
            stage_candidate(self.root, base, index=index)
            tree = git(self.root, "write-tree", index=index)
        changed = [native.safe_path(path) for path in git(
            self.root, "diff", "--no-ext-diff", "--no-textconv", "--no-renames",
            "--name-only", "-z", base, tree).split("\0") if path]
        # Before generation, enforce dependency-only executable/configuration
        # scope but do not demand that yet-to-be-generated consumers cover the
        # target. The complete group is mandatory after generation and at handoff.
        native.validate_tree(self.root, base, tree, changed, group, complete=complete)
        return tree, changed

    def npm_baseline(self, base, initial, manifests):
        with self.baseline(base) as checkout:
            before = worktree_state(checkout)
            self.run("base-npm-api", ("npm", "ci"), cwd=checkout / "api")
            for target in API_GENERATORS:
                self.run("base-api-" + target, ("make", "-C", "api", target), cwd=checkout)
            after = worktree_state(checkout)
            drift = {path: state for path, state in after.items() if before.get(path) != state}
            drift.update({path: None for path in before.keys() - after.keys()})
            return {path: state for path, state in drift.items()
                    if generated_api_path(path) and path not in manifests and initial.get(path) == before.get(path)}

    def exclude_baseline_drift(self, base, drift):
        excluded = []
        for path, expected in sorted(drift.items()):
            if file_state(self.root, path) != expected:
                continue
            # Both the original candidate and clean base had identical bytes;
            # only this run's byte-identical generator drift is discarded.
            exists = bool(git(self.root, "ls-tree", base, "--", path))
            if exists:
                git(self.root, "restore", "--source=" + base, "--worktree", "--", path)
            else:
                (self.root / path).unlink(missing_ok=True)
            excluded.append(path)
        self.results["excluded_baseline_identical_generation"] = excluded
        self.save()

    def validate(self, repo, inventory_file, group_id, title_file, body_file, compiler):
        inputs = {Path(path).resolve(): Path(path).read_bytes() for path in (inventory_file, title_file, body_file)}
        base, group, manifests = load_group(repo, inventory_file, group_id)
        self.results.update(base_sha=base, group=group_id, ecosystem=group["ecosystem"],
                            family=group["family"], manifests=sorted(manifests))
        self.save()
        require(git(self.root, "rev-parse", "--verify", base + "^{commit}") == base,
                "inventory base commit is unavailable")
        require(git(self.root, "merge-base", base, "HEAD") == base,
                "candidate HEAD must descend from the inventory base; do not use a native PR checkout")
        staged = set(git(self.root, "diff", "--cached", "--name-only").splitlines())
        unstaged = set(git(self.root, "diff", "--name-only").splitlines())
        require(not staged.intersection(unstaged), "partially staged candidate paths; preserve or commit them before validation")
        initial = worktree_state(self.root)
        self.snapshot(base, group, complete=False)
        plan = consumer_plan(self.root, base, group, manifests, compiler)
        drift = {}
        if group["ecosystem"] == "npm" and any(str(PurePosixPath(path).parent) == "api" for path in manifests):
            drift = self.npm_baseline(base, initial, manifests)
        if group["ecosystem"] == "go":
            self.run("go-all-tidy", ("make", "all-tidy"))
            first = worktree_state(self.root)
            self.run("go-all-tidy-fixpoint", ("make", "all-tidy"))
            require(first == worktree_state(self.root), "make all-tidy did not reach a second-run fixpoint")
            self.run("go-vendor", ("make", "vendor"))
        else:
            for name, argv, directory in plan:
                self.run(name, argv, cwd=self.root / directory)
        if drift:
            self.exclude_baseline_drift(base, drift)
        tree, changed = self.snapshot(base, group)
        require(bool(changed), "candidate has no dependency changes")
        stage_candidate(self.root, base)
        require(git(self.root, "write-tree") == tree, "candidate changed while staging validated scope")
        if git(self.root, "diff", "--cached", "--name-only"):
            git(self.root, "commit", "-m", "chore: validate native dependency candidate")
        clean(self.root)
        head = git(self.root, "rev-parse", "HEAD")
        require(git(self.root, "rev-parse", "HEAD^{tree}") == tree, "candidate commit differs from validated tree")
        if group["ecosystem"] == "go":
            for name, argv, directory in plan:
                self.run(name, argv, cwd=self.root / directory)
                clean(self.root)
            modules = self.workspace_modules("go-workspace")
            self.run("go-build", ("go", "build", *("./" + module + "/..." for module in modules)))
            self.run("go-acrauth-build", ("go", "build", "./..."),
                     cwd=self.root / "image-sync/oc-mirror/acrauth", env_overrides={"GOWORK": "off"})
            self.run("go-acrauth-test", ("go", "test", "-timeout", "1200s", "./..."),
                     cwd=self.root / "image-sync/oc-mirror/acrauth", env_overrides={"GOWORK": "off"})
            clean(self.root)
        elif group["ecosystem"] == "npm":
            self.test_generated_go(changed, tree)
        for index, argv in enumerate(COMMON_GATES, 1):
            self.run("common-" + str(index), argv)
            clean(self.root)
        require(git(self.root, "rev-parse", "HEAD") == head, "candidate HEAD changed during validation")
        require(git(self.root, "rev-parse", "HEAD^{tree}") == tree, "candidate tree changed during validation")
        require(all(path.read_bytes() == content for path, content in inputs.items()),
                "inventory or publication text changed during validation")
        # Revalidate the exact committed scope, not only the initial manifests.
        native.validate_tree(self.root, base, tree, changed, group)
        clean(self.root)
        validated_body = self.validated_body(body_file, base, head)
        validated_body_bytes = validated_body.read_bytes()
        result = native.prepare_request(repo, inventory_file, group_id, title_file, validated_body,
                                        self.artifacts, cwd=self.root)
        # A late consumer/background write or input change must not leave a
        # publication artifact even if request construction itself succeeded.
        clean(self.root)
        require(git(self.root, "rev-parse", "HEAD") == head
                and validated_body.read_bytes() == validated_body_bytes
                and all(path.read_bytes() == content for path, content in inputs.items()),
                "candidate or request inputs changed during request construction")
        self.results.update(status="passed", candidate_head_sha=head, candidate_tree=tree)
        self.save()
        return result


def validate_candidate(repo, inventory_file, group_id, title_file, body_file, artifact_dir, *, compiler=None, cwd=None):
    root = git(cwd, "rev-parse", "--show-toplevel")
    validator = Validator(root, artifact_dir)
    try:
        return validator.validate(repo, inventory_file, group_id, title_file, body_file, compiler)
    except Exception as error:
        (validator.artifacts / native.REQUEST_FILE).unlink(missing_ok=True)
        validator.results.update(status="failed", error=str(error))
        validator.save()
        raise


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("repo")
    parser.add_argument("inventory_file")
    parser.add_argument("group")
    parser.add_argument("title_file")
    parser.add_argument("body_file")
    parser.add_argument("artifact_dir")
    parser.add_argument("--compiler")
    args = parser.parse_args(argv)
    try:
        result = validate_candidate(args.repo, args.inventory_file, args.group, args.title_file,
                                    args.body_file, args.artifact_dir, compiler=args.compiler)
    except (OSError, ValueError, KeyError, TypeError, subprocess.CalledProcessError) as error:
        print("native candidate validation failed: " + str(error), file=sys.stderr)
        return 1
    print(json.dumps(result, separators=(",", ":"), sort_keys=True))
    return 0


if __name__ == "__main__":
    sys.exit(main())
