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

package coreapihelpers

import (
	"testing"

	"github.com/stretchr/testify/assert"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armdeploymentstacks"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
)

const (
	testIdentityResourceIDString      = "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/test-rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/test-identity"
	testOtherIdentityResourceIDString = "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/test-rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/other-identity"
	testRoleDefinitionResourceID      = "/providers/Microsoft.Authorization/roleDefinitions/00000000-0000-0000-0000-000000000000"
	testPrincipalID                   = "test-principal"
)

func testIdentityResourceID(t *testing.T) *azcorearm.ResourceID {
	t.Helper()
	return metadataapi.Must(azcorearm.ParseResourceID(testIdentityResourceIDString))
}

func testAzureResource(t *testing.T) *azcorearm.ResourceID {
	t.Helper()
	return metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/test-managed-rg/providers/Microsoft.Authorization/roleAssignments/11111111-1111-1111-1111-111111111111"))
}

// testManagedStackDesire builds a StackDesire whose last-observed Azure state
// reports each of managedIDs as "managed".
func testManagedStackDesire(managedIDs ...*azcorearm.ResourceID) *coreapi.StackDesire {
	resources := make([]*armdeploymentstacks.ManagedResourceReference, 0, len(managedIDs))
	for _, id := range managedIDs {
		resources = append(resources, &armdeploymentstacks.ManagedResourceReference{
			ID:     ptr.To(id.String()),
			Status: ptr.To(armdeploymentstacks.ResourceStatusModeManaged),
		})
	}
	return &coreapi.StackDesire{
		Status: coreapi.StackDesireStatus{
			Stack: &armdeploymentstacks.DeploymentStack{
				Properties: &armdeploymentstacks.DeploymentStackProperties{
					Resources: resources,
				},
			},
		},
	}
}

func TestRoleAssignmentStatusManaged(t *testing.T) {
	tests := []struct {
		name     string
		status   *coreapi.RoleAssignmentStatus
		desire   *coreapi.StackDesire
		expected bool
	}{
		{
			name:     "no reserved resource ID and no deconfigure timestamp, considered as not managed",
			status:   &coreapi.RoleAssignmentStatus{},
			desire:   testManagedStackDesire(testAzureResource(t)),
			expected: false,
		},
		{
			name: "reserved resource ID present and managed by desire, considered as managed",
			status: &coreapi.RoleAssignmentStatus{
				ReservedAzureResourceID: testAzureResource(t),
			},
			desire:   testManagedStackDesire(testAzureResource(t)),
			expected: true,
		},
		{
			name: "reserved resource ID present but not yet observed in desire, considered as not managed",
			status: &coreapi.RoleAssignmentStatus{
				ReservedAzureResourceID: testAzureResource(t),
			},
			desire:   testManagedStackDesire(),
			expected: false,
		},
		{
			name: "nil desire, considered as not managed",
			status: &coreapi.RoleAssignmentStatus{
				ReservedAzureResourceID: testAzureResource(t),
			},
			desire:   nil,
			expected: false,
		},
		{
			name: "desire reports the resource as failed rather than managed, considered as not managed",
			status: &coreapi.RoleAssignmentStatus{
				ReservedAzureResourceID: testAzureResource(t),
			},
			desire: &coreapi.StackDesire{
				Status: coreapi.StackDesireStatus{
					Stack: &armdeploymentstacks.DeploymentStack{
						Properties: &armdeploymentstacks.DeploymentStackProperties{
							FailedResources: []*armdeploymentstacks.ResourceReferenceExtended{
								{ID: ptr.To(testAzureResource(t).String())},
							},
						},
					},
				},
			},
			expected: false,
		},
		{
			name: "resource ID present but draining, considered as not managed",
			status: &coreapi.RoleAssignmentStatus{
				ReservedAzureResourceID: testAzureResource(t),
				DeconfigureTimestamp:    ptrTime(metav1.Now()),
			},
			desire:   testManagedStackDesire(testAzureResource(t)),
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, RoleAssignmentStatusManaged(tt.status, tt.desire))
		})
	}
}

