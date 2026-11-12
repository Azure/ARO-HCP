"""Native candidate transitions with real isolated Git and fake consumer boundaries."""

from contextlib import contextmanager, redirect_stderr, redirect_stdout
import copy
import hashlib
import io
import json
import os
from pathlib import Path
import signal
import subprocess
import sys
import tempfile
import time
import unittest
from unittest.mock import patch

import native_dependabot_maintenance as native
import validate_native_candidate as runner


REPO = "Azure/ARO-HCP"
GENERATED = "internal/azureapi/v20261001/generated/models.go"
BASE_GENERATED = "package generated\n\nconst Model = 0\n"
BASELINE_GENERATED = "package generated\n\nconst Model = 1\n"
CANDIDATE_GENERATED = "package generated\n\nconst Model = 2\n"

SDK = "test/sdk/v20990101preview/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
VENDORED_SDK = "vendor/github.com/Azure/ARO-HCP/" + SDK


def git(root, *args):
    return subprocess.run(["git", *args], cwd=root, capture_output=True,
                          text=True, check=True).stdout.strip()


class CandidateFixture(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.directory = Path(self.temporary.name)
        self.repo = self.directory / "candidate"
        self.repo.mkdir()
        self.artifacts = self.directory / "artifacts"
        self.inventory = self.directory / "inventory.json"
        self.title = self.directory / "title.txt"
        self.body = self.directory / "body.txt"
        self.title.write_text("chore(deps): update prettier\n")
        self.body.write_text("Validate the complete native dependency group.\n")
        git(self.repo, "init", "-b", "main")
        git(self.repo, "config", "user.name", "Fixture")
        git(self.repo, "config", "user.email", "fixture@example.test")
        self.write(".gitignore", "node_modules\n__pycache__\n")
        self.write(".yamllint.yml", "rules:\n  key-duplicates: enable\n")
        self.write("vendor/example/THIRD PARTY NOTICES", "unchanged vendor notices\n")
        (self.repo / "vendor/example/notice-link").symlink_to("THIRD PARTY NOTICES")
        self.write("Makefile", "verify-yamlfmt:\n\ttrue\n")
        self.write("config/Makefile", "detect-change:\n\ttrue\n")
        self.write("api/Makefile", "".join(target + ":\n\ttrue\n" for target in runner.API_TARGETS))
        self.write(GENERATED, BASE_GENERATED)
        self.write("internal/go.mod", "module github.com/Azure/ARO-HCP/internal\n\ngo 1.26.0\n")
        self.write("go.work", "go 1.26.0\n\nuse (\n\t./internal\n)\n")
        self.workspace = ["./internal"]
        self.package = "prettier"
        self.family = "prettier"
        self.old = "3.9.9"
        self.new = "3.9.10"
        self.npm(self.old)
        git(self.repo, "add", ".")
        git(self.repo, "commit", "-m", "base")
        self.base = git(self.repo, "rev-parse", "HEAD")
        self.npm(self.new)
        self.observe()
        self.commands = []
        self.consumer_effect = None
        self.baseline_output = BASELINE_GENERATED
        self.candidate_output = BASELINE_GENERATED
        self.api_patch = patch.object(native, "api", side_effect=AssertionError("no API access expected"))
        self.api_patch.start()
        self.addCleanup(self.api_patch.stop)
        self.command_patch = patch.object(runner.Validator, "run", autospec=True, side_effect=self.consume)
        self.command_patch.start()
        self.addCleanup(self.command_patch.stop)

    def write(self, path, text, *, root=None):
        target = (self.repo if root is None else Path(root)) / path
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_text(text)

    def npm(self, version, *, directory="api", package=None):
        package = self.package if package is None else package
        manifest = {"devDependencies": {package: version}}
        lock = {"lockfileVersion": 3, "packages": {
            "": {"devDependencies": {package: version}},
            "node_modules/" + package: {"version": version, "resolved": "https://registry.npmjs.org/fixture"}}}
        self.write(directory + "/package.json", json.dumps(manifest) + "\n")
        self.write(directory + "/package-lock.json", json.dumps(lock) + "\n")

    def observe(self, *, ecosystem="npm", family=None, intents=None):
        family = self.family if family is None else family
        group_id = hashlib.sha256((ecosystem + ":" + family).encode()).hexdigest()[:24]
        if intents is None:
            intents = [{"ecosystem": ecosystem, "package": self.package, "old": self.old,
                        "target": self.new, "manifest": "api/package.json", "direct": True,
                        "key": ["devDependencies", self.package], "lock_manifests": ["api/package-lock.json"]}]
        source = {"number": 42, "head_sha": "a" * 40, "group": group_id,
                  "ecosystem": ecosystem, "family": family, "intents": intents}
        self.group = {"group": group_id, "ecosystem": ecosystem, "family": family,
                      "sources": [source], "blockers": [], "next_action": {"action": "prepare_replacement"}}
        self.inventory.write_text(json.dumps({"repository": REPO, "base_sha": self.base, "groups": [self.group]}))
        return group_id

    def consume(self, validator, name, argv, *, cwd=None, env_overrides=None):
        cwd = validator.root if cwd is None else Path(cwd)
        self.commands.append((name, tuple(argv), cwd, env_overrides))
        if self.consumer_effect:
            self.consumer_effect(validator, name, argv, cwd)
        if name in ("base-api-models", "npm-api-models"):
            text = self.baseline_output if name.startswith("base-") else self.candidate_output
            if text is not None:
                self.write(GENERATED, text, root=cwd)
        if name.startswith("common-") or name in ("go-verify-vendor", "go-test-compile", "go-test-unit", "go-lint", "go-verify"):
            self.assertNotEqual(git(self.repo, "rev-parse", "HEAD"), self.base,
                                "clean-tree gates must use a locally committed candidate")
        log = validator.attempt / (name + ".fixture.log")
        if name in ("go-workspace", "npm-generated-workspace"):
            log.write_text(json.dumps({"Use": [{"DiskPath": directory} for directory in self.workspace]}))
        else:
            log.write_text("fixture consumer boundary\n")
        validator.results["steps"].append({"name": name, "argv": list(argv), "cwd": str(cwd),
                                          "log": str(log), "status": "passed", "returncode": 0,
                                          "environment": dict(env_overrides or {}),
                                          "timeout_seconds": runner.CONSUMER_TIMEOUT})
        validator.save()
        return log

    def prepare_typespec(self):
        git(self.repo, "restore", "api/package.json", "api/package-lock.json")
        self.package, self.family = "@typespec/compiler", "typespec"
        self.old, self.new = "1.16.0", "1.17.0"
        self.npm(self.old)
        git(self.repo, "add", "api/package.json", "api/package-lock.json")
        git(self.repo, "commit", "-m", "TypeSpec base")
        self.base = git(self.repo, "rev-parse", "HEAD")
        self.npm(self.new)
        self.observe()
        self.candidate_output = CANDIDATE_GENERATED

    def prepare_go(self):
        git(self.repo, "restore", "api/package.json", "api/package-lock.json")
        self.package = self.family = "example.com/library"
        self.old, self.new = "v1.0.0", "v1.1.0"
        self.workspace = ["./backend"]
        self.write("go.work", "go 1.26.0\n\nuse (\n\t./backend\n)\n")
        targets = ("all-tidy", "vendor", "verify-vendor", "test-compile", "test-unit", "lint", "verify", "verify-yamlfmt")
        self.write("Makefile", "".join(target + ":\n\ttrue\n" for target in targets))
        for directory in ("backend", "image-sync/oc-mirror/acrauth"):
            self.write(directory + "/go.mod", self.go_module(directory, self.old))
        for directory in native.VENDORS:
            self.write(directory + "modules.txt", f"# {self.package} {self.old}\n## explicit; go 1.26.0\n{self.package}\n")
            self.write(directory + "example.com/library/library.go", "package library\nconst Version = 1\n")
        git(self.repo, "add", ".")
        git(self.repo, "commit", "-m", "Go base")
        self.base = git(self.repo, "rev-parse", "HEAD")
        self.write("backend/go.mod", self.go_module("backend", self.new))
        self.observe(ecosystem="go", intents=[{
            "ecosystem": "go", "package": self.package, "old": self.old, "target": self.new,
            "manifest": "backend/go.mod", "key": self.package, "direct": True}])
        self.consumer_effect = self.generate_go

    def ignore_vendor_and_scratch(self):
        self.write(".git/info/exclude", "".join("/" + directory + "\n" for directory in native.VENDORS)
                   + "/scratch-cache/\n")

    def prepare_sdk(self):
        self.prepare_typespec()
        git(self.repo, "restore", "api/package.json", "api/package-lock.json")
        self.write(SDK + "/go.mod", f"module github.com/Azure/ARO-HCP/{SDK}\n\ngo 1.26.0\n")
        for directory in (SDK, VENDORED_SDK):
            self.write(directory + "/models.go", BASE_GENERATED)
            self.write(directory + "/LICENSE", "SDK license baseline\n")
        makefile = self.repo / "Makefile"
        makefile.write_text(makefile.read_text() + "verify-vendor:\n"
                            + f"\tcmp {SDK}/models.go {VENDORED_SDK}/models.go\n"
                            + f"\tcmp {SDK}/LICENSE {VENDORED_SDK}/LICENSE\n")
        git(self.repo, "add", ".")
        git(self.repo, "commit", "-m", "generated SDK and vendored consumer base")
        self.base = git(self.repo, "rev-parse", "HEAD")
        self.npm(self.new)
        self.observe()

    def go_module(self, directory, version):
        return f"module github.com/Azure/ARO-HCP/{directory}\n\ngo 1.26.0\n\nrequire {self.package} {version}\n"

    def generate_go(self, validator, name, argv, cwd):
        if tuple(argv) == ("make", "all-tidy"):
            directory = "image-sync/oc-mirror/acrauth"
            self.write(directory + "/go.mod", self.go_module(directory, self.new))
        if tuple(argv) == ("make", "vendor"):
            for directory in native.VENDORS:
                self.write(directory + "modules.txt", f"# {self.package} {self.new}\n## explicit; go 1.26.0\n{self.package}\n")
                self.write(directory + "example.com/library/library.go", "package library\nconst Version = 2\n")

    def prepare_actions(self):
        git(self.repo, "restore", "api/package.json", "api/package-lock.json")
        old_pin, new_pin = "1" * 40, "2" * 40
        workflow = "name: CI\non: push\njobs:\n  build:\n    steps:\n      - uses: actions/checkout@" + old_pin + "\n"
        for suffix in (".md", ".lock.yml"):
            self.write(".github/workflows/agent" + suffix, workflow)
        self.write(".github/aw/actions-lock.json", self.action_lock(old_pin))
        git(self.repo, "add", ".")
        git(self.repo, "commit", "-m", "agentic Actions base")
        self.base = git(self.repo, "rev-parse", "HEAD")
        self.write(".github/workflows/agent.md", workflow.replace(old_pin, new_pin))
        self.observe(ecosystem="actions", family="actions/checkout", intents=[{
            "ecosystem": "actions", "package": "actions/checkout", "old": old_pin, "target": new_pin,
            "manifest": ".github/workflows/agent.md", "key": [5, "actions/checkout"], "direct": True}])
        return workflow.replace(old_pin, new_pin), new_pin

    def action_lock(self, pin):
        return json.dumps({"entries": {"actions/checkout@v1": {
            "repo": "actions/checkout", "version": "v1", "sha": pin}}})

    def validate(self, **kwargs):
        return runner.validate_candidate(REPO, self.inventory, self.group["group"], self.title,
                                         self.body, self.artifacts, cwd=self.repo, **kwargs)

    def assert_no_request(self):
        self.assertFalse((self.artifacts / native.REQUEST_FILE).exists())

    def results(self):
        return json.loads((self.artifacts / runner.RESULTS_FILE).read_text())

    @contextmanager
    def in_repo(self):
        previous = Path.cwd()
        os.chdir(self.repo)
        try:
            yield
        finally:
            os.chdir(previous)

    def test_prettier_success_excludes_only_proven_identical_baseline_drift(self):
        result = self.validate()
        request_bytes = (self.artifacts / native.REQUEST_FILE).read_bytes()
        self.assertEqual(result, {"request_sha256": hashlib.sha256(request_bytes).hexdigest()})
        request = json.loads(request_bytes)
        self.assertEqual(set(request), {"source_prs", "expected_base_sha", "patch", "title", "body"})
        self.assertEqual(request["source_prs"], [{"number": 42, "head_sha": "a" * 40}])
        self.assertEqual(request["expected_base_sha"], self.base)
        self.assertIn("### Repository-owned candidate validation", request["body"])
        self.assertIn("**npm-api-models**: passed (exit 0), `make -C api models`", request["body"])
        self.assertIn(self.base, request["body"])
        self.assertIn(git(self.repo, "rev-parse", "HEAD"), request["body"])
        self.assertEqual(self.body.read_text(), "Validate the complete native dependency group.\n")
        self.assertEqual(native.patch_paths(request["patch"]), ["api/package-lock.json", "api/package.json"])
        self.assertEqual((self.repo / GENERATED).read_text(), BASE_GENERATED)
        self.assertEqual(git(self.repo, "status", "--porcelain"), "")
        self.assertEqual(self.results()["excluded_baseline_identical_generation"], [GENERATED])
        self.assertEqual(self.results()["status"], "passed")
        names = [name for name, _, _, _ in self.commands]
        self.assertEqual(names[-2:], ["common-1", "common-2"])
        for target in runner.API_TARGETS:
            self.assertIn("npm-api-" + target, names)
        self.assertNotIn("base-api-lint-openapi", names)
        worktrees = git(self.repo, "worktree", "list", "--porcelain")
        self.assertNotIn("native-base-", worktrees)

    def test_failure_preserves_log_channel_and_removes_stale_request_only(self):
        self.artifacts.mkdir()
        (self.artifacts / native.REQUEST_FILE).write_text("stale success")
        sentinel = self.artifacts / "keep.log"
        sentinel.write_text("previous evidence")

        def fail(validator, name, argv, cwd):
            if name == "npm-api-swagger":
                raise ValueError("swagger exited 17; full log retained")

        self.consumer_effect = fail
        stdout, stderr = io.StringIO(), io.StringIO()
        with self.in_repo(), redirect_stdout(stdout), redirect_stderr(stderr):
            code = runner.main([REPO, str(self.inventory), self.group["group"], str(self.title),
                                str(self.body), str(self.artifacts)])
        self.assertEqual(code, 1)
        self.assertEqual(stdout.getvalue(), "")
        self.assertIn("swagger exited 17", stderr.getvalue())
        self.assert_no_request()
        self.assertEqual(sentinel.read_text(), "previous evidence")
        self.assertEqual(self.results()["status"], "failed")
        self.assertEqual(git(self.repo, "rev-parse", "HEAD"), self.base)
        self.assertFalse(any(name.startswith("common-") for name, _, _, _ in self.commands))

    def test_cli_success_stdout_is_only_the_digest_object(self):
        stdout, stderr = io.StringIO(), io.StringIO()
        with self.in_repo(), redirect_stdout(stdout), redirect_stderr(stderr):
            code = runner.main([REPO, str(self.inventory), self.group["group"], str(self.title),
                                str(self.body), str(self.artifacts)])
        self.assertEqual(code, 0)
        self.assertEqual(set(json.loads(stdout.getvalue())), {"request_sha256"})
        self.assertEqual(stderr.getvalue(), "")

    def test_real_candidate_generation_cannot_be_mistaken_for_base_drift(self):
        self.candidate_output = CANDIDATE_GENERATED
        with self.assertRaisesRegex(ValueError, "protected or unrelated replacement path"):
            self.validate()
        self.assertEqual((self.repo / GENERATED).read_text(), CANDIDATE_GENERATED)
        self.assert_no_request()
        self.assertFalse(any(name.startswith("common-") for name, _, _, _ in self.commands))

    def test_preexisting_generated_changes_are_not_reset(self):
        self.write(GENERATED, CANDIDATE_GENERATED)
        with self.assertRaisesRegex(ValueError, "protected or unrelated replacement path"):
            self.validate()
        self.assertEqual((self.repo / GENERATED).read_text(), CANDIDATE_GENERATED)
        self.assertEqual(self.commands, [])
        self.assert_no_request()

    def test_typespec_keeps_candidate_specific_generated_bytes_in_request(self):
        self.prepare_typespec()
        self.validate()
        request = json.loads((self.artifacts / native.REQUEST_FILE).read_text())
        self.assertIn(GENERATED, native.patch_paths(request["patch"]))
        self.assertEqual((self.repo / GENERATED).read_text(), CANDIDATE_GENERATED)
        self.assertEqual(self.results().get("excluded_baseline_identical_generation"), [])

    def test_changed_generated_internal_go_must_pass_real_consumer_boundary(self):
        self.prepare_typespec()

        def fail_generated(validator, name, argv, cwd):
            if argv[0] == "go" and "test" in argv and "./internal/azureapi/..." in argv:
                raise ValueError("generated internal Go consumer failed")

        self.consumer_effect = fail_generated
        with self.assertRaisesRegex(ValueError, "generated internal Go consumer failed"):
            self.validate()
        self.assert_no_request()
        self.assertNotEqual(git(self.repo, "rev-parse", "HEAD"), self.base)
        self.assertEqual((self.repo / GENERATED).read_text(), CANDIDATE_GENERATED)

    def test_generated_sdk_module_is_discovered_and_its_tests_can_block_publication(self):
        self.prepare_typespec()
        git(self.repo, "restore", "api/package.json", "api/package-lock.json")
        sdk = "test/sdk/v20990101preview/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
        self.write(sdk + "/go.mod", f"module github.com/Azure/ARO-HCP/{sdk}\n\ngo 1.26.0\n")
        self.write(sdk + "/models.go", BASE_GENERATED)
        git(self.repo, "add", ".")
        git(self.repo, "commit", "-m", "generated SDK base")
        self.base = git(self.repo, "rev-parse", "HEAD")
        self.npm(self.new)
        self.observe()

        def sdk_boundary(validator, name, argv, cwd):
            if name == "npm-api-testsdk":
                self.write(sdk + "/models.go", CANDIDATE_GENERATED)
            if argv[0] == "go" and "test" in argv and cwd == self.repo / sdk:
                raise ValueError("generated SDK consumer failed")

        self.consumer_effect = sdk_boundary
        with self.assertRaisesRegex(ValueError, "generated SDK consumer failed"):
            self.validate()
        self.assert_no_request()
        self.assertEqual((self.repo / sdk / "models.go").read_text(), CANDIDATE_GENERATED)

    def assert_stale_sdk_blocks(self, filename, content):
        self.prepare_sdk()
        self.candidate_output = BASE_GENERATED
        vendor_checks = []

        def sdk_boundary(validator, name, argv, cwd):
            if name == "npm-api-testsdk":
                self.write(SDK + "/" + filename, content)
            if name == "npm-generated-verify-vendor":
                # Exercise a real repository consumer against the on-disk SDK
                # and its committed root vendor copy, not a mocked failure code.
                result = subprocess.run(argv, cwd=cwd, capture_output=True, text=True)
                vendor_checks.append(result)
                if result.returncode:
                    raise ValueError("generated SDK vendor is stale")

        self.consumer_effect = sdk_boundary
        with self.assertRaisesRegex(ValueError, "generated SDK vendor is stale"):
            self.validate()
        self.assertEqual(len(vendor_checks), 1)
        self.assertNotEqual(vendor_checks[0].returncode, 0)
        self.assertEqual(git(self.repo, "show", "HEAD:" + SDK + "/" + filename), content.strip())
        self.assertNotEqual((self.repo / VENDORED_SDK / filename).read_text(), content)
        self.assert_no_request()
        self.assertFalse(any(name.startswith("common-") for name, _, _, _ in self.commands))

    def test_standalone_sdk_tests_cannot_publish_stale_root_vendor(self):
        self.assert_stale_sdk_blocks("models.go", CANDIDATE_GENERATED)
        self.assertTrue(any(name.startswith("npm-generated-sdk-test-") for name, _, _, _ in self.commands))

    def test_sdk_license_only_changes_still_require_vendor_verification(self):
        self.assert_stale_sdk_blocks("LICENSE", "SDK license changed\n")
        self.assertFalse(any(name.startswith("npm-generated-sdk-test-") for name, _, _, _ in self.commands))

    def test_typespec_recipe_noops_are_rejected_before_consumers(self):
        self.prepare_typespec()
        self.write("api/Makefile", "".join(target + ":\n\ttrue # disabled\n" for target in runner.API_TARGETS))
        with self.assertRaisesRegex(ValueError, "protected or unrelated replacement path"):
            self.validate()
        self.assertEqual(self.commands, [])
        self.assert_no_request()

    def test_typespec_generation_cannot_change_consumer_recipes(self):
        self.prepare_typespec()

        def disable_recipes(validator, name, argv, cwd):
            if name == "npm-api-testsdk":
                self.write("api/Makefile", "".join(target + ":\n\ttrue # disabled\n" for target in runner.API_TARGETS))

        self.consumer_effect = disable_recipes
        with self.assertRaisesRegex(ValueError, "protected or unrelated replacement path"):
            self.validate()
        self.assert_no_request()
        self.assertEqual(git(self.repo, "rev-parse", "HEAD"), self.base)

    def test_restored_committed_output_uses_current_bytes_in_snapshot_and_real_index(self):
        self.prepare_typespec()
        self.write(GENERATED, CANDIDATE_GENERATED)
        git(self.repo, "add", ".")
        git(self.repo, "commit", "-m", "previous candidate with generated output")
        previous_head = git(self.repo, "rev-parse", "HEAD")
        self.write(GENERATED, BASE_GENERATED)
        self.candidate_output = BASE_GENERATED
        validator = runner.Validator(self.repo, self.artifacts)
        tree, changed = validator.snapshot(self.base, self.group)
        self.assertNotIn(GENERATED, changed)
        self.assertEqual(git(self.repo, "show", tree + ":" + GENERATED), BASE_GENERATED.strip())
        self.assertEqual(git(self.repo, "show", ":" + GENERATED), CANDIDATE_GENERATED.strip(),
                         "isolated snapshots must not mutate the real index")
        self.validate()
        request = json.loads((self.artifacts / native.REQUEST_FILE).read_text())
        self.assertEqual(native.patch_paths(request["patch"]), ["api/package-lock.json", "api/package.json"])
        self.assertEqual(git(self.repo, "write-tree"), tree)
        self.assertEqual(self.results()["candidate_tree"], tree)
        self.assertEqual(git(self.repo, "show", "HEAD:" + GENERATED), BASE_GENERATED.strip())
        self.assertEqual(git(self.repo, "status", "--porcelain"), "")
        self.assertNotEqual(git(self.repo, "rev-parse", "HEAD"), previous_head)
        final_head = git(self.repo, "rev-parse", "HEAD")
        self.validate()
        self.assertEqual(git(self.repo, "rev-parse", "HEAD"), final_head)
        self.assertEqual(git(self.repo, "write-tree"), tree)
        self.assertEqual(git(self.repo, "status", "--porcelain"), "")

    def test_protected_source_changes_are_refused_before_execution(self):
        self.write("api/Makefile", "models:\n\tunsafe-command\n")
        with self.assertRaisesRegex(ValueError, "protected or unrelated replacement path"):
            self.validate()
        self.assertEqual(self.commands, [])
        self.assert_no_request()

    def test_unexpected_untracked_input_is_preserved_and_refused(self):
        self.write("inventory-scratch.json", "do not stage me\n")
        with self.assertRaisesRegex(ValueError, "protected or unrelated replacement path"):
            self.validate()
        self.assertEqual((self.repo / "inventory-scratch.json").read_text(), "do not stage me\n")
        self.assertEqual(self.commands, [])
        self.assert_no_request()

    def test_unexpected_consumer_output_is_not_silently_staged(self):
        def output(validator, name, argv, cwd):
            if name == "npm-api-testsdk":
                self.write("unexpected-output.txt", "diagnostic artifact\n")

        self.consumer_effect = output
        with self.assertRaisesRegex(ValueError, "protected or unrelated replacement path"):
            self.validate()
        self.assertTrue((self.repo / "unexpected-output.txt").exists())
        self.assert_no_request()

    def test_common_gate_must_leave_committed_candidate_clean(self):
        def dirty(validator, name, argv, cwd):
            if name == "common-1":
                self.write("new-post-validation-output.txt", "unexpected\n")

        self.consumer_effect = dirty
        with self.assertRaisesRegex(ValueError, "left candidate dirty"):
            self.validate()
        self.assert_no_request()
        self.assertFalse(any(name == "common-2" for name, _, _, _ in self.commands))

    def test_late_worktree_change_invalidates_newly_constructed_request(self):
        original = native.prepare_request

        def mutate(*args, **kwargs):
            result = original(*args, **kwargs)
            self.write("late-output.txt", "late write\n")
            return result

        with patch.object(native, "prepare_request", side_effect=mutate):
            with self.assertRaisesRegex(ValueError, "left candidate dirty"):
                self.validate()
        self.assert_no_request()
        self.assertEqual(self.results()["status"], "failed")

    def test_late_clean_commit_invalidates_newly_constructed_request(self):
        original = native.prepare_request

        def mutate(*args, **kwargs):
            result = original(*args, **kwargs)
            git(self.repo, "commit", "--allow-empty", "-m", "late commit")
            return result

        with patch.object(native, "prepare_request", side_effect=mutate):
            with self.assertRaisesRegex(ValueError, "changed during request construction"):
                self.validate()
        self.assert_no_request()

    def test_publication_text_changes_during_consumer_fail_closed(self):
        def mutate(validator, name, argv, cwd):
            if name == "common-2":
                self.body.write_text("changed after validation began\n")

        self.consumer_effect = mutate
        with self.assertRaisesRegex(ValueError, "publication text changed"):
            self.validate()
        self.assert_no_request()

    def test_unsupported_npm_make_convention_has_concrete_error(self):
        git(self.repo, "restore", "api/package.json", "api/package-lock.json")
        self.npm(self.old, directory="other")
        self.write("other/Makefile", "build:\n\ttrue\n")
        git(self.repo, "add", ".")
        git(self.repo, "commit", "-m", "other consumer")
        self.base = git(self.repo, "rev-parse", "HEAD")
        self.npm(self.new, directory="other")
        intent = {"ecosystem": "npm", "package": self.package, "old": self.old, "target": self.new,
                  "manifest": "other/package.json", "key": ["devDependencies", self.package], "direct": True,
                  "lock_manifests": ["other/package-lock.json"]}
        self.observe(intents=[intent])
        with self.assertRaisesRegex(ValueError, "unsupported npm consumer other: missing fixed Make consumers"):
            self.validate()
        self.assertEqual(self.commands, [])
        self.assert_no_request()

    def test_failure_in_another_group_source_consumer_blocks_publication(self):
        git(self.repo, "restore", "api/package.json", "api/package-lock.json")
        self.npm(self.old, directory="other")
        self.write("other/Makefile", "fmt:\n\ttrue\nlint:\n\ttrue\ntest:\n\ttrue\n")
        git(self.repo, "add", ".")
        git(self.repo, "commit", "-m", "other consumer")
        self.base = git(self.repo, "rev-parse", "HEAD")
        self.npm(self.new)
        self.npm(self.new, directory="other")
        self.observe()
        document = json.loads(self.inventory.read_text())
        other = copy.deepcopy(document["groups"][0]["sources"][0])
        other.update(number=43, head_sha="b" * 40)
        other["intents"][0].update(manifest="other/package.json", lock_manifests=["other/package-lock.json"])
        document["groups"][0]["sources"].append(other)
        self.inventory.write_text(json.dumps(document))
        def fail_other(validator, name, argv, cwd):
            if cwd == self.repo and tuple(argv) == ("make", "-C", "other", "test"):
                raise ValueError("other source consumer failed")

        self.consumer_effect = fail_other
        with self.assertRaisesRegex(ValueError, "other source consumer failed"):
            self.validate()
        self.assert_no_request()
        self.assertEqual(git(self.repo, "rev-parse", "HEAD"), self.base)

    def test_native_transitive_intents_outside_primary_family_are_validated(self):
        git(self.repo, "restore", "api/package.json", "api/package-lock.json")
        lock_path = self.repo / "api/package-lock.json"
        lock = json.loads(lock_path.read_text())
        lock["packages"]["node_modules/transitive"] = {"version": "1.0.0"}
        lock_path.write_text(json.dumps(lock))
        git(self.repo, "add", ".")
        git(self.repo, "commit", "-m", "transitive base")
        self.base = git(self.repo, "rev-parse", "HEAD")
        self.npm(self.new)
        lock = json.loads(lock_path.read_text())
        lock["packages"]["node_modules/transitive"] = {"version": "1.1.0"}
        lock_path.write_text(json.dumps(lock))
        self.observe()
        document = json.loads(self.inventory.read_text())
        document["groups"][0]["sources"][0]["intents"].append({
            "ecosystem": "npm", "package": "transitive", "old": "1.0.0", "target": "1.1.0",
            "manifest": "api/package-lock.json", "key": "node_modules/transitive", "direct": False})
        self.inventory.write_text(json.dumps(document))
        self.validate()
        request = json.loads((self.artifacts / native.REQUEST_FILE).read_text())
        self.assertIn('"1.1.0"', request["patch"])
        self.assertEqual(git(self.repo, "status", "--porcelain"), "")

    def test_ordinary_actions_yaml_consumer_failure_blocks_publication(self):
        git(self.repo, "restore", "api/package.json", "api/package-lock.json")
        old_pin, new_pin = "1" * 40, "2" * 40
        workflow = "name: CI\non: push\njobs:\n  build:\n    steps:\n      - uses: actions/checkout@" + old_pin + "\n"
        self.write(".github/workflows/ci.yml", workflow)
        git(self.repo, "add", ".")
        git(self.repo, "commit", "-m", "Actions base")
        self.base = git(self.repo, "rev-parse", "HEAD")
        self.write(".github/workflows/ci.yml", workflow.replace(old_pin, new_pin))
        self.observe(ecosystem="actions", family="actions/checkout", intents=[{
            "ecosystem": "actions", "package": "actions/checkout", "old": old_pin, "target": new_pin,
            "manifest": ".github/workflows/ci.yml", "key": [6, "actions/checkout"], "direct": True}])
        def fail_yaml(validator, name, argv, cwd):
            if argv[0] == "yamllint":
                raise ValueError("ordinary workflow YAML validation failed")

        self.consumer_effect = fail_yaml
        with self.assertRaisesRegex(ValueError, "ordinary workflow YAML validation failed"):
            self.validate(compiler=sys.executable)
        self.assert_no_request()
        self.assertEqual(git(self.repo, "rev-parse", "HEAD"), self.base)

    def test_actions_preflight_keeps_native_lock_allowlist_but_defers_stale_generated_coverage(self):
        _, new_pin = self.prepare_actions()
        self.write(".github/aw/actions-lock.json", self.action_lock(new_pin))
        validator = runner.Validator(self.repo, self.artifacts)
        with patch.object(native, "api", return_value={"sha": new_pin}):
            tree, changed = validator.snapshot(self.base, self.group, complete=False)
            with self.assertRaisesRegex(ValueError, "does not cover target"):
                native.validate_tree(self.repo, self.base, tree, changed, self.group)
        self.assert_no_request()
        self.assertEqual(git(self.repo, "rev-parse", "HEAD"), self.base)

    def test_actions_runner_regenerates_stale_consumers_before_strict_final_guard(self):
        workflow, new_pin = self.prepare_actions()

        def compile_consumer(validator, name, argv, cwd):
            if "compile" in argv:
                self.write(".github/workflows/agent.lock.yml", workflow)
                self.write(".github/aw/actions-lock.json", self.action_lock(new_pin))

        self.consumer_effect = compile_consumer
        with patch.object(native, "api", return_value={"sha": new_pin}):
            self.validate(compiler=sys.executable)
        request = json.loads((self.artifacts / native.REQUEST_FILE).read_text())
        changed = native.patch_paths(request["patch"])
        self.assertIn(".github/workflows/agent.md", changed)
        self.assertIn(".github/workflows/agent.lock.yml", changed)
        self.assertIn(".github/aw/actions-lock.json", changed)
        self.assertEqual(git(self.repo, "status", "--porcelain"), "")

    def test_actions_generator_cannot_change_workflow_behavior(self):
        workflow, new_pin = self.prepare_actions()

        def unsafe_compiler(validator, name, argv, cwd):
            if "compile" in argv:
                self.write(".github/workflows/agent.lock.yml", workflow.replace("on: push", "on: pull_request_target"))
                self.write(".github/aw/actions-lock.json", self.action_lock(new_pin))

        self.consumer_effect = unsafe_compiler
        with patch.object(native, "api", return_value={"sha": new_pin}):
            with self.assertRaisesRegex(ValueError, "non-pin Actions source change"):
                self.validate(compiler=sys.executable)
        self.assert_no_request()
        self.assertIn("pull_request_target", (self.repo / ".github/workflows/agent.lock.yml").read_text())
        self.assertEqual(git(self.repo, "rev-parse", "HEAD"), self.base)

    def test_unsupported_inventory_ecosystem_fails_without_any_consumer(self):
        document = json.loads(self.inventory.read_text())
        document["groups"][0]["ecosystem"] = "bundler"
        self.inventory.write_text(json.dumps(document))
        with self.assertRaisesRegex(ValueError, "unsupported inventory ecosystem"):
            self.validate()
        self.assert_no_request()
        self.assertEqual(self.commands, [])

    def test_validation_evidence_cannot_overflow_existing_publication_body_bound(self):
        self.body.write_text("x" * 20000)
        with self.assertRaisesRegex(ValueError, "invalid publication text"):
            self.validate()
        self.assert_no_request()
        self.assertEqual(self.body.read_text(), "x" * 20000)

    def test_actions_requires_compiler_even_when_source_is_not_agentic(self):
        with self.assertRaisesRegex(ValueError, "Actions validation requires --compiler"):
            runner.consumer_plan(self.repo, self.base, {"ecosystem": "actions"},
                                 {".github/workflows/ci.yml"}, None)

    def test_wrong_base_ancestry_is_rejected_before_any_consumer(self):
        git(self.repo, "stash", "push", "-m", "save candidate")
        git(self.repo, "checkout", "--orphan", "unrelated")
        git(self.repo, "commit", "-m", "unrelated root")
        with self.assertRaises((ValueError, subprocess.CalledProcessError)):
            self.validate()
        self.assert_no_request()
        self.assertEqual(self.commands, [])

    def test_partially_staged_paths_are_not_overwritten(self):
        git(self.repo, "add", "api/package.json")
        self.write("api/package.json", json.dumps({"devDependencies": {"prettier": "3.9.11"}}))
        with self.assertRaisesRegex(ValueError, "partially staged"):
            self.validate()
        self.assertIn("3.9.10", git(self.repo, "show", ":api/package.json"))
        self.assert_no_request()

    def test_go_generation_retains_both_vendor_consumers_and_commits_before_checks(self):
        self.prepare_go()
        self.validate()
        request = json.loads((self.artifacts / native.REQUEST_FILE).read_text())
        changed = native.patch_paths(request["patch"])
        for directory in native.VENDORS:
            self.assertIn(directory + "modules.txt", changed)
            self.assertIn(directory + "example.com/library/library.go", changed)
        self.assertIn("image-sync/oc-mirror/acrauth/go.mod", changed)
        self.assertEqual(git(self.repo, "status", "--porcelain"), "")
        self.assertIn("go-build", request["body"])
        self.assertIn("go-test-unit", request["body"])

    def assert_deleted_vendor_files_survive_rerun(self, *, committed):
        self.prepare_go()
        obsolete = [directory + "example.com/library/obsolete.go" for directory in native.VENDORS]
        for path in obsolete:
            self.write(path, "package library\nconst Obsolete = true\n")
        git(self.repo, "add", "--", *obsolete)
        git(self.repo, "commit", "-m", "vendor deletion baseline")
        self.base = git(self.repo, "rev-parse", "HEAD")
        inventory = json.loads(self.inventory.read_text())
        inventory["base_sha"] = self.base
        self.inventory.write_text(json.dumps(inventory))
        git(self.repo, "rm", "--", *obsolete)
        if committed:
            git(self.repo, "commit", "-m", "record vendor removals")
        self.validate()
        request = json.loads((self.artifacts / native.REQUEST_FILE).read_text())
        for path in obsolete:
            self.assertIn(path, native.patch_paths(request["patch"]))
            self.assertFalse((self.repo / path).exists())
            self.assertEqual(git(self.repo, "ls-files", "--", path), "")
        tree = git(self.repo, "write-tree")
        head = git(self.repo, "rev-parse", "HEAD")
        self.validate()
        self.assertEqual(git(self.repo, "write-tree"), tree)
        self.assertEqual(git(self.repo, "rev-parse", "HEAD"), head)
        self.assertEqual(git(self.repo, "status", "--porcelain"), "")
        self.assertEqual(json.loads((self.artifacts / native.REQUEST_FILE).read_text())["patch"], request["patch"])

    def test_staged_vendor_deletions_are_published_and_rerunnable(self):
        self.assert_deleted_vendor_files_survive_rerun(committed=False)

    def test_committed_vendor_deletions_are_published_and_rerunnable(self):
        self.assert_deleted_vendor_files_survive_rerun(committed=True)

    def test_ignored_vendor_additions_are_in_committed_patch_but_ignored_cache_is_not(self):
        self.prepare_go()
        self.ignore_vendor_and_scratch()
        calls = 0
        additions = [directory + "example.com/library/added.go" for directory in native.VENDORS]
        initial = [directory + "example.com/library/initial.go" for directory in native.VENDORS]
        content = "package library\nconst Added = 3\n"
        for path in initial:
            self.write(path, content)

        def generated(validator, name, argv, cwd):
            nonlocal calls
            calls += 1
            self.generate_go(validator, name, argv, cwd)
            self.write("scratch-cache/ignored.txt", str(calls))
            if tuple(argv) == ("make", "vendor"):
                for path in additions:
                    self.write(path, content)
            if name == "go-verify-vendor":
                for path in initial + additions:
                    self.assertEqual(git(self.repo, "show", "HEAD:" + path), content.strip())
                    self.assertEqual(git(self.repo, "show", ":" + path), content.strip())

        self.consumer_effect = generated
        self.validate()
        request = json.loads((self.artifacts / native.REQUEST_FILE).read_text())
        changed = native.patch_paths(request["patch"])
        for path in initial + additions:
            self.assertIn(path, changed)
            self.assertEqual((self.repo / path).read_text(), content)
        self.assertIn("+const Added = 3", request["patch"])
        self.assertNotIn("scratch-cache", request["patch"])
        self.assertEqual(git(self.repo, "ls-files", "--", "scratch-cache"), "")
        self.assertNotIn("scratch-cache/ignored.txt", runner.worktree_state(self.repo))
        self.assertNotIn("scratch-cache/ignored.txt", runner.paths(self.repo, self.base))
        self.assertEqual(self.results()["candidate_tree"], git(self.repo, "write-tree"))
        self.assertEqual(git(self.repo, "write-tree"), git(self.repo, "rev-parse", "HEAD^{tree}"))
        self.assertEqual(git(self.repo, "status", "--porcelain"), "")
        runner.clean(self.repo)

    def test_go_tidy_fixpoint_includes_new_ignored_vendor_files(self):
        self.prepare_go()
        self.ignore_vendor_and_scratch()
        tidy_runs = 0

        def unstable(validator, name, argv, cwd):
            nonlocal tidy_runs
            self.generate_go(validator, name, argv, cwd)
            if tuple(argv) == ("make", "all-tidy"):
                tidy_runs += 1
                if tidy_runs == 2:
                    for directory in native.VENDORS:
                        self.write(directory + "example.com/library/new.go", "package library\n")

        self.consumer_effect = unstable
        with self.assertRaisesRegex(ValueError, "did not reach a second-run fixpoint"):
            self.validate()
        self.assert_no_request()
        self.assertEqual(git(self.repo, "rev-parse", "HEAD"), self.base)
        self.assertFalse(any(name == "go-vendor" for name, _, _, _ in self.commands))

    def test_ignored_vendor_output_after_passing_checks_invalidates_request(self):
        self.prepare_go()
        self.ignore_vendor_and_scratch()
        original = native.prepare_request

        def late_output(*args, **kwargs):
            result = original(*args, **kwargs)
            for directory in native.VENDORS:
                self.write(directory + "example.com/library/late.go", "package library\n")
            return result

        with patch.object(native, "prepare_request", side_effect=late_output):
            with self.assertRaisesRegex(ValueError, "left candidate dirty"):
                self.validate()
        self.assert_no_request()
        self.assertEqual(self.results()["status"], "failed")
        self.assertTrue(any(name == "common-2" for name, _, _, _ in self.commands))
        for directory in native.VENDORS:
            path = directory + "example.com/library/late.go"
            self.assertTrue((self.repo / path).is_file())
            self.assertIn(path, runner.paths(self.repo, self.base))
            self.assertEqual(git(self.repo, "ls-files", "--", path), "")

    def test_go_unit_failure_does_not_prepare_request(self):
        self.prepare_go()

        def fail_unit(validator, name, argv, cwd):
            self.generate_go(validator, name, argv, cwd)
            if tuple(argv) == ("make", "test-unit"):
                raise ValueError("workspace unit tests failed")

        self.consumer_effect = fail_unit
        with self.assertRaisesRegex(ValueError, "workspace unit tests failed"):
            self.validate()
        self.assert_no_request()
        self.assertNotEqual(git(self.repo, "rev-parse", "HEAD"), self.base)

    def test_go_tidy_must_reach_fixpoint_before_commit(self):
        self.prepare_go()
        tidy_runs = 0

        def unstable(validator, name, argv, cwd):
            nonlocal tidy_runs
            self.generate_go(validator, name, argv, cwd)
            if tuple(argv) == ("make", "all-tidy"):
                tidy_runs += 1
                self.write("backend/go.sum", f"example.com/unstable v1.0.{tidy_runs} h1:AAAA=\n")

        self.consumer_effect = unstable
        with self.assertRaisesRegex(ValueError, "did not reach a second-run fixpoint"):
            self.validate()
        self.assert_no_request()
        self.assertEqual(git(self.repo, "rev-parse", "HEAD"), self.base)


class CommandBoundaryTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.directory = Path(self.temporary.name)
        self.root = self.directory / "candidate"
        self.root.mkdir()
        self.validator = runner.Validator(self.root, self.directory / "artifacts")

    def test_inherited_make_controls_cannot_hide_recipe_failures(self):
        (self.root / "Makefile").write_text("check:\n\t@echo executed > marker\n\t@exit 37\n")
        for name in ("MAKEFLAGS", "MFLAGS", "GNUMAKEFLAGS"):
            for flag in ("-i", "-n", "-q", "-t"):
                with self.subTest(variable=name, flag=flag):
                    (self.root / "marker").unlink(missing_ok=True)
                    with patch.dict(os.environ, {name: flag}):
                        with self.assertRaisesRegex(ValueError, "exited"):
                            self.validator.run("make-consumer", ("make", "check"))
                    self.assertEqual((self.root / "marker").read_text(), "executed\n")
                    step = self.validator.results["steps"][-1]
                    self.assertNotEqual(step["returncode"], 0)
                    self.assertEqual(step["status"], "failed")
                    self.assertIn("37", Path(step["log"]).read_text())

    def test_inherited_makefiles_cannot_override_consumers_or_recursive_checks(self):
        (self.root / "Makefile").write_text("check:\n\t@$(MAKE) -f child.mk verify\n")
        (self.root / "child.mk").write_text("verify:\n\t@echo checked > marker\n\t@exit 37\n")
        extra = self.directory / "inherited.mk"
        extra.write_text("override MAKEFLAGS += -i\n")
        controls = {"MAKEFILES": str(extra), "MAKELEVEL": "10", "MAKEOVERRIDES": "MAKEFLAGS=-i"}
        with patch.dict(os.environ, controls):
            with self.assertRaisesRegex(ValueError, "exited"):
                self.validator.run("recursive-consumer", ("make", "check"))
        self.assertEqual((self.root / "marker").read_text(), "checked\n")
        self.assertNotEqual(self.validator.results["steps"][-1]["returncode"], 0)

    def test_inherited_make_command_cannot_hide_recursive_failures(self):
        (self.root / "Makefile").write_text("check:\n\t@$(MAKE) -f child.mk verify\n")
        (self.root / "child.mk").write_text("verify:\n\t@echo executed > marker\n\t@exit 37\n")
        for command in ("make -i", "make -n", "true"):
            with self.subTest(command=command):
                (self.root / "marker").unlink(missing_ok=True)
                with patch.dict(os.environ, {"MAKE": command}):
                    with self.assertRaisesRegex(ValueError, "exited"):
                        self.validator.run("recursive-consumer", ("make", "check"))
                self.assertEqual((self.root / "marker").read_text(), "executed\n")
                step = self.validator.results["steps"][-1]
                self.assertNotEqual(step["returncode"], 0)
                self.assertIn("37", Path(step["log"]).read_text())

    def test_consumer_overrides_cannot_reenable_make_dry_run(self):
        (self.root / "Makefile").write_text("check:\n\t@echo checked > marker\n\t@exit 37\n")
        with self.assertRaisesRegex(ValueError, "exited"):
            self.validator.run("make-consumer", ("make", "check"), env_overrides={"MAKEFLAGS": "-n"})
        self.assertEqual((self.root / "marker").read_text(), "checked\n")

    def test_nonzero_preserves_full_combined_output_and_exit_status(self):
        text = "diagnostic" * 1000
        with self.assertRaisesRegex(ValueError, "exited 7"):
            self.validator.run("failing-consumer", (sys.executable, "-c",
                               "import sys;sys.stdout.write('start\\n');sys.stderr.write(" + repr(text) + ");sys.exit(7)"))
        step = self.validator.results["steps"][0]
        log = Path(step["log"]).read_text()
        self.assertIn("start", log)
        self.assertIn(text, log)
        self.assertEqual(step["status"], "failed")
        self.assertEqual(step["returncode"], 7)
        self.assertEqual(step["timeout_seconds"], runner.CONSUMER_TIMEOUT)

    def test_timeout_kills_entire_process_group_and_keeps_failure(self):
        child = ("import json,os,subprocess,sys,time;"
                 "grandchild=subprocess.Popen([sys.executable,'-c','import time;time.sleep(60)']);"
                 "print(json.dumps([os.getpid(),grandchild.pid]),flush=True);time.sleep(60)")
        parent_pid = None
        try:
            with patch.object(runner, "CONSUMER_TIMEOUT", 1):
                with self.assertRaisesRegex(ValueError, "exceeded 1s"):
                    self.validator.run("timeout", (sys.executable, "-c", child))
            step = self.validator.results["steps"][0]
            parent_pid, grandchild_pid = json.loads(Path(step["log"]).read_text())
            self.assertEqual(step["status"], "timeout")
            self.assertEqual(step["returncode"], -signal.SIGKILL)
            for pid in (parent_pid, grandchild_pid):
                deadline = time.monotonic() + 5
                while time.monotonic() < deadline:
                    try:
                        state = Path(f"/proc/{pid}/stat").read_text().split(") ", 1)[1].split()[0]
                    except FileNotFoundError:
                        break
                    # A killed orphan can await init reaping, but must not run.
                    if state == "Z":
                        break
                    time.sleep(0.01)
                else:
                    self.fail(f"consumer descendant {pid} survived the timeout")
        finally:
            if parent_pid is None and self.validator.results["steps"]:
                log = Path(self.validator.results["steps"][0]["log"])
                if log.exists() and log.read_text().strip():
                    parent_pid = json.loads(log.read_text())[0]
            if parent_pid is not None:
                try:
                    os.killpg(parent_pid, signal.SIGKILL)
                except ProcessLookupError:
                    pass

    def test_missing_consumer_is_failure_not_skip(self):
        with self.assertRaisesRegex(ValueError, "could not start"):
            self.validator.run("missing", (str(self.directory / "absent-tool"),))
        self.assertEqual(self.validator.results["steps"][0]["status"], "failed")

    def test_later_attempt_does_not_overwrite_failed_attempt_evidence(self):
        with self.assertRaisesRegex(ValueError, "exited 3"):
            self.validator.run("consumer", (sys.executable, "-c", "import sys;print('first evidence');sys.exit(3)"))
        first_step = self.validator.results["steps"][0]
        first_log = Path(first_step["log"])
        first_log_bytes = first_log.read_bytes()
        first_results = self.validator.attempt / runner.RESULTS_FILE
        first_result_bytes = first_results.read_bytes()
        second = runner.Validator(self.root, self.directory / "artifacts")
        second.run("consumer", (sys.executable, "-c", "print('second evidence')"))
        self.assertNotEqual(first_log.parent, Path(second.results["steps"][0]["log"]).parent)
        self.assertEqual(first_log.read_bytes(), first_log_bytes)
        self.assertEqual(first_results.read_bytes(), first_result_bytes)


if __name__ == "__main__":
    unittest.main()
