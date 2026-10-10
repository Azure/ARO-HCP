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

package deletion

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clocktesting "k8s.io/utils/clock/testing"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/billingcosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/corelistertesting"
)

func TestTLSCertificateDeletionGates(test *testing.T) {
	reference := &coreapi.AzureTLSCertificateReference{KeyVaultURL: "https://vault.vault.azure.net/", CertificateName: "certificate"}
	for _, scenario := range []struct {
		name         string
		kas, ingress *coreapi.TLSCertificate
	}{
		{name: "empty KAS wrapper", kas: &coreapi.TLSCertificate{}},
		{name: "empty ingress wrapper", ingress: &coreapi.TLSCertificate{}},
		{name: "KAS pending", kas: &coreapi.TLSCertificate{PendingReference: reference}},
		{name: "KAS confirmed", kas: &coreapi.TLSCertificate{AzureReference: reference}},
		{name: "ingress pending", ingress: &coreapi.TLSCertificate{PendingReference: reference}},
		{name: "ingress confirmed", ingress: &coreapi.TLSCertificate{AzureReference: reference}},
		{name: "both", kas: &coreapi.TLSCertificate{PendingReference: reference}, ingress: &coreapi.TLSCertificate{AzureReference: reference}},
		{name: "partial reference", kas: &coreapi.TLSCertificate{AzureReference: &coreapi.AzureTLSCertificateReference{CertificateName: "certificate"}}},
	} {
		test.Run(scenario.name, func(test *testing.T) {
			cluster := newTestClusterWithNewDeletionApproach(test, func(cluster *coreapi.Cluster) {
				now := metav1.Now()
				cluster.ServiceProviderProperties.DeletionTimestamp = &now
				cluster.ServiceProviderProperties.ClusterServiceDeletionTimestamp = &now
				cluster.ServiceProviderProperties.ClusterServiceID = nil
			})
			providerID := metadataapi.Must(azcorearm.ParseResourceID(cluster.ResourceID.String() + "/serviceProviderClusters/" + coreapi.ServiceProviderClusterResourceName))
			provider := &coreapi.ServiceProviderCluster{
				CosmosMetadata: coreapi.CosmosMetadata{ResourceID: providerID, PartitionKey: cluster.PartitionKey},
				Status:         coreapi.ServiceProviderClusterStatus{AzureResources: coreapi.AzureResources{KubeAPIServerCertificate: scenario.kas, IngressCertificate: scenario.ingress}},
			}
			database, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(test.Context(), []any{cluster, provider})
			require.NoError(test, err)
			key := controllerutils.HCPClusterKey{SubscriptionID: testSubscriptionID, ResourceGroupName: testResourceGroupName, HCPClusterName: testClusterName}
			deleter := &clusterDeletionController{
				resourcesDBClient:            database,
				clusterLister:                &corelistertesting.DBClusterLister{ResourcesDBClient: database},
				serviceProviderClusterLister: &corelistertesting.DBServiceProviderClusterLister{ResourcesDBClient: database},
				billingDBClient:              billingcosmosstoragetesting.NewMockBillingDBClient(),
				passiveClock:                 clocktesting.NewFakePassiveClock(time.Now()),
			}
			cleanup := &clusterChildResourcesCleanupController{resourcesDBClient: database}
			allowed, err := deleter.deletePreconditionTLSCertificatesCleared(test.Context(), key)
			require.NoError(test, err)
			require.False(test, allowed)
			allowed, err = cleanup.extraDeleteGateShouldDeleteServiceProviderCluster(test.Context(), providerID)
			require.NoError(test, err)
			require.False(test, allowed)
			require.NoError(test, deleter.SyncOnce(test.Context(), key))
			_, err = database.HCPClusters(key.SubscriptionID, key.ResourceGroupName).Get(test.Context(), key.HCPClusterName)
			require.NoError(test, err)
			providerCRUD := database.ServiceProviderClusters(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
			provider, err = providerCRUD.Get(test.Context(), coreapi.ServiceProviderClusterResourceName)
			require.NoError(test, err)
			provider.Status.AzureResources.KubeAPIServerCertificate = nil
			provider.Status.AzureResources.IngressCertificate = nil
			_, err = providerCRUD.Replace(test.Context(), provider, nil)
			require.NoError(test, err)
			allowed, err = deleter.deletePreconditionTLSCertificatesCleared(test.Context(), key)
			require.NoError(test, err)
			require.True(test, allowed)
			allowed, err = cleanup.extraDeleteGateShouldDeleteServiceProviderCluster(test.Context(), providerID)
			require.NoError(test, err)
			require.True(test, allowed)
			require.NoError(test, providerCRUD.Delete(test.Context(), coreapi.ServiceProviderClusterResourceName))
			require.NoError(test, deleter.SyncOnce(test.Context(), key))
			_, err = database.HCPClusters(key.SubscriptionID, key.ResourceGroupName).Get(test.Context(), key.HCPClusterName)
			require.True(test, cosmosstorageutils.IsNotFoundError(err))
		})
	}
}
