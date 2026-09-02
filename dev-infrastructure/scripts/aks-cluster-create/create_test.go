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
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/go-logr/logr"
	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	azfake "github.com/Azure/azure-sdk-for-go/sdk/azcore/fake"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v6"
	armcomputefake "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v6/fake"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v6"
	armcontainerservicefake "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v6/fake"

	"github.com/Azure/ARO-HCP/fleet/pkg/azure/agentpoolspec"
	"github.com/Azure/ARO-HCP/fleet/pkg/azure/skucache"
	"github.com/Azure/ARO-HCP/fleet/pkg/compute"
)

func assertGolden(t *testing.T, got string) {
	t.Helper()
	golden := filepath.Join("testdata", t.Name()+".json")

	if os.Getenv("UPDATE_GOLDEN") != "" {
		require.NoError(t, os.MkdirAll(filepath.Dir(golden), 0o755))
		require.NoError(t, os.WriteFile(golden, []byte(got), 0o644))
		return
	}

	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("golden file not found: %s (run with UPDATE_GOLDEN=1 to create)", golden)
	}
	if diff := cmp.Diff(string(want), got); diff != "" {
		t.Errorf("golden file mismatch (-want +got):\n%s", diff)
	}
}

func testSystemPool() compute.Pool {
	return compute.Pool{
		Role:              compute.PoolRoleSystem,
		Name:              "s1abc1234567",
		Spec:              compute.VMSpec{Size: "Standard_D4ds_v6", Family: "standardDDSv6Family", VCPUs: 4, MemoryGB: 16, SecondaryNICs: 0},
		AvailabilityZones: []string{"1", "2", "3"},
		MaxCount:          3,
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

func TestBuildManagedCluster(t *testing.T) {
	o := testValidatedOptions()
	bootstrap := testSystemPool()
	networkConfig := compute.NetworkConfig{VnetSubnetID: o.nodeSubnetID, PodSubnetID: o.podSubnetID}

	got := buildManagedCluster(o, bootstrap, networkConfig)

	b, err := json.MarshalIndent(got, "", "  ")
	require.NoError(t, err)
	assertGolden(t, string(b)+"\n")
}

func TestBuildManagedClusterNonCiliumSkipsAdvancedNetworking(t *testing.T) {
	o := testValidatedOptions()
	o.networkDataplane = "azure"
	bootstrap := testSystemPool()
	networkConfig := compute.NetworkConfig{VnetSubnetID: o.nodeSubnetID, PodSubnetID: o.podSubnetID}

	got := buildManagedCluster(o, bootstrap, networkConfig)

	assert.Nil(t, got.Properties.NetworkProfile.AdvancedNetworking, "advanced networking should only be set for the cilium dataplane")
}

func TestToClusterAgentPoolProfile(t *testing.T) {
	pool := testSystemPool()
	networkConfig := compute.NetworkConfig{VnetSubnetID: "/subnet/node", PodSubnetID: "/subnet/pod"}
	props := agentpoolspec.Build(pool, networkConfig)

	got := toClusterAgentPoolProfile(pool.Name, props)

	require.NotNil(t, got.Name)
	assert.Equal(t, pool.Name, *got.Name)
	assert.Equal(t, props.VMSize, got.VMSize)
	assert.Equal(t, props.AvailabilityZones, got.AvailabilityZones)
	assert.Equal(t, props.OSDiskSizeGB, got.OSDiskSizeGB)
	assert.Equal(t, props.Mode, got.Mode)
	assert.Equal(t, props.Type, got.Type)
	assert.Equal(t, props.MaxCount, got.MaxCount)
	assert.Equal(t, props.NodeLabels, got.NodeLabels)
	assert.Equal(t, props.NodeTaints, got.NodeTaints)
	assert.Equal(t, props.VnetSubnetID, got.VnetSubnetID)
	assert.Equal(t, props.PodSubnetID, got.PodSubnetID)
	assert.Equal(t, props.SecurityProfile, got.SecurityProfile)
	assert.Equal(t, props.UpgradeSettings, got.UpgradeSettings)
}

func TestToClusterAgentPoolProfileFieldCoverage(t *testing.T) {
	propsType := reflect.TypeFor[armcontainerservice.ManagedClusterAgentPoolProfileProperties]()
	profileType := reflect.TypeFor[armcontainerservice.ManagedClusterAgentPoolProfile]()

	profileFields := make(map[string]reflect.Type, profileType.NumField())
	for i := 0; i < profileType.NumField(); i++ {
		f := profileType.Field(i)
		profileFields[f.Name] = f.Type
	}

	for i := 0; i < propsType.NumField(); i++ {
		f := propsType.Field(i)
		pf, exists := profileFields[f.Name]
		if !exists {
			t.Errorf("ManagedClusterAgentPoolProfileProperties has field %s that ManagedClusterAgentPoolProfile lacks — toClusterAgentPoolProfile may need updating", f.Name)
			continue
		}
		if pf != f.Type {
			t.Errorf("field %s type mismatch: Properties has %s, Profile has %s", f.Name, f.Type, pf)
		}
	}
}

func TestSplitPoolsByRole(t *testing.T) {
	systemPool := compute.Pool{Role: compute.PoolRoleSystem, Name: "system1"}
	infraPool := compute.Pool{Role: compute.PoolRoleInfra, Name: "infra1"}
	workerPool := compute.Pool{Role: compute.PoolRoleWorker, Name: "worker1"}

	system, infra, worker := splitPoolsByRole([]compute.Pool{workerPool, systemPool, infraPool, systemPool})

	assert.Equal(t, []compute.Pool{systemPool, systemPool}, system)
	assert.Equal(t, []compute.Pool{infraPool}, infra)
	assert.Equal(t, []compute.Pool{workerPool}, worker)
}

func TestSplitPoolsByRoleEmpty(t *testing.T) {
	system, infra, worker := splitPoolsByRole(nil)
	assert.Nil(t, system)
	assert.Nil(t, infra)
	assert.Nil(t, worker)
}

func TestHasProvisioningTag(t *testing.T) {
	tests := []struct {
		name string
		tags map[string]*string
		want bool
	}{
		{name: "nil tags", tags: nil, want: false},
		{name: "tag absent", tags: map[string]*string{"other": ptr.To("x")}, want: false},
		{name: "tag false", tags: map[string]*string{provisioningTagKey: ptr.To("false")}, want: false},
		{name: "tag nil value", tags: map[string]*string{provisioningTagKey: nil}, want: false},
		{name: "tag true", tags: map[string]*string{provisioningTagKey: ptr.To("true")}, want: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.want, hasProvisioningTag(test.tags))
		})
	}
}

