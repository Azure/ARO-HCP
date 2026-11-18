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
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestManagedIdentitiesWithDataPlaneWorkloadsOIDCFederationJSONMapRoundTrip(t *testing.T) {
	t.Parallel()

	identityResourceID := "/subscriptions/00000000-0000-0000-0000-000000000000/resourcegroups/test-rg/providers/microsoft.managedidentity/userassignedidentities/identity-a"
	identity := DataplaneOIDCFederationIdentityInstance{
		ClientID:    "client-a",
		PrincipalID: "principal-a",
		TenantID:    "tenant-a",
	}
	operatorName := "azure-disk-csi-driver"
	key := DataplaneOIDCFederationAssignmentKey{
		IdentityResourceID: identityResourceID,
		OperatorName:       operatorName,
	}
	original := map[DataplaneOIDCFederationAssignmentKey]*DataplaneOIDCFederationAssignmentStatus{
		key: {
			TargetIdentity:  identity,
			EnsuredIdentity: &identity,
		},
	}

	encoded, err := json.Marshal(original)
	require.NoError(t, err)

	var decoded map[DataplaneOIDCFederationAssignmentKey]*DataplaneOIDCFederationAssignmentStatus
	require.NoError(t, json.Unmarshal(encoded, &decoded))
	require.Contains(t, decoded, key)
	assert.Equal(t, identity, decoded[key].TargetIdentity)
	require.NotNil(t, decoded[key].EnsuredIdentity)
	assert.Equal(t, identity, *decoded[key].EnsuredIdentity)
}

func TestDataplaneOIDCFederationAssignmentKeyTextRoundTrip(t *testing.T) {
	t.Parallel()

	mixedCaseIdentityResourceID := "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/Test-RG/providers/Microsoft.ManagedIdentity/userAssignedIdentities/Identity-A"
	key := DataplaneOIDCFederationAssignmentKey{
		IdentityResourceID: strings.ToLower(mixedCaseIdentityResourceID),
		OperatorName:       "cloud-controller-manager",
	}
	assert.Equal(t, "/subscriptions/00000000-0000-0000-0000-000000000000/resourcegroups/test-rg/providers/microsoft.managedidentity/userassignedidentities/identity-a", key.IdentityResourceID)

	encoded, err := key.MarshalText()
	require.NoError(t, err)

	var decoded DataplaneOIDCFederationAssignmentKey
	require.NoError(t, decoded.UnmarshalText(encoded))
	assert.Equal(t, key, decoded)

	require.NoError(t, decoded.UnmarshalText([]byte(mixedCaseIdentityResourceID+"|"+key.OperatorName)))
	assert.Equal(t, key, decoded)
}
