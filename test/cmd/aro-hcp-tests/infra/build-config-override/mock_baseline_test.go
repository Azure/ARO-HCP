// Copyright 2026 Microsoft Corporation
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package buildconfigoverride

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/Azure/ARO-HCP/test/pkg/leaseenv"
)

func TestMockBundleBaselineContinuity(t *testing.T) {
	clearEnvironment(t)
	checkout := t.TempDir()
	write := func(path, text string) {
		t.Helper()
		path = filepath.Join(checkout, path)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0755); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"provision-from-main.sh", "build-config-override.sh"} {
		data, err := os.ReadFile(filepath.Join("../../../../../hack/ci", name))
		if err != nil {
			t.Fatal(err)
		}
		write("hack/ci/"+name, string(data))
	}
	write("hack/ci/az-login.sh", "export DEPLOY_ENV=ci02\n")
	write("hack/ci/resolve-acr-images.sh", "true\n")
	write("config/rendered/dev/dev/westus3.yaml", "{}\n")
	write("hack/ci/provision-environment.sh", "#!/bin/bash\nset -euo pipefail\nsource hack/ci/build-config-override.sh\ncp \"$OVERRIDE_CONFIG_FILE\" \"$SHARED_DIR/baseline.yaml\"\n")
	write("test/aro-hcp-tests", "#!/bin/bash\nexec \"$CLI_TEST_BINARY\" -test.run=^TestCommandProcess$ -- \"$@\"\n")
	write("bin/git", `#!/bin/bash
set -euo pipefail
case "$1" in
  checkout)
    printf 'return 91\n' > hack/ci/build-config-override.sh
    printf '#!/bin/bash\nexit 92\n' > test/aro-hcp-tests
    ;;
  rev-parse) echo abcdef0 ;;
  cat-file) exit 0 ;;
  *) exit 93 ;;
esac
`)
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	principal := func(name string) leaseenv.MockPrincipal {
		return leaseenv.MockPrincipal{ClientID: uuid.NewString(), PrincipalID: uuid.NewString(), CertificateName: name}
	}
	bundle := leaseenv.MockIdentityBundlePayload{SlotName: "slot", SubscriptionID: uuid.NewString(), MSI: principal("msi"), Backend: principal("backend"), ClustersService: principal("cs")}
	payload, err := json.Marshal(bundle)
	if err != nil {
		t.Fatal(err)
	}
	shared := t.TempDir()
	t.Setenv("SHARED_DIR", shared)
	t.Setenv("CLI_TEST_BINARY", binary)
	t.Setenv("ARO_HCP_INFRA_TEST_PROCESS", "1")
	t.Setenv("ARO_HCP_TESTS_BINARY", filepath.Join(checkout, "test/aro-hcp-tests"))
	t.Setenv("PULL_BASE_SHA", "baseline")
	t.Setenv("JOB_TYPE", "presubmit")
	t.Setenv("JOB_NAME", "test")
	t.Setenv("PATH", filepath.Join(checkout, "bin")+":"+os.Getenv("PATH"))
	t.Setenv(leaseenv.MockIdentityBundle, string(payload))
	t.Setenv(leaseenv.MSIMockSP, "legacy-ignored")
	t.Setenv(leaseenv.ARMHelperSP, "legacy-one legacy-two")
	command := exec.Command("bash", "hack/ci/provision-from-main.sh")
	command.Dir = checkout
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("baseline adapter bridge: %v: %s", err, output)
	}
	baseline, err := os.ReadFile(filepath.Join(shared, "baseline.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{bundle.MSI.ClientID, bundle.MSI.PrincipalID, bundle.Backend.ClientID, bundle.ClustersService.ClientID} {
		if !strings.Contains(string(baseline), expected) {
			t.Fatalf("baseline missing identity %s", expected)
		}
	}
	t.Setenv("DEPLOY_ENV", "ci02")
	if _, err := execute("--source-dir", checkout, "--shared-dir", shared, "--deploy-env", "ci02"); err != nil {
		t.Fatal(err)
	}
	upgrade, err := os.ReadFile(filepath.Join(shared, "config-override.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if string(baseline) != string(upgrade) {
		t.Fatal("baseline and in-place upgrade identity overrides differ")
	}
}
