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
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/json"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	hsv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/Azure/ARO-HCP/backend/pkg/kubeapplierhelpers"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/kubeappliercosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listers/kubeapplierlisters"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/fleetlistertesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/kubeapplierlistertesting"
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

func newSeededReadDesireLister(ctx context.Context, readDesires ...*kubeapplierapi.ReadDesire) (kubeapplierlisters.ReadDesireLister, error) {
	resources := []any{}
	for _, rd := range readDesires {
		if rd != nil {
			resources = append(resources, rd)
		}
	}

	mockKubeApplierDB, err := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClientWithResources(ctx, resources)
	if err != nil {
		return nil, err
	}

	kubeApplierClients := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClients()
	managementClusterID := metadataapi.Must(azcorearm.ParseResourceID(
		"/providers/microsoft.redhatopenshift/stamps/1/managementclusters/mgmt-a"))
	kubeApplierClients.Register(managementClusterID, mockKubeApplierDB)

	return &kubeapplierlistertesting.DBReadDesireLister{
		Clients: kubeApplierClients,
		Lister: &fleetlistertesting.SliceManagementClusterLister{
			ManagementClusters: []*fleetapi.ManagementCluster{
				{
					CosmosMetadata: coreapi.CosmosMetadata{ResourceID: managementClusterID},
				},
			},
		},
	}, nil
}

func newTestCluster(hcpClusterName string, opts ...func(*coreapi.HCPOpenShiftCluster)) *coreapi.HCPOpenShiftCluster {
	resourceID := metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + testSubscriptionID +
			"/resourceGroups/" + testResourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + hcpClusterName,
	))

	cluster := &coreapi.HCPOpenShiftCluster{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(resourceID.SubscriptionID),
		},
		TrackedResource: coreapi.TrackedResource{
			Resource: coreapi.Resource{
				ID:   resourceID,
				Name: hcpClusterName,
				Type: resourceID.ResourceType.String(),
			},
		},
		ServiceProviderProperties: coreapi.HCPOpenShiftClusterServiceProviderProperties{
			ClusterServiceID: metadataapi.Ptr(metadataapi.Must(metadataapi.NewInternalID(testClusterServiceIDStr))),
		},
	}

	for _, opt := range opts {
		opt(cluster)
	}

	return cluster
}

func newTestHostedClusterReadDesire(t *testing.T, opts ...func(*hsv1beta1.HostedCluster)) *kubeapplierapi.ReadDesire {
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

	resourceIDString := kubeapplierapi.ToClusterScopedReadDesireResourceIDString(
		testSubscriptionID,
		testResourceGroupName,
		testClusterName,
		kubeapplierhelpers.ReadDesireNameReadonlyHostedCluster,
	)
	resourceID := metadataapi.Must(azcorearm.ParseResourceID(resourceIDString))

	raw, err := json.Marshal(hostedCluster)
	require.NoError(t, err)

	managementClusterResourceID := metadataapi.Must(azcorearm.ParseResourceID(
		"/providers/microsoft.redhatopenshift/stamps/1/managementclusters/mgmt-a"))

	return &kubeapplierapi.ReadDesire{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(managementClusterResourceID.String()),
		},
		Spec: kubeapplierapi.ReadDesireSpec{
			ManagementCluster: managementClusterResourceID,
		},
		Status: kubeapplierapi.ReadDesireStatus{
			KubeContent: &runtime.RawExtension{Raw: raw},
		},
	}
}

func newTestServingCAReadDesire(t *testing.T, caBundlePEM string) *kubeapplierapi.ReadDesire {
	t.Helper()

	secret := &corev1.Secret{
		Data: map[string][]byte{
			"tls.crt": []byte(caBundlePEM),
		},
	}

	raw, err := json.Marshal(secret)
	require.NoError(t, err)

	resourceIDString := kubeapplierapi.ToClusterScopedReadDesireResourceIDString(
		testSubscriptionID,
		testResourceGroupName,
		testClusterName,
		kubeapplierhelpers.ReadDesireNameServingCA,
	)
	resourceID := metadataapi.Must(azcorearm.ParseResourceID(resourceIDString))

	managementClusterResourceID := metadataapi.Must(azcorearm.ParseResourceID(
		"/providers/microsoft.redhatopenshift/stamps/1/managementclusters/mgmt-a"))

	return &kubeapplierapi.ReadDesire{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(managementClusterResourceID.String()),
		},
		Spec: kubeapplierapi.ReadDesireSpec{
			ManagementCluster: managementClusterResourceID,
		},
		Status: kubeapplierapi.ReadDesireStatus{
			KubeContent: &runtime.RawExtension{Raw: raw},
		},
	}
}
