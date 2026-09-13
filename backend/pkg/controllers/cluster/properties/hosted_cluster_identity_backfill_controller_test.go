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

package properties

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/corelistertesting"
	"github.com/Azure/ARO-HCP/internal/ocm"
)

func TestHostedClusterIdentityBackfillSyncer(t *testing.T) {
	ctx := context.Background()
	cluster := newTestCluster(testClusterName)
	clusterServiceID := metadataapi.Must(metadataapi.NewInternalID("/api/aro_hcp/v1alpha1/clusters/abc123"))
	cluster.ServiceProviderProperties.ClusterServiceID = &clusterServiceID
	spc := newTestServiceProviderCluster(testClusterName, nil, nil)
	db, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, spc})
	require.NoError(t, err)

	csCluster, err := arohcpv1alpha1.NewCluster().ID("abc123").DomainPrefix("chosen-name").Build()
	require.NoError(t, err)
	ctrl := gomock.NewController(t)
	mockCS := ocm.NewMockClusterServiceClientSpec(ctrl)
	mockCS.EXPECT().GetCluster(gomock.Any(), clusterServiceID).Return(csCluster, nil)

	syncer := &hostedClusterIdentityBackfillSyncer{
		resourcesDBClient:            db,
		clusterLister:                &corelistertesting.SliceClusterLister{Clusters: []*coreapi.HCPOpenShiftCluster{cluster}},
		serviceProviderClusterLister: &corelistertesting.DBServiceProviderClusterLister{ResourcesDBClient: db},
		clustersServiceClient:        mockCS,
		environmentIdentifier:        "production",
	}
	require.NoError(t, syncer.SyncOnce(ctx, controllerutils.HCPClusterKey{SubscriptionID: testSubscriptionID, ResourceGroupName: testResourceGroupName, HCPClusterName: testClusterName}))

	updated, err := db.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, coreapi.ServiceProviderClusterResourceName)
	require.NoError(t, err)
	assert.Equal(t, "ocm-production-abc123", updated.Status.HostedClusterNamespace)
	assert.Equal(t, "chosen-name", updated.Status.HostedClusterName)
}