func TestDesiredTags(t *testing.T) {
	o := testValidatedOptions()
	got := desiredTags(o)
	assert.Equal(t, map[string]string{
		"clusterType": "mgmt",
		"persist":     "true",
	}, got)
}

func TestEnsurePool(t *testing.T) {
	pool := compute.Pool{
		Role:              compute.PoolRoleWorker,
		Name:              "w1abc1234567",
		Spec:              compute.VMSpec{Size: "Standard_E16ds_v6", Family: "standardEDSv6Family", VCPUs: 16, MemoryGB: 128, SecondaryNICs: 1},
		AvailabilityZones: []string{"1"},
		MaxCount:          5,
		OSDiskSizeGB:      100,
	}
	networkConfig := compute.NetworkConfig{VnetSubnetID: "/subnet/node", PodSubnetID: "/subnet/pod"}

	tests := []struct {
		name               string
		get                func(ctx context.Context, resourceGroupName, resourceName, agentPoolName string, options *armcontainerservice.AgentPoolsClientGetOptions) (resp azfake.Responder[armcontainerservice.AgentPoolsClientGetResponse], errResp azfake.ErrorResponder)
		wantCreateOrUpdate bool
		wantErr            string
	}{
		{
			name: "pool not found creates it",
			get: func(ctx context.Context, resourceGroupName, resourceName, agentPoolName string, options *armcontainerservice.AgentPoolsClientGetOptions) (resp azfake.Responder[armcontainerservice.AgentPoolsClientGetResponse], errResp azfake.ErrorResponder) {
				errResp.SetResponseError(http.StatusNotFound, "PoolNotFound")
				return
			},
			wantCreateOrUpdate: true,
		},
		{
			name: "pool exists and succeeded is left alone",
			get: func(ctx context.Context, resourceGroupName, resourceName, agentPoolName string, options *armcontainerservice.AgentPoolsClientGetOptions) (resp azfake.Responder[armcontainerservice.AgentPoolsClientGetResponse], errResp azfake.ErrorResponder) {
				resp.SetResponse(http.StatusOK, armcontainerservice.AgentPoolsClientGetResponse{
					AgentPool: armcontainerservice.AgentPool{
						Properties: &armcontainerservice.ManagedClusterAgentPoolProfileProperties{
							ProvisioningState: ptr.To("Succeeded"),
						},
					},
				}, nil)
				return
			},
			wantCreateOrUpdate: false,
		},
		{
			name: "pool exists in Failed state is re-created",
			get: func(ctx context.Context, resourceGroupName, resourceName, agentPoolName string, options *armcontainerservice.AgentPoolsClientGetOptions) (resp azfake.Responder[armcontainerservice.AgentPoolsClientGetResponse], errResp azfake.ErrorResponder) {
				resp.SetResponse(http.StatusOK, armcontainerservice.AgentPoolsClientGetResponse{
					AgentPool: armcontainerservice.AgentPool{
						Properties: &armcontainerservice.ManagedClusterAgentPoolProfileProperties{
							ProvisioningState: ptr.To("Failed"),
						},
					},
				}, nil)
				return
			},
			wantCreateOrUpdate: true,
		},
		{
			name: "unexpected Get error is returned",
			get: func(ctx context.Context, resourceGroupName, resourceName, agentPoolName string, options *armcontainerservice.AgentPoolsClientGetOptions) (resp azfake.Responder[armcontainerservice.AgentPoolsClientGetResponse], errResp azfake.ErrorResponder) {
				errResp.SetResponseError(http.StatusInternalServerError, "InternalError")
				return
			},
			wantCreateOrUpdate: false,
			wantErr:            "checking pool",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			createOrUpdateCalled := false
			srv := armcontainerservicefake.AgentPoolsServer{
				Get: test.get,
				BeginCreateOrUpdate: func(ctx context.Context, resourceGroupName, resourceName, agentPoolName string, parameters armcontainerservice.AgentPool, options *armcontainerservice.AgentPoolsClientBeginCreateOrUpdateOptions) (resp azfake.PollerResponder[armcontainerservice.AgentPoolsClientCreateOrUpdateResponse], errResp azfake.ErrorResponder) {
					createOrUpdateCalled = true
					resp.SetTerminalResponse(http.StatusOK, armcontainerservice.AgentPoolsClientCreateOrUpdateResponse{AgentPool: parameters}, nil)
					return
				},
			}
			transport := armcontainerservicefake.NewAgentPoolsServerTransport(&srv)
			client, err := armcontainerservice.NewAgentPoolsClient("sub1", &azfake.TokenCredential{}, &azcorearm.ClientOptions{
				ClientOptions: policy.ClientOptions{Transport: transport},
			})
			require.NoError(t, err)

			err = ensurePool(context.Background(), client, "rg1", "cluster1", pool, networkConfig, logr.Discard())

			if len(test.wantErr) > 0 {
				require.Error(t, err)
				assert.Contains(t, err.Error(), test.wantErr)
			} else {
				require.NoError(t, err)
			}
			assert.Equal(t, test.wantCreateOrUpdate, createOrUpdateCalled)
		})
	}
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
		want    *armcontainerservice.ManagedCluster
		wantErr string
	}{
		{
			name: "cluster not found returns nil, nil",
			get: func(ctx context.Context, resourceGroupName, resourceName string, options *armcontainerservice.ManagedClustersClientGetOptions) (resp azfake.Responder[armcontainerservice.ManagedClustersClientGetResponse], errResp azfake.ErrorResponder) {
				errResp.SetResponseError(http.StatusNotFound, "ClusterNotFound")
				return
			},
			want: nil,
		},
		{
			name: "cluster found is returned",
			get: func(ctx context.Context, resourceGroupName, resourceName string, options *armcontainerservice.ManagedClustersClientGetOptions) (resp azfake.Responder[armcontainerservice.ManagedClustersClientGetResponse], errResp azfake.ErrorResponder) {
				resp.SetResponse(http.StatusOK, armcontainerservice.ManagedClustersClientGetResponse{
					ManagedCluster: armcontainerservice.ManagedCluster{Name: ptr.To("cluster1")},
				}, nil)
				return
			},
			want: &armcontainerservice.ManagedCluster{Name: ptr.To("cluster1")},
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
			assert.Equal(t, test.want, got)
		})
	}
}

