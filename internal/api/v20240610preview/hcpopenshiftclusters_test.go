// Copyright 2025 Microsoft Corporation
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

package v20240610preview

import (
	"fmt"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
)

var (
	managedIdentity1 = api.NewTestUserAssignedIdentity("myManagedIdentity1")
	managedIdentity2 = api.NewTestUserAssignedIdentity("myManagedIdentity2")
	managedIdentity3 = api.NewTestUserAssignedIdentity("myManagedIdentity3")
)

func compareErrors(x, y []arm.CloudErrorBody) string {
	return cmp.Diff(x, y,
		cmpopts.SortSlices(func(x, y arm.CloudErrorBody) bool { return x.Target < y.Target }),
		cmpopts.IgnoreFields(arm.CloudErrorBody{}, "Code"))
}

func TestClusterRequiredForPut(t *testing.T) {
	tests := []struct {
		name         string
		tweaks       *api.HCPOpenShiftCluster
		expectErrors []arm.CloudErrorBody
	}{
		{
			name:   "Minimum valid cluster",
			tweaks: &api.HCPOpenShiftCluster{},
		},
		{
			name: "Cluster with identities",
			tweaks: &api.HCPOpenShiftCluster{
				Properties: api.HCPOpenShiftClusterProperties{
					Platform: api.PlatformProfile{
						OperatorsAuthentication: api.OperatorsAuthenticationProfile{
							UserAssignedIdentities: api.UserAssignedIdentitiesProfile{
								ControlPlaneOperators: map[string]string{
									"operatorX": managedIdentity1,
								},
								ServiceManagedIdentity: managedIdentity2,
							},
						},
					},
				},
				Identity: arm.ManagedServiceIdentity{
					UserAssignedIdentities: map[string]*arm.UserAssignedIdentity{
						managedIdentity1: &arm.UserAssignedIdentity{},
						managedIdentity2: &arm.UserAssignedIdentity{},
					},
				},
			},
		},
		{
			name: "Cluster with broken identities",
			tweaks: &api.HCPOpenShiftCluster{
				Properties: api.HCPOpenShiftClusterProperties{
					Platform: api.PlatformProfile{
						OperatorsAuthentication: api.OperatorsAuthenticationProfile{
							UserAssignedIdentities: api.UserAssignedIdentitiesProfile{
								ControlPlaneOperators: map[string]string{
									"operatorX": managedIdentity1,
								},
								ServiceManagedIdentity: managedIdentity2,
							},
						},
					},
				},
				Identity: arm.ManagedServiceIdentity{
					UserAssignedIdentities: map[string]*arm.UserAssignedIdentity{
						managedIdentity3: &arm.UserAssignedIdentity{},
					},
				},
			},
			expectErrors: []arm.CloudErrorBody{
				{
					Message: "identity " + managedIdentity1 + " is not assigned to this resource",
					Target:  "properties.platform.operatorsAuthentication.userAssignedIdentities.controlPlaneOperators[operatorX]",
				},
				{
					Message: "identity " + managedIdentity3 + " is assigned to this resource but not used",
					Target:  "identity.UserAssignedIdentities",
				},
				{
					Message: "identity " + managedIdentity2 + " is not assigned to this resource",
					Target:  "properties.platform.operatorsAuthentication.userAssignedIdentities.serviceManagedIdentity",
				},
			},
		},
		{
			name: "Cluster with multiple identities",
			tweaks: &api.HCPOpenShiftCluster{
				Properties: api.HCPOpenShiftClusterProperties{
					Platform: api.PlatformProfile{
						OperatorsAuthentication: api.OperatorsAuthenticationProfile{
							UserAssignedIdentities: api.UserAssignedIdentitiesProfile{
								ControlPlaneOperators: map[string]string{
									"operatorX": managedIdentity1,
									"operatorY": managedIdentity1,
								},
								ServiceManagedIdentity: managedIdentity1,
							},
						},
					},
				},
				Identity: arm.ManagedServiceIdentity{
					UserAssignedIdentities: map[string]*arm.UserAssignedIdentity{
						managedIdentity1: &arm.UserAssignedIdentity{},
					},
				},
			},
			expectErrors: []arm.CloudErrorBody{
				{
					Message: "identity " + managedIdentity1 + " is used multiple times",
					Target:  "properties.platform.operatorsAuthentication.userAssignedIdentities.controlPlaneOperators[operatorX]",
				},
				{
					Message: "identity " + managedIdentity1 + " is used multiple times",
					Target:  "properties.platform.operatorsAuthentication.userAssignedIdentities.controlPlaneOperators[operatorY]",
				},
				{
					Message: "identity " + managedIdentity1 + " is used multiple times",
					Target:  "properties.platform.operatorsAuthentication.userAssignedIdentities.serviceManagedIdentity",
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resource := api.ClusterTestCase(t, tt.tweaks)
			actualErrors := validateStaticComplex(resource)
			fmt.Printf("tt: %v\n", actualErrors)

			diff := compareErrors(tt.expectErrors, actualErrors)
			if diff != "" {
				t.Fatalf("Expected error mismatch:\n%s", diff)
			}
		})
	}
}
