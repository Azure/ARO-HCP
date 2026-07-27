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

package frontend

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"k8s.io/apimachinery/pkg/util/wait"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/test-integration/utils/integrationutils"
	hcpsdk20260630preview "github.com/Azure/ARO-HCP/test/sdk/v20260630preview/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
)

func TestRequestAdminCredentialStoresCSR(t *testing.T) {
	defer integrationutils.VerifyNoNewGoLeaks(t)
	integrationutils.WithAndWithoutCosmos(t, testRequestAdminCredentialStoresCSR)
}

func testRequestAdminCredentialStoresCSR(t *testing.T, withMock bool) {
	ctx := context.Background()
	ctx, cancel := context.WithCancel(ctx)

	testInfo, err := integrationutils.NewIntegrationTestInfoFromEnv(ctx, t, withMock)
	require.NoError(t, err)

	frontendErrCh := make(chan error, 1)
	go func() {
		frontendErrCh <- testInfo.Frontend.Run(ctx)
	}()
	defer func() {
		cancel()
		require.NoError(t, <-frontendErrCh)
		testInfo.Cleanup(context.Background())
	}()

	err = wait.PollUntilContextCancel(ctx, 100*time.Millisecond, true, func(ctx context.Context) (bool, error) {
		resp, err := http.Get(testInfo.FrontendURL)
		if err != nil {
			return false, nil
		}
		resp.Body.Close()
		return true, nil
	})
	require.NoError(t, err)

	subscriptionID := api.TestSubscriptionID
	resourceGroupName := "testResourceGroup"
	clusterName := "testCluster"
	clusterResourceID := api.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + subscriptionID +
			"/resourceGroups/" + resourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + clusterName))
	clusterInternalID := api.Must(api.NewInternalID("/api/clusters_mgmt/v1/clusters/test-cs-id"))

	// Register the subscription.
	sub := &arm.Subscription{
		CosmosMetadata: arm.CosmosMetadata{
			ResourceID:   api.Must(azcorearm.ParseResourceID("/subscriptions/" + subscriptionID)),
			PartitionKey: strings.ToLower(subscriptionID),
		},
		State: arm.SubscriptionStateRegistered,
		Properties: &arm.SubscriptionProperties{
			TenantId: api.Ptr(api.TestTenantID),
		},
	}
	_, err = testInfo.ResourcesDBClient().Subscriptions().Create(ctx, sub, nil)
	require.NoError(t, err)

	// Create a cluster in Succeeded state with a ClusterServiceID.
	cluster := &api.HCPOpenShiftCluster{
		CosmosMetadata: arm.CosmosMetadata{
			ResourceID:   clusterResourceID,
			PartitionKey: strings.ToLower(subscriptionID),
		},
		TrackedResource: arm.TrackedResource{
			Resource: arm.NewResource(clusterResourceID),
		},
		ServiceProviderProperties: api.HCPOpenShiftClusterServiceProviderProperties{
			ProvisioningState: arm.ProvisioningStateSucceeded,
			ClusterServiceID:  &clusterInternalID,
		},
	}
	cluster.Location = "eastus"
	_, err = testInfo.ResourcesDBClient().HCPClusters(subscriptionID, resourceGroupName).Create(ctx, cluster, nil)
	require.NoError(t, err)

	// Generate a CSR.
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	csrTemplate := &x509.CertificateRequest{
		Subject: pkix.Name{
			CommonName:   "system:customer-break-glass:system-admin",
			Organization: []string{"system:masters"},
		},
	}
	csrDER, err := x509.CreateCertificateRequest(rand.Reader, csrTemplate, key)
	require.NoError(t, err)
	csrPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrDER})

	// Build a v2026 SDK client pointing at the test frontend.
	clientFactory := integrationutils.Get20260630ClientFactory(testInfo.FrontendURL, subscriptionID)
	clustersClient := clientFactory.NewHcpOpenShiftClustersClient()

	// POST requestAdminCredential with the CSR body.
	_, err = clustersClient.BeginRequestAdminCredential(ctx, resourceGroupName, clusterName,
		&hcpsdk20260630preview.HcpOpenShiftClustersClientBeginRequestAdminCredentialOptions{
			Body: &hcpsdk20260630preview.HcpOpenShiftClusterAdminCredentialRequest{
				CertificateRequest: to.Ptr(string(csrPEM)),
			},
		})
	require.NoError(t, err)

	// Verify the operation was stored in cosmos with the CertificateRequest.
	operationsIter := testInfo.ResourcesDBClient().Operations(subscriptionID).ListActiveOperations(nil)
	var found *api.Operation
	for _, op := range operationsIter.Items(ctx) {
		if op.Request == database.OperationRequestRequestCredential {
			found = op
			break
		}
	}
	require.NoError(t, operationsIter.GetError())
	require.NotNil(t, found, "expected a requestCredential operation in cosmos")
	require.Equal(t, string(csrPEM), found.CertificateRequest, "CertificateRequest should be stored in the operation")
}
