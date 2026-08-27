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

package readdesires

import (
	"context"
	"strings"
	"testing"

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	hsv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/Azure/ARO-HCP/backend/pkg/kubeapplierhelpers"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/kubeappliercosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/corelistertesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/fleetlistertesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/kubeapplierlistertesting"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const (
	readDesireTestSubscriptionID    = "00000000-0000-0000-0000-000000000000"
	readDesireTestResourceGroupName = "test-rg"
	readDesireTestClusterName       = "test-cluster"
	readDesireTestEnvIdentifier     = "int"
	readDesireTestDomainPrefix      = "cluster1"
	readDesireTestClusterServiceID  = "/api/clusters_mgmt/v1/clusters/abc123"
	readDesireTestControlPlaneNS    = "ocm-int-abc123-cluster1"
)

var readDesireTestManagementClusterResourceID = metadataapi.Must(azcorearm.ParseResourceID(
	"/providers/microsoft.redhatopenshift/stamps/1/managementclusters/default",
))

func readDesireTestKey() controllerutils.HCPClusterKey {
	return controllerutils.HCPClusterKey{
		SubscriptionID:    readDesireTestSubscriptionID,
		ResourceGroupName: readDesireTestResourceGroupName,
		HCPClusterName:    readDesireTestClusterName,
	}
}

func newTestCluster(opts ...func(*coreapi.HCPOpenShiftCluster)) *coreapi.HCPOpenShiftCluster {
	resourceID := metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + readDesireTestSubscriptionID +
			"/resourceGroups/" + readDesireTestResourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + readDesireTestClusterName,
	))
	cluster := &coreapi.HCPOpenShiftCluster{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(resourceID.SubscriptionID),
		},
		TrackedResource: coreapi.TrackedResource{
			Resource: coreapi.Resource{
				ID:   resourceID,
				Name: readDesireTestClusterName,
				Type: resourceID.ResourceType.String(),
			},
		},
		ServiceProviderProperties: coreapi.HCPOpenShiftClusterServiceProviderProperties{
			ClusterServiceID: metadataapi.Ptr(metadataapi.Must(metadataapi.NewInternalID(readDesireTestClusterServiceID))),
		},
		CustomerProperties: coreapi.HCPOpenShiftClusterCustomerProperties{
			DNS: coreapi.CustomerDNSProfile{
				BaseDomainPrefix: readDesireTestDomainPrefix,
			},
			Version: coreapi.VersionProfile{
				ID: "4.20.0",
			},
		},
	}
	for _, opt := range opts {
		opt(cluster)
	}
	return cluster
}

func newTestSPC(mcResourceID *azcorearm.ResourceID, opts ...func(*coreapi.ServiceProviderCluster)) *coreapi.ServiceProviderCluster {
	spcResourceID := metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + readDesireTestSubscriptionID +
			"/resourceGroups/" + readDesireTestResourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + readDesireTestClusterName +
			"/serviceProviderClusters/" + coreapi.ServiceProviderClusterResourceName,
	))
	spc := &coreapi.ServiceProviderCluster{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   spcResourceID,
			PartitionKey: strings.ToLower(spcResourceID.SubscriptionID),
		},
		Status: coreapi.ServiceProviderClusterStatus{
			ManagementClusterResourceID: mcResourceID,
		},
	}
	for _, opt := range opts {
		opt(spc)
	}
	return spc
}

// newTestReadDesire builds a ReadDesire document for seeding the mock
// kube-applier container in tests.
func newTestReadDesire(resourceIDString string, mc *azcorearm.ResourceID, target kubeapplierapi.ResourceReference) *kubeapplierapi.ReadDesire {
	return &kubeapplierapi.ReadDesire{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   metadataapi.Must(azcorearm.ParseResourceID(resourceIDString)),
			PartitionKey: strings.ToLower(mc.String()),
		},
		Spec: kubeapplierapi.ReadDesireSpec{
			ManagementCluster: mc,
			TargetItem:        target,
		},
	}
}

