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
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

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

func TestRoleAssignmentStatusConfigured(t *testing.T) {
	tests := []struct {
		name     string
		status   *coreapi.RoleAssignmentStatus
		expected bool
	}{
		{
			name:     "no azure resource and no deconfigure timestamp, considered as not configured",
			status:   &coreapi.RoleAssignmentStatus{},
			expected: false,
		},
		{
			name: "azure resource present and not draining, considered as configured",
			status: &coreapi.RoleAssignmentStatus{
				AzureResource: testAzureResource(t),
			},
			expected: true,
		},
		{
			name: "no azure resource yet, considered as not configured",
			status: &coreapi.RoleAssignmentStatus{
				PendingAzureResource: testAzureResource(t),
			},
			expected: false,
		},
		{
			name: "azure resource present but draining, considered as not configured",
			status: &coreapi.RoleAssignmentStatus{
				AzureResource:        testAzureResource(t),
				DeconfigureTimestamp: ptrTime(metav1.Now()),
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, RoleAssignmentStatusConfigured(tt.status))
		})
	}
}

func TestServiceProviderClusterStatusRoleAssignmentConfigured(t *testing.T) {
	tests := []struct {
		name     string
		status   *coreapi.ServiceProviderClusterStatus
		expected bool
	}{
		{
			name:     "key not present considered as not configured",
			status:   &coreapi.ServiceProviderClusterStatus{},
			expected: false,
		},
		{
			name: "ensured azure resource with matching resource ID, considered as configured",
			status: &coreapi.ServiceProviderClusterStatus{
				RoleAssignmentsOverManagedResourceGroup: map[coreapi.RoleAssignmentKey]*coreapi.RoleAssignmentStatus{
					{PrincipalID: testPrincipalID, RoleDefinitionResourceID: testRoleDefinitionResourceID}: {
						AzureResource: testAzureResource(t),
						TargetIdentity: &coreapi.RoleAssignmentTargetIdentity{
							ResourceID: testIdentityResourceID(t),
						},
					},
				},
			},
			expected: true,
		},
		{
			name: "case-insensitive resource ID match, considered as configured",
			status: &coreapi.ServiceProviderClusterStatus{
				RoleAssignmentsOverManagedResourceGroup: map[coreapi.RoleAssignmentKey]*coreapi.RoleAssignmentStatus{
					{PrincipalID: testPrincipalID, RoleDefinitionResourceID: testRoleDefinitionResourceID}: {
						AzureResource: testAzureResource(t),
						TargetIdentity: &coreapi.RoleAssignmentTargetIdentity{
							ResourceID: metadataapi.Must(azcorearm.ParseResourceID(strings.ToUpper(testIdentityResourceIDString))),
						},
					},
				},
			},
			expected: true,
		},
		{
			name: "mismatched resource ID, considered as not configured",
			status: &coreapi.ServiceProviderClusterStatus{
				RoleAssignmentsOverManagedResourceGroup: map[coreapi.RoleAssignmentKey]*coreapi.RoleAssignmentStatus{
					{PrincipalID: testPrincipalID, RoleDefinitionResourceID: testRoleDefinitionResourceID}: {
						AzureResource: testAzureResource(t),
						TargetIdentity: &coreapi.RoleAssignmentTargetIdentity{
							ResourceID: metadataapi.Must(azcorearm.ParseResourceID(testOtherIdentityResourceIDString)),
						},
					},
				},
			},
			expected: false,
		},
		{
			name: "missing azure resource, considered as not configured",
			status: &coreapi.ServiceProviderClusterStatus{
				RoleAssignmentsOverManagedResourceGroup: map[coreapi.RoleAssignmentKey]*coreapi.RoleAssignmentStatus{
					{PrincipalID: testPrincipalID, RoleDefinitionResourceID: testRoleDefinitionResourceID}: {
						TargetIdentity: &coreapi.RoleAssignmentTargetIdentity{
							ResourceID: testIdentityResourceID(t),
						},
					},
				},
			},
			expected: false,
		},
		{
			name: "draining key, considered as not configured",
			status: &coreapi.ServiceProviderClusterStatus{
				RoleAssignmentsOverManagedResourceGroup: map[coreapi.RoleAssignmentKey]*coreapi.RoleAssignmentStatus{
					{PrincipalID: testPrincipalID, RoleDefinitionResourceID: testRoleDefinitionResourceID}: {
						AzureResource:        testAzureResource(t),
						DeconfigureTimestamp: ptrTime(metav1.Now()),
						TargetIdentity: &coreapi.RoleAssignmentTargetIdentity{
							ResourceID: testIdentityResourceID(t),
						},
					},
				},
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ServiceProviderClusterStatusRoleAssignmentConfigured(tt.status, testIdentityResourceID(t), testPrincipalID, testRoleDefinitionResourceID)
			assert.Equal(t, tt.expected, got)
		})
	}
}

