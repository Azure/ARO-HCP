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

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/kubeappliercosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/corelistertesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/fleetlistertesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/kubeapplierlistertesting"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const (
	nodePoolReadDesireTestSubscriptionID    = "00000000-0000-0000-0000-000000000000"
	nodePoolReadDesireTestResourceGroupName = "test-rg"
	nodePoolReadDesireTestClusterName       = "test-cluster"
	nodePoolReadDesireTestEnvIdentifier     = "int"
	nodePoolReadDesireTestDomainPrefix      = "cluster1"
	nodePoolReadDesireTestClusterServiceID  = "/api/clusters_mgmt/v1/clusters/abc123"
)

var nodePoolReadDesireTestManagementClusterResourceID = metadataapi.Must(azcorearm.ParseResourceID(
	"/providers/microsoft.redhatopenshift/stamps/1/managementclusters/default",
))

func nodePoolReadDesireTestKey(nodePoolName string) controllerutils.HCPNodePoolKey {
	return controllerutils.HCPNodePoolKey{
		SubscriptionID:    nodePoolReadDesireTestSubscriptionID,
		ResourceGroupName: nodePoolReadDesireTestResourceGroupName,
		HCPClusterName:    nodePoolReadDesireTestClusterName,
		HCPNodePoolName:   nodePoolName,
	}
}

func newTestNodePoolCluster(opts ...func(*coreapi.HCPOpenShiftCluster)) *coreapi.HCPOpenShiftCluster {
	resourceID := metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + nodePoolReadDesireTestSubscriptionID +
			"/resourceGroups/" + nodePoolReadDesireTestResourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + nodePoolReadDesireTestClusterName,
	))
	cluster := &coreapi.HCPOpenShiftCluster{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(resourceID.SubscriptionID),
		},
		TrackedResource: coreapi.TrackedResource{
			Resource: coreapi.Resource{
				ID:   resourceID,
				Name: nodePoolReadDesireTestClusterName,
				Type: resourceID.ResourceType.String(),
			},
		},
		ServiceProviderProperties: coreapi.HCPOpenShiftClusterServiceProviderProperties{
			ClusterServiceID: metadataapi.Ptr(metadataapi.Must(metadataapi.NewInternalID(nodePoolReadDesireTestClusterServiceID))),
		},
		CustomerProperties: coreapi.HCPOpenShiftClusterCustomerProperties{
			DNS: coreapi.CustomerDNSProfile{
				BaseDomainPrefix: nodePoolReadDesireTestDomainPrefix,
			},
		},
	}
	for _, opt := range opts {
		opt(cluster)
	}
	return cluster
}

func newTestNodePoolSPC(mcResourceID *azcorearm.ResourceID, opts ...func(*coreapi.ServiceProviderCluster)) *coreapi.ServiceProviderCluster {
	spcResourceID := metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + nodePoolReadDesireTestSubscriptionID +
			"/resourceGroups/" + nodePoolReadDesireTestResourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + nodePoolReadDesireTestClusterName +
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

// newTestNodePool builds an HCPOpenShiftClusterNodePool named name. name is used verbatim
// (not lowercased) so tests can exercise ARM node pool names containing uppercase letters,
// which Cluster Service lowercases when naming the corresponding Hypershift NodePool object.
func newTestNodePool(name string, opts ...func(*coreapi.HCPOpenShiftClusterNodePool)) *coreapi.HCPOpenShiftClusterNodePool {
	resourceID := metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + nodePoolReadDesireTestSubscriptionID +
			"/resourceGroups/" + nodePoolReadDesireTestResourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + nodePoolReadDesireTestClusterName +
			"/nodePools/" + name,
	))
	nodePool := &coreapi.HCPOpenShiftClusterNodePool{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(resourceID.SubscriptionID),
		},
		TrackedResource: coreapi.TrackedResource{
			Resource: coreapi.Resource{
				ID:   resourceID,
				Name: name,
				Type: resourceID.ResourceType.String(),
			},
		},
		ServiceProviderProperties: coreapi.HCPOpenShiftClusterNodePoolServiceProviderProperties{
			ClusterServiceID: metadataapi.Ptr(metadataapi.Must(metadataapi.NewInternalID(
				nodePoolReadDesireTestClusterServiceID + "/node_pools/" + name,
			))),
		},
	}
	for _, opt := range opts {
		opt(nodePool)
	}
	return nodePool
}