func TestCreateClusterScopedReadDesires_SyncOnce(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                         string
		resources                    []any
		cachedServiceProviderCluster *coreapi.ServiceProviderCluster
		kubeApplierDesires           []any
		wantErr                      bool
		verifyDB                     func(t *testing.T, ctx context.Context, kaClient *kubeappliercosmosstoragetesting.MockKubeApplierDBClient)
	}{
		{
			name: "creates HostedCluster and cluster-autoscaler ReadDesires",
			resources: []any{
				newTestCluster(),
			},
			cachedServiceProviderCluster: newTestSPC(readDesireTestManagementClusterResourceID),
			verifyDB: func(t *testing.T, ctx context.Context, kaClient *kubeappliercosmosstoragetesting.MockKubeApplierDBClient) {
				t.Helper()
				crud, err := kaClient.ReadDesiresForCluster(readDesireTestSubscriptionID, readDesireTestResourceGroupName, readDesireTestClusterName)
				require.NoError(t, err)

				hostedClusterRD, err := crud.Get(ctx, readDesireNameReadonlyHostedCluster)
				require.NoError(t, err)
				assert.Equal(t, hostedClusterTarget(readDesireTestEnvIdentifier, "abc123", readDesireTestDomainPrefix), hostedClusterRD.Spec.TargetItem)

				autoscalerRD, err := crud.Get(ctx, kubeapplierhelpers.ReadDesireNameReadonlyHypershiftControlPlaneComponentClusterAutoscaler)
				require.NoError(t, err)
				assert.Equal(t, clusterAutoscalerTarget(readDesireTestEnvIdentifier, "abc123", readDesireTestDomainPrefix), autoscalerRD.Spec.TargetItem)
				assert.Equal(t, "controlplanecomponents", autoscalerRD.Spec.TargetItem.Resource)
				assert.Equal(t, "cluster-autoscaler", autoscalerRD.Spec.TargetItem.Name)
				assert.Equal(t, controllerutils.HostedControlPlaneNamespace(readDesireTestEnvIdentifier, "abc123", readDesireTestDomainPrefix), autoscalerRD.Spec.TargetItem.Namespace)
				assert.Equal(t, hsv1beta1.SchemeGroupVersion.Group, autoscalerRD.Spec.TargetItem.Group)
				assert.Equal(t, hsv1beta1.SchemeGroupVersion.Version, autoscalerRD.Spec.TargetItem.Version)
			},
		},
		{
			name: "creates serving CA ReadDesire when ControlPlaneNamespace is set",
			resources: []any{
				newTestCluster(),
			},
			cachedServiceProviderCluster: newTestSPC(readDesireTestManagementClusterResourceID, func(spc *coreapi.ServiceProviderCluster) {
				spc.Status.ControlPlaneNamespace = readDesireTestControlPlaneNS
			}),
			verifyDB: func(t *testing.T, ctx context.Context, kaClient *kubeappliercosmosstoragetesting.MockKubeApplierDBClient) {
				t.Helper()
				crud, err := kaClient.ReadDesiresForCluster(readDesireTestSubscriptionID, readDesireTestResourceGroupName, readDesireTestClusterName)
				require.NoError(t, err)

				servingCARD, err := crud.Get(ctx, kubeapplierhelpers.ReadDesireNameServingCA)
				require.NoError(t, err)
				assert.Equal(t, servingCATarget(readDesireTestControlPlaneNS), servingCARD.Spec.TargetItem)
				assert.Equal(t, readDesireTestControlPlaneNS, servingCARD.Spec.TargetItem.Namespace)
				assert.Equal(t, "secrets", servingCARD.Spec.TargetItem.Resource)
			},
		},
		{
			// Regression: the serving CA ReadDesire used to be gated on a
			// minimum OpenShift version. That gate has been removed, so the
			// serving CA (and the HostedCluster and cluster-autoscaler
			// ReadDesires) must be created regardless of the cluster version.
			name: "creates serving CA ReadDesire regardless of cluster version when ControlPlaneNamespace is set",
			resources: []any{
				newTestCluster(func(c *coreapi.HCPOpenShiftCluster) {
					c.CustomerProperties.Version.ID = "4.19.0"
				}),
			},
			cachedServiceProviderCluster: newTestSPC(readDesireTestManagementClusterResourceID, func(spc *coreapi.ServiceProviderCluster) {
				spc.Status.ControlPlaneNamespace = readDesireTestControlPlaneNS
			}),
			verifyDB: func(t *testing.T, ctx context.Context, kaClient *kubeappliercosmosstoragetesting.MockKubeApplierDBClient) {
				t.Helper()
				crud, err := kaClient.ReadDesiresForCluster(readDesireTestSubscriptionID, readDesireTestResourceGroupName, readDesireTestClusterName)
				require.NoError(t, err)

				servingCARD, err := crud.Get(ctx, kubeapplierhelpers.ReadDesireNameServingCA)
				require.NoError(t, err)
				assert.Equal(t, servingCATarget(readDesireTestControlPlaneNS), servingCARD.Spec.TargetItem)

				_, err = crud.Get(ctx, readDesireNameReadonlyHostedCluster)
				require.NoError(t, err)

				autoscalerRD, err := crud.Get(ctx, kubeapplierhelpers.ReadDesireNameReadonlyHypershiftControlPlaneComponentClusterAutoscaler)
				require.NoError(t, err)
				assert.Equal(t, clusterAutoscalerTarget(readDesireTestEnvIdentifier, "abc123", readDesireTestDomainPrefix), autoscalerRD.Spec.TargetItem)
			},
		},
		{
			name: "does not create serving CA ReadDesire when ControlPlaneNamespace is not set",
			resources: []any{
				newTestCluster(), // no ControlPlaneNamespace on SPC
			},
			cachedServiceProviderCluster: newTestSPC(readDesireTestManagementClusterResourceID),
			verifyDB: func(t *testing.T, ctx context.Context, kaClient *kubeappliercosmosstoragetesting.MockKubeApplierDBClient) {
				t.Helper()
				crud, err := kaClient.ReadDesiresForCluster(readDesireTestSubscriptionID, readDesireTestResourceGroupName, readDesireTestClusterName)
				require.NoError(t, err)

				_, err = crud.Get(ctx, kubeapplierhelpers.ReadDesireNameServingCA)
				require.Error(t, err)
			},
		},
		{
			name: "skips when domain prefix is not yet synced",
			resources: []any{
				newTestCluster(func(c *coreapi.HCPOpenShiftCluster) {
					c.CustomerProperties.DNS.BaseDomainPrefix = ""
				}),
			},
			cachedServiceProviderCluster: newTestSPC(readDesireTestManagementClusterResourceID),
			verifyDB: func(t *testing.T, ctx context.Context, kaClient *kubeappliercosmosstoragetesting.MockKubeApplierDBClient) {
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
			verifyDB: func(t *testing.T, ctx context.Context, kaClient *kubeappliercosmosstoragetesting.MockKubeApplierDBClient) {
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
				newTestReadDesire(
					kubeapplierapi.ToClusterScopedReadDesireResourceIDString(
						readDesireTestSubscriptionID, readDesireTestResourceGroupName, readDesireTestClusterName, kubeapplierhelpers.ReadDesireNameReadonlyHypershiftControlPlaneComponentClusterAutoscaler),
					readDesireTestManagementClusterResourceID,
					clusterAutoscalerTarget(readDesireTestEnvIdentifier, "abc123", "old-prefix"),
				),
			},
			verifyDB: func(t *testing.T, ctx context.Context, kaClient *kubeappliercosmosstoragetesting.MockKubeApplierDBClient) {
				t.Helper()
				crud, err := kaClient.ReadDesiresForCluster(readDesireTestSubscriptionID, readDesireTestResourceGroupName, readDesireTestClusterName)
				require.NoError(t, err)
				autoscalerRD, err := crud.Get(ctx, kubeapplierhelpers.ReadDesireNameReadonlyHypershiftControlPlaneComponentClusterAutoscaler)
				require.NoError(t, err)
				assert.Equal(t, controllerutils.HostedControlPlaneNamespace(readDesireTestEnvIdentifier, "abc123", readDesireTestDomainPrefix), autoscalerRD.Spec.TargetItem.Namespace)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := utils.ContextWithLogger(context.Background(), testr.New(t))

			mockResourcesDBClient, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, tt.resources)
			require.NoError(t, err)

			mockKubeApplierDBClients := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClients()
			mockKubeApplierClient, err := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClientWithResources(ctx, tt.kubeApplierDesires)
			require.NoError(t, err)
			mockKubeApplierDBClients.Register(readDesireTestManagementClusterResourceID, mockKubeApplierClient)

			serviceProviderClusterListerStub := &corelistertesting.SliceServiceProviderClusterLister{}
			if tt.cachedServiceProviderCluster != nil {
				serviceProviderClusterListerStub.ServiceProviderClusters = []*coreapi.ServiceProviderCluster{tt.cachedServiceProviderCluster}
			}

			mcLister := &fleetlistertesting.SliceManagementClusterLister{
				ManagementClusters: []*fleetapi.ManagementCluster{{CosmosMetadata: coreapi.CosmosMetadata{ResourceID: readDesireTestManagementClusterResourceID}}},
			}

			syncer := &createClusterScopedReadDesiresSyncer{
				resourcesDBClient:                   mockResourcesDBClient,
				kubeApplierDBClients:                mockKubeApplierDBClients,
				serviceProviderClusterLister:        serviceProviderClusterListerStub,
				readDesireLister:                    &kubeapplierlistertesting.DBReadDesireLister{Clients: mockKubeApplierDBClients, Lister: mcLister},
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
