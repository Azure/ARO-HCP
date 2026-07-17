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
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/json"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	hsv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/Azure/ARO-HCP/backend/pkg/kubeapplierhelpers"
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
	resources := []any{}
	if readDesire != nil {
		resources = append(resources, readDesire)
	}

	mockKubeApplierDB, err := databasetesting.NewMockKubeApplierDBClientWithResources(ctx, resources)
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
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(resourceID.SubscriptionID),
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

func newTestHostedClusterReadDesire(t *testing.T, opts ...func(*hsv1beta1.HostedCluster)) *kubeapplier.ReadDesire {
	t.Helper()

	hostedCluster := &hsv1beta1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{Name: testBaseDomainPrefix},
		Spec: hsv1beta1.HostedClusterSpec{
			DNS:                  hsv1beta1.DNSSpec{BaseDomain: testHostedClusterIngressBaseDomain},
			KubeAPIServerDNSName: testAPIHost,
			IssuerURL:            testIssuerURL,
		},
		Status: hsv1beta1.HostedClusterStatus{
			ControlPlaneEndpoint: hsv1beta1.APIEndpoint{Port: testAPIPort},
		},
	}
	for _, opt := range opts {
		opt(hostedCluster)
	}

	resourceIDString := kubeapplier.ToClusterScopedReadDesireResourceIDString(
		testSubscriptionID,
		testResourceGroupName,
		testClusterName,
		kubeapplierhelpers.ReadDesireNameReadonlyHostedCluster,
	)
	resourceID := api.Must(azcorearm.ParseResourceID(resourceIDString))

	raw, err := json.Marshal(hostedCluster)
	require.NoError(t, err)

	managementClusterResourceID := api.Must(azcorearm.ParseResourceID(
		"/providers/microsoft.redhatopenshift/stamps/1/managementclusters/mgmt-a"))

	return &kubeapplier.ReadDesire{
		CosmosMetadata: api.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(managementClusterResourceID.String()),
		},
		Spec: kubeapplier.ReadDesireSpec{
			ManagementCluster: managementClusterResourceID,
		},
		Status: kubeapplier.ReadDesireStatus{
			KubeContent: &runtime.RawExtension{Raw: raw},
		},
	}
}
