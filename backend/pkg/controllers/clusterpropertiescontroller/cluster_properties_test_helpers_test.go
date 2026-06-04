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

package clusterpropertiescontroller

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/json"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	hsv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/Azure/ARO-HCP/backend/pkg/maestrohelpers"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/api/fleet"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplier"
	dblisters "github.com/Azure/ARO-HCP/internal/database/listers"
	internallistertesting "github.com/Azure/ARO-HCP/internal/database/listertesting"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
)

const (
	testSubscriptionID      = "00000000-0000-0000-0000-000000000000"
	testResourceGroupName   = "test-rg"
	testClusterName         = "test-cluster"
	testOtherClusterName    = "other-cluster"
	testClusterServiceIDStr = "/api/clusters_mgmt/v1/clusters/abc123"

	testConsoleURL                     = "https://console-openshift-console.apps.aro.cluster1.example.com"
	testBaseDomain                     = "example.com"
	testHostedClusterIngressBaseDomain = "aro.cluster1.example.com"
	testBaseDomainPrefix               = "cluster1"
	testAPIHost                        = "api.cluster1.example.com"
	testAPIPort                        = int32(6443)
	testAPIURL                         = "https://api.cluster1.example.com:6443"
	testIssuerURL                      = "https://issuer.example.com/cluster1"
)

func newSeededReadDesireLister(ctx context.Context, readDesire *kubeapplier.ReadDesire) (dblisters.ReadDesireLister, error) {
	mockKubeApplierDB, err := databasetesting.NewMockKubeApplierDBClientWithResources(ctx, []any{readDesire})
	if err != nil {
		return nil, err
	}

	kubeApplierClients := databasetesting.NewMockKubeApplierDBClients()
	managementClusterID := api.Must(azcorearm.ParseResourceID(
		"/providers/microsoft.redhatopenshift/stamps/1/managementclusters/mgmt-a"))
	kubeApplierClients.Register(managementClusterID, mockKubeApplierDB)

	return &internallistertesting.DBReadDesireLister{
		Clients: kubeApplierClients,
		Lister: &internallistertesting.SliceManagementClusterLister{
			ManagementClusters: []*fleet.ManagementCluster{
				{
					CosmosMetadata: api.CosmosMetadata{ResourceID: managementClusterID},
					ResourceID:     managementClusterID,
				},
			},
		},
	}, nil
}

func newTestCluster(hcpClusterName string, opts ...func(*api.HCPOpenShiftCluster)) *api.HCPOpenShiftCluster {
	resourceID := api.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + testSubscriptionID +
			"/resourceGroups/" + testResourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + hcpClusterName,
	))

	cluster := &api.HCPOpenShiftCluster{
		CosmosMetadata: arm.CosmosMetadata{
			ResourceID: resourceID,
		},
		TrackedResource: arm.TrackedResource{
			Resource: arm.Resource{
				ID:   resourceID,
				Name: hcpClusterName,
				Type: resourceID.ResourceType.String(),
			},
		},
		ServiceProviderProperties: api.HCPOpenShiftClusterServiceProviderProperties{
			ClusterServiceID: api.Ptr(api.Must(api.NewInternalID(testClusterServiceIDStr))),
		},
	}

	for _, opt := range opts {
		opt(cluster)
	}

	return cluster
}

func newHostedClusterReadDesire(t *testing.T, hostedCluster *hsv1beta1.HostedCluster) *kubeapplier.ReadDesire {
	t.Helper()

	resourceIDString := kubeapplier.ToClusterScopedReadDesireResourceIDString(
		testSubscriptionID,
		testResourceGroupName,
		testClusterName,
		maestrohelpers.ReadDesireNameReadonlyHostedCluster,
	)
	resourceID := api.Must(azcorearm.ParseResourceID(resourceIDString))

	raw, err := json.Marshal(hostedCluster)
	require.NoError(t, err)

	return &kubeapplier.ReadDesire{
		CosmosMetadata: api.CosmosMetadata{
			ResourceID: resourceID,
		},
		Spec: kubeapplier.ReadDesireSpec{
			ManagementCluster: api.Must(azcorearm.ParseResourceID(
				"/providers/microsoft.redhatopenshift/stamps/1/managementclusters/mgmt-a")),
		},
		Status: kubeapplier.ReadDesireStatus{
			KubeContent: &runtime.RawExtension{Raw: raw},
		},
	}
}

type alwaysSyncCooldownChecker struct{}

func (c *alwaysSyncCooldownChecker) CanSync(ctx context.Context, key any) bool {
	return true
}
