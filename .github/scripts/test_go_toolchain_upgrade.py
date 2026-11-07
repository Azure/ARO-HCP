#!/usr/bin/env python3
"""Deterministic consumer-level tests; no network, Docker daemon or Go required."""

import base64
from contextlib import ExitStack
import importlib.util
import io
import json
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest import mock


SPEC = importlib.util.spec_from_file_location("go_toolchain_upgrade", Path(__file__).with_name("go_toolchain_upgrade.py"))
upgrade = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(upgrade)


class VersionTests(unittest.TestCase):
    def test_latest_official_patch_stays_in_workspace_minor(self):
        releases = [{"version": "go1.27.3", "stable": True},
                    {"version": "go1.26.9", "stable": True},
                    {"version": "go1.26.10", "stable": True},
                    {"version": "go1.26.11rc1", "stable": False}]
        self.assertEqual(upgrade.latest_patch("1.26.0", releases), "1.26.10")

    def test_never_downgrades(self):
        with self.assertRaisesRegex(upgrade.Blocked, "refusing downgrade"):
            upgrade.latest_patch("1.26.11", [{"version": "go1.26.10", "stable": True}])

    def test_no_same_minor_release_waits(self):
        with self.assertRaises(upgrade.Blocked):
            upgrade.latest_patch("1.26.0", [{"version": "go1.27.0", "stable": True}])

    def test_dependency_pr_is_not_a_compiler_upgrade(self):
        self.assertFalse(upgrade.upgrade_patch([
            {"filename": "module/go.mod", "patch": "@@ -3 +3 @@\n-require google.golang.org/grpc v1.26.0\n+require google.golang.org/grpc v1.26.10"},
            {"filename": "module/go.sum", "patch": "-google.golang.org/protobuf v1.26.0 h1:old\n+google.golang.org/protobuf v1.26.10 h1:new"}]))

    def test_actual_directive_diff_identifies_every_owner(self):
        for directive in ("go 1.26.0", "toolchain go1.26.0"):
            with self.subTest(directive=directive):
                self.assertTrue(upgrade.upgrade_patch([
                    {"filename": "go.work", "patch": f"@@ -1 +1 @@\n-{directive}\n+{directive[:-1]}10"}]))

    def test_absent_compiler_diff_fails_closed(self):
        with self.assertRaisesRegex(upgrade.Blocked, "cannot inspect"):
            upgrade.upgrade_patch([{"filename": "module/go.mod"}])

    def test_devcontainer_node_feature_is_not_a_go_upgrade(self):
        before = {"image": "example.com/go:current", "features": {
            "ghcr.io/devcontainers/features/go:1": {"version": "1.26.0"},
            "ghcr.io/devcontainers/features/node:1": {"version": "22"}}}
        node = json.loads(json.dumps(before))
        node["features"]["ghcr.io/devcontainers/features/node:1"]["version"] = "24"
        files = [{"filename": upgrade.DEVCONFIG, "patch": '-"version": "22"\n+"version": "24"'}]
        self.assertFalse(upgrade.upgrade_patch(files, (json.dumps(before), json.dumps(node))))
        go = json.loads(json.dumps(before))
        go["features"]["ghcr.io/devcontainers/features/go:1"]["version"] = "1.26.10"
        self.assertTrue(upgrade.upgrade_patch(files, (json.dumps(before), json.dumps(go))))
        with self.assertRaisesRegex(upgrade.Blocked, "complete base/head"):
            upgrade.upgrade_patch(files)

    def test_open_upgrade_scan_stops_at_first_proven_blocker(self):
        prs = [{"number": 7689, "html_url": "https://example.com/7689"},
               {"number": 7690, "html_url": "https://example.com/7690"}]
        files = [{"filename": "go.work", "patch": "-go 1.26.0\n+go 1.26.10"}]
        with mock.patch.object(upgrade, "gh_pages", side_effect=[prs, files]) as pages, \
             mock.patch.object(upgrade, "gh_json", return_value={"changed_files": 1}) as detail:
            self.assertEqual(upgrade.open_upgrades("Azure/ARO-HCP"),
                             [{"number": 7689, "url": "https://example.com/7689"}])
        self.assertEqual(pages.call_count, 2)
        detail.assert_called_once_with("repos/Azure/ARO-HCP/pulls/7689")


