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

package stackscopes

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	configtypes "github.com/Azure/ARO-Tools/config/types"
)

const pipelineHeader = `$schema: pipeline.schema.v1
serviceGroup: Microsoft.Azure.ARO.HCP.Stacks
rolloutName: Test Rollout
resourceGroups:
`

const resourceGroupLevelStep = `  steps:
  - name: stack
    action: ARMStack
    template: stack.bicep
    parameters: stack.bicepparam
    deploymentLevel: ResourceGroup
    actionOnUnmanage: delete
`

const subscriptionLevelStep = `  steps:
  - name: stack
    action: ARMStack
    template: stack.bicep
    parameters: stack.bicepparam
    deploymentLevel: Subscription
    actionOnUnmanage: delete
`

func TestFindCollisions(t *testing.T) {
	t.Parallel()

	render := func(region string, stamp int) (configtypes.Configuration, error) {
		return configtypes.Configuration{
			"region":      region,
			"stamp":       strconv.Itoa(stamp),
			"geo":         map[string]string{"eastus2": "us", "centralus": "us", "uksouth": "uk"}[region],
			"sharedRG":    map[string]string{"eastus2": "Shared-RG", "centralus": "shared-rg", "uksouth": "uk-rg"}[region],
			"optional":    region != "centralus",
			"stackAction": "ARMStack",
		}, nil
	}

	tests := []struct {
		name           string
		resourceGroups string
		otherPipeline  string
		want           []collision
	}{
		{
			name: "unconstrained step in a region-scoped resource group does not collide",
			resourceGroups: `- name: regional
  resourceGroup: '{{ .region }}-shared'
  subscription: sub
` + resourceGroupLevelStep,
		},
		{
			name: "two listed regions sharing a resource group collide",
			resourceGroups: `- name: shared
  resourceGroup: 'kusto-{{ .geo }}'
  subscription: sub
  executionConstraints:
  - clouds: [public]
    environments: [prod]
    regions: [eastus2, centralus, uksouth]
` + resourceGroupLevelStep,
			want: []collision{{
				Cloud:       "public",
				Environment: "prod",
				Scope:       stackScope{Name: "stacks.shared.stack", Subscription: "sub", ResourceGroup: "kusto-us"},
				Executions:  []string{"centralus: Microsoft.Azure.ARO.HCP.Stacks/shared/stack", "eastus2: Microsoft.Azure.ARO.HCP.Stacks/shared/stack"},
			}},
		},
		{
			name: "a single listed region executes once and does not collide",
			resourceGroups: `- name: shared
  resourceGroup: global
  subscription: sub
  executionConstraints:
  - clouds: [public]
    environments: [prod]
    regions: [eastus2]
` + resourceGroupLevelStep,
		},
		{
			name: "the first matching constraint wins, as in the sdp-pipelines generator",
			resourceGroups: `- name: shared
  resourceGroup: global
  subscription: sub
  executionConstraints:
  - environments: [prod]
    regions: [eastus2]
  - regions: [eastus2, centralus]
` + resourceGroupLevelStep,
		},
		{
			name: "a constraint for another environment means the step does not execute",
			resourceGroups: `- name: shared
  resourceGroup: global
  subscription: sub
  executionConstraints:
  - environments: [int]
    regions: [eastus2, centralus]
` + resourceGroupLevelStep,
		},
		{
			name: "stamped resource group without the stamp in its name collides across stamps",
			resourceGroups: `- name: management
  stamped: true
  resourceGroup: '{{ .region }}-mgmt'
  subscription: sub
  executionConstraints:
  - environments: [prod]
    regions: [eastus2, uksouth]
` + resourceGroupLevelStep,
			want: []collision{
				{
					Cloud:       "public",
					Environment: "prod",
					Scope:       stackScope{Name: "stacks.management.stack", Subscription: "sub", ResourceGroup: "eastus2-mgmt"},
					Executions:  []string{"eastus2 stamp 1: Microsoft.Azure.ARO.HCP.Stacks/management/stack", "eastus2 stamp 2: Microsoft.Azure.ARO.HCP.Stacks/management/stack"},
				},
				{
					Cloud:       "public",
					Environment: "prod",
					Scope:       stackScope{Name: "stacks.management.stack", Subscription: "sub", ResourceGroup: "uksouth-mgmt"},
					Executions:  []string{"uksouth stamp 1: Microsoft.Azure.ARO.HCP.Stacks/management/stack", "uksouth stamp 2: Microsoft.Azure.ARO.HCP.Stacks/management/stack"},
				},
			},
		},
		{
			name: "stamped resource group with the stamp in its name does not collide",
			resourceGroups: `- name: management
  stamped: true
  resourceGroup: '{{ .region }}-mgmt-{{ .stamp }}'
  subscription: sub
` + resourceGroupLevelStep,
		},
		{
			name: "a matching constraint without regions executes in every region",
			resourceGroups: `- name: shared
  resourceGroup: global
  subscription: sub
  executionConstraints:
  - environments: [prod]
` + resourceGroupLevelStep,
			want: []collision{{
				Cloud:       "public",
				Environment: "prod",
				Scope:       stackScope{Name: "stacks.shared.stack", Subscription: "sub", ResourceGroup: "global"},
				Executions: []string{
					"centralus: Microsoft.Azure.ARO.HCP.Stacks/shared/stack",
					"eastus2: Microsoft.Azure.ARO.HCP.Stacks/shared/stack",
					"uksouth: Microsoft.Azure.ARO.HCP.Stacks/shared/stack",
				},
			}},
		},
		{
			name: "subscription-level stack collides across resource groups in one subscription",
			resourceGroups: `- name: regional
  resourceGroup: '{{ .region }}-shared'
  subscription: sub
  executionConstraints:
  - environments: [prod]
    regions: [eastus2, uksouth]
` + subscriptionLevelStep,
			want: []collision{{
				Cloud:       "public",
				Environment: "prod",
				Scope:       stackScope{Name: "stacks.regional.stack", Subscription: "sub"},
				Executions:  []string{"eastus2: Microsoft.Azure.ARO.HCP.Stacks/regional/stack", "uksouth: Microsoft.Azure.ARO.HCP.Stacks/regional/stack"},
			}},
		},
		{
			name: "service groups that generate the same stack name collide",
			resourceGroups: `- name: shared
  resourceGroup: global
  subscription: sub
  executionConstraints:
  - environments: [prod]
    regions: [eastus2]
` + resourceGroupLevelStep,
			otherPipeline: `$schema: pipeline.schema.v1
serviceGroup: Microsoft.Azure.ARO.Other.Stacks
rolloutName: Other Rollout
resourceGroups:
- name: shared
  resourceGroup: global
  subscription: sub
  executionConstraints:
  - environments: [prod]
    regions: [eastus2]
` + resourceGroupLevelStep,
			want: []collision{{
				Cloud:       "public",
				Environment: "prod",
				Scope:       stackScope{Name: "stacks.shared.stack", Subscription: "sub", ResourceGroup: "global"},
				Executions:  []string{"eastus2: Microsoft.Azure.ARO.HCP.Stacks/shared/stack", "eastus2: Microsoft.Azure.ARO.Other.Stacks/shared/stack"},
			}},
		},
		{
			name: "resource group names that differ only in case collide",
			resourceGroups: `- name: shared
  resourceGroup: '{{ .sharedRG }}'
  subscription: sub
  executionConstraints:
  - environments: [prod]
    regions: [eastus2, centralus]
` + resourceGroupLevelStep,
			want: []collision{{
				Cloud:       "public",
				Environment: "prod",
				Scope:       stackScope{Name: "stacks.shared.stack", Subscription: "sub", ResourceGroup: "shared-rg"},
				Executions:  []string{"centralus: Microsoft.Azure.ARO.HCP.Stacks/shared/stack", "eastus2: Microsoft.Azure.ARO.HCP.Stacks/shared/stack"},
			}},
		},
		{
			name: "a conditional step absent from the first region still collides in later regions",
			resourceGroups: `- name: shared
  resourceGroup: global
  subscription: sub
  executionConstraints:
  - environments: [prod]
    regions: [centralus, eastus2, uksouth]
  steps:
  - name: output
    action: ARM
    template: output.bicep
    parameters: output.bicepparam
    deploymentLevel: ResourceGroup
    outputOnly: true
  {{- if .optional }}
  - name: stack
    action: ARMStack
    template: stack.bicep
    parameters: stack.bicepparam
    deploymentLevel: ResourceGroup
    actionOnUnmanage: delete
  {{- end }}
`,
			want: []collision{{
				Cloud:       "public",
				Environment: "prod",
				Scope:       stackScope{Name: "stacks.shared.stack", Subscription: "sub", ResourceGroup: "global"},
				Executions:  []string{"eastus2: Microsoft.Azure.ARO.HCP.Stacks/shared/stack", "uksouth: Microsoft.Azure.ARO.HCP.Stacks/shared/stack"},
			}},
		},
		{
			name: "a templated action that renders to ARMStack is checked",
			resourceGroups: `- name: shared
  resourceGroup: global
  subscription: sub
  executionConstraints:
  - environments: [prod]
    regions: [eastus2, uksouth]
  steps:
  - name: stack
    action: '{{ .stackAction }}'
    template: stack.bicep
    parameters: stack.bicepparam
    deploymentLevel: ResourceGroup
    actionOnUnmanage: delete
`,
			want: []collision{{
				Cloud:       "public",
				Environment: "prod",
				Scope:       stackScope{Name: "stacks.shared.stack", Subscription: "sub", ResourceGroup: "global"},
				Executions:  []string{"eastus2: Microsoft.Azure.ARO.HCP.Stacks/shared/stack", "uksouth: Microsoft.Azure.ARO.HCP.Stacks/shared/stack"},
			}},
		},
		{
			name: "stack delete steps are ignored",
			resourceGroups: `- name: shared
  resourceGroup: global
  subscription: sub
  steps:
  - name: output
    action: ARM
    template: output.bicep
    parameters: output.bicepparam
    deploymentLevel: ResourceGroup
    outputOnly: true
  - name: remove-legacy-stack
    action: ARMStack
    operation: delete
    deploymentLevel: ResourceGroup
    deploymentStackNameForDelete:
      value: Legacy.shared.stack
    actionOnUnmanage: detach
    identityFrom:
      resourceGroup: shared
      step: output
      name: identity
`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			path := filepath.Join(dir, "pipeline.yaml")
			require.NoError(t, os.WriteFile(path, []byte(pipelineHeader+tc.resourceGroups), 0600))
			pipelines := []servicePipeline{{ServiceGroup: "Microsoft.Azure.ARO.HCP.Stacks", Path: path}}
			if tc.otherPipeline != "" {
				other := filepath.Join(dir, "other.yaml")
				require.NoError(t, os.WriteFile(other, []byte(tc.otherPipeline), 0600))
				pipelines = append(pipelines, servicePipeline{ServiceGroup: "Microsoft.Azure.ARO.Other.Stacks", Path: other})
			}

			got, err := findCollisions("public", "prod", []string{"centralus", "eastus2", "uksouth"}, pipelines, render)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}
