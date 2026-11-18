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

package client

import (
	"context"
	"encoding/base64"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/msi-dataplane/pkg/dataplane"

	"github.com/Azure/ARO-HCP/internal/certificate"
	"github.com/Azure/ARO-HCP/internal/certificatetest"
)

func newTestCertificateBundle(t *testing.T) []byte {
	t.Helper()

	certPEM, keyPEM, err := certificatetest.GenerateTestCertificate(t, 7)
	require.NoError(t, err)

	return append(keyPEM, certPEM...)
}

func TestHardcodedIdentityManagedIdentitiesDataplaneClient_GetUserAssignedIdentitiesCredentials(t *testing.T) {
	bundle := newTestCertificateBundle(t)
	hardcodedIdentity := &HardcodedIdentity{
		ClientID:                "client-id",
		CertificateBundleReader: &certificate.MockReader{Bundle: bundle},
		PrincipalID:             "principal-id",
		TenantID:                "tenant-id",
	}

	dataplaneClient := newHardcodedIdentityManagedIdentitiesDataPlaneClient(&cloud.AzurePublic, hardcodedIdentity)

	const identityResourceID = "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/operator"
	creds, err := dataplaneClient.GetUserAssignedIdentitiesCredentials(context.Background(), dataplane.UserAssignedIdentitiesRequest{
		IdentityIDs: []string{identityResourceID},
	})
	require.NoError(t, err)
	require.Len(t, creds.ExplicitIdentities, 1)

	identity := creds.ExplicitIdentities[0]
	assert.Equal(t, hardcodedIdentity.ClientID, *identity.ClientID)
	assert.Equal(t, hardcodedIdentity.TenantID, *identity.TenantID)
	assert.Equal(t, hardcodedIdentity.PrincipalID, *identity.ObjectID)
	assert.Equal(t, identityResourceID, *identity.ResourceID)
	require.NotNil(t, identity.ClientSecret)

	// The ClientSecret must be exactly the original bundle bytes, base64 encoded,
	// with no re-encoding, so that PKCS#12 (.pfx) bundles survive unchanged.
	assert.Equal(t, base64.StdEncoding.EncodeToString(bundle), *identity.ClientSecret)

	// The ClientSecret must also be usable the same way the real Managed Identities
	// Data Plane client's response is used: base64 decoded and parsed as a certificate bundle.
	cred, err := dataplane.GetCredential(azcore.ClientOptions{}, identity)
	require.NoError(t, err, "failed to build a credential from the encoded certificate bundle")
	assert.NotNil(t, cred)
}

func TestHardcodedIdentityManagedIdentitiesDataplaneClient_GetUserAssignedIdentitiesCredentials_CertificateReadError(t *testing.T) {
	readErr := errors.New("simulated certificate read failure")
	hardcodedIdentity := &HardcodedIdentity{
		ClientID:                "client-id",
		CertificateBundleReader: &certificate.MockReader{BundleErr: readErr},
		PrincipalID:             "principal-id",
		TenantID:                "tenant-id",
	}

	dataplaneClient := newHardcodedIdentityManagedIdentitiesDataPlaneClient(&cloud.AzurePublic, hardcodedIdentity)

	_, err := dataplaneClient.GetUserAssignedIdentitiesCredentials(context.Background(), dataplane.UserAssignedIdentitiesRequest{
		IdentityIDs: []string{"resource-id"},
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, readErr), "expected the underlying certificate read error to survive wrapping, got: %v", err)
}
