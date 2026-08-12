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

package validationutils

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v2"
	azurecheckaccessv2client "github.com/Azure/checkaccess-v2-go-sdk/client"

	"github.com/Azure/ARO-HCP/backend/pkg/azure/cachedreader"
)

func TestFetchRoleDefinitions(t *testing.T) {
	roleDefID1, err := azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/providers/Microsoft.Authorization/roleDefinitions/11111111-1111-1111-1111-111111111111")
	require.NoError(t, err)
	roleDefID2, err := azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/providers/Microsoft.Authorization/roleDefinitions/22222222-2222-2222-2222-222222222222")
	require.NoError(t, err)

	roleDef1 := armauthorization.RoleDefinition{
		ID:   ptr.To(roleDefID1.String()),
		Name: ptr.To("Role1"),
		Properties: &armauthorization.RoleDefinitionProperties{
			Permissions: []*armauthorization.Permission{
				{Actions: []*string{ptr.To("Microsoft.Network/networkSecurityGroups/read")}},
			},
		},
	}
	roleDef2 := armauthorization.RoleDefinition{
		ID:   ptr.To(roleDefID2.String()),
		Name: ptr.To("Role2"),
		Properties: &armauthorization.RoleDefinitionProperties{
			Permissions: []*armauthorization.Permission{
				{Actions: []*string{ptr.To("Microsoft.Network/virtualNetworks/read")}},
			},
		},
	}

	tests := []struct {
		name        string
		resourceIDs []*azcorearm.ResourceID
		setupMock   func(*cachedreader.MockRoleDefinitionsCachedReader)
		wantResult  []armauthorization.RoleDefinition
		wantErr     bool
	}{
		{
			name:        "single role definition returns one definition",
			resourceIDs: []*azcorearm.ResourceID{roleDefID1},
			setupMock: func(m *cachedreader.MockRoleDefinitionsCachedReader) {
				m.EXPECT().GetCachedByID(gomock.Any(), roleDefID1.String(), nil).
					Return(armauthorization.RoleDefinitionsClientGetByIDResponse{RoleDefinition: roleDef1}, nil)
			},
			wantResult: []armauthorization.RoleDefinition{roleDef1},
			wantErr:    false,
		},
		{
			name:        "multiple role definition IDs returns all",
			resourceIDs: []*azcorearm.ResourceID{roleDefID1, roleDefID2},
			setupMock: func(m *cachedreader.MockRoleDefinitionsCachedReader) {
				m.EXPECT().GetCachedByID(gomock.Any(), roleDefID1.String(), nil).
					Return(armauthorization.RoleDefinitionsClientGetByIDResponse{RoleDefinition: roleDef1}, nil)
				m.EXPECT().GetCachedByID(gomock.Any(), roleDefID2.String(), nil).
					Return(armauthorization.RoleDefinitionsClientGetByIDResponse{RoleDefinition: roleDef2}, nil)
			},
			wantResult: []armauthorization.RoleDefinition{roleDef1, roleDef2},
			wantErr:    false,
		},
		{
			name:        "cached reader returns error",
			resourceIDs: []*azcorearm.ResourceID{roleDefID1},
			setupMock: func(m *cachedreader.MockRoleDefinitionsCachedReader) {
				m.EXPECT().GetCachedByID(gomock.Any(), roleDefID1.String(), nil).
					Return(armauthorization.RoleDefinitionsClientGetByIDResponse{}, fmt.Errorf("cache miss and fetch failed"))
			},
			wantResult: nil,
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			mockCachedReader := cachedreader.NewMockRoleDefinitionsCachedReader(ctrl)
			if tt.setupMock != nil {
				tt.setupMock(mockCachedReader)
			}

			readers := &cachedreader.BackendIdentityAzureCachedReaders{
				RoleDefinitionsCachedReader: mockCachedReader,
			}
			result, err := fetchRoleDefinitions(context.Background(), tt.resourceIDs, readers)

			if tt.wantErr {
				assert.Error(t, err)
				assert.Nil(t, result)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.wantResult, result)
			}
		})
	}
}

func TestCollectNotAllowedAndDeniedActions(t *testing.T) {
	tests := []struct {
		name     string
		input    []azurecheckaccessv2client.AuthorizationDecision
		expected []*checkaccessv2AuthorizationDecisionData
	}{
		{
			name:     "empty input returns nil",
			input:    []azurecheckaccessv2client.AuthorizationDecision{},
			expected: nil,
		},
		{
			name: "all allowed returns nil",
			input: []azurecheckaccessv2client.AuthorizationDecision{
				{ActionId: "Microsoft.Network/networkSecurityGroups/read", AccessDecision: azurecheckaccessv2client.Allowed},
				{ActionId: "Microsoft.Network/networkSecurityGroups/write", AccessDecision: azurecheckaccessv2client.Allowed},
			},
			expected: nil,
		},
		{
			name: "mix of allowed, not allowed, and denied returns only non-allowed",
			input: []azurecheckaccessv2client.AuthorizationDecision{
				{ActionId: "Microsoft.Network/networkSecurityGroups/read", AccessDecision: azurecheckaccessv2client.Allowed},
				{ActionId: "Microsoft.Network/networkSecurityGroups/write", AccessDecision: azurecheckaccessv2client.NotAllowed},
				{ActionId: "Microsoft.Network/networkSecurityGroups/join/action", AccessDecision: azurecheckaccessv2client.Denied},
			},
			expected: []*checkaccessv2AuthorizationDecisionData{
				{ActionID: "Microsoft.Network/networkSecurityGroups/write", IsDataAction: false, AccessDecision: azurecheckaccessv2client.NotAllowed},
				{ActionID: "Microsoft.Network/networkSecurityGroups/join/action", IsDataAction: false, AccessDecision: azurecheckaccessv2client.Denied},
			},
		},
		{
			name: "all not allowed or denied returns all",
			input: []azurecheckaccessv2client.AuthorizationDecision{
				{ActionId: "Microsoft.Network/networkSecurityGroups/read", AccessDecision: azurecheckaccessv2client.NotAllowed},
				{ActionId: "Microsoft.Network/networkSecurityGroups/write", AccessDecision: azurecheckaccessv2client.Denied},
			},
			expected: []*checkaccessv2AuthorizationDecisionData{
				{ActionID: "Microsoft.Network/networkSecurityGroups/read", IsDataAction: false, AccessDecision: azurecheckaccessv2client.NotAllowed},
				{ActionID: "Microsoft.Network/networkSecurityGroups/write", IsDataAction: false, AccessDecision: azurecheckaccessv2client.Denied},
			},
		},
		{
			name: "data actions are correctly propagated",
			input: []azurecheckaccessv2client.AuthorizationDecision{
				{ActionId: "Microsoft.Storage/storageAccounts/blobServices/containers/blobs/read", AccessDecision: azurecheckaccessv2client.NotAllowed, IsDataAction: true},
				{ActionId: "Microsoft.Network/networkSecurityGroups/read", AccessDecision: azurecheckaccessv2client.Allowed, IsDataAction: false},
			},
			expected: []*checkaccessv2AuthorizationDecisionData{
				{ActionID: "Microsoft.Storage/storageAccounts/blobServices/containers/blobs/read", IsDataAction: true, AccessDecision: azurecheckaccessv2client.NotAllowed},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := collectNotAllowedAndDeniedActions(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}
