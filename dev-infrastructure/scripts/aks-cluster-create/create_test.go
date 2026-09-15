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

package main

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	azfake "github.com/Azure/azure-sdk-for-go/sdk/azcore/fake"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v6"
	armcomputefake "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v6/fake"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v8"
	armcontainerservicefake "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v8/fake"

	"github.com/Azure/ARO-HCP/fleet/pkg/azure/skucache"
	"github.com/Azure/ARO-HCP/fleet/pkg/compute"
)

func memoryBytes(value string) int64 {
	quantity := resource.MustParse(value)
	return quantity.Value()
}

func testSystemPool() compute.Pool {
	return compute.Pool{
		Role:              compute.PoolRoleSystem,
		Name:              "s1abc1234567",
		Spec:              compute.VMSpec{Size: "Standard_D4ds_v6", Family: "standardDDSv6Family", VCPUs: 4, MemoryBytes: memoryBytes("16Gi"), SecondaryNICs: 0},
		AvailabilityZones: []string{"1", "2", "3"},
		MaxCount:          3,
		MinCount:          1,
		OSDiskSizeGB:      32,
		MaxPods:           100,
		Labels:            map[string]string{compute.RoleLabel: string(compute.PoolRoleSystem)},
		Taints:            []string{compute.TaintCriticalAddonsOnly},
	}
}

func testValidatedOptions() *validatedOptions {
	return &validatedOptions{
		subscriptionID: "sub1",
		resourceGroup:  "rg1",
		clusterName:    "cluster1",
		region:         "eastus",

		nodeSubnetID:         "/subscriptions/sub1/.../node-subnet",
		podSubnetID:          "/subscriptions/sub1/.../pod-subnet",
		networkDataplane:     "cilium",
		networkPolicy:        "cilium",
		outboundIPResourceID: "/subscriptions/sub1/.../outbound-ip",

		managedIdentityID: "/subscriptions/sub1/.../mi1",
		etcdKMSKeyURI:     "https://kv1.vault.azure.net/keys/aks-etcd-encryption/abc123",

		kubernetesVersion: "1.31.1",
		clusterTags:       map[string]string{"clusterType": "mgmt", "persist": "true"},
	}
}

func TestBuildManagedClusterNonCiliumSkipsAdvancedNetworking(t *testing.T) {
	o := testValidatedOptions()
	o.networkDataplane = "azure"
	bootstrap := testSystemPool()

	got, _, err := o.desiredClusterSpec(nil, &bootstrap)
	require.NoError(t, err)

	assert.Nil(t, got.Properties.NetworkProfile.AdvancedNetworking, "advanced networking should only be set for the cilium dataplane")
}

// newManagedClustersTestClient builds a ManagedClustersClient backed by the
// given fake server.
func newManagedClustersTestClient(t *testing.T, srv *armcontainerservicefake.ManagedClustersServer) *armcontainerservice.ManagedClustersClient {
	t.Helper()
	transport := armcontainerservicefake.NewManagedClustersServerTransport(srv)
	client, err := armcontainerservice.NewManagedClustersClient("sub1", &azfake.TokenCredential{}, &azcorearm.ClientOptions{
		ClientOptions: policy.ClientOptions{Transport: transport},
	})
	require.NoError(t, err)
	return client
}

