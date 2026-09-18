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

	"k8s.io/utils/ptr"
)

func TestManagedIdentitiesWithDataPlaneWorkloadsOIDCFederationJSONMapRoundTrip(t *testing.T) {
	t.Parallel()

	key := "/subscriptions/00000000-0000-0000-0000-000000000000/resourcegroups/test-rg/providers/microsoft.managedidentity/userassignedidentities/identity-a"
	identity := DataplaneOIDCFederationIdentityInstance{
		ClientID:    "client-a",
		PrincipalID: "principal-a",
		TenantID:    "tenant-a",
	}
	operatorName := "azure-disk-csi-driver"
	original := map[string]*ManagedIdentityDataplaneOIDCFederationStatus{
		key: {
			TargetIdentity: identity,
			Operators: map[string]*DataplaneOIDCFederationOperatorStatus{
				operatorName: {
					EnsuredIdentity: &identity,
				},
			},
		},
	}

	encoded, err := json.Marshal(original)
	require.NoError(t, err)

	var decoded map[string]*ManagedIdentityDataplaneOIDCFederationStatus
	require.NoError(t, json.Unmarshal(encoded, &decoded))
	require.Contains(t, decoded, key)
	assert.True(t, decoded[key].OperatorEnsured(operatorName))
	assert.True(t, decoded[key].TargetIdentityEnsured())
	assert.Equal(t, identity, decoded[key].TargetIdentity)
	require.NotNil(t, decoded[key].Operators[operatorName])
	require.NotNil(t, decoded[key].Operators[operatorName].EnsuredIdentity)
	assert.Equal(t, identity, *decoded[key].Operators[operatorName].EnsuredIdentity)
}

func TestManagedIdentityDataplaneOIDCFederationStatusOperatorEnsured(t *testing.T) {
	t.Parallel()

	identity := DataplaneOIDCFederationIdentityInstance{
		ClientID:    "client-a",
		PrincipalID: "principal-a",
		TenantID:    "tenant-a",
	}
	rotated := identity
	rotated.ClientID = "client-b"
	operatorName := "azure-disk-csi-driver"

	assert.False(t, (*ManagedIdentityDataplaneOIDCFederationStatus)(nil).OperatorEnsured(operatorName))
	assert.False(t, (&ManagedIdentityDataplaneOIDCFederationStatus{TargetIdentity: identity}).OperatorEnsured(operatorName))
	assert.True(t, (&ManagedIdentityDataplaneOIDCFederationStatus{
		TargetIdentity: identity,
		Operators: map[string]*DataplaneOIDCFederationOperatorStatus{
			operatorName: {EnsuredIdentity: ptr.To(identity)},
		},
	}).OperatorEnsured(operatorName))
	assert.False(t, (&ManagedIdentityDataplaneOIDCFederationStatus{
		TargetIdentity: identity,
		Operators: map[string]*DataplaneOIDCFederationOperatorStatus{
			operatorName: {EnsuredIdentity: ptr.To(rotated)},
		},
	}).OperatorEnsured(operatorName))
	assert.False(t, (&ManagedIdentityDataplaneOIDCFederationStatus{
		TargetIdentity: identity,
		Operators: map[string]*DataplaneOIDCFederationOperatorStatus{
			"other-operator": {EnsuredIdentity: ptr.To(identity)},
		},
	}).OperatorEnsured(operatorName))
}

func TestManagedIdentityDataplaneOIDCFederationStatusTargetIdentityEnsured(t *testing.T) {
	t.Parallel()

	identity := DataplaneOIDCFederationIdentityInstance{
		ClientID:    "client-a",
		PrincipalID: "principal-a",
		TenantID:    "tenant-a",
	}
	rotated := identity
	rotated.ClientID = "client-b"

	assert.False(t, (*ManagedIdentityDataplaneOIDCFederationStatus)(nil).TargetIdentityEnsured())
	assert.False(t, (&ManagedIdentityDataplaneOIDCFederationStatus{TargetIdentity: identity}).TargetIdentityEnsured())
	assert.True(t, (&ManagedIdentityDataplaneOIDCFederationStatus{
		TargetIdentity: identity,
		Operators: map[string]*DataplaneOIDCFederationOperatorStatus{
			"azure-disk-csi-driver": {EnsuredIdentity: ptr.To(identity)},
		},
	}).TargetIdentityEnsured())
	assert.False(t, (&ManagedIdentityDataplaneOIDCFederationStatus{
		TargetIdentity: identity,
		Operators: map[string]*DataplaneOIDCFederationOperatorStatus{
			"azure-disk-csi-driver": {EnsuredIdentity: ptr.To(identity)},
			"azure-file-csi-driver": {},
		},
	}).TargetIdentityEnsured())
	assert.False(t, (&ManagedIdentityDataplaneOIDCFederationStatus{
		TargetIdentity: identity,
		Operators: map[string]*DataplaneOIDCFederationOperatorStatus{
			"azure-disk-csi-driver": {EnsuredIdentity: ptr.To(rotated)},
		},
	}).TargetIdentityEnsured())
}
