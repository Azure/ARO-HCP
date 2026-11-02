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
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
)

func seedProviderIDs(t *testing.T, ctx context.Context, db corecosmosstorage.ResourcesDBClient, cluster *coreapi.Cluster, withClusterID bool) {
	t.Helper()
	if cluster == nil {
		return
	}
	providerCRUD := db.ServiceProviderClusters(cluster.ID.SubscriptionID, cluster.ID.ResourceGroupName, cluster.ID.Name)
	provider, err := providerCRUD.Get(ctx, coreapi.ServiceProviderClusterResourceName)
	if cosmosstorageutils.IsNotFoundError(err) {
		providerID := metadataapi.Must(azcorearm.ParseResourceID(cluster.ID.String() + "/serviceProviderClusters/default"))
		provider, err = providerCRUD.Create(ctx, &coreapi.ServiceProviderCluster{CosmosMetadata: coreapi.CosmosMetadata{ResourceID: providerID, PartitionKey: cluster.ID.SubscriptionID}}, nil)
	}
	require.NoError(t, err)
	provider.ClusterServiceID = providerForCluster(cluster, withClusterID).ClusterServiceID
	_, err = db.ServiceProviderClusters(cluster.ID.SubscriptionID, cluster.ID.ResourceGroupName, cluster.ID.Name).Replace(ctx, provider, nil)
	require.NoError(t, err)
}

func providerForCluster(cluster *coreapi.Cluster, withClusterID bool) *coreapi.ServiceProviderCluster {
	if cluster == nil {
		return nil
	}
	provider := &coreapi.ServiceProviderCluster{}
	if withClusterID {
		id := metadataapi.Must(metadataapi.NewInternalID(testClusterServiceIDStr))
		provider.ClusterServiceID = &id
	}
	return provider
}

func TestClusterDeletionCompletionSurvivesProviderCleanup(t *testing.T) {
	id := metadataapi.Must(metadataapi.NewInternalID("/api/aro_hcp/v1alpha1/clusters/test"))
	for _, testCase := range []struct {
		name      string
		completed bool
		provider  *coreapi.ServiceProviderCluster
		want      bool
	}{
		{name: "missing provider is not proof of completion"},
		{name: "empty provider still requires completion", provider: &coreapi.ServiceProviderCluster{}},
		{name: "completed cleanup survives missing provider", completed: true, want: true},
		{name: "completed with empty provider", completed: true, provider: &coreapi.ServiceProviderCluster{}, want: true},
		{name: "pending ID blocks cleanup", completed: true, provider: &coreapi.ServiceProviderCluster{PendingClusterServiceID: &id}},
		{name: "final ID blocks cleanup", completed: true, provider: &coreapi.ServiceProviderCluster{ClusterServiceID: &id}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			cluster := newTestCluster(t, nil)
			now := metav1.Now()
			cluster.ServiceProviderProperties.DeletionTimestamp = &now
			cluster.ServiceProviderProperties.ClusterServiceDeletionTimestamp = &now
			cluster.ServiceProviderProperties.ClusterServiceDeletionCompleted = testCase.completed
			require.Equal(t, testCase.want, (&clusterChildResourcesCleanupController{}).NeedsWork(cluster, testCase.provider))
			require.Equal(t, testCase.want, (&clusterDeletionController{}).NeedsWork(cluster, testCase.provider))
		})
	}
}
