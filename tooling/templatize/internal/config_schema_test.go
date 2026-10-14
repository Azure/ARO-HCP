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

func TestInfrastructureIdentityResourceGroupSchema(t *testing.T) {
	provider, err := config.NewConfigProvider(filepath.Join(repoRootDir, "config/config.yaml"))
	require.NoError(t, err)
	ev2, err := ev2config.ResolveConfig("public", "westus3")
	require.NoError(t, err)
	resolver, err := provider.GetResolver(&config.ConfigReplacements{
		CloudReplacement:       "dev",
		EnvironmentReplacement: "pers",
		RegionReplacement:      "westus3",
		RegionShortReplacement: "usw3",
		StampReplacement:       "1",
		Ev2Config:              ev2,
	})
	require.NoError(t, err)
	baseline, err := resolver.GetRegionConfiguration("westus3")
	require.NoError(t, err)
	require.NoError(t, resolver.ValidateSchema(baseline))
	require.Equal(t, map[string]any{
		"useLeased":                false,
		"serviceResourceGroup":     "",
		"managementResourceGroups": "",
	}, baseline["infrastructureIdentities"])
	encoded, err := json.Marshal(baseline)
	require.NoError(t, err)

	for _, field := range []string{"serviceResourceGroup", "managementResourceGroups"} {
		for _, test := range []struct {
			name  string
			value any
			valid bool
		}{
			{name: "one character", value: "a", valid: true},
			{name: "allowed punctuation", value: "AZaz09_rg.(bundle)-", valid: true},
			{name: "maximum length", value: strings.Repeat("a", 90), valid: true},
			{name: "empty leased name", value: ""},
			{name: "too long", value: strings.Repeat("a", 91)},
			{name: "trailing period", value: "rg."},
			{name: "only period", value: "."},
			{name: "quote", value: "bad'rg"},
			{name: "backslash", value: `bad\rg`},
			{name: "slash", value: "bad/rg"},
			{name: "space", value: "bad rg"},
			{name: "newline", value: "rg\n"},
			{name: "mapping delimiter", value: "rg,other"},
			{name: "non-string", value: 123},
		} {
			t.Run(field+"/"+test.name, func(t *testing.T) {
				var mutated configtypes.Configuration
				require.NoError(t, json.Unmarshal(encoded, &mutated))
				identities := map[string]any{
					"useLeased":                true,
					"serviceResourceGroup":     "service-rg",
					"managementResourceGroups": "1=management-rg",
				}
				value := test.value
				if field == "managementResourceGroups" {
					if name, ok := value.(string); ok {
						value = "1=management-rg,2=" + name
					}
				}
				identities[field] = value
				mutated["infrastructureIdentities"] = identities
				err := resolver.ValidateSchema(mutated)
				if test.valid {
					require.NoError(t, err)
				} else {
					require.ErrorContains(t, err, field)
				}
			})
		}
	}
}

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

			value, err := baseline.GetByPath("svc.aks.systemPoolOnly")
			require.NoError(t, err)
			require.Equal(t, true, value)
			provenance, err := resolver.ValueProvenance(region, "svc.aks.systemPoolOnly")
			require.NoError(t, err)
			require.True(t, provenance.DefaultSet)
			require.Equal(t, false, provenance.Default, "system-pool-only must be opt-in")
			wantSystemPool := map[string]string{
				"svc.aks.systemAgentPool.minCount": "3",
				"svc.aks.systemAgentPool.maxCount": "5",
				"svc.aks.systemAgentPool.vmSize":   "Standard_D4ds_v6",
			}
			if environment == "pers" {
				wantSystemPool["svc.aks.systemAgentPool.vmSize"] = "Standard_D4ds_v5"
				wantSystemPool["svc.aks.systemAgentPool.osDiskSizeGB"] = "64"
			}
			for path, want := range wantSystemPool {
				value, err := baseline.GetByPath(path)
				require.NoError(t, err)
				require.Equal(t, want, fmt.Sprint(value), path)
			}

			t.Run("parameter template", func(t *testing.T) {
				rendered, err := config.PreprocessFile(filepath.Join(repoRootDir, "dev-infrastructure/configurations/svc-cluster.tmpl.bicepparam"), baseline)
				require.NoError(t, err)
				require.Contains(t, strings.Split(string(rendered), "\n"), "param systemPoolOnly = true", "render an unquoted Bicep boolean from the resolved config")
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
				{name: "service enabled pers", cluster: "svc", value: true, environment: "pers"},
				{name: "service enabled ci02", cluster: "svc", value: true, environment: "ci02", wantError: "svc.aks.systemPoolOnly is supported only in ci00, ci01 and pers"},
				{name: "service enabled prod", cluster: "svc", value: true, environment: "prod", wantError: "svc.aks.systemPoolOnly is supported only in ci00, ci01 and pers"},
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

func TestMgmtSchedulingConfigSchema(t *testing.T) {
	provider, err := config.NewConfigProvider(filepath.Join(repoRootDir, "config/config.yaml"))
	require.NoError(t, err)
	ev2, err := ev2config.ResolveConfig("public", "westus3")
	require.NoError(t, err)
	resolver, err := provider.GetResolver(&config.ConfigReplacements{
		CloudReplacement:       "dev",
		EnvironmentReplacement: "pers",
		RegionReplacement:      "westus3",
		RegionShortReplacement: "usw3",
		StampReplacement:       "1",
		Ev2Config:              ev2,
	})
	require.NoError(t, err)
	baseline, err := resolver.GetRegionConfiguration("westus3")
	require.NoError(t, err)
	require.NoError(t, resolver.ValidateSchema(baseline))
	baselineJSON, err := json.Marshal(baseline)
	require.NoError(t, err)

	for _, test := range []struct {
		name      string
		mutate    func(map[string]any)
		wantError bool
	}{
		{name: "default infra", mutate: func(scheduling map[string]any) {
			require.Equal(t, "infra", scheduling["role"])
			require.Len(t, scheduling, 1)
		}},
		{name: "system override", mutate: func(scheduling map[string]any) {
			scheduling["role"] = "system"
		}},
		{name: "missing role", wantError: true, mutate: func(scheduling map[string]any) { delete(scheduling, "role") }},
		{name: "empty role", wantError: true, mutate: func(scheduling map[string]any) { scheduling["role"] = "" }},
		{name: "invalid role", wantError: true, mutate: func(scheduling map[string]any) { scheduling["role"] = "worker" }},
		{name: "invalid role type", wantError: true, mutate: func(scheduling map[string]any) { scheduling["role"] = true }},
		{name: "unknown scheduling field", wantError: true, mutate: func(scheduling map[string]any) { scheduling["unknown"] = true }},
		{name: "legacy tolerations list", wantError: true, mutate: func(scheduling map[string]any) { scheduling["tolerations"] = []any{} }},
		{name: "removed operator", wantError: true, mutate: func(scheduling map[string]any) { scheduling["tolerationOperator"] = "Equal" }},
		{name: "removed value", wantError: true, mutate: func(scheduling map[string]any) {
			scheduling["tolerationValue"] = "true"
		}},
		{name: "removed effect", wantError: true, mutate: func(scheduling map[string]any) {
			scheduling["tolerationEffect"] = "NoSchedule"
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			var mutated configtypes.Configuration
			require.NoError(t, json.Unmarshal(baselineJSON, &mutated))
			test.mutate(mutated["mgmt"].(map[string]any)["scheduling"].(map[string]any))
			err := resolver.ValidateSchema(mutated)
			if test.wantError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}