class ManifestSemanticsTests(unittest.TestCase):
    def test_workspace_single_or_block_layout_has_equal_parsed_semantics(self):
        before = {"Go": "1.26.0", "Use": [{"DiskPath": "./service", "ModulePath": ""}], "Replace": None}
        after = {**before, "Go": "1.26.10"}
        with mock.patch.object(upgrade, "run", side_effect=[json.dumps(before), json.dumps(after)]) as command:
            old = upgrade.dependency_content("go 1.26.0\nuse (\n ./service\n)\n", True)
            new = upgrade.dependency_content("go 1.26.10\nuse ./service\n", True)
        self.assertEqual(old, new)
        for call in command.call_args_list:
            self.assertEqual(call.args[0][:4], ["go", "work", "edit", "-json"])
            self.assertEqual(call.kwargs["env"]["GOTOOLCHAIN"], "local")
            self.assertEqual(call.kwargs["env"]["GOWORK"], "off")

    def test_module_versions_and_replacements_are_semantic_but_indirect_and_order_are_not(self):
        before = {"Go": "1.26.0", "Module": {"Path": "example.com/service"},
                  "Require": [{"Path": "example.com/a", "Version": "v1.0.0", "Indirect": True},
                              {"Path": "example.com/b", "Version": "v2.0.0", "Indirect": False}],
                  "Replace": [{"Old": {"Path": "example.com/local"}, "New": {"Path": "../local"}}]}
        after = json.loads(json.dumps(before))
        after["Go"] = "1.26.10"
        after["Require"].reverse()
        after["Require"][0]["Indirect"] = True
        self.assertEqual(upgrade.manifest_semantics(before), upgrade.manifest_semantics(after))
        mutations = (("Module", {"Path": "example.com/different"}),
                     ("Require", [{"Path": "example.com/a", "Version": "v1.1.0"}]),
                     ("Replace", [{"Old": {"Path": "example.com/local"}, "New": {"Path": "../different"}}]))
        for key, value in mutations:
            with self.subTest(directive=key):
                self.assertNotEqual(upgrade.manifest_semantics(before),
                                    upgrade.manifest_semantics({**before, key: value}))

    def test_workspace_membership_changes_are_rejected(self):
        before = {"Go": "1.26.0", "Use": [{"DiskPath": "./service", "ModulePath": ""}], "Replace": None}
        after = {**before, "Use": [{"DiskPath": "./other", "ModulePath": ""}]}
        self.assertNotEqual(upgrade.manifest_semantics(before), upgrade.manifest_semantics(after))


class CheckoutTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name).resolve()
        self.write(".gitignore", "node_modules/\n")
        self.write("go.work", "go 1.26.0\n\nuse (\n\t./service\n)\n")
        self.write("service/go.mod", "module example.com/service\n\ngo 1.26.0\n\ntoolchain go1.26.0\n\nrequire google.golang.org/grpc v1.26.0\n")
        # Deliberately excluded from go.work, like oc-mirror/acrauth.
        self.write("outside/go.mod", "module example.com/outside\n\ngo 1.25.7\n\nrequire google.golang.org/protobuf v1.26.0\n")
        self.checksums = "google.golang.org/grpc v1.26.0 h1:untouched\ngoogle.golang.org/protobuf v1.26.0/go.mod h1:also-untouched\n"
        self.write("service/go.sum", self.checksums)
        self.write("api/Makefile", "TESTSDK_OUTPUT_DIR = ../generated/sdk/${VERSION}/client\n\ngenerate:\n\tprintf 'module github.com/Azure/ARO-HCP/internal/azureapi/$(VERSION)/generated\\n\\ngo 1.26.0\\n' > $(TYPESPEC_GO_OUTPUT_DIR)/go.mod; \\\n\techo done\n")
        self.bingo = "module _ // Fake go.mod auto-created by 'bingo'. DO NOT EDIT.\n"
        self.sdk = "module example.com/generated/sdk\n\ngo 1.25.0\n"
        self.write(".bingo/go.mod", self.bingo)
        self.write("generated/sdk/v20200101/client/go.mod", self.sdk)
        self.write(".ci-operator.yaml", "build_root_image:\n  namespace: ocp\n  name: builder\n  tag: current-builder\n")
        self.devconfig = ('{\n  "image": "example.com/dev:current",\n'
                          '  "features": {\n    "ghcr.io/devcontainers/features/go:1": {\n'
                          '      "version": "1.25.7" // Keep unrelated tooling unchanged.\n'
                          '    },\n    "ghcr.io/devcontainers/features/node:1": {"version": "latest"}\n'
                          '  },\n  "postCreateCommand": "./never-execute.sh"\n}\n')
        self.write(".devcontainer/devcontainer.json", self.devconfig)
        self.write("service/Dockerfile", "FROM example.com/golang:current AS builder\nRUN go build .\nFROM scratch\nCOPY --from=builder /app /app\n")
        self.write("generated.go", "// Code generated by fixture. DO NOT EDIT.\npackage generated\n")
        self.git("init", "-q")
        self.git("add", ".")
        self.git("-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-qm", "fixture")
        self.head = self.git("rev-parse", "HEAD")
        self.plan_path = Path(self.temp.name).parent / (self.root.name + "-plan.json")
        self.addCleanup(lambda: self.plan_path.unlink(missing_ok=True))
        self.real_run = upgrade.run

    def write(self, path, text):
        local = self.root / path
        local.parent.mkdir(parents=True, exist_ok=True)
        local.write_text(text)

    def git(self, *args):
        return upgrade.run(["git", *args], cwd=self.root)

    def ready_plan(self, **overrides):
        patches = dict(default_head=mock.Mock(return_value=self.head),
                       open_upgrades=mock.Mock(return_value=[]),
                       builder_readiness=mock.Mock(return_value=([
                           {"image": "example.com/golang:current", "digest": "example.com/golang@sha256:" + "a" * 64,
                            "version": "1.26.10"}], [], [])))
        patches.update(overrides)
        with ExitStack() as stack:
            for name, value in patches.items():
                stack.enter_context(mock.patch.object(upgrade, name, value))
            stack.enter_context(mock.patch.object(upgrade.urllib.request, "urlopen", return_value=io.StringIO(
                '[{"version":"go1.26.10","stable":true},{"version":"go1.27.1","stable":true}]')))
            plan = upgrade.make_plan(self.root, "Azure/ARO-HCP")
        self.plan_path.write_text(json.dumps(plan))
        return plan

    def fake_go(self, args, **kwargs):
        if args[:3] == ["go", "env", "GOVERSION"]:
            self.assertEqual(kwargs["env"]["GOTOOLCHAIN"], "local")
            return "go1.26.10"
        if args[:3] in (["go", "mod", "edit"], ["go", "work", "edit"]):
            self.assertEqual(kwargs["env"]["GOWORK"], "off")
            path = Path(args[-1])
            text = path.read_text()
            if "-json" in args:
                # Fixture-only command adapter; semantic edge cases use actual
                # Go JSON shapes above rather than reimplementing Go's parser.
                data = {"Go": upgrade.directives(text)["go"]}
                if args[1] == "work":
                    data["Use"] = [{"DiskPath": name, "ModulePath": ""}
                                   for name in upgrade.re.findall(r"\./[^\s()]+", text)]
                    data["Replace"] = None
                else:
                    data["Module"] = {"Path": upgrade.re.search(r"^module\s+(\S+)", text, upgrade.re.MULTILINE)[1]}
                    data["Require"] = [{"Path": match[0], "Version": match[1], "Indirect": False}
                                       for match in upgrade.re.findall(r"^(?:require\s+)?(\S+)\s+(v\S+)", text, upgrade.re.MULTILINE)]
                return json.dumps(data)
            for flag in args[3:-1]:
                if flag.startswith("-go="):
                    text = upgrade.re.sub(r"^go \S+", "go " + flag.split("=", 1)[1], text, flags=upgrade.re.MULTILINE)
                elif flag.startswith("-toolchain="):
                    text = upgrade.re.sub(r"^toolchain \S+", "toolchain " + flag.split("=", 1)[1], text, flags=upgrade.re.MULTILINE)
            path.write_text(text)
            return ""
        return self.real_run(args, **kwargs)

    def apply(self):
        with mock.patch.object(upgrade, "run", side_effect=self.fake_go):
            upgrade.apply_plan(self.root, self.plan_path)

    def check(self):
        with mock.patch.object(upgrade, "run", side_effect=self.fake_go):
            upgrade.check_plan(self.root, self.plan_path)

    def test_plan_includes_outside_workspace_and_exact_target(self):
        plan = self.ready_plan()
        self.assertEqual(plan["status"], "ready")
        self.assertEqual(plan["target"], "1.26.10")
        self.assertIn("outside/go.mod", [item["path"] for item in plan["modules"]])
        self.assertEqual({item["path"] for item in plan["excluded_modules"]},
                         {".bingo/go.mod", "generated/sdk/v20200101/client/go.mod"})

    def test_existing_human_native_or_robot_pr_waits_without_mutation(self):
        overlap = [{"number": 7689, "url": "https://github.com/Azure/ARO-HCP/pull/7689"}]
        builders = mock.Mock()
        before = (self.root / "service/go.mod").read_text()
        plan = self.ready_plan(open_upgrades=mock.Mock(return_value=overlap), builder_readiness=builders)
        self.assertEqual(plan["status"], "blocked")
        self.assertIn("7689", plan["reason"])
        builders.assert_not_called()
        self.assertEqual((self.root / "service/go.mod").read_text(), before)

    def test_builder_unavailable_or_old_is_normal_blocked_plan(self):
        for reason in ("cannot prove compiler in builder unavailable", "builder has Go 1.26.9, needs at least 1.26.10"):
            with self.subTest(reason=reason):
                plan = self.ready_plan(builder_readiness=mock.Mock(return_value=([], [], [reason])))
                self.assertEqual(plan["status"], "blocked")
                self.assertIn(reason, plan["reason"])

    def test_target_older_than_module_never_downgrades(self):
        self.write("outside/go.mod", "module example.com/outside\n\ngo 1.27.0\n")
        self.git("add", ".")
        self.git("-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-qm", "newer outside module")
        self.head = self.git("rev-parse", "HEAD")
        plan = self.ready_plan()
        self.assertEqual(plan["status"], "blocked")
        self.assertIn("refusing downgrade", plan["reason"])

    def test_missing_tracked_workspace_module_fails_closed(self):
        (self.root / "service/go.mod").unlink()
        with self.assertRaisesRegex(ValueError, "regular checkout file"):
            self.ready_plan()

    def test_unknown_application_module_missing_go_reports_filename(self):
        self.write("outside/go.mod", "module example.com/outside\n")
        with self.assertRaisesRegex(ValueError, r"outside/go\.mod: missing"):
            self.ready_plan()

    def test_generated_sdk_and_bingo_are_preserved_and_protected(self):
        self.ready_plan()
        self.apply()
        self.assertEqual((self.root / ".bingo/go.mod").read_text(), self.bingo)
        self.assertEqual((self.root / "generated/sdk/v20200101/client/go.mod").read_text(), self.sdk)
        self.check()
        self.write("generated/sdk/v20200101/client/go.mod", self.sdk.replace("1.25.0", "1.26.10"))
        with self.assertRaisesRegex(ValueError, "protected manifest"):
            self.check()

    def test_apply_changes_only_directives_and_recipe_preserving_checksum_keys(self):
        self.ready_plan()
        self.apply()
        for name in ("go.work", "service/go.mod", "outside/go.mod"):
            self.assertEqual(upgrade.directives((self.root / name).read_text())["go"], "1.26.10")
        self.assertEqual(upgrade.directives((self.root / "service/go.mod").read_text())["toolchain"], "1.26.10")
        self.assertIn("google.golang.org/grpc v1.26.0", (self.root / "service/go.mod").read_text())
        self.assertIn("google.golang.org/protobuf v1.26.0", (self.root / "outside/go.mod").read_text())
        self.assertEqual((self.root / "service/go.sum").read_text(), self.checksums)
        self.assertIn("\\n\\ngo 1.26.10\\n'", (self.root / "api/Makefile").read_text())
        dev = upgrade.jsonc((self.root / upgrade.DEVCONFIG).read_text())
        self.assertEqual(dev["image"], "example.com/dev:current")
        self.assertEqual(dev["features"]["ghcr.io/devcontainers/features/go:1"]["version"], "1.26.10")
        self.assertEqual(dev["features"]["ghcr.io/devcontainers/features/node:1"]["version"], "latest")
        self.check()

    def test_tampered_plan_minor_jump_or_write_path_is_rejected(self):
        plan = self.ready_plan()
        for bad in ({**plan, "target": "1.27.0"},
                    {**plan, "modules": [*plan["modules"], {"path": "../victim/go.mod"}]}):
            self.plan_path.write_text(json.dumps(bad))
            with self.assertRaises(ValueError):
                self.apply()
        self.assertEqual(upgrade.directives((self.root / "go.work").read_text())["go"], "1.26.0")

    def test_dependency_bump_after_tidy_is_rejected_staged_or_unstaged(self):
        self.ready_plan()
        self.apply()
        path = self.root / "service/go.mod"
        path.write_text(path.read_text().replace("grpc v1.26.0", "grpc v1.26.10"))
        self.git("add", "--update")
        with self.assertRaisesRegex(ValueError, "unrelated dependency"):
            self.check()

    def test_checksum_edit_after_generators_is_rejected(self):
        self.ready_plan()
        self.apply()
        self.write("service/go.sum", self.checksums.replace("v1.26.0", "v1.26.10"))
        with self.assertRaisesRegex(ValueError, "checksum"):
            self.check()

    def test_new_tracked_module_or_output_is_rejected(self):
        self.ready_plan()
        self.apply()
        self.write("unexpected", "output\n")
        with self.assertRaisesRegex(ValueError, "untracked"):
            self.check()
        self.git("add", "unexpected")
        with self.assertRaisesRegex(ValueError, "inventory"):
            self.check()

    def test_generated_tracked_go_changes_are_allowed(self):
        self.ready_plan()
        self.apply()
        self.write("generated.go", "// Code generated by fixture. DO NOT EDIT.\npackage generated\nconst Value = 1\n")
        self.git("add", "--update")
        self.check()

    def test_recheck_blocks_new_upgrade_or_default_branch_movement(self):
        self.ready_plan()
        self.apply()
        with mock.patch.object(upgrade, "default_head", return_value="moved"), mock.patch.object(upgrade, "run", side_effect=self.fake_go):
            with self.assertRaisesRegex(upgrade.Blocked, "default branch moved"):
                upgrade.recheck_plan(self.root, self.plan_path, "Azure/ARO-HCP")
        with mock.patch.object(upgrade, "default_head", return_value=self.head), \
             mock.patch.object(upgrade, "run", side_effect=self.fake_go), \
             mock.patch.object(upgrade, "open_upgrades", return_value=[{"number": 7689}]):
            with self.assertRaisesRegex(upgrade.Blocked, "7689"):
                upgrade.recheck_plan(self.root, self.plan_path, "Azure/ARO-HCP")

    def test_recheck_accepts_one_valid_upgrade_commit_and_revalidates_scope(self):
        plan = self.ready_plan()
        self.apply()
        self.git("add", "--update")
        self.git("-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-qm", "upgrade")
        with mock.patch.object(upgrade, "default_head", return_value=self.head), \
             mock.patch.object(upgrade, "run", side_effect=self.fake_go), \
             mock.patch.object(upgrade, "open_upgrades", return_value=[]), \
             mock.patch.object(upgrade, "builder_readiness", return_value=(plan["builders"], [], [])), \
             mock.patch.object(upgrade, "draft_notifications_safe") as notifications:
            upgrade.recheck_plan(self.root, self.plan_path, "Azure/ARO-HCP")
            notifications.assert_called_once_with()
        self.write("generated.go", "package malicious\n")
        with mock.patch.object(upgrade, "run", side_effect=self.fake_go):
            with self.assertRaisesRegex(ValueError, "unexpected generated change"):
                upgrade.recheck_plan(self.root, self.plan_path, "Azure/ARO-HCP")

    def test_proposed_devcontainer_is_built_and_compiler_probed_by_immutable_id(self):
        immutable = "sha256:" + "b" * 64
        calls = []
        proposed_configs = []
        def command(args, **kwargs):
            calls.append(args)
            if args[0] == "npx":
                proposed_configs.append(upgrade.jsonc(Path(args[args.index("--config") + 1]).read_text()))
                self.assertEqual(args[2], upgrade.DEVCONTAINERS_CLI)
                self.assertIn("build", args)
                return '{"outcome":"success"}'
            if args[:3] == ["docker", "image", "inspect"]:
                return immutable
            if args[:2] == ["docker", "run"]:
                return "go version go1.26.10 linux/amd64"
            return ""
        with mock.patch.object(upgrade, "run", side_effect=command):
            builder = upgrade.probe_devcontainer(self.root, "1.26.10")
        self.assertEqual(builder["digest"], immutable)
        self.assertEqual(builder["digest_kind"], "docker-image-id")
        self.assertEqual(proposed_configs[0]["image"], "example.com/dev:current")
        self.assertEqual(proposed_configs[0]["features"]["ghcr.io/devcontainers/features/go:1"]["version"], "1.26.10")
        self.assertEqual((self.root / upgrade.DEVCONFIG).read_text(), self.devconfig)
        execution = next(args for args in calls if args[:2] == ["docker", "run"])
        self.assertIn(immutable, execution)
        self.assertIn("--network=none", execution)
        self.assertFalse(any(arg.startswith(("--mount", "--volume")) for arg in execution))

    def test_devcontainer_unrelated_feature_change_is_rejected(self):
        self.ready_plan()
        self.apply()
        path = self.root / upgrade.DEVCONFIG
        path.write_text(path.read_text().replace('"version": "latest"', '"version": "22"'))
        with self.assertRaisesRegex(ValueError, "unrelated configuration"):
            self.check()

    def test_builder_inventory_uses_live_root_and_local_images_not_guessed_tags(self):
        config = {"build_root": {"image_stream_tag": {"namespace": "live", "name": "actual-root", "tag": "published"}},
                  "base_images": {"nested": {"namespace": "live", "name": "nested", "tag": "published"},
                                  "copy-only": {"namespace": "ci", "name": "utility", "tag": "latest"}},
                  "images": {"items": [{"from": "nested", "to": "runner", "dockerfile_literal": "FROM nested\n"}]}}
        listing = [{"name": "Azure-ARO-HCP-main.yaml", "path": upgrade.RELEASE_PATH + "/Azure-ARO-HCP-main.yaml"}]
        release_sha = "d" * 40
        def api(endpoint):
            if endpoint == "repos/openshift/release":
                return {"default_branch": "main"}
            if endpoint == "repos/openshift/release/commits/main":
                return {"sha": release_sha}
            self.assertTrue(endpoint.endswith("?ref=" + release_sha))
            if endpoint.endswith("ARO-HCP?ref=" + release_sha):
                return listing
            return {"encoding": "base64", "sha": "live-config-sha", "content": base64.b64encode(json.dumps(config).encode()).decode()}
        with mock.patch.object(upgrade, "gh_json", side_effect=api):
            images, sources, constraints = upgrade.discover_builders(self.root, "Azure/ARO-HCP")
        self.assertIn("registry.ci.openshift.org/live/actual-root:published", images)
        self.assertIn("registry.ci.openshift.org/live/nested:published", images)
        self.assertIn("example.com/golang:current", images)
        self.assertNotIn("example.com/dev:current", images)  # Go feature overrides base compiler.
        self.assertNotIn("registry.ci.openshift.org/ci/utility:latest", images)
        self.assertEqual(sources[0]["sha"], "live-config-sha")
        self.assertEqual(sources[0]["revision"], release_sha)
        self.assertEqual(constraints, [])

    def test_live_inline_root_and_no_build_periodic_are_discovered(self):
        literal = "FROM example.com/golang:ci-capz\nRUN go install example.com/testtool@v1.0.0\n"
        configs = {
            "Azure-ARO-HCP-main.yaml": {"build_root": {"from_repository": True}},
            "Azure-ARO-HCP-main__capz-e2e.yaml": {"build_root": {"project_image": {"dockerfile_literal": literal}}},
            "Azure-ARO-HCP-main__periodic.yaml": {"tests": [{"steps": {"workflow": "prebuilt-tests"}}]},
        }
        listing = [{"name": name, "path": upgrade.RELEASE_PATH + "/" + name} for name in configs]
        release_sha = "e" * 40
        def api(endpoint):
            if endpoint == "repos/openshift/release":
                return {"default_branch": "main"}
            if endpoint == "repos/openshift/release/commits/main":
                return {"sha": release_sha}
            self.assertTrue(endpoint.endswith("?ref=" + release_sha))
            if endpoint.endswith("ARO-HCP?ref=" + release_sha):
                return listing
            name = endpoint.split("/")[-1].split("?")[0]
            return {"encoding": "base64", "sha": name, "content": base64.b64encode(json.dumps(configs[name]).encode()).decode()}
        with mock.patch.object(upgrade, "gh_json", side_effect=api):
            images, sources, projects = upgrade.discover_builders(self.root, "Azure/ARO-HCP")
        self.assertIn("example.com/golang:ci-capz", images)
        self.assertEqual(projects[0]["dockerfile"], literal)
        self.assertEqual(len(sources), 3)
        self.assertEqual({item["revision"] for item in sources}, {release_sha})

    def test_incomplete_live_config_inventory_is_blocked(self):
        responses = [{"default_branch": "main"}, {"sha": "f" * 40}, []]
        with mock.patch.object(upgrade, "gh_json", side_effect=responses):
            with self.assertRaisesRegex(upgrade.Blocked, "inventory incomplete"):
                upgrade.discover_builders(self.root, "Azure/ARO-HCP")

    def test_installed_ignored_native_dependencies_cannot_enter_published_diff(self):
        self.ready_plan()
        self.apply()
        self.write("api/node_modules/native.node", "temporary")
        binary = self.root / "api/node_modules/native.node"
        binary.write_bytes(b"\x7fELF" + b"\x00" * 64)
        self.check()  # npm-installed dependencies are not part of the upgrade.
        self.git("add", "--force", "api/node_modules/native.node")
        with self.assertRaisesRegex(ValueError, "inventory"):
            self.check()


