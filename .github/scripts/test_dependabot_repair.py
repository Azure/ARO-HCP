"""Exercise the compiled owned-PR repair step against a local Git remote."""

import json
import os
import subprocess
import tempfile
import unittest
from pathlib import Path

import yaml


ROOT = Path(__file__).resolve().parents[2]


def git(cwd, *args):
    return subprocess.run(
        ["git", *args], cwd=cwd, check=True, capture_output=True, text=True
    ).stdout.strip()


class RepairTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        root = Path(self.temp.name)
        self.remote = root / "remote.git"
        self.seed = root / "seed"
        self.runner = root / "runner"
        self.bin = root / "bin"
        self.bin.mkdir()
        self.seed.mkdir()
        git(root, "init", "--bare", str(self.remote))
        git(self.seed, "init", "-b", "main")
        git(self.seed, "config", "user.name", "Test")
        git(self.seed, "config", "user.email", "test@example.com")
        self.manifest = self.seed / "api/package.json"
        self.makefile = self.seed / "api/Makefile"
        self.generated = self.seed / "internal/azureapi/v20261001preview/generated/models.go"
        for file, value in (
            (self.manifest, '{"typespec":"1.15.0"}\n'),
            (self.makefile, "models:\n\t@echo old\n"),
            (self.generated, "package generated\n"),
        ):
            file.parent.mkdir(parents=True, exist_ok=True)
            file.write_text(value)
        git(self.seed, "add", ".")
        git(self.seed, "commit", "-m", "original")
        git(self.seed, "remote", "add", "origin", str(self.remote))
        git(self.seed, "push", "origin", "main")
        git(self.seed, "switch", "-c", "bot/typespec")
        self.manifest.write_text('{"typespec":"1.16.0"}\n')
        git(self.seed, "add", ".")
        git(self.seed, "commit", "-m", "bump typespec")
        self.old_head = git(self.seed, "rev-parse", "HEAD")
        git(self.seed, "push", "origin", "bot/typespec")
        git(self.seed, "switch", "main")
        self.manifest.write_text('{"typespec":"1.15.0","other":"new"}\n')
        git(self.seed, "add", ".")
        git(self.seed, "commit", "-m", "main change conflicting with the bump")
        self.base = git(self.seed, "rev-parse", "HEAD")
        git(self.seed, "push", "origin", "main")
        self.manifest.write_text('{"typespec":"1.16.0","other":"new"}\n')
        self.makefile.write_text("models:\n\t@trap cleanup EXIT; echo fixed\n")
        self.generated.write_text("package generated\n\nconst Typespec = \"1.16.0\"\n")
        self.patch = git(
            self.seed, "diff", "--", "api/package.json", "api/Makefile",
            "internal/azureapi/v20261001preview/generated/models.go",
        ) + "\n"
        git(root, "clone", "--branch", "main", str(self.remote), str(self.runner))
        self.output = root / "output.json"
        self.command = (
            yaml.safe_load((ROOT / ".github/workflows/dependabot-remediation.md").read_text().split("---", 2)[1])
            ["safe-outputs"]["jobs"]["repair-owned-pr"]["steps"][2]["run"]
        )
        gh = self.bin / "gh"
        gh.write_text(
            "#!/bin/sh\n"
            "if [ \"$1\" = auth ]; then exit 0; fi\n"
            "if [ \"$1\" != api ]; then exit 1; fi\n"
            "sha=$(git --git-dir=\"$TEST_REMOTE\" rev-parse refs/heads/bot/typespec)\n"
            "if [ \"${3:-}\" = --jq ]; then printf '%s\\n' \"$sha\"; exit 0; fi\n"
            "printf '{\"state\":\"open\",\"user\":{\"login\":\"aro-hcp-robot[bot]\"},"
            "\"head\":{\"repo\":{\"full_name\":\"Azure/ARO-HCP\"},\"ref\":\"bot/typespec\",\"sha\":\"%s\"},"
            "\"base\":{\"ref\":\"main\"},\"title\":\"fix(deps): typespec to 1.16.0\","
            "\"labels\":[{\"name\":\"agentic-dependabot\"}]}' \"$sha\"\n"
        )
        gh.chmod(0o755)

    def execute(self, base=None):
        item = {
            "type": "repair_owned_pr",
            "pull_request_number": "42",
            "expected_head_sha": self.old_head,
            "expected_base_sha": base or self.base,
            "patch": self.patch,
        }
        self.output.write_text(json.dumps({"items": [item]}))
        env = os.environ.copy()
        env.update({
            "GH_AW_AGENT_OUTPUT": str(self.output),
            "RUNNER_TEMP": self.temp.name,
            "GH_TOKEN": "test-token",
            "REPO": "Azure/ARO-HCP",
            "TEST_REMOTE": str(self.remote),
            "PATH": f"{self.bin}:{env['PATH']}",
        })
        return subprocess.run(
            ["bash", "-c", self.command],
            cwd=self.runner, env=env, capture_output=True, text=True,
        )

    def test_dirty_branch_is_rebuilt_on_main_without_a_new_pr(self):
        result = self.execute()
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        new_head = git(self.seed, "--git-dir", str(self.remote), "rev-parse", "refs/heads/bot/typespec")
        self.assertNotEqual(new_head, self.old_head)
        self.assertEqual(git(self.seed, "--git-dir", str(self.remote), "rev-parse", f"{new_head}^"), self.base)
        self.assertIn(
            '"other":"new"', git(self.seed, "--git-dir", str(self.remote), "show", f"{new_head}:api/package.json"),
        )

    def test_stale_main_rejects_rebuild_without_moving_branch(self):
        result = self.execute(base=self.old_head)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("main moved", result.stderr)
        self.assertEqual(
            git(self.seed, "--git-dir", str(self.remote), "rev-parse", "refs/heads/bot/typespec"),
            self.old_head,
        )


if __name__ == "__main__":
    unittest.main()
