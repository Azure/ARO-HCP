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

package controllers

import (
	"context"
	"strings"
	"testing"

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	hsv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/listertesting"
	"github.com/Azure/ARO-HCP/backend/pkg/maestrohelpers"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const (
	readDesireTestSubscriptionID    = "00000000-0000-0000-0000-000000000000"
	readDesireTestResourceGroupName = "test-rg"
	readDesireTestClusterName       = "test-cluster"
	readDesireTestEnvIdentifier     = "int"
	readDesireTestDomainPrefix      = "cluster1"
	readDesireTestClusterServiceID  = "/api/clusters_mgmt/v1/clusters/abc123"
)

var readDesireTestManagementClusterResourceID = api.Must(azcorearm.ParseResourceID(
	"/providers/microsoft.redhatopenshift/stamps/1/managementclusters/default",
))

func readDesireTestKey() controllerutils.HCPClusterKey {
	return controllerutils.HCPClusterKey{
		SubscriptionID:    readDesireTestSubscriptionID,
		ResourceGroupName: readDesireTestResourceGroupName,
		HCPClusterName:    readDesireTestClusterName,
	}
}

func newTestCluster(opts ...func(*api.HCPOpenShiftCluster)) *api.HCPOpenShiftCluster {
	resourceID := api.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + readDesireTestSubscriptionID +
			"/resourceGroups/" + readDesireTestResourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + readDesireTestClusterName,
	))
	cluster := &api.HCPOpenShiftCluster{
		CosmosMetadata: arm.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(resourceID.SubscriptionID),
		},
		TrackedResource: arm.TrackedResource{
			Resource: arm.Resource{
				ID:   resourceID,
				Name: readDesireTestClusterName,
				Type: resourceID.ResourceType.String(),
			},
		},
		ServiceProviderProperties: api.HCPOpenShiftClusterServiceProviderProperties{
			ClusterServiceID: api.Ptr(api.Must(api.NewInternalID(readDesireTestClusterServiceID))),
		},
		CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
			DNS: api.CustomerDNSProfile{
				BaseDomainPrefix: readDesireTestDomainPrefix,
			},
			Version: api.VersionProfile{
				ID: "4.20.0",
			},
		},
	}
	for _, opt := range opts {
		opt(cluster)
	}
	return cluster
}

func newTestSPC(mcResourceID *azcorearm.ResourceID) *api.ServiceProviderCluster {
	spcResourceID := api.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + readDesireTestSubscriptionID +
			"/resourceGroups/" + readDesireTestResourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + readDesireTestClusterName +
			"/serviceProviderClusters/" + api.ServiceProviderClusterResourceName,
	))
	return &api.ServiceProviderCluster{
		CosmosMetadata: api.CosmosMetadata{
			ResourceID:   spcResourceID,
			PartitionKey: strings.ToLower(spcResourceID.SubscriptionID),
		},
		Status: api.ServiceProviderClusterStatus{
			ManagementClusterResourceID: mcResourceID,
		},
	}
}