func TestGetCluster(t *testing.T) {
	tests := []struct {
		name    string
		get     func(ctx context.Context, resourceGroupName, resourceName string, options *armcontainerservice.ManagedClustersClientGetOptions) (resp azfake.Responder[armcontainerservice.ManagedClustersClientGetResponse], errResp azfake.ErrorResponder)
		wantErr string
	}{
		{
			name: "cluster not found returns nil, nil",
			get: func(ctx context.Context, resourceGroupName, resourceName string, options *armcontainerservice.ManagedClustersClientGetOptions) (resp azfake.Responder[armcontainerservice.ManagedClustersClientGetResponse], errResp azfake.ErrorResponder) {
				errResp.SetResponseError(http.StatusNotFound, "ClusterNotFound")
				return
			},
		},
		{
			name: "unexpected error is returned",
			get: func(ctx context.Context, resourceGroupName, resourceName string, options *armcontainerservice.ManagedClustersClientGetOptions) (resp azfake.Responder[armcontainerservice.ManagedClustersClientGetResponse], errResp azfake.ErrorResponder) {
				errResp.SetResponseError(http.StatusInternalServerError, "InternalError")
				return
			},
			wantErr: "500",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := newManagedClustersTestClient(t, &armcontainerservicefake.ManagedClustersServer{Get: test.get})

			got, err := getCluster(context.Background(), client, "rg1", "cluster1")

			if len(test.wantErr) > 0 {
				require.Error(t, err)
				assert.Contains(t, err.Error(), test.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Nil(t, got)
		})
	}
}

// newRunTestSKUCache returns a SKUCache serving skus via the real Azure SDK
// fake transport.
func newRunTestSKUCache(t *testing.T, skus []*armcompute.ResourceSKU) *skucache.SKUCache {
	t.Helper()
	srv := armcomputefake.ResourceSKUsServer{
		NewListPager: func(_ *armcompute.ResourceSKUsClientListOptions) (resp azfake.PagerResponder[armcompute.ResourceSKUsClientListResponse]) {
			resp.AddPage(http.StatusOK, armcompute.ResourceSKUsClientListResponse{
				ResourceSKUsResult: armcompute.ResourceSKUsResult{Value: skus},
			}, nil)
			return
		},
	}
	transport := armcomputefake.NewResourceSKUsServerTransport(&srv)
	return skucache.NewSKUCache("eastus", &azfake.TokenCredential{}, &policy.ClientOptions{Transport: transport}, nil)
}

func TestDesiredPoolsResolveRegionalZones(t *testing.T) {
	tests := []struct {
		name  string
		zones string
		want  []poolPlacement
	}{
		{
			name: "empty zones use regional count and select first three worker zones",
			want: []poolPlacement{
				{name: "s1abc1234567", zones: []string{"1", "2", "3", "4"}},
				{name: "w1", zones: []string{"1"}},
				{name: "w2", zones: []string{"2"}},
				{name: "w3", zones: []string{"3"}},
				{name: "i1", zones: []string{"1"}},
			},
		},
		{
			name:  "explicit zones preserve selection and order",
			zones: "4,2,1",
			want: []poolPlacement{
				{name: "s1abc1234567", zones: []string{"4", "2", "1"}},
				{name: "w4", zones: []string{"4"}},
				{name: "w2", zones: []string{"2"}},
				{name: "w1", zones: []string{"1"}},
				{name: "i4", zones: []string{"4"}},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			raw := newRawOptionsFromEnv(envFunc(map[string]string{
				"REGION_AVAILABILITY_ZONE_COUNT": "4",
				"SYSTEM_POOL_ZONES":              test.zones,
				"USER_POOL_ZONES":                test.zones,
				"INFRA_POOL_ZONES":               test.zones,
			}))
			validated, err := raw.Validate()
			require.NoError(t, err)
			var skus []*armcompute.ResourceSKU
			for _, name := range []string{"Standard_D4ds_v6", "Standard_E16ds_v6"} {
				skus = append(skus, &armcompute.ResourceSKU{
					Name: ptr.To(name), ResourceType: ptr.To("virtualMachines"),
					Capabilities: []*armcompute.ResourceSKUCapabilities{
						{Name: ptr.To("vCPUs"), Value: ptr.To("4")},
						{Name: ptr.To("MemoryGB"), Value: ptr.To("16")},
					},
				})
			}
			completed := &completedOptions{validatedOptions: validated, skuCache: newRunTestSKUCache(t, skus)}
			pools, err := completed.desiredPools(context.Background())
			require.NoError(t, err)
			var placements []poolPlacement
			for _, pool := range pools {
				placements = append(placements, poolPlacement{name: pool.Name, zones: pool.AvailabilityZones})
			}
			assert.Equal(t, test.want, placements)
		})
	}
}

func TestRunUnknownPoolSize(t *testing.T) {
	o := testValidatedOptions()
	o.system = poolConfig{name: "sys", vmSize: "missing", osDiskSizeGB: 32, minCount: 1, maxCount: 3, poolCount: 1}
	completed := &completedOptions{
		validatedOptions: o,
		skuCache:         newRunTestSKUCache(t, nil),
	}

	err := completed.run(context.Background())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found in region SKUs")
}
