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

import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest

import tool_version_inventory as inventory


ROOT = Path(__file__).resolve().parents[2]
SCRIPT = ROOT / ".github/scripts/tool_version_inventory.py"
HELPER = ROOT / "dev-infrastructure/openshift-ci/update-tool-versions.sh"

# Run the real inventory CLI and metadata helper, replacing only upstream/Go
# commands. Fail the fixture if collection starts installing or mutating tools.
COMMAND_FIXTURE = '''
import json
import os
from pathlib import Path
import sys

config = json.loads(Path(os.environ["INVENTORY_FIXTURE"]).read_text())
args = sys.argv[1:]
name = Path(sys.argv[0]).name
with open(os.environ["INVENTORY_CALLS"], "a") as calls:
    calls.write(json.dumps([name, *args]) + "\\n")
if name == "gh" and args[0] == "api":
    value = config["releases"][args[1]]
elif name == "go" and args[0] == "-C":
    assert os.environ["GOWORK"] == "off"
    assert os.environ["GOTOOLCHAIN"] == "local"
    if args[2:5] == ["mod", "edit", "-json"]:
        value = config["pins"][args[5].removeprefix("-modfile=")]
    elif args[2:6] == ["list", "-m", "-json", args[-1]]:
        value = config["modules"][args[-1]]
    else:
        raise AssertionError("unexpected Go command: " + repr(args))
else:
    raise AssertionError("unexpected command: " + repr(sys.argv))
if isinstance(value, dict) and "exit" in value:
    print(value.get("stderr", "fixture lookup failure"), file=sys.stderr)
    sys.exit(value["exit"])
if isinstance(value, str):
    print(value)
else:
    print(json.dumps(value))
'''


class InventoryTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.directory = Path(self.temporary.name)
        self.root = self.directory / "repo"
        self.bingo = self.root / ".bingo"
        self.bingo.mkdir(parents=True)
        ci = self.root / "dev-infrastructure/openshift-ci"
        ci.mkdir(parents=True)
        shutil.copyfile(HELPER, ci / HELPER.name)
        (ci / "versions.mk").write_text("PROMTOOL_VERSION ?= 3.2.1\nBICEP_VERSION ?= 0.48.1\n")
        (ci / "Dockerfile").write_text("ARG PROMTOOL_VERSION=3.2.1\nARG BICEP_VERSION=0.48.1\n")
        self.config = {
            "pins": {}, "modules": {},
            "releases": {
                "https://api.github.com/repos/prometheus/prometheus/releases/latest":
                    {"tag_name": "v3.15.1", "draft": False, "prerelease": False},
                "https://api.github.com/repos/Azure/bicep/releases/latest":
                    {"tag_name": "v0.48.1", "draft": False, "prerelease": False},
            },
        }
        self.add_tool("generator-one", "example.org/generator", "v0.34.3", "v0.35.0")
        self.add_tool("generator-two", "example.org/generator", "v0.34.3", "v0.35.0")
        self.add_tool("brand-new-tool", "example.org/new/v2", "v2.3.0", "v2.2.0")
        # Fake root must not become an upstream family or invoke Go metadata.
        (self.bingo / "go.mod").write_text("module fake\n")
        self.bin = self.directory / "bin"
        self.bin.mkdir()
        for name in ("go", "gh"):
            executable = self.bin / name
            executable.write_text(f"#!{sys.executable}\n" + COMMAND_FIXTURE)
            executable.chmod(0o755)
        self.calls = self.directory / "calls.jsonl"

    def add_tool(self, name, module, current, latest):
        (self.bingo / f"{name}.mod").write_text(f"module _\nrequire {module} {current}\n")
        self.config["pins"][f"{name}.mod"] = {
            "Require": [{"Path": module, "Version": current, "Indirect": False}],
        }
        self.config["modules"][f"{module}@latest"] = {"Path": module, "Version": latest}

    def run_inventory(self):
        fixture = self.directory / "fixture.json"
        fixture.write_text(json.dumps(self.config))
        before = {str(path): path.read_bytes() for path in self.root.rglob("*") if path.is_file()}
        result = subprocess.run(
            [sys.executable, str(SCRIPT), "--root", str(self.root)],
            env={**os.environ, "PATH": str(self.bin) + os.pathsep + os.environ["PATH"],
                 "INVENTORY_FIXTURE": str(fixture), "INVENTORY_CALLS": str(self.calls)},
            capture_output=True, text=True, timeout=30,
        )
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        after = {str(path): path.read_bytes() for path in self.root.rglob("*") if path.is_file()}
        self.assertEqual(before, after, "inventory must not write repository files")
        output = json.loads(result.stdout)
        rows = {row["family"]: row for row in output["families"]}
        self.assertEqual(len(rows), len(output["families"]), "duplicate upstream families")
        return output, rows

    def test_cli_groups_dynamic_pins_and_classifies_numeric_versions(self):
        output, rows = self.run_inventory()
        self.assertTrue(output["complete"])
        self.assertEqual(output["errors"], [])
        self.assertEqual(rows["prometheus/prometheus"]["current"], "3.2.1")
        self.assertEqual(rows["prometheus/prometheus"]["latest"], "3.15.1")
        self.assertEqual(rows["prometheus/prometheus"]["status"], "outdated")
        self.assertEqual(rows["Azure/bicep"]["status"], "current")
        self.assertEqual(rows["example.org/new/v2"]["status"], "ahead")
        self.assertEqual(rows["example.org/generator"]["toolNames"], ["generator-one", "generator-two"])
        self.assertEqual(rows["example.org/generator"]["sourceURL"], "https://pkg.go.dev/example.org/generator")
        self.assertEqual(len(rows), 4)
        calls = [json.loads(line) for line in self.calls.read_text().splitlines()]
        lookups = [call for call in calls if "example.org/generator@latest" in call]
        self.assertEqual(len(lookups), 1, "shared upstream must only be resolved once")
        self.assertFalse(any("-modfile=go.mod" in call for call in calls))

    def test_api_failure_is_explicit_and_does_not_hide_other_families(self):
        self.config["releases"]["https://api.github.com/repos/prometheus/prometheus/releases/latest"] = {
            "exit": 1, "stderr": "HTTP 403: API rate limit exceeded",
        }
        output, rows = self.run_inventory()
        self.assertFalse(output["complete"])
        row = rows["prometheus/prometheus"]
        self.assertEqual(row["status"], "error")
        self.assertIsNone(row["latest"])
        self.assertIn("403", row["reason"])
        self.assertEqual(rows["Azure/bicep"]["status"], "current")

    def test_missing_or_malformed_latest_never_means_current(self):
        for response in ({}, {"tag_name": None, "draft": False, "prerelease": False}, "not json"):
            with self.subTest(response=response):
                self.config["releases"]["https://api.github.com/repos/Azure/bicep/releases/latest"] = response
                output, rows = self.run_inventory()
                self.assertFalse(output["complete"])
                self.assertEqual(rows["Azure/bicep"]["status"], "error")

    def test_prerelease_and_incoherent_family_are_blocked_not_current(self):
        self.config["modules"]["example.org/new/v2@latest"]["Version"] = "v2.4.0-rc.1"
        self.config["pins"]["generator-two.mod"]["Require"][0]["Version"] = "v0.33.0"
        _, rows = self.run_inventory()
        self.assertEqual(rows["example.org/new/v2"]["status"], "blocked")
        self.assertEqual(rows["example.org/generator"]["status"], "blocked")
        self.assertIsNone(rows["example.org/generator"]["current"])
        self.assertEqual(rows["example.org/generator"]["pins"][1]["current"], "v0.33.0")

    def test_unreadable_pin_and_toolchain_failure_remain_errors(self):
        self.config["pins"]["generator-one.mod"] = {"Require": []}
        self.config["modules"]["example.org/new/v2@latest"] = {
            "exit": 1, "stderr": "requires go >= 1.27 (running go 1.26; GOTOOLCHAIN=local)",
        }
        output, rows = self.run_inventory()
        self.assertFalse(output["complete"])
        self.assertEqual(rows["pin:.bingo/generator-one.mod"]["status"], "error")
        self.assertEqual(rows["example.org/new/v2"]["status"], "error")
        self.assertIn("requires go", rows["example.org/new/v2"]["reason"])
        self.assertEqual(rows["example.org/generator"]["toolNames"], ["generator-two"])

    def test_missing_ci_pin_is_not_silently_omitted(self):
        (self.root / "dev-infrastructure/openshift-ci/versions.mk").write_text("BICEP_VERSION ?= 0.48.1\n")
        output, rows = self.run_inventory()
        self.assertFalse(output["complete"])
        self.assertEqual(rows["prometheus/prometheus"]["status"], "error")
        self.assertIsNone(rows["prometheus/prometheus"]["current"])



class VersionTests(unittest.TestCase):
    def test_semver_precedence_not_lexical_order(self):
        versions = ["v1.0.0-alpha", "1.0.0-alpha.2", "1.0.0-alpha.10", "1.0.0-rc.1",
                    "1.0.0", "3.2.1", "3.15.1"]
        self.assertEqual(sorted(versions, key=inventory.version_key), versions)
        self.assertEqual(inventory.version_key("v1.2.3+build.2"), inventory.version_key("1.2.3"))

    def test_unknown_versions_fail_closed(self):
        for version in (None, "", "latest", "null", "1.2", "1.02.3", "1.2.3-01"):
            with self.subTest(version=version), self.assertRaises(ValueError):
                inventory.version_key(version)


if __name__ == "__main__":
    unittest.main()