func TestServiceProviderClusterStatusIdentityRoleAssignmentsConfigured(t *testing.T) {
	tests := []struct {
		name     string
		status   *coreapi.ServiceProviderClusterStatus
		expected bool
	}{
		{
			name:     "no entries, considered as not configured",
			status:   &coreapi.ServiceProviderClusterStatus{},
			expected: false,
		},
		{
			name: "entries only for a different identity, considered as not configured",
			status: &coreapi.ServiceProviderClusterStatus{
				RoleAssignmentsOverManagedResourceGroup: map[coreapi.RoleAssignmentKey]*coreapi.RoleAssignmentStatus{
					{PrincipalID: testPrincipalID, RoleDefinitionResourceID: testRoleDefinitionResourceID}: {
						AzureResource: testAzureResource(t),
						TargetIdentity: &coreapi.RoleAssignmentTargetIdentity{
							ResourceID: metadataapi.Must(azcorearm.ParseResourceID(testOtherIdentityResourceIDString)),
						},
					},
				},
			},
			expected: false,
		},
		{
			name: "entries only for a different principal on the same identity, considered as not configured",
			status: &coreapi.ServiceProviderClusterStatus{
				RoleAssignmentsOverManagedResourceGroup: map[coreapi.RoleAssignmentKey]*coreapi.RoleAssignmentStatus{
					{PrincipalID: "other-principal", RoleDefinitionResourceID: testRoleDefinitionResourceID}: {
						AzureResource: testAzureResource(t),
						TargetIdentity: &coreapi.RoleAssignmentTargetIdentity{
							ResourceID: testIdentityResourceID(t),
						},
					},
				},
			},
			expected: false,
		},
		{
			name: "single desired key that is configured, considered as configured",
			status: &coreapi.ServiceProviderClusterStatus{
				RoleAssignmentsOverManagedResourceGroup: map[coreapi.RoleAssignmentKey]*coreapi.RoleAssignmentStatus{
					{PrincipalID: testPrincipalID, RoleDefinitionResourceID: testRoleDefinitionResourceID}: {
						AzureResource: testAzureResource(t),
						TargetIdentity: &coreapi.RoleAssignmentTargetIdentity{
							ResourceID: testIdentityResourceID(t),
						},
					},
				},
			},
			expected: true,
		},
		{
			name: "single desired key that is not yet configured, considered as not configured",
			status: &coreapi.ServiceProviderClusterStatus{
				RoleAssignmentsOverManagedResourceGroup: map[coreapi.RoleAssignmentKey]*coreapi.RoleAssignmentStatus{
					{PrincipalID: testPrincipalID, RoleDefinitionResourceID: testRoleDefinitionResourceID}: {
						TargetIdentity: &coreapi.RoleAssignmentTargetIdentity{
							ResourceID: testIdentityResourceID(t),
						},
					},
				},
			},
			expected: false,
		},
		{
			name: "only a draining key for the identity with nothing else desired, considered as not configured",
			status: &coreapi.ServiceProviderClusterStatus{
				RoleAssignmentsOverManagedResourceGroup: map[coreapi.RoleAssignmentKey]*coreapi.RoleAssignmentStatus{
					{PrincipalID: testPrincipalID, RoleDefinitionResourceID: testRoleDefinitionResourceID}: {
						AzureResource:        testAzureResource(t),
						DeconfigureTimestamp: ptrTime(metav1.Now()),
						TargetIdentity: &coreapi.RoleAssignmentTargetIdentity{
							ResourceID: testIdentityResourceID(t),
						},
					},
				},
			},
			expected: false,
		},
		{
			name: "multiple desired keys all configured, considered as configured",
			status: &coreapi.ServiceProviderClusterStatus{
				RoleAssignmentsOverManagedResourceGroup: map[coreapi.RoleAssignmentKey]*coreapi.RoleAssignmentStatus{
					{PrincipalID: testPrincipalID, RoleDefinitionResourceID: testRoleDefinitionResourceID}: {
						AzureResource: testAzureResource(t),
						TargetIdentity: &coreapi.RoleAssignmentTargetIdentity{
							ResourceID: testIdentityResourceID(t),
						},
					},
					{PrincipalID: testPrincipalID, RoleDefinitionResourceID: testRoleDefinitionResourceID + "-2"}: {
						AzureResource: testAzureResource(t),
						TargetIdentity: &coreapi.RoleAssignmentTargetIdentity{
							ResourceID: testIdentityResourceID(t),
						},
					},
				},
			},
			expected: true,
		},
		{
			name: "multiple desired keys with one not configured, considered as not configured",
			status: &coreapi.ServiceProviderClusterStatus{
				RoleAssignmentsOverManagedResourceGroup: map[coreapi.RoleAssignmentKey]*coreapi.RoleAssignmentStatus{
					{PrincipalID: testPrincipalID, RoleDefinitionResourceID: testRoleDefinitionResourceID}: {
						AzureResource: testAzureResource(t),
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
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ServiceProviderClusterStatusIdentityRoleAssignmentsConfigured(tt.status, testIdentityResourceID(t), testPrincipalID)
			assert.Equal(t, tt.expected, got)
		})
	}
}

func TestServiceProviderClusterStatusDesiredRoleAssignmentsConfigured(t *testing.T) {
	tests := []struct {
		name     string
		status   *coreapi.ServiceProviderClusterStatus
		expected bool
	}{
		{
			name:     "no entries, considered as not configured",
			status:   &coreapi.ServiceProviderClusterStatus{},
			expected: false,
		},
		{
			name: "all entries draining, considered as not configured",
			status: &coreapi.ServiceProviderClusterStatus{
				RoleAssignmentsOverManagedResourceGroup: map[coreapi.RoleAssignmentKey]*coreapi.RoleAssignmentStatus{
					{PrincipalID: testPrincipalID, RoleDefinitionResourceID: testRoleDefinitionResourceID}: {
						AzureResource:        testAzureResource(t),
						DeconfigureTimestamp: ptrTime(metav1.Now()),
					},
				},
			},
			expected: false,
		},
		{
			name: "all desired keys configured, considered as configured",
			status: &coreapi.ServiceProviderClusterStatus{
				RoleAssignmentsOverManagedResourceGroup: map[coreapi.RoleAssignmentKey]*coreapi.RoleAssignmentStatus{
					{PrincipalID: testPrincipalID, RoleDefinitionResourceID: testRoleDefinitionResourceID}: {
						AzureResource: testAzureResource(t),
					},
					{PrincipalID: testPrincipalID, RoleDefinitionResourceID: testRoleDefinitionResourceID + "-2"}: {
						AzureResource: testAzureResource(t),
					},
				},
			},
			expected: true,
		},
		{
			name: "one desired key not configured, considered as not configured",
			status: &coreapi.ServiceProviderClusterStatus{
				RoleAssignmentsOverManagedResourceGroup: map[coreapi.RoleAssignmentKey]*coreapi.RoleAssignmentStatus{
					{PrincipalID: testPrincipalID, RoleDefinitionResourceID: testRoleDefinitionResourceID}: {
						AzureResource: testAzureResource(t),
					},
					{PrincipalID: testPrincipalID, RoleDefinitionResourceID: testRoleDefinitionResourceID + "-2"}: {},
				},
			},
			expected: false,
		},
		{
			name: "draining entries alongside a configured desired entry, considered as configured",
			status: &coreapi.ServiceProviderClusterStatus{
				RoleAssignmentsOverManagedResourceGroup: map[coreapi.RoleAssignmentKey]*coreapi.RoleAssignmentStatus{
					{PrincipalID: testPrincipalID, RoleDefinitionResourceID: testRoleDefinitionResourceID}: {
						AzureResource: testAzureResource(t),
					},
					{PrincipalID: "stale-principal", RoleDefinitionResourceID: testRoleDefinitionResourceID}: {
						AzureResource:        testAzureResource(t),
						DeconfigureTimestamp: ptrTime(metav1.Now()),
					},
				},
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ServiceProviderClusterStatusDesiredRoleAssignmentsConfigured(tt.status)
			assert.Equal(t, tt.expected, got)
		})
	}
}

func ptrTime(t metav1.Time) *metav1.Time {
	return &t
}
