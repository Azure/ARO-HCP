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

package azurehelpers

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"k8s.io/utils/ptr"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v2"
)

func TestActionsFromRoleDefinition(t *testing.T) {
	tests := []struct {
		name              string
		roleDefinition    armauthorization.RoleDefinition
		expectedActions   []string
		expectError       bool
		errorContainsText string
	}{
		{
			name:              "returns error when properties is nil",
			roleDefinition:    armauthorization.RoleDefinition{ID: ptr.To("rd1")},
			expectError:       true,
			errorContainsText: "doesn't contain permissions",
		},
		{
			name: "returns error when permissions is nil",
			roleDefinition: armauthorization.RoleDefinition{
				Properties: &armauthorization.RoleDefinitionProperties{
					Permissions: nil,
				},
			},
			expectError:       true,
			errorContainsText: "doesn't contain permissions",
		},
		{
			name: "collects actions from all permission entries",
			roleDefinition: armauthorization.RoleDefinition{
				ID: ptr.To("/subscriptions/sub/resource"),
				Properties: &armauthorization.RoleDefinitionProperties{
					Permissions: []*armauthorization.Permission{
						{
							Actions: []*string{
								ptr.To("Microsoft.Network/dnsZones/A/delete"),
								ptr.To("Microsoft.Network/dnsZones/A/write"),
							},
							DataActions: []*string{ptr.To("Microsoft.Storage/storageAccounts/blobServices/containers/blobs/read")},
						},
						{
							Actions: []*string{
								ptr.To("Microsoft.Network/privateDnsZones/A/delete"),
								ptr.To("Microsoft.Network/privateDnsZones/A/write"),
							},
						},
						{
							Actions: []*string{ptr.To("Microsoft.Network/virtualNetworks/subnets/read")},
							DataActions: []*string{
								ptr.To("Microsoft.Storage/storageAccounts/blobServices/containers/blobs/write"),
							},
						},
						{Actions: []*string{ptr.To("Microsoft.Network/virtualNetworks/subnets/join/action")}},
					},
				},
			},
			expectedActions: []string{
				"Microsoft.Network/dnsZones/A/delete",
				"Microsoft.Network/dnsZones/A/write",
				"Microsoft.Network/privateDnsZones/A/delete",
				"Microsoft.Network/privateDnsZones/A/write",
				"Microsoft.Network/virtualNetworks/subnets/read",
				"Microsoft.Network/virtualNetworks/subnets/join/action",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actions, err := ActionsFromRoleDefinition(tt.roleDefinition)
			if tt.expectError {
				require.Error(t, err)
				if tt.errorContainsText != "" {
					assert.ErrorContains(t, err, tt.errorContainsText)
				}
				return
			}

			require.NoError(t, err)
			assert.ElementsMatch(t, tt.expectedActions, actions)
		})
	}
}

