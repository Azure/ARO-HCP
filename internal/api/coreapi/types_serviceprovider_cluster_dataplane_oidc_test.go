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

	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
)

func TestManagedIdentityDataplaneOIDCFederationKeyTextRoundTrip(t *testing.T) {
	t.Parallel()

	key := ManagedIdentityDataplaneOIDCFederationKey{
		ResourceID:  "/subscriptions/00000000-0000-0000-0000-000000000000/resourcegroups/test-rg/providers/microsoft.managedidentity/userassignedidentities/identity-a",
		ClientID:    "client-a",
		PrincipalID: "principal-a",
		TenantID:    "tenant-a",
	}

	text, err := key.MarshalText()
	require.NoError(t, err)

	var decoded ManagedIdentityDataplaneOIDCFederationKey
	require.NoError(t, decoded.UnmarshalText(text))
	assert.Equal(t, key, decoded)
}

func TestManagedIdentityDataplaneOIDCFederationKeyJSONMapRoundTrip(t *testing.T) {
	t.Parallel()

	key := ManagedIdentityDataplaneOIDCFederationKey{
		ResourceID:  "/subscriptions/00000000-0000-0000-0000-000000000000/resourcegroups/test-rg/providers/microsoft.managedidentity/userassignedidentities/identity-a",
		ClientID:    "client-a",
		PrincipalID: "principal-a",
		TenantID:    "tenant-a",
	}
	original := map[ManagedIdentityDataplaneOIDCFederationKey]*ManagedIdentityDataplaneOIDCFederationStatus{
		key: {Phase: ManagedIdentityDataplaneOIDCFederationPhasePendingConfigure},
	}

	encoded, err := json.Marshal(original)
	require.NoError(t, err)

	var decoded map[ManagedIdentityDataplaneOIDCFederationKey]*ManagedIdentityDataplaneOIDCFederationStatus
	require.NoError(t, json.Unmarshal(encoded, &decoded))
	require.Contains(t, decoded, key)
	assert.Equal(t, ManagedIdentityDataplaneOIDCFederationPhasePendingConfigure, decoded[key].Phase)
}

func TestManagedIdentityDetailsDataplaneOIDCFederationKey(t *testing.T) {
	t.Parallel()

	identity := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/Test-RG/providers/Microsoft.ManagedIdentity/userAssignedIdentities/Identity-A"))

	t.Run("resolved identity", func(t *testing.T) {
		t.Parallel()
		details := &ManagedIdentityDetails{
			ResourceID:  identity,
			ClientID:    ptr.To("client-a"),
			PrincipalID: ptr.To("principal-a"),
			TenantID:    ptr.To("tenant-a"),
		}
		key, ok := details.AsDataplaneOIDCFederationKey()
		require.True(t, ok)
		assert.Equal(t, strings.ToLower(identity.String()), key.ResourceID)
		assert.Equal(t, "client-a", key.ClientID)
		assert.Equal(t, "principal-a", key.PrincipalID)
		assert.Equal(t, "tenant-a", key.TenantID)
	})

	t.Run("missing client id", func(t *testing.T) {
		t.Parallel()
		details := &ManagedIdentityDetails{
			ResourceID:  identity,
			PrincipalID: ptr.To("principal-a"),
			TenantID:    ptr.To("tenant-a"),
		}
		_, ok := details.AsDataplaneOIDCFederationKey()
		assert.False(t, ok)
	})
}