func TestCreateClusterScopedReadDesires_SyncOnce(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                         string
		resources                    []any
		cachedServiceProviderCluster *api.ServiceProviderCluster
		kubeApplierDesires           []any
		wantErr                      bool
		verifyDB                     func(t *testing.T, ctx context.Context, kaClient *databasetesting.MockKubeApplierDBClient)
	}{
		{
			name: "creates HostedCluster and cluster-autoscaler ReadDesires",
			resources: []any{
				newTestCluster(),
			},
			cachedServiceProviderCluster: newTestSPC(readDesireTestManagementClusterResourceID),
			verifyDB: func(t *testing.T, ctx context.Context, kaClient *databasetesting.MockKubeApplierDBClient) {
				t.Helper()
				crud, err := kaClient.ReadDesiresForCluster(readDesireTestSubscriptionID, readDesireTestResourceGroupName, readDesireTestClusterName)
				require.NoError(t, err)

				hostedClusterRD, err := crud.Get(ctx, readDesireNameReadonlyHostedCluster)
				require.NoError(t, err)
				assert.Equal(t, hostedClusterTarget(readDesireTestEnvIdentifier, "abc123", readDesireTestDomainPrefix), hostedClusterRD.Spec.TargetItem)

				autoscalerRD, err := crud.Get(ctx, maestrohelpers.ReadDesireNameReadonlyHypershiftControlPlaneComponentClusterAutoscaler)
				require.NoError(t, err)
				assert.Equal(t, clusterAutoscalerTarget(readDesireTestEnvIdentifier, "abc123", readDesireTestDomainPrefix), autoscalerRD.Spec.TargetItem)
				assert.Equal(t, "controlplanecomponents", autoscalerRD.Spec.TargetItem.Resource)
				assert.Equal(t, "cluster-autoscaler", autoscalerRD.Spec.TargetItem.Name)
				assert.Equal(t, hostedControlPlaneNamespace(readDesireTestEnvIdentifier, "abc123", readDesireTestDomainPrefix), autoscalerRD.Spec.TargetItem.Namespace)
				assert.Equal(t, hsv1beta1.SchemeGroupVersion.Group, autoscalerRD.Spec.TargetItem.Group)
				assert.Equal(t, hsv1beta1.SchemeGroupVersion.Version, autoscalerRD.Spec.TargetItem.Version)
			},
		},
		{
			name: "creates cluster-autoscaler ReadDesire even when cluster version is below 4.20",
			resources: []any{
				newTestCluster(func(c *api.HCPOpenShiftCluster) {
					c.CustomerProperties.Version.ID = "4.19.0"
				}),
			},
			cachedServiceProviderCluster: newTestSPC(readDesireTestManagementClusterResourceID),
			verifyDB: func(t *testing.T, ctx context.Context, kaClient *databasetesting.MockKubeApplierDBClient) {
				t.Helper()
				crud, err := kaClient.ReadDesiresForCluster(readDesireTestSubscriptionID, readDesireTestResourceGroupName, readDesireTestClusterName)
				require.NoError(t, err)

				_, err = crud.Get(ctx, readDesireNameReadonlyHostedCluster)
				require.NoError(t, err)

				autoscalerRD, err := crud.Get(ctx, maestrohelpers.ReadDesireNameReadonlyHypershiftControlPlaneComponentClusterAutoscaler)
				require.NoError(t, err)
				assert.Equal(t, clusterAutoscalerTarget(readDesireTestEnvIdentifier, "abc123", readDesireTestDomainPrefix), autoscalerRD.Spec.TargetItem)
			},
		},
		{
			name: "skips when domain prefix is not yet synced",
			resources: []any{
				newTestCluster(func(c *api.HCPOpenShiftCluster) {
					c.CustomerProperties.DNS.BaseDomainPrefix = ""
				}),
			},
			cachedServiceProviderCluster: newTestSPC(readDesireTestManagementClusterResourceID),
			verifyDB: func(t *testing.T, ctx context.Context, kaClient *databasetesting.MockKubeApplierDBClient) {
				t.Helper()
				crud, err := kaClient.ReadDesiresForCluster(readDesireTestSubscriptionID, readDesireTestResourceGroupName, readDesireTestClusterName)
				require.NoError(t, err)
				_, err = crud.Get(ctx, readDesireNameReadonlyHostedCluster)
				require.Error(t, err)
			},
		},
		{
			name: "skips when management cluster is not placed",
			resources: []any{
				newTestCluster(),
			},
			verifyDB: func(t *testing.T, ctx context.Context, kaClient *databasetesting.MockKubeApplierDBClient) {
				t.Helper()
				crud, err := kaClient.ReadDesiresForCluster(readDesireTestSubscriptionID, readDesireTestResourceGroupName, readDesireTestClusterName)
				require.NoError(t, err)
				_, err = crud.Get(ctx, readDesireNameReadonlyHostedCluster)
				require.Error(t, err)
			},
		},
		{
			name: "replaces cluster-autoscaler ReadDesire when target namespace changes",
			resources: []any{
				newTestCluster(),
			},
			cachedServiceProviderCluster: newTestSPC(readDesireTestManagementClusterResourceID),
			kubeApplierDesires: []any{
				buildReadDesire(
					kubeapplier.ToClusterScopedReadDesireResourceIDString(
						readDesireTestSubscriptionID, readDesireTestResourceGroupName, readDesireTestClusterName, maestrohelpers.ReadDesireNameReadonlyHypershiftControlPlaneComponentClusterAutoscaler),
					readDesireTestManagementClusterResourceID,
					clusterAutoscalerTarget(readDesireTestEnvIdentifier, "abc123", "old-prefix"),
				),
			},
			verifyDB: func(t *testing.T, ctx context.Context, kaClient *databasetesting.MockKubeApplierDBClient) {
				t.Helper()
				crud, err := kaClient.ReadDesiresForCluster(readDesireTestSubscriptionID, readDesireTestResourceGroupName, readDesireTestClusterName)
				require.NoError(t, err)
				autoscalerRD, err := crud.Get(ctx, maestrohelpers.ReadDesireNameReadonlyHypershiftControlPlaneComponentClusterAutoscaler)
				require.NoError(t, err)
				assert.Equal(t, hostedControlPlaneNamespace(readDesireTestEnvIdentifier, "abc123", readDesireTestDomainPrefix), autoscalerRD.Spec.TargetItem.Namespace)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := utils.ContextWithLogger(context.Background(), testr.New(t))

			mockResourcesDBClient, err := databasetesting.NewMockResourcesDBClientWithResources(ctx, tt.resources)
			require.NoError(t, err)

			mockKubeApplierDBClients := databasetesting.NewMockKubeApplierDBClients()
			mockKubeApplierClient, err := databasetesting.NewMockKubeApplierDBClientWithResources(ctx, tt.kubeApplierDesires)
			require.NoError(t, err)
			mockKubeApplierDBClients.Register(readDesireTestManagementClusterResourceID, mockKubeApplierClient)

			serviceProviderClusterListerStub := &listertesting.SliceServiceProviderClusterLister{}
			if tt.cachedServiceProviderCluster != nil {
				serviceProviderClusterListerStub.ServiceProviderClusters = []*api.ServiceProviderCluster{tt.cachedServiceProviderCluster}
			}

			syncer := &createClusterScopedReadDesiresSyncer{
				resourcesDBClient:                   mockResourcesDBClient,
				kubeApplierDBClients:                mockKubeApplierDBClients,
				serviceProviderClusterLister:        serviceProviderClusterListerStub,
				hostedClusterNamespaceEnvIdentifier: readDesireTestEnvIdentifier,
			}

			err = syncer.SyncOnce(ctx, readDesireTestKey())
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			if tt.verifyDB != nil {
				tt.verifyDB(t, ctx, mockKubeApplierClient)
			}
		})
	}
}
