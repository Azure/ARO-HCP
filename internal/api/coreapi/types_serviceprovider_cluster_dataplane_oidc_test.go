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

func TestManagedIdentitiesWithDataPlaneWorkloadsOIDCFederationJSONMapRoundTrip(t *testing.T) {
	t.Parallel()

	key := "/subscriptions/00000000-0000-0000-0000-000000000000/resourcegroups/test-rg/providers/microsoft.managedidentity/userassignedidentities/identity-a"
	original := map[string]*ManagedIdentityDataplaneOIDCFederationStatus{
		key: {
			Phase: ManagedIdentityDataplaneOIDCFederationPhasePendingConfigure,
			ObservedIdentity: ManagedIdentityDataplaneOIDCFederationObservedIdentity{
				ClientID:    "client-a",
				PrincipalID: "principal-a",
				TenantID:    "tenant-a",
			},
		},
	}

	encoded, err := json.Marshal(original)
	require.NoError(t, err)

	var decoded map[string]*ManagedIdentityDataplaneOIDCFederationStatus
	require.NoError(t, json.Unmarshal(encoded, &decoded))
	require.Contains(t, decoded, key)
	assert.Equal(t, ManagedIdentityDataplaneOIDCFederationPhasePendingConfigure, decoded[key].Phase)
	assert.Equal(t, ManagedIdentityDataplaneOIDCFederationObservedIdentity{
		ClientID:    "client-a",
		PrincipalID: "principal-a",
		TenantID:    "tenant-a",
	}, decoded[key].ObservedIdentity)
}