func TestServiceProviderClusterStatusIdentityRoleAssignmentsConfigured(t *testing.T) {
	tests := []struct {
		name     string
		status   *coreapi.ServiceProviderClusterStatus
		desire   *coreapi.StackDesire
		expected bool
	}{
		{
			name:     "no entries, considered as not configured",
			status:   &coreapi.ServiceProviderClusterStatus{},
			desire:   testManagedStackDesire(testAzureResource(t)),
			expected: false,
		},
		{
			name: "entries only for a different identity, considered as not configured",
			status: &coreapi.ServiceProviderClusterStatus{
				RoleAssignmentsOverManagedResourceGroup: map[coreapi.RoleAssignmentKey]*coreapi.RoleAssignmentStatus{
					{PrincipalID: testPrincipalID, RoleDefinitionResourceID: testRoleDefinitionResourceID}: {
						ReservedAzureResourceID: testAzureResource(t),
						TargetIdentity: &coreapi.RoleAssignmentTargetIdentity{
							ResourceID: metadataapi.Must(azcorearm.ParseResourceID(testOtherIdentityResourceIDString)),
						},
					},
				},
			},
			desire:   testManagedStackDesire(testAzureResource(t)),
			expected: false,
		},
		{
			name: "entries only for a different principal on the same identity, considered as not configured",
			status: &coreapi.ServiceProviderClusterStatus{
				RoleAssignmentsOverManagedResourceGroup: map[coreapi.RoleAssignmentKey]*coreapi.RoleAssignmentStatus{
					{PrincipalID: "other-principal", RoleDefinitionResourceID: testRoleDefinitionResourceID}: {
						ReservedAzureResourceID: testAzureResource(t),
						TargetIdentity: &coreapi.RoleAssignmentTargetIdentity{
							ResourceID: testIdentityResourceID(t),
						},
					},
				},
			},
			desire:   testManagedStackDesire(testAzureResource(t)),
			expected: false,
		},
		{
			name: "single desired key that is managed, considered as configured",
			status: &coreapi.ServiceProviderClusterStatus{
				RoleAssignmentsOverManagedResourceGroup: map[coreapi.RoleAssignmentKey]*coreapi.RoleAssignmentStatus{
					{PrincipalID: testPrincipalID, RoleDefinitionResourceID: testRoleDefinitionResourceID}: {
						ReservedAzureResourceID: testAzureResource(t),
						TargetIdentity: &coreapi.RoleAssignmentTargetIdentity{
							ResourceID: testIdentityResourceID(t),
						},
					},
				},
			},
			desire:   testManagedStackDesire(testAzureResource(t)),
			expected: true,
		},
		{
			name: "single desired key that is not yet managed, considered as not configured",
			status: &coreapi.ServiceProviderClusterStatus{
				RoleAssignmentsOverManagedResourceGroup: map[coreapi.RoleAssignmentKey]*coreapi.RoleAssignmentStatus{
					{PrincipalID: testPrincipalID, RoleDefinitionResourceID: testRoleDefinitionResourceID}: {
						ReservedAzureResourceID: testAzureResource(t),
						TargetIdentity: &coreapi.RoleAssignmentTargetIdentity{
							ResourceID: testIdentityResourceID(t),
						},
					},
				},
			},
			desire:   testManagedStackDesire(),
			expected: false,
		},
		{
			name: "only a draining key for the identity with nothing else desired, considered as not configured",
			status: &coreapi.ServiceProviderClusterStatus{
				RoleAssignmentsOverManagedResourceGroup: map[coreapi.RoleAssignmentKey]*coreapi.RoleAssignmentStatus{
					{PrincipalID: testPrincipalID, RoleDefinitionResourceID: testRoleDefinitionResourceID}: {
						ReservedAzureResourceID: testAzureResource(t),
						DeconfigureTimestamp:    ptrTime(metav1.Now()),
						TargetIdentity: &coreapi.RoleAssignmentTargetIdentity{
							ResourceID: testIdentityResourceID(t),
						},
					},
				},
			},
			desire:   testManagedStackDesire(testAzureResource(t)),
			expected: false,
		},
		{
			name: "multiple desired keys all managed, considered as configured",
			status: &coreapi.ServiceProviderClusterStatus{
				RoleAssignmentsOverManagedResourceGroup: map[coreapi.RoleAssignmentKey]*coreapi.RoleAssignmentStatus{
					{PrincipalID: testPrincipalID, RoleDefinitionResourceID: testRoleDefinitionResourceID}: {
						ReservedAzureResourceID: testAzureResource(t),
						TargetIdentity: &coreapi.RoleAssignmentTargetIdentity{
							ResourceID: testIdentityResourceID(t),
						},
					},
					{PrincipalID: testPrincipalID, RoleDefinitionResourceID: testRoleDefinitionResourceID + "-2"}: {
						ReservedAzureResourceID: testAzureResource(t),
						TargetIdentity: &coreapi.RoleAssignmentTargetIdentity{
							ResourceID: testIdentityResourceID(t),
						},
					},
				},
			},
			desire:   testManagedStackDesire(testAzureResource(t)),
			expected: true,
		},
		{
			name: "multiple desired keys with one not managed, considered as not configured",
			status: &coreapi.ServiceProviderClusterStatus{
				RoleAssignmentsOverManagedResourceGroup: map[coreapi.RoleAssignmentKey]*coreapi.RoleAssignmentStatus{
					{PrincipalID: testPrincipalID, RoleDefinitionResourceID: testRoleDefinitionResourceID}: {
						ReservedAzureResourceID: testAzureResource(t),
						TargetIdentity: &coreapi.RoleAssignmentTargetIdentity{
							ResourceID: testIdentityResourceID(t),
						},
					},
					{PrincipalID: testPrincipalID, RoleDefinitionResourceID: testRoleDefinitionResourceID + "-2"}: {
						TargetIdentity: &coreapi.RoleAssignmentTargetIdentity{
							ResourceID: testIdentityResourceID(t),
						},
					},
				},
			},
			desire:   testManagedStackDesire(testAzureResource(t)),
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ServiceProviderClusterStatusIdentityRoleAssignmentsConfigured(tt.status, tt.desire, testIdentityResourceID(t), testPrincipalID)
			assert.Equal(t, tt.expected, got)
		})
	}
}

func ptrTime(t metav1.Time) *metav1.Time {
	return &t
}
