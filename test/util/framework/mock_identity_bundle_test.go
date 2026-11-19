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

package framework

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/google/uuid"

	"github.com/Azure/ARO-HCP/test/pkg/leaseenv"
)

func TestSlotMockPrincipalAlignment(t *testing.T) {
	principal := func(name string) leaseenv.MockPrincipal {
		return leaseenv.MockPrincipal{ClientID: uuid.NewString(), PrincipalID: uuid.NewString(), CertificateName: name}
	}
	bundle := leaseenv.MockIdentityBundlePayload{SlotName: "slot", SubscriptionID: uuid.NewString(), MSI: principal("msi"), Backend: principal("backend"), ClustersService: principal("cs")}
	payload, err := json.Marshal(bundle)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(leaseenv.MockIdentityBundle, string(payload))
	t.Setenv(leaseenv.MSIMockSP, "unused-legacy")
	t.Setenv("MI_MOCK_PRINCIPAL_ID", bundle.MSI.PrincipalID)
	if got, err := MIMockPrincipalID(); err != nil || got != bundle.MSI.PrincipalID {
		t.Fatalf("wrong MSI principal: %s, %v", got, err)
	}
	for _, invalid := range []struct{ payload, explicit string }{{"{}", ""}, {string(payload), uuid.NewString()}} {
		t.Setenv(leaseenv.MockIdentityBundle, invalid.payload)
		t.Setenv("MI_MOCK_PRINCIPAL_ID", invalid.explicit)
		if got, err := MIMockPrincipalID(); err == nil || got != "" {
			t.Fatalf("invalid or conflicting bundle returned %q, %v", got, err)
		}
	}
}

func TestNonSlotMockPrincipalConfiguration(t *testing.T) {
	const scenarioEnv = "ARO_HCP_TEST_MOCK_CONFIG_SCENARIO"
	if scenario := os.Getenv(scenarioEnv); scenario != "" {
		if err := os.Unsetenv(leaseenv.MockIdentityBundle); err != nil {
			t.Fatal(err)
		}
		t.Setenv("MI_MOCK_PRINCIPAL_ID", "")
		t.Setenv(leaseenv.MSIMockSP, "obsolete-ignored")
		t.Setenv("ARO_HCP_CONFIG_FILE_OVERRIDE", "")
		t.Setenv("ARO_HCP_CLOUD", "dev")
		t.Setenv("DEPLOY_ENV", "pers")
		t.Setenv("REGION", "westus3")
		principal := "11111111-1111-1111-1111-111111111111"
		value := fmt.Sprintf("%q", principal)
		want := principal
		wantError := false
		switch scenario {
		case "empty":
			value, want = `""`, ""
		case "invalid-type":
			value, want, wantError = "42", "", true
		case "config-error":
			want, wantError = "", true
		case "explicit":
			t.Setenv("MI_MOCK_PRINCIPAL_ID", principal)
		}
		configFile := filepath.Join(t.TempDir(), "config.yaml")
		if scenario != "config-error" && scenario != "explicit" {
			data := fmt.Sprintf("defaults:\n  miMockPrincipalId: %s\nclouds:\n  dev:\n    environments:\n      pers:\n        regions:\n          westus3: {}\n", value)
			if err := os.WriteFile(configFile, []byte(data), 0600); err != nil {
				t.Fatal(err)
			}
		}
		t.Setenv("ARO_HCP_CONFIG_FILE", configFile)
		got, err := MIMockPrincipalID()
		if (err != nil) != wantError || got != want {
			t.Fatalf("principal=%q, error=%v; want %q, error=%t", got, err, want, wantError)
		}
		return
	}
	for _, scenario := range []string{"personal", "empty", "invalid-type", "config-error", "explicit"} {
		t.Run(scenario, func(t *testing.T) {
			command := exec.Command(os.Args[0], "-test.run=^TestNonSlotMockPrincipalConfiguration$")
			command.Env = append(os.Environ(), scenarioEnv+"="+scenario)
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("configuration subprocess failed: %v\n%s", err, output)
			}
		})
	}
}