func TestRemoveProvisioningTag(t *testing.T) {
	t.Run("sends the desired tag set", func(t *testing.T) {
		var gotTags map[string]*string
		client := newManagedClustersTestClient(t, &armcontainerservicefake.ManagedClustersServer{
			BeginUpdateTags: func(ctx context.Context, resourceGroupName, resourceName string, parameters armcontainerservice.TagsObject, options *armcontainerservice.ManagedClustersClientBeginUpdateTagsOptions) (resp azfake.PollerResponder[armcontainerservice.ManagedClustersClientUpdateTagsResponse], errResp azfake.ErrorResponder) {
				gotTags = parameters.Tags
				resp.SetTerminalResponse(http.StatusOK, armcontainerservice.ManagedClustersClientUpdateTagsResponse{}, nil)
				return
			},
		})

		err := removeProvisioningTag(context.Background(), client, "rg1", "cluster1", map[string]string{"clusterType": "mgmt"})

		require.NoError(t, err)
		require.Contains(t, gotTags, "clusterType")
		assert.Equal(t, "mgmt", *gotTags["clusterType"])
	})

	t.Run("update error is returned", func(t *testing.T) {
		client := newManagedClustersTestClient(t, &armcontainerservicefake.ManagedClustersServer{
			BeginUpdateTags: func(ctx context.Context, resourceGroupName, resourceName string, parameters armcontainerservice.TagsObject, options *armcontainerservice.ManagedClustersClientBeginUpdateTagsOptions) (resp azfake.PollerResponder[armcontainerservice.ManagedClustersClientUpdateTagsResponse], errResp azfake.ErrorResponder) {
				errResp.SetResponseError(http.StatusInternalServerError, "InternalError")
				return
			},
		})

		err := removeProvisioningTag(context.Background(), client, "rg1", "cluster1", map[string]string{})

		require.Error(t, err)
	})
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

func TestRun(t *testing.T) {
	t.Run("cluster already fully provisioned plans but applies nothing", func(t *testing.T) {
		clustersClient := newManagedClustersTestClient(t, &armcontainerservicefake.ManagedClustersServer{
			Get: func(ctx context.Context, resourceGroupName, resourceName string, options *armcontainerservice.ManagedClustersClientGetOptions) (resp azfake.Responder[armcontainerservice.ManagedClustersClientGetResponse], errResp azfake.ErrorResponder) {
				resp.SetResponse(http.StatusOK, armcontainerservice.ManagedClustersClientGetResponse{
					ManagedCluster: armcontainerservice.ManagedCluster{Name: ptr.To("cluster1")},
				}, nil)
				return
			},
			// No BeginCreateOrUpdate/BeginUpdateTags handler: the fake transport
			// errors on any unhandled call, tripping the test if run() mutates
			// anything past the plan-only short-circuit.
		})

		o := testValidatedOptions()
		o.profile = compute.Profile{BudgetStrategy: compute.UnlimitedBudget}

		completed := &completedOptions{
			validatedOptions: o,
			clustersClient:   clustersClient,
			skuCache:         newRunTestSKUCache(t, nil),
		}

		err := run(context.Background(), completed, logr.Discard())

		require.NoError(t, err)
	})

	t.Run("required tier allocation failure surfaces an error", func(t *testing.T) {
		clustersClient := newManagedClustersTestClient(t, &armcontainerservicefake.ManagedClustersServer{
			Get: func(ctx context.Context, resourceGroupName, resourceName string, options *armcontainerservice.ManagedClustersClientGetOptions) (resp azfake.Responder[armcontainerservice.ManagedClustersClientGetResponse], errResp azfake.ErrorResponder) {
				errResp.SetResponseError(http.StatusNotFound, "ClusterNotFound")
				return
			},
		})

		o := testValidatedOptions()
		o.profile = compute.Profile{
			Tiers: []compute.TierConfig{
				{
					Role:           compute.PoolRoleSystem,
					PoolMode:       compute.PoolModeSpanZones,
					Cores:          4,
					OSDiskSizeGB:   32,
					MaxNodes:       1,
					FamilyPriority: []compute.VMFamily{"missingFamily"},
					Required:       true,
				},
			},
			BudgetStrategy: compute.UnlimitedBudget,
		}
		o.zones = []string{"1", "2", "3"}

		completed := &completedOptions{
			validatedOptions: o,
			clustersClient:   clustersClient,
			skuCache:         newRunTestSKUCache(t, nil),
		}

		err := run(context.Background(), completed, logr.Discard())

		require.Error(t, err)
		assert.Contains(t, err.Error(), "required tier allocation failed")
	})
}