class BuilderTests(unittest.TestCase):
    def test_probe_executes_digest_isolated_without_mounts_or_network(self):
        immutable = "example.com/golang@sha256:" + "a" * 64
        calls = []
        def command(args, **kwargs):
            calls.append(args)
            if args[:3] == ["docker", "image", "inspect"]:
                return json.dumps([immutable])
            if args[:2] == ["docker", "run"]:
                return "go version go1.26.10 linux/amd64"
            return ""
        with mock.patch.object(upgrade, "run", side_effect=command):
            builder = upgrade.probe_builder("example.com/golang:current")
        self.assertEqual(builder["digest"], immutable)
        execution = calls[-1]
        self.assertIn(immutable, execution)
        self.assertNotIn("example.com/golang:current", execution)
        self.assertIn("--network=none", execution)
        self.assertIn("--read-only", execution)
        self.assertIn("--cap-drop=ALL", execution)
        self.assertFalse(any(arg.startswith(("--mount", "--volume")) for arg in execution))

    def test_probe_unavailable_is_blocked_not_error(self):
        error = subprocess.CalledProcessError(1, ["docker", "pull"], stderr="registry unavailable")
        with mock.patch.object(upgrade, "run", side_effect=error):
            with self.assertRaisesRegex(upgrade.Blocked, "registry unavailable"):
                upgrade.probe_builder("example.com/golang:current")

    def test_registry_auth_and_invalid_configuration_are_errors_not_waits(self):
        for message in ("unauthorized", "forbidden", "denied", "insufficient_scope", "invalid reference format"):
            with self.subTest(message=message):
                error = subprocess.CalledProcessError(1, ["docker", "pull"], stderr=message)
                with mock.patch.object(upgrade, "run", side_effect=error):
                    with self.assertRaises((ValueError, subprocess.CalledProcessError)):
                        upgrade.probe_builder("example.com/golang:current")

    def test_composite_authorization_errors_remain_errors(self):
        error = subprocess.CalledProcessError(1, ["docker", "buildx"], stderr="registry unauthorized")
        with mock.patch.object(upgrade, "run", side_effect=error):
            with self.assertRaisesRegex(ValueError, "authorization failure"):
                upgrade.probe_project({"source": "capz", "dockerfile": "FROM example.com/go:current\n"})

    def test_privileged_or_secret_inline_builds_are_rejected_before_execution(self):
        directives = ("RUN --network=host curl example.com", "RUN --security=insecure echo x",
                      "RUN --mount=type=secret,id=token echo x", "RUN --mount=type=ssh echo x",
                      "# syntax=untrusted.example.com/frontend\nRUN echo x")
        for directive in directives:
            with self.subTest(directive=directive), mock.patch.object(upgrade, "run") as command:
                with self.assertRaisesRegex(ValueError, "unsafe"):
                    upgrade.probe_project({"source": "capz", "dockerfile": "FROM example.com/go:current\n" + directive})
                command.assert_not_called()

    def test_old_builder_blocks_and_collects_actual_version(self):
        builder = {"image": "example.com/golang:current", "digest": "sha256:fixture", "version": "1.26.9"}
        with mock.patch.object(upgrade, "discover_builders", return_value=({builder["image"]: ["source"]}, [], [])), \
             mock.patch.object(upgrade, "probe_builder", return_value=builder), \
             mock.patch.object(upgrade, "probe_devcontainer", return_value=None):
            builders, _, reasons = upgrade.builder_readiness(Path("."), "Azure/ARO-HCP", "1.26.10")
        self.assertEqual(builders[0]["version"], "1.26.9")
        self.assertIn("needs at least 1.26.10", reasons[0])

    def test_unknown_variable_base_fails_closed(self):
        with self.assertRaisesRegex(upgrade.Blocked, "unresolved"):
            upgrade.docker_stages("FROM ${UNDECLARED}\nRUN go build .\n")

    def test_arg_defaults_resolve_actual_builder_base(self):
        stages = upgrade.docker_stages("ARG BUILDER_IMAGE_TAG=current\nFROM registry.example.com/ocp/builder:${BUILDER_IMAGE_TAG}\n")
        self.assertEqual(stages[0]["image"], "registry.example.com/ocp/builder:current")

    def test_all_real_builder_kinds_can_become_ready(self):
        base = {"image": "example.com/golang:current", "digest": "sha256:base", "version": "1.26.10"}
        project = {"image": "project-image:capz", "digest": "sha256:project", "version": "1.26.10"}
        dev = {"image": "devcontainer:base", "digest": "sha256:dev", "version": "1.26.10"}
        with mock.patch.object(upgrade, "discover_builders", return_value=({base["image"]: ["source"]}, [], [{"source": "capz"}])), \
             mock.patch.object(upgrade, "probe_builder", return_value=base), \
             mock.patch.object(upgrade, "probe_project", return_value=project), \
             mock.patch.object(upgrade, "probe_devcontainer", return_value=dev):
            builders, _, reasons = upgrade.builder_readiness(Path("."), "Azure/ARO-HCP", "1.26.10")
        self.assertEqual(reasons, [])
        self.assertEqual({item["image"] for item in builders}, {base["image"], project["image"], dev["image"]})

    def test_inline_root_builds_exact_live_dockerfile_and_executes_content_id(self):
        literal = "FROM example.com/golang:ci-capz\nRUN go install example.com/testtool@v1.0.0\n"
        immutable = "sha256:" + "c" * 64
        calls = []
        def command(args, **kwargs):
            calls.append(args)
            if args[:3] == ["docker", "buildx", "create"]:
                self.assertIn("memory=2g", args)
                self.assertIn("cpu-quota=200000", args)
            if args[:3] == ["docker", "buildx", "build"]:
                self.assertEqual(Path(args[-1], "Dockerfile").read_text(), literal)
                self.assertEqual(kwargs["timeout"], 1800)
                self.assertNotIn("--network=host", args)
            if args[:3] == ["docker", "image", "inspect"]:
                return immutable
            if args[:2] == ["docker", "run"]:
                return "go version go1.26.10 linux/amd64"
            return ""
        with mock.patch.object(upgrade, "run", side_effect=command):
            builder = upgrade.probe_project({"source": "capz", "dockerfile": literal})
        self.assertEqual(builder["digest"], immutable)
        self.assertEqual(builder["version"], "1.26.10")
        self.assertIn(immutable, next(args for args in calls if args[:2] == ["docker", "run"]))