func TestCreateNodePoolScopedReadDesires_SyncOnce(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		nodePoolName   string
		wantTargetName string
	}{
		{
			name:           "already-lowercase node pool name",
			nodePoolName:   "test-nodepool",
			wantTargetName: nodePoolReadDesireTestDomainPrefix + "-test-nodepool",
		},
		{
			// Regression test: Cluster Service lowercases the ARM node pool name for
			// both its internal node pool ID and the AzureNodePool.ResourceName it
			// derives the Hypershift NodePool object's name from (see
			// internal/ocm/convert.go's BuildCSNodePool). ARM node pool names may
			// contain uppercase letters (e.g. "nodepool-128GiB"), so the ReadDesire's
			// target must lowercase the name too, or kube-applier will watch a
			// mixed-case object name that never matches the real (lowercase) object
			// on the management cluster and the NodePool will never be observed.
			name:           "mixed-case node pool name is lowercased in the ReadDesire target",
			nodePoolName:   "Test-NodePool",
			wantTargetName: nodePoolReadDesireTestDomainPrefix + "-test-nodepool",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := utils.ContextWithLogger(context.Background(), testr.New(t))

			mockResourcesDBClient, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{
				newTestNodePoolCluster(),
				newTestNodePool(tt.nodePoolName),
			})
			require.NoError(t, err)

			mockKubeApplierDBClients := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClients()
			mockKubeApplierClient, err := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClientWithResources(ctx, nil)
			require.NoError(t, err)
			mockKubeApplierDBClients.Register(nodePoolReadDesireTestManagementClusterResourceID, mockKubeApplierClient)

			serviceProviderClusterListerStub := &corelistertesting.SliceServiceProviderClusterLister{
				ServiceProviderClusters: []*coreapi.ServiceProviderCluster{newTestNodePoolSPC(nodePoolReadDesireTestManagementClusterResourceID)},
			}

			mcLister := &fleetlistertesting.SliceManagementClusterLister{
				ManagementClusters: []*fleetapi.ManagementCluster{{CosmosMetadata: coreapi.CosmosMetadata{ResourceID: nodePoolReadDesireTestManagementClusterResourceID}}},
			}

			syncer := &createNodePoolScopedReadDesiresSyncer{
				resourcesDBClient:                   mockResourcesDBClient,
				kubeApplierDBClients:                mockKubeApplierDBClients,
				serviceProviderClusterLister:        serviceProviderClusterListerStub,
				readDesireLister:                    &kubeapplierlistertesting.DBReadDesireLister{Clients: mockKubeApplierDBClients, Lister: mcLister},
				hostedClusterNamespaceEnvIdentifier: nodePoolReadDesireTestEnvIdentifier,
			}

			err = syncer.SyncOnce(ctx, nodePoolReadDesireTestKey(tt.nodePoolName))
			require.NoError(t, err)

			crud, err := mockKubeApplierClient.ReadDesiresForNodePool(nodePoolReadDesireTestSubscriptionID, nodePoolReadDesireTestResourceGroupName, nodePoolReadDesireTestClusterName, tt.nodePoolName)
			require.NoError(t, err)

			readDesire, err := crud.Get(ctx, readDesireNameReadonlyNodePool)
			require.NoError(t, err)
			assert.Equal(t, tt.wantTargetName, readDesire.Spec.TargetItem.Name, "ReadDesire target name must match the (lowercased) name Cluster Service gives the Hypershift NodePool object")
		})
	}
}
