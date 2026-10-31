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
from pathlib import Path
import shlex
import shutil
import subprocess
import tempfile
import unittest
from urllib.parse import urlsplit


ROOT = Path(__file__).resolve().parents[2]


class ToolNetworkPolicyTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        # Decode the network object passed to AWF, not its reporting metadata.
        lock = ROOT / ".github/workflows/tool-version-updates.lock.yml"
        line = next(line.strip() for line in lock.read_text().splitlines()
                    if "printf" in line and 'allowDomains' in line)
        config = shlex.split(line)[2]
        cls.network = json.JSONDecoder().raw_decode(config.split('"network":', 1)[1])[0]

    def allows(self, url):
        # AWF's plain-domain ACL matches the domain itself and its subdomains.
        host = urlsplit(url).hostname
        return any(host == domain or host.endswith("." + domain)
                   for domain in self.network["allowDomains"])

    def test_updater_requests_and_official_release_redirects_are_permitted(self):
        with tempfile.TemporaryDirectory() as temporary:
            scratch = Path(temporary)
            tools = ROOT / "dev-infrastructure/openshift-ci"
            for name in ("update-tool-versions.sh", "versions.mk", "Dockerfile", "Dockerfile.nested-podman"):
                shutil.copyfile(tools / name, scratch / name)
            commands = scratch / "bin"
            commands.mkdir()
            # Discover dependencies before isolating PATH. CI installs pinned
            # gojq as /usr/local/bin/jq, not necessarily /usr/bin/jq.
            for name in ("dirname", "grep", "sort", "tail", "sed"):
                executable = shutil.which(name)
                if executable is None:
                    self.fail(f"Fixture requires executable: {name}")
                (commands / name).symlink_to(Path(executable).resolve())
            jq = shutil.which("jq")
            if jq is None:
                self.fail("Fixture requires the repository-pinned jq executable on PATH")
            jq = str(Path(jq).resolve())
            external = scratch / "external-tools"
            external.mkdir()
            jq_trace = scratch / "jq-executions"
            jq_wrapper = external / "jq"
            jq_wrapper.write_text(
                "#!/bin/bash\nset -euo pipefail\n"
                f"printf '%s\\n' {shlex.quote(jq)} >>\"$JQ_TRACE\"\n"
                f"exec {shlex.quote(jq)} \"$@\"\n")
            jq_wrapper.chmod(0o755)
            curl = commands / "curl"
            curl.write_text("#!/bin/bash\nset -euo pipefail\n"
                            "for argument in \"$@\"; do\n"
                            "  case \"$argument\" in https://*) printf '%s\\n' \"$argument\" >>\"$URL_LOG\";; esac\n"
                            "done\nprintf '%s\\n' '{\"tag_name\":\"v0.0.0\"}'\n")
            curl.chmod(0o755)
            request_log = scratch / "requests"
            result = subprocess.run(["/bin/bash", str(scratch / "update-tool-versions.sh")],
                                    env={"PATH": f"{commands}:{external}", "HOME": temporary,
                                         "URL_LOG": str(request_log), "JQ_TRACE": str(jq_trace)},
                                    capture_output=True, text=True, timeout=10)
            self.assertEqual(result.returncode, 0,
                             f"Updater failed ({result.returncode})\nstdout:\n{result.stdout}\nstderr:\n{result.stderr}")
            # The fixture must use the selected external executable even on
            # developer hosts that also have a system jq.
            self.assertEqual(set(jq_trace.read_text().splitlines()), {jq})
            requests = request_log.read_text().splitlines()
        self.assertEqual({urlsplit(url).hostname for url in requests}, {"api.github.com"})
        for url in requests + [
            "https://github.com/Azure/bicep/releases/download/v0.48.1/bicep-linux-x64",
            "https://github.com/prometheus/prometheus/releases/download/v3.2.1/prometheus-3.2.1.linux-amd64.tar.gz",
            "https://release-assets.githubusercontent.com/github-production-release-asset/123/asset",
        ]:
            with self.subTest(url=url):
                self.assertTrue(self.allows(url), f"Updater/download blocked by generated AWF policy: {url}")

    def test_unrelated_hosts_and_hostname_spoofs_remain_denied(self):
        for url in ("https://example.invalid/asset",
                    "https://api.github.com.evil.invalid/releases/latest",
                    "https://github.com.evil.invalid/asset",
                    "https://release-assets.githubusercontent.com.evil.invalid/asset"):
            with self.subTest(url=url):
                self.assertFalse(self.allows(url))
        self.assertNotIn("*", self.network["allowDomains"])


if __name__ == "__main__":
    unittest.main()