func TestDataActionsFromRoleDefinition(t *testing.T) {
	tests := []struct {
		name                string
		roleDefinition      armauthorization.RoleDefinition
		expectedDataActions []string
		expectError         bool
		errorContainsText   string
	}{
		{
			name:              "returns error when properties is nil",
			roleDefinition:    armauthorization.RoleDefinition{},
			expectError:       true,
			errorContainsText: "doesn't contain permissions",
		},
		{
			name: "returns error when permissions is nil",
			roleDefinition: armauthorization.RoleDefinition{
				Properties: &armauthorization.RoleDefinitionProperties{
					Permissions: nil,
				},
			},
			expectError:       true,
			errorContainsText: "doesn't contain permissions",
		},
		{
			name: "collects data actions",
			roleDefinition: armauthorization.RoleDefinition{
				Properties: &armauthorization.RoleDefinitionProperties{
					Permissions: []*armauthorization.Permission{
						{
							DataActions: []*string{
								ptr.To("Microsoft.Storage/storageAccounts/blobServices/containers/blobs/read"),
								ptr.To("Microsoft.Storage/storageAccounts/blobServices/containers/blobs/write"),
							},
							Actions: []*string{ptr.To("Microsoft.Compute/*/read")},
						},
						{
							DataActions: []*string{ptr.To("Microsoft.Storage/storageAccounts/queueServices/queues/messages/read")},
							Actions: []*string{
								ptr.To("Microsoft.Compute/virtualMachines/read"),
								ptr.To("Microsoft.Compute/virtualMachines/write"),
							},
						},
						{
							Actions: []*string{ptr.To("Microsoft.Network/virtualNetworks/read")},
						},
					},
				},
			},
			expectedDataActions: []string{
				"Microsoft.Storage/storageAccounts/blobServices/containers/blobs/read",
				"Microsoft.Storage/storageAccounts/blobServices/containers/blobs/write",
				"Microsoft.Storage/storageAccounts/queueServices/queues/messages/read",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := DataActionsFromRoleDefinition(tt.roleDefinition)
			if tt.expectError {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.ElementsMatch(t, tt.expectedDataActions, data)
		})
	}
}

func TestUnionActions(t *testing.T) {
	tests := []struct {
		name            string
		roleDefinitions []armauthorization.RoleDefinition
		expectedActions []string
		expectError     bool
	}{
		{
			name: "unions and deduplicates",
			roleDefinitions: []armauthorization.RoleDefinition{
				{
					Properties: &armauthorization.RoleDefinitionProperties{
						Permissions: []*armauthorization.Permission{
							{
								Actions: []*string{
									ptr.To("Microsoft.Network/dnsZones/A/delete"),
									ptr.To("Microsoft.Network/dnsZones/A/write"),
								},
								DataActions: []*string{ptr.To("data/actions/a")},
							},
							{
								Actions: []*string{
									ptr.To("Microsoft.Network/privateDnsZones/A/delete"),
									ptr.To("Microsoft.Network/privateDnsZones/A/write"),
								},
							},
						},
					},
				},
				{
					Properties: &armauthorization.RoleDefinitionProperties{
						Permissions: []*armauthorization.Permission{
							{
								Actions: []*string{
									ptr.To("Microsoft.Network/virtualNetworks/subnets/read"),
									ptr.To("Microsoft.Network/virtualNetworks/subnets/join/action"),
								},
								DataActions: []*string{ptr.To("data/actions/b")},
							},
							{Actions: []*string{ptr.To("Microsoft.Network/dnsZones/A/delete")}},
						},
					},
				},
			},
			expectedActions: []string{
				"Microsoft.Network/dnsZones/A/delete",
				"Microsoft.Network/dnsZones/A/write",
				"Microsoft.Network/privateDnsZones/A/delete",
				"Microsoft.Network/privateDnsZones/A/write",
				"Microsoft.Network/virtualNetworks/subnets/read",
				"Microsoft.Network/virtualNetworks/subnets/join/action",
			},
		},
		{
			name: "propagates error from a role definition",
			roleDefinitions: []armauthorization.RoleDefinition{
				{Properties: nil},
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := UnionActions(tt.roleDefinitions)
			if tt.expectError {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.ElementsMatch(t, tt.expectedActions, out)
		})
	}
}

func TestIntersectActions(t *testing.T) {
	tests := []struct {
		name     string
		a        []string
		b        []string
		expected []string
	}{
		{
			name: "returns intersection of a and b",
			a: []string{
				"Microsoft.Network/dnsZones/A/delete",
				"Microsoft.Network/dnsZones/A/write",
				"Microsoft.Network/privateDnsZones/A/delete",
			},
			b: []string{
				"Microsoft.Network/dnsZones/A/write",
				"Microsoft.Network/privateDnsZones/A/delete",
				"Microsoft.Network/virtualNetworks/subnets/read",
			},
			expected: []string{
				"Microsoft.Network/dnsZones/A/write",
				"Microsoft.Network/privateDnsZones/A/delete",
			},
		},
		{
			name:     "returns empty when there is no overlap",
			a:        []string{"Microsoft.Network/dnsZones/A/delete"},
			b:        []string{"Microsoft.Network/virtualNetworks/subnets/read"},
			expected: nil,
		},
		{
			name:     "returns empty when a is empty",
			a:        nil,
			b:        []string{"Microsoft.Network/dnsZones/A/delete"},
			expected: nil,
		},
		{
			name:     "returns empty when b is empty",
			a:        []string{"Microsoft.Network/dnsZones/A/delete"},
			b:        nil,
			expected: nil,
		},
		{
			name:     "returns empty when both slices are empty",
			a:        nil,
			b:        nil,
			expected: nil,
		},
		{
			name: "deduplicates elements from a",
			a: []string{
				"Microsoft.Network/dnsZones/A/delete",
				"Microsoft.Network/dnsZones/A/delete",
				"Microsoft.Network/dnsZones/A/write",
			},
			b: []string{"Microsoft.Network/dnsZones/A/delete"},
			expected: []string{
				"Microsoft.Network/dnsZones/A/delete",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.ElementsMatch(t, tt.expected, IntersectActions(tt.a, tt.b))
		})
	}
}

func TestUnionDataActions(t *testing.T) {
	tests := []struct {
		name                string
		roleDefinitions     []armauthorization.RoleDefinition
		expectedDataActions []string
		expectError         bool
	}{
		{
			name: "unions and deduplicates",
			roleDefinitions: []armauthorization.RoleDefinition{
				{
					Properties: &armauthorization.RoleDefinitionProperties{
						Permissions: []*armauthorization.Permission{
							{
								DataActions: []*string{
									ptr.To("Microsoft.Storage/storageAccounts/blobServices/containers/blobs/read"),
									ptr.To("Microsoft.Storage/storageAccounts/blobServices/containers/blobs/write"),
								},
								Actions: []*string{ptr.To("Microsoft.Network/dnsZones/A/delete")},
							},
							{
								DataActions: []*string{ptr.To("Microsoft.Storage/storageAccounts/queueServices/queues/messages/read")},
							},
						},
					},
				},
				{
					Properties: &armauthorization.RoleDefinitionProperties{
						Permissions: []*armauthorization.Permission{
							{
								DataActions: []*string{
									ptr.To("Microsoft.Storage/storageAccounts/blobServices/containers/blobs/read"),
									ptr.To("Microsoft.Storage/storageAccounts/blobServices/containers/blobs/delete"),
								},
								Actions: []*string{
									ptr.To("Microsoft.Network/dnsZones/A/write"),
									ptr.To("Microsoft.Compute/virtualMachines/read"),
								},
							},
						},
					},
				},
			},
			expectedDataActions: []string{
				"Microsoft.Storage/storageAccounts/blobServices/containers/blobs/read",
				"Microsoft.Storage/storageAccounts/blobServices/containers/blobs/write",
				"Microsoft.Storage/storageAccounts/queueServices/queues/messages/read",
				"Microsoft.Storage/storageAccounts/blobServices/containers/blobs/delete",
			},
		},
		{
			name: "propagates error from a role definition",
			roleDefinitions: []armauthorization.RoleDefinition{
				{Properties: nil},
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := UnionDataActions(tt.roleDefinitions)
			if tt.expectError {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.ElementsMatch(t, tt.expectedDataActions, out)
		})
	}
}
