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

package pipeline

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	configtypes "github.com/Azure/ARO-Tools/config/types"
	"github.com/Azure/ARO-Tools/pipelines/types"
	"github.com/Azure/ARO-Tools/tools/stackctl/cmd/deletestack"
)

func TestARMStackDeleteOptions(t *testing.T) {
	const (
		serviceGroup   = "Microsoft.Azure.ARO.HCP.Global"
		subscriptionID = "00000000-0000-0000-0000-000000000000"
		resourceGroup  = "global-shared-resources"
	)
	cfg := configtypes.Configuration{"global": map[string]any{"acrStackName": "Global.singleton.acrs"}}
	outputs := Outputs{
		serviceGroup: {
			"global": {
				"output": ArmOutput(map[string]any{
					"stackName": map[string]any{"type": "string", "value": "Global.singleton.imagemirror"},
				}),
			},
		},
	}
	deleteStep := func(mutate func(s *types.ARMStackStep)) *types.ARMStackStep {
		s := &types.ARMStackStep{
			Operation:                    types.ARMStackOperationDelete,
			DeploymentLevel:              "ResourceGroup",
			DeploymentStackNameForDelete: &types.Value{Value: "MSI.CredentialRefresher.management.msi-keyvault-permission"},
			ActionOnUnmanage:             "detach",
		}
		if mutate != nil {
			mutate(s)
		}
		return s
	}
	defaultTimeout := deletestack.DefaultOptions().Timeout

	for _, tc := range []struct {
		name        string
		step        *types.ARMStackStep
		wantName    string
		wantAction  string
		wantTimeout time.Duration
		wantError   string
	}{
		{
			name:        "literal name, detach, default timeout",
			step:        deleteStep(nil),
			wantName:    "MSI.CredentialRefresher.management.msi-keyvault-permission",
			wantAction:  "detach",
			wantTimeout: defaultTimeout,
		},
		{
			name:        "delete action is passed through",
			step:        deleteStep(func(s *types.ARMStackStep) { s.ActionOnUnmanage = "delete" }),
			wantName:    "MSI.CredentialRefresher.management.msi-keyvault-permission",
			wantAction:  "delete",
			wantTimeout: defaultTimeout,
		},
		{
			name: "name from config",
			step: deleteStep(func(s *types.ARMStackStep) {
				s.DeploymentStackNameForDelete = &types.Value{ConfigRef: "global.acrStackName"}
			}),
			wantName:    "Global.singleton.acrs",
			wantAction:  "detach",
			wantTimeout: defaultTimeout,
		},
		{
			name: "name from step input",
			step: deleteStep(func(s *types.ARMStackStep) {
				s.DeploymentStackNameForDelete = &types.Value{Input: &types.Input{StepDependency: types.StepDependency{ResourceGroup: "global", Step: "output"}, Name: "stackName"}}
			}),
			wantName:    "Global.singleton.imagemirror",
			wantAction:  "detach",
			wantTimeout: defaultTimeout,
		},
		{
			name:        "valid timeout",
			step:        deleteStep(func(s *types.ARMStackStep) { s.Timeout = "10m" }),
			wantName:    "MSI.CredentialRefresher.management.msi-keyvault-permission",
			wantAction:  "detach",
			wantTimeout: 10 * time.Minute,
		},
		{
			name:      "invalid timeout",
			step:      deleteStep(func(s *types.ARMStackStep) { s.Timeout = "soon" }),
			wantError: "failed to parse timeout",
		},
		{
			name:      "missing name",
			step:      deleteStep(func(s *types.ARMStackStep) { s.DeploymentStackNameForDelete = nil }),
			wantError: "deploymentStackNameForDelete is required",
		},
		{
			name: "unresolvable config name",
			step: deleteStep(func(s *types.ARMStackStep) {
				s.DeploymentStackNameForDelete = &types.Value{ConfigRef: "global.missing"}
			}),
			wantError: "failed to resolve deploymentStackNameForDelete",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opts, err := armStackDeleteOptions(tc.step, cfg, outputs, serviceGroup, "dev", subscriptionID, resourceGroup)
			if tc.wantError != "" {
				require.ErrorContains(t, err, tc.wantError)
				return
			}
			require.NoError(t, err)
			require.Equal(t, "dev", opts.CloudOptions.Cloud)
			require.Equal(t, subscriptionID, opts.SubscriptionID)
			require.Equal(t, resourceGroup, opts.ResourceGroup)
			require.Equal(t, tc.wantName, opts.Name)
			require.Equal(t, tc.wantAction, opts.ActionOnUnmanage)
			require.Equal(t, tc.wantTimeout, opts.Timeout)
			_, err = opts.Validate()
			require.NoError(t, err, "options built from a valid step must pass the tool's own validation")
		})
	}
}
