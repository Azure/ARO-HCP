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
	"testing"

	"github.com/stretchr/testify/assert"

	azurecheckaccessv2client "github.com/Azure/checkaccess-v2-go-sdk/client"
)

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
