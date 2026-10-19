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
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/Azure/ARO-HCP/test/pkg/leaseenv"
	"github.com/Azure/ARO-HCP/tooling/templatize/cmd/configuration/render"
)

type renderedIdentityConfig struct {
	InfrastructureIdentities map[string]any `yaml:"infrastructureIdentities"`
	Svc                      struct {
		AKS struct{ Name string } `yaml:"aks"`
	} `yaml:"svc"`
	Mgmt struct {
		AKS struct{ Name string } `yaml:"aks"`
	} `yaml:"mgmt"`
}

func leasedIdentityBundle() map[string]any {
	return map[string]any{
		"serviceResourceGroup":       "lease-service-rg",
		"serviceAksIdentityName":     "lease-service-aks",
		"managementResourceGroups":   map[string]string{"1": "lease-management-one", "2": "lease-management-two"},
		"managementAksIdentityNames": map[string]string{"1": "aks-one", "2": "aks-two"},
	}
}

func generateIdentityOverride(t *testing.T, bundle map[string]any) (string, error) {
	t.Helper()
	data, err := json.Marshal(bundle)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(leaseenv.InfrastructureIdentityBundle, string(data))
	overlay := filepath.Join(t.TempDir(), "override.yaml")
	stdout, err := execute("--deploy-env", "ci01", "--output-file", overlay)
	if err != nil && stdout != "" {
		t.Fatalf("failed override generation published stdout: %q", stdout)
	}
	return overlay, err
}

// Exercise the consumer-facing override CLI before handing its output to the
// generic renderer. Configuration schema expectations belong to this consumer.
func renderIdentityConfig(t *testing.T, overlay, stamp, buildID string) (renderedIdentityConfig, error) {
	t.Helper()
	t.Setenv("BUILD_ID", buildID)
	t.Setenv("USER", "test")
	root, err := filepath.Abs("../../../../..")
	if err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(t.TempDir(), "rendered.yaml")
	command, err := render.NewCommand()
	if err != nil {
		t.Fatal(err)
	}
	command.SetOut(&bytes.Buffer{})
	command.SetErr(&bytes.Buffer{})
	command.SetArgs([]string{
		"--service-config-file", filepath.Join(root, "config/config.yaml"),
		"--config-file-override", overlay,
		"--dev-settings-file", filepath.Join(root, "tooling/templatize/settings.yaml"),
		"--cloud", "dev", "--environment", "ci01", "--region", "canadacentral",
		"--stamp", stamp, "--ev2-cloud", "public", "--output", output,
	})
	if err := command.Execute(); err != nil {
		return renderedIdentityConfig{}, err
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	var rendered renderedIdentityConfig
	if err := yaml.Unmarshal(data, &rendered); err != nil {
		t.Fatal(err)
	}
	return rendered, nil
}

func TestLeasedIdentityNamesSurviveBuildIDChanges(t *testing.T) {
	for _, stamp := range []string{"1", "2"} {
		t.Run("stamp-"+stamp, func(t *testing.T) {
			clearEnvironment(t)
			overlay, err := generateIdentityOverride(t, leasedIdentityBundle())
			if err != nil {
				t.Fatal(err)
			}
			first, err := renderIdentityConfig(t, overlay, stamp, "2100000000000000111")
			if err != nil {
				t.Fatal(err)
			}
			second, err := renderIdentityConfig(t, overlay, stamp, "2100000000000000222")
			if err != nil {
				t.Fatal(err)
			}
			want := map[string]any{
				"useLeased":                  true,
				"serviceResourceGroup":       "lease-service-rg",
				"serviceAksIdentityName":     "lease-service-aks",
				"managementResourceGroups":   "1=lease-management-one,2=lease-management-two",
				"managementAksIdentityNames": "1=aks-one,2=aks-two",
			}
			for _, result := range []renderedIdentityConfig{first, second} {
				if !reflect.DeepEqual(result.InfrastructureIdentities, want) {
					t.Fatalf("rendered identities = %#v, want %#v", result.InfrastructureIdentities, want)
				}
				if result.Svc.AKS.Name == "" || result.Mgmt.AKS.Name == "" {
					t.Fatalf("rendered cluster names must not be empty: %#v", result)
				}
			}
			if first.Svc.AKS.Name == second.Svc.AKS.Name {
				t.Fatal("service cluster must remain job-specific")
			}
			if first.Mgmt.AKS.Name == second.Mgmt.AKS.Name {
				t.Fatal("management cluster must remain job-specific")
			}
		})
	}
}

func TestLeasedIdentityOverrideRejectsIncompleteContract(t *testing.T) {
	for _, test := range []struct {
		name, field string
		value       any
	}{
		{"missing service name", "serviceAksIdentityName", ""},
		{"missing current stamp name", "managementAksIdentityNames", map[string]string{"1": "aks-one"}},
		{"invalid current stamp name", "managementAksIdentityNames", map[string]string{"1": "aks-one", "2": "bad.name"}},
		{"missing current stamp group", "managementResourceGroups", map[string]string{"1": "lease-management-one"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			clearEnvironment(t)
			bundle := leasedIdentityBundle()
			bundle[test.field] = test.value
			overlay, err := generateIdentityOverride(t, bundle)
			if err == nil || !strings.Contains(err.Error(), leaseenv.InfrastructureIdentityBundle) {
				t.Fatalf("expected identity contract rejection, got %v", err)
			}
			if _, err := os.Stat(overlay); !os.IsNotExist(err) {
				t.Fatalf("invalid identity override must not be published: %v", err)
			}
		})
	}
}

func TestLeasedIdentityRenderingRejectsMissingSelectedStamp(t *testing.T) {
	clearEnvironment(t)
	bundle := leasedIdentityBundle()
	bundle["managementResourceGroups"] = map[string]string{"1": "lease-management-one"}
	bundle["managementAksIdentityNames"] = map[string]string{"1": "aks-one"}
	// The CLI can validate agreement between maps, but does not select the
	// deployment stamp. Only rendering for stamp 2 can reject this contract.
	overlay, err := generateIdentityOverride(t, bundle)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := renderIdentityConfig(t, overlay, "1", "2100000000000000111"); err != nil {
		t.Fatalf("valid stamp 1 must render: %v", err)
	}
	if _, err := renderIdentityConfig(t, overlay, "2", "2100000000000000111"); err == nil {
		t.Fatal("rendering must reject missing stamp 2 identity names and resource groups")
	}
}
