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

package render

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"sigs.k8s.io/yaml"
)

type renderedIdentityConfig struct {
	InfrastructureIdentities map[string]any `json:"infrastructureIdentities"`
	Svc                      struct {
		AKS struct{ Name string } `json:"aks"`
	} `json:"svc"`
	Mgmt struct {
		AKS struct{ Name string } `json:"aks"`
	} `json:"mgmt"`
}

func leasedIdentityConfig() map[string]any {
	return map[string]any{
		"useLeased":                  true,
		"serviceResourceGroup":       "lease-service-rg",
		"serviceAksIdentityName":     "lease-service-aks",
		"managementResourceGroups":   "1=lease-management-one,2=lease-management-two",
		"managementAksIdentityNames": "1=aks-one,2=aks-two",
	}
}

// Exercise the actual renderer, dev BUILD_ID expansion and schema validation,
// rather than coupling configuration assertions to the shell adapter.
func renderIdentityConfig(t *testing.T, identities map[string]any, stamp, buildID string) (renderedIdentityConfig, error) {
	t.Helper()
	t.Setenv("BUILD_ID", buildID)
	t.Setenv("USER", "test")
	root, err := filepath.Abs("../../../../..")
	require.NoError(t, err)
	directory := t.TempDir()
	overlay := filepath.Join(directory, "override.yaml")
	output := filepath.Join(directory, "rendered.yaml")
	data, err := json.Marshal(map[string]any{
		"clouds": map[string]any{"dev": map[string]any{"environments": map[string]any{
			"ci01": map[string]any{"defaults": map[string]any{"infrastructureIdentities": identities}},
		}}},
	})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(overlay, data, 0600))
	command, err := NewCommand()
	require.NoError(t, err)
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
	data, err = os.ReadFile(output)
	require.NoError(t, err)
	var rendered renderedIdentityConfig
	require.NoError(t, yaml.Unmarshal(data, &rendered))
	return rendered, nil
}

func TestLeasedIdentityNamesSurviveBuildIDChanges(t *testing.T) {
	for _, stamp := range []string{"1", "2"} {
		t.Run("stamp-"+stamp, func(t *testing.T) {
			identities := leasedIdentityConfig()
			first, err := renderIdentityConfig(t, identities, stamp, "2100000000000000111")
			require.NoError(t, err)
			second, err := renderIdentityConfig(t, identities, stamp, "2100000000000000222")
			require.NoError(t, err)
			require.Equal(t, identities, first.InfrastructureIdentities)
			require.Equal(t, identities, second.InfrastructureIdentities)
			require.NotEqual(t, first.Svc.AKS.Name, second.Svc.AKS.Name, "service cluster must remain job-specific")
			require.NotEqual(t, first.Mgmt.AKS.Name, second.Mgmt.AKS.Name, "management cluster must remain job-specific")
		})
	}
}

func TestLeasedIdentityRenderingRejectsIncompleteContract(t *testing.T) {
	for _, test := range []struct {
		name, field, value string
	}{
		{"missing service name", "serviceAksIdentityName", ""},
		{"missing current stamp name", "managementAksIdentityNames", "1=aks-one"},
		{"invalid current stamp name", "managementAksIdentityNames", "1=aks-one,2=bad.name"},
		{"missing current stamp group", "managementResourceGroups", "1=lease-management-one"},
	} {
		t.Run(test.name, func(t *testing.T) {
			identities := leasedIdentityConfig()
			identities[test.field] = test.value
			_, err := renderIdentityConfig(t, identities, "2", "2100000000000000111")
			require.Error(t, err)
		})
	}
}
