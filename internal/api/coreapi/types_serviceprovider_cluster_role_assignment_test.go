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

package coreapi

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRoleAssignmentKeyTextRoundTrip(t *testing.T) {
	t.Parallel()

	key := RoleAssignmentKey{
		ResourceID:       "/subscriptions/00000000-0000-0000-0000-000000000000/resourcegroups/test-rg/providers/microsoft.managedidentity/userassignedidentities/identity-a",
		PrincipalID:      "principal-a",
		RoleDefinitionResourceID: "/providers/Microsoft.Authorization/roleDefinitions/88366f10-ed47-4cc0-9fab-c8a06148393e",
	}

	text, err := key.MarshalText()
	require.NoError(t, err)

	var decoded RoleAssignmentKey
	require.NoError(t, decoded.UnmarshalText(text))
	assert.Equal(t, key, decoded)
}

func TestRoleAssignmentKeyJSONMapRoundTrip(t *testing.T) {
	t.Parallel()

	key := RoleAssignmentKey{
		ResourceID:       "/subscriptions/00000000-0000-0000-0000-000000000000/resourcegroups/test-rg/providers/microsoft.managedidentity/userassignedidentities/identity-a",
		PrincipalID:      "principal-a",
		RoleDefinitionResourceID: "/providers/Microsoft.Authorization/roleDefinitions/88366f10-ed47-4cc0-9fab-c8a06148393e",
	}
	original := map[RoleAssignmentKey]*RoleAssignmentStatus{
		key: {
			Phase: RoleAssignmentPhasePendingConfigure,
			ObservedIdentity: &RoleAssignmentObservedIdentity{
				ClientID:    "client-a",
				TenantID:    "tenant-a",
				PrincipalID: "principal-a",
			},
		},
	}

	encoded, err := json.Marshal(original)
	require.NoError(t, err)

	var decoded map[RoleAssignmentKey]*RoleAssignmentStatus
	require.NoError(t, json.Unmarshal(encoded, &decoded))
	require.Contains(t, decoded, key)
	assert.Equal(t, RoleAssignmentPhasePendingConfigure, decoded[key].Phase)
	require.NotNil(t, decoded[key].ObservedIdentity)
	assert.Equal(t, "client-a", decoded[key].ObservedIdentity.ClientID)
	assert.Equal(t, "tenant-a", decoded[key].ObservedIdentity.TenantID)
	assert.Equal(t, "principal-a", decoded[key].ObservedIdentity.PrincipalID)
}

func TestRoleAssignmentKeyUnmarshalRejectsWrongPartCount(t *testing.T) {
	t.Parallel()

	var key RoleAssignmentKey
	err := key.UnmarshalText([]byte("only|two"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expected 3 parts")
}
