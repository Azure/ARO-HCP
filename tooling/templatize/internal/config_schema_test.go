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

package internal

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Azure/ARO-Tools/config"
	"github.com/Azure/ARO-Tools/config/ev2config"
	configtypes "github.com/Azure/ARO-Tools/config/types"
)

func TestSystemPoolOnlyConfigSchema(t *testing.T) {
	provider, err := config.NewConfigProvider(filepath.Join(repoRootDir, "config/config.yaml"))
	require.NoError(t, err)

	for _, environment := range []string{"ci00", "ci01", "pers"} {
		t.Run(environment, func(t *testing.T) {
			region, regionShort := "centralus", "cus"
			if environment == "pers" {
				region, regionShort = "westus3", "usw3"
			}
			ev2, err := ev2config.ResolveConfig("public", region)
			require.NoError(t, err)
			resolver, err := provider.GetResolver(&config.ConfigReplacements{
				CloudReplacement:       "dev",
				EnvironmentReplacement: environment,
				RegionReplacement:      region,
				RegionShortReplacement: regionShort,
				StampReplacement:       "1",
				Ev2Config:              ev2,
			})
			require.NoError(t, err)
			baseline, err := resolver.GetRegionConfiguration(region)
			require.NoError(t, err)
			require.NoError(t, resolver.ValidateSchema(baseline), "resolved baseline must pass before mutations")
			require.Equal(t, "Public", baseline["cloud"], "CEL sees the Azure cloud, not the dev config context")
			require.Equal(t, environment, baseline["environmentName"])

			wantEnabled := environment != "pers"
			value, err := baseline.GetByPath("svc.aks.systemPoolOnly")
			require.NoError(t, err)
			require.Equal(t, wantEnabled, value)
			provenance, err := resolver.ValueProvenance(region, "svc.aks.systemPoolOnly")
			require.NoError(t, err)
			require.True(t, provenance.DefaultSet)
			require.Equal(t, false, provenance.Default, "system-pool-only must be opt-in")
			if wantEnabled {
				for path, want := range map[string]string{
					"svc.aks.systemAgentPool.minCount": "3",
					"svc.aks.systemAgentPool.maxCount": "5",
					"svc.aks.systemAgentPool.vmSize":   "Standard_D4ds_v6",
				} {
					value, err := baseline.GetByPath(path)
					require.NoError(t, err)
					require.Equal(t, want, fmt.Sprint(value), path)
				}
			}

			t.Run("parameter template", func(t *testing.T) {
				rendered, err := config.PreprocessFile(filepath.Join(repoRootDir, "dev-infrastructure/configurations/svc-cluster.tmpl.bicepparam"), baseline)
				require.NoError(t, err)
				require.Contains(t, strings.Split(string(rendered), "\n"), fmt.Sprintf("param systemPoolOnly = %t", wantEnabled), "render an unquoted Bicep boolean from the resolved config")
			})

			encoded, err := json.Marshal(baseline)
			require.NoError(t, err)
			for _, test := range []struct {
				name        string
				cluster     string
				value       any
				remove      bool
				environment string
				wantError   string
			}{
				{name: "service disabled", cluster: "svc", value: false},
				{name: "service omitted", cluster: "svc", remove: true},
				{name: "service enabled ci00", cluster: "svc", value: true, environment: "ci00"},
				{name: "service enabled ci01", cluster: "svc", value: true, environment: "ci01"},
				{name: "service enabled pers", cluster: "svc", value: true, environment: "pers", wantError: "svc.aks.systemPoolOnly is supported only in ci00 and ci01"},
				{name: "service enabled ci02", cluster: "svc", value: true, environment: "ci02", wantError: "svc.aks.systemPoolOnly is supported only in ci00 and ci01"},
				{name: "service enabled prod", cluster: "svc", value: true, environment: "prod", wantError: "svc.aks.systemPoolOnly is supported only in ci00 and ci01"},
				{name: "service string rejected", cluster: "svc", value: "true", wantError: "at '/svc/aks/systemPoolOnly': got string, want boolean"},
				{name: "management disabled", cluster: "mgmt", value: false},
				{name: "management omitted", cluster: "mgmt", remove: true},
				{name: "management enabled", cluster: "mgmt", value: true, wantError: "systemPoolOnly is not supported for management clusters"},
			} {
				t.Run(test.name, func(t *testing.T) {
					// Deep-copy so mutations cannot change another case's baseline.
					var mutated configtypes.Configuration
					require.NoError(t, json.Unmarshal(encoded, &mutated))
					aks, err := mutated.GetByPath(test.cluster + ".aks")
					require.NoError(t, err)
					if test.remove {
						delete(aks.(map[string]any), "systemPoolOnly")
					} else {
						aks.(map[string]any)["systemPoolOnly"] = test.value
					}
					if test.environment != "" {
						mutated["environmentName"] = test.environment
					}
					err = resolver.ValidateSchema(mutated)
					if test.wantError == "" {
						require.NoError(t, err)
					} else {
						require.ErrorContains(t, err, test.wantError)
					}
				})
			}
		})
	}
}
