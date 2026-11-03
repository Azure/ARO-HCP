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

package federatedidentitycredential

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
)

func TestGenerateFederatedIdentityCredentialName(t *testing.T) {
	tests := []struct {
		name                    string
		csClusterID             string
		dataPlaneOperatorName   string
		serviceAccountNamespace string
		serviceAccountName      string
		// expectedName pins the exact generated name for this case. Consumers of this function
		// (Cluster Service and the backend) rely on the generated names being predictable and
		// stable across releases, so every case is checked exactly rather than just by shape: if
		// one of these ever needs to change, the naming contract broke and every already-created
		// Azure Federated Identity Credential will no longer be found by the consumers relying on
		// it.
		expectedName string
	}{
		{
			name:                    "typical inputs",
			csClusterID:             "cs-cluster-id",
			dataPlaneOperatorName:   "ingress",
			serviceAccountNamespace: "openshift-ingress",
			serviceAccountName:      "ingress-operator",
			expectedName:            "aro-hcp-cs-cluster-id-51e3f7cd-15a6-58d8-8a1f-4cbaba806875",
		},
		{
			name:                    "empty inputs",
			csClusterID:             "",
			dataPlaneOperatorName:   "",
			serviceAccountNamespace: "",
			serviceAccountName:      "",
			expectedName:            "aro-hcp--81b51805-ccae-5d9c-a4d3-a883848e1b0a",
		},
		{
			name:                    "inputs containing the separator character",
			csClusterID:             "cs$cluster",
			dataPlaneOperatorName:   "data$plane",
			serviceAccountNamespace: "ns$space",
			serviceAccountName:      "sa$name",
			expectedName:            "aro-hcp-cs$cluster-1b8540e1-e4a5-5deb-a840-d56d1a9671a7",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			name := GenerateFederatedIdentityCredentialName(tt.csClusterID, tt.dataPlaneOperatorName, tt.serviceAccountNamespace, tt.serviceAccountName)
			assert.Equal(t, tt.expectedName, name)

			again := GenerateFederatedIdentityCredentialName(tt.csClusterID, tt.dataPlaneOperatorName, tt.serviceAccountNamespace, tt.serviceAccountName)
			assert.Equal(t, name, again, "generated name must be deterministic for the same inputs")
		})
	}
}

// TestGenerateFederatedIdentityCredentialNameUniqueness asserts that varying any single input
// parameter, including swapping values between parameters, changes the generated name. The
// function concatenates its inputs with "$" before hashing, so this also guards against
// different parameter combinations colliding on the same concatenated string.
func TestGenerateFederatedIdentityCredentialNameUniqueness(t *testing.T) {
	base := GenerateFederatedIdentityCredentialName("cluster-a", "ingress", "openshift-ingress", "ingress-operator")

	variants := map[string]string{
		"different csClusterID":              GenerateFederatedIdentityCredentialName("cluster-b", "ingress", "openshift-ingress", "ingress-operator"),
		"different dataPlaneOperatorName":    GenerateFederatedIdentityCredentialName("cluster-a", "disk-csi-driver", "openshift-ingress", "ingress-operator"),
		"different serviceAccountNamespace":  GenerateFederatedIdentityCredentialName("cluster-a", "ingress", "openshift-other", "ingress-operator"),
		"different serviceAccountName":       GenerateFederatedIdentityCredentialName("cluster-a", "ingress", "openshift-ingress", "other-operator"),
		"parameters shifted across the join": GenerateFederatedIdentityCredentialName("cluster-aingress", "openshift-ingress", "ingress-operator", ""),
	}

	for description, variant := range variants {
		assert.NotEqual(t, base, variant, "%s should produce a different generated name", description)
	}
}

func TestGenerateFederatedIdentityCredentialResourceID(t *testing.T) {
	userAssignedIdentityResourceID, err := azcorearm.ParseResourceID(
		"/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/test-rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/test-identity",
	)
	require.NoError(t, err, "failed to parse test user assigned identity resource ID")

	tests := []struct {
		name                           string
		userAssignedIdentityResourceID *azcorearm.ResourceID
		wantErr                        bool
		// expectedResourceIDStr pins the exact generated resource ID string, mirroring how
		// TestGenerateFederatedIdentityCredentialName pins its expected names.
		expectedResourceIDStr string
	}{
		{
			name:                           "valid user assigned identity resource ID",
			userAssignedIdentityResourceID: userAssignedIdentityResourceID,
			expectedResourceIDStr:          "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/test-rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/test-identity/federatedIdentityCredentials/aro-hcp-cs-cluster-id-51e3f7cd-15a6-58d8-8a1f-4cbaba806875",
		},
		{
			name:                           "nil user assigned identity resource ID",
			userAssignedIdentityResourceID: nil,
			wantErr:                        true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resourceID, err := GenerateFederatedIdentityCredentialResourceID(tt.userAssignedIdentityResourceID, "cs-cluster-id", "ingress", "openshift-ingress", "ingress-operator")

			if tt.wantErr {
				require.Error(t, err, "expected an error when the user assigned identity resource ID is nil")
				assert.Nil(t, resourceID, "expected a nil resource ID when the user assigned identity resource ID is nil")
				return
			}
			require.NoError(t, err, "unexpected error generating the federated identity credential resource ID")
			require.NotNil(t, resourceID, "generated resource ID was nil")
			assert.Equal(t, tt.expectedResourceIDStr, resourceID.String(), "unexpected generated resource ID string")
		})
	}
}
