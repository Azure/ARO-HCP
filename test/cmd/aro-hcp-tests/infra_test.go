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

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Azure/ARO-HCP/test/pkg/leaseenv"
	"github.com/Azure/ARO-HCP/test/util/framework"
)

func TestOfflineCommandsSkipSuiteSetup(t *testing.T) {
	for _, args := range [][]string{
		{},
		{"--help"},
		{"help"},
		{"infra", "build-config-override"},
		{"infra", "--help"},
		{"help", "infra"},
		{"--help", "infra"},
		{"infra", "build-config-override", "--help"},
		{"help", "infra", "build-config-override"},
		{"list", "--help"},
		{"list", "suites", "--help"},
		{"info", "--help"},
		{"images", "--help"},
		{"update", "--help"},
		{"run-suite", "--help"},
		{"run-test", "--help"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			for _, key := range []string{
				leaseenv.MSIMockSP, leaseenv.ARMHelperSP, leaseenv.InfrastructureIdentityBundle,
				"BACKEND_IMAGE", "FRONTEND_IMAGE", "ADMIN_API_IMAGE", "SESSIONGATE_IMAGE", "HCP_RECOVERY_IMAGE",
				"FLEET_IMAGE", "MGMT_AGENT_IMAGE", "SWIFT_RECORDER_IMAGE", "KUBE_APPLIER_IMAGE", "EXPORTER_IMAGE",
				"USE_OC_LOGIN_REGISTRIES",
			} {
				t.Setenv(key, "")
			}
			// Suite initialization would warn on the invalid pooled-identity setting.
			t.Setenv("ARO_HCP_SUITE_PARALLELISM", "")
			t.Setenv(framework.UsePooledIdentitiesEnvvar, "invalid")
			t.Setenv(leaseenv.MSIContainers, "")
			t.Setenv("PATH", "")
			t.Setenv("DEPLOY_ENV", "offline")
			shared := t.TempDir()
			t.Setenv("SHARED_DIR", shared)
			var stdout bytes.Buffer
			stderr := captureStderr(t, func() {
				root, err := setupCli()
				if err != nil {
					t.Fatal(err)
				}
				root.SetArgs(args)
				root.SetOut(&stdout)
				if err := root.Execute(); err != nil {
					t.Fatal(err)
				}
			})
			if stderr != "" {
				t.Fatalf("offline command emitted suite diagnostics: %s", stderr)
			}
			if len(args) <= 1 {
				for _, command := range []string{"infra", "list", "run-suite"} {
					if !strings.Contains(stdout.String(), command) {
						t.Fatalf("root help is missing %q: %s", command, stdout.String())
					}
				}
			}
			if len(args) == 2 && args[0] == "infra" && args[1] == "build-config-override" {
				if stdout.String() != "\n" {
					t.Fatalf("unexpected command stdout: %q", stdout.String())
				}
				if _, err := os.Stat(filepath.Join(shared, "config-override.yaml")); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}