class PublicationTests(unittest.TestCase):
    def test_draft_notification_gate_rejects_global_enable_or_local_override(self):
        for global_ignore, local in ((True, {}), (False, {}), (True, {"blunderbuss": {"ignore_drafts": False}})):
            with self.subTest(global_ignore=global_ignore, local=local):
                configs = [{"blunderbuss": {"ignore_drafts": global_ignore}}, {}, local]
                contents = [{"encoding": "base64", "content": base64.b64encode(json.dumps(config).encode()).decode()}
                            for config in configs]
                release_sha = "a" * 40
                responses = [{"default_branch": "main"}, {"sha": release_sha}, *contents]
                with mock.patch.object(upgrade, "gh_json", side_effect=responses) as api:
                    if global_ignore and not local:
                        upgrade.draft_notifications_safe()
                    else:
                        with self.assertRaises(upgrade.Blocked):
                            upgrade.draft_notifications_safe()
                    self.assertEqual(api.call_args_list[0], mock.call("repos/openshift/release"))
                    self.assertEqual(api.call_args_list[1], mock.call("repos/openshift/release/commits/main"))
                    self.assertTrue(all(call.args[0].endswith("?ref=" + release_sha)
                                        for call in api.call_args_list[2:]))


if __name__ == "__main__":
    unittest.main()
