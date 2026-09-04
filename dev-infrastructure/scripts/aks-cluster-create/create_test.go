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
	"testing"

	"github.com/go-logr/logr"
	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"k8s.io/apimachinery/pkg/util/sets"
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
		Spec:              compute.VMSpec{Size: "Standard_D4ds_v6", Family: "standardDDSv6Family", VCPUs: 4, MemoryGiB: 16, SecondaryNICs: 0},
		AvailabilityZones: []string{"1", "2", "3"},
		MaxCount:          3,
		InitialMinCount:   1,
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

	got, _, err := buildClusterSpec(o, nil, &bootstrap)
	require.NoError(t, err)

	b, err := json.MarshalIndent(got, "", "  ")
	require.NoError(t, err)
	assertGolden(t, string(b)+"\n")
}

func TestBuildManagedClusterNonCiliumSkipsAdvancedNetworking(t *testing.T) {
	o := testValidatedOptions()
	o.networkDataplane = "azure"
	bootstrap := testSystemPool()

	got, _, err := buildClusterSpec(o, nil, &bootstrap)
	require.NoError(t, err)

	assert.Nil(t, got.Properties.NetworkProfile.AdvancedNetworking, "advanced networking should only be set for the cilium dataplane")
}

func TestBootstrapPoolConfigurationMatchesAgentPoolRequest(t *testing.T) {
	validated := testValidatedOptions()
	bootstrap := testSystemPool()
	var inlinePool map[string]json.RawMessage
	clustersClient := newManagedClustersTestClient(t, &armcontainerservicefake.ManagedClustersServer{
		BeginCreateOrUpdate: func(_ context.Context, _, _ string, parameters armcontainerservice.ManagedCluster, _ *armcontainerservice.ManagedClustersClientBeginCreateOrUpdateOptions) (resp azfake.PollerResponder[armcontainerservice.ManagedClustersClientCreateOrUpdateResponse], errResp azfake.ErrorResponder) {
			require.NotNil(t, parameters.Properties)
			require.Len(t, parameters.Properties.AgentPoolProfiles, 1)
			pool := parameters.Properties.AgentPoolProfiles[0]
			require.NotNil(t, pool)
			require.Equal(t, bootstrap.Name, ptr.Deref(pool.Name, ""))
			body, err := json.Marshal(pool)
			require.NoError(t, err)
			require.NoError(t, json.Unmarshal(body, &inlinePool))
			resp.SetTerminalResponse(http.StatusOK, armcontainerservice.ManagedClustersClientCreateOrUpdateResponse{ManagedCluster: parameters}, nil)
			return
		},
	})
	var agentPoolBody []byte
	poolServer := armcontainerservicefake.AgentPoolsServer{
		Get: func(context.Context, string, string, string, *armcontainerservice.AgentPoolsClientGetOptions) (resp azfake.Responder[armcontainerservice.AgentPoolsClientGetResponse], errResp azfake.ErrorResponder) {
			errResp.SetResponseError(http.StatusNotFound, "NotFound")
			return
		},
		BeginCreateOrUpdate: func(_ context.Context, _, _, name string, parameters armcontainerservice.AgentPool, _ *armcontainerservice.AgentPoolsClientBeginCreateOrUpdateOptions) (resp azfake.PollerResponder[armcontainerservice.AgentPoolsClientCreateOrUpdateResponse], errResp azfake.ErrorResponder) {
			require.Equal(t, bootstrap.Name, name)
			var err error
			agentPoolBody, err = json.Marshal(parameters.Properties)
			require.NoError(t, err)
			resp.SetTerminalResponse(http.StatusOK, armcontainerservice.AgentPoolsClientCreateOrUpdateResponse{AgentPool: parameters}, nil)
			return
		},
	}
	poolsClient, err := armcontainerservice.NewAgentPoolsClient("sub1", &azfake.TokenCredential{}, &azcorearm.ClientOptions{ClientOptions: policy.ClientOptions{Transport: armcontainerservicefake.NewAgentPoolsServerTransport(&poolServer)}})
	require.NoError(t, err)
	o := &completedOptions{validatedOptions: validated, clustersClient: clustersClient}
	_, err = o.ensureManagedCluster(context.Background(), nil, &bootstrap, logr.Discard())
	require.NoError(t, err)
	networkConfig := compute.NetworkConfig{VnetSubnetID: validated.nodeSubnetID, PodSubnetID: validated.podSubnetID}
	_, err = ensurePool(context.Background(), poolsClient, validated.resourceGroup, validated.clusterName, bootstrap, networkConfig, logr.Discard())
	require.NoError(t, err)

	// Compare what the two ARM endpoints received, not adapter fields or SDK
	// types. Only the inline request includes the pool name in its properties.
	delete(inlinePool, "name")
	inlineBody, err := json.Marshal(inlinePool)
	require.NoError(t, err)
	assert.JSONEq(t, string(agentPoolBody), string(inlineBody), "inline bootstrap must preserve the same networking, scaling, scheduling, and security configuration as the agent pool API")
}

func TestEnsurePool(t *testing.T) {
	pool := compute.Pool{
		Role:              compute.PoolRoleWorker,
		Name:              "w1abc1234567",
		Spec:              compute.VMSpec{Size: "Standard_E16ds_v6", Family: "standardEDSv6Family", VCPUs: 16, MemoryGiB: 128, SecondaryNICs: 1},
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
							ETag:              ptr.To("pool-etag"),
							ProvisioningState: ptr.To("Failed"),
						},
					},
				}, nil)
				return
			},
			wantCreateOrUpdate: true,
		},
		{
			name: "failed pool without an ETag cannot be overwritten",
			get: func(ctx context.Context, resourceGroupName, resourceName, agentPoolName string, options *armcontainerservice.AgentPoolsClientGetOptions) (resp azfake.Responder[armcontainerservice.AgentPoolsClientGetResponse], errResp azfake.ErrorResponder) {
				resp.SetResponse(http.StatusOK, armcontainerservice.AgentPoolsClientGetResponse{
					AgentPool: armcontainerservice.AgentPool{
						Properties: &armcontainerservice.ManagedClusterAgentPoolProfileProperties{ProvisioningState: ptr.To("Failed")},
					},
				}, nil)
				return
			},
			wantErr: "no ETag",
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
					if options.IfMatch == nil {
						require.Equal(t, ptr.To("*"), options.IfNoneMatch)
					} else {
						require.Equal(t, ptr.To("pool-etag"), options.IfMatch)
						require.Nil(t, options.IfNoneMatch)
					}
					resp.SetTerminalResponse(http.StatusOK, armcontainerservice.AgentPoolsClientCreateOrUpdateResponse{AgentPool: parameters}, nil)
					return
				},
			}
			transport := armcontainerservicefake.NewAgentPoolsServerTransport(&srv)
			client, err := armcontainerservice.NewAgentPoolsClient("sub1", &azfake.TokenCredential{}, &azcorearm.ClientOptions{
				ClientOptions: policy.ClientOptions{Transport: transport},
			})
			require.NoError(t, err)

			_, err = ensurePool(context.Background(), client, "rg1", "cluster1", pool, networkConfig, logr.Discard())

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
	t.Run("finished cluster reconciles configuration without provisioning pools", func(t *testing.T) {
		o := testValidatedOptions()
		o.profile = compute.Profile{BudgetStrategy: compute.UnlimitedBudget}
		bootstrap := testSystemPool()
		live, _, err := buildClusterSpec(o, nil, &bootstrap)
		require.NoError(t, err)
		live.Tags = map[string]*string{"clusterType": ptr.To("mgmt"), "persist": ptr.To("true")}
		live.ETag = ptr.To("etag-1")
		live.Properties.ProvisioningState = ptr.To("Succeeded")
		live.Properties.KubernetesVersion = ptr.To("1.35.2")
		live.Properties.SecurityProfile.AzureKeyVaultKms.KeyID = ptr.To("https://kv1.vault.azure.net/keys/aks-etcd-encryption/old")
		live.Tags["arohcp-capacity-system"] = ptr.To(`{"vcpus":12,"memoryGiB":48,"swiftNICs":0}`)
		live.Tags["arohcp-capacity-infra"] = ptr.To(`{"vcpus":8,"memoryGiB":32,"swiftNICs":0}`)
		live.Tags["arohcp-capacity-worker"] = ptr.To(`{"vcpus":64,"memoryGiB":512,"swiftNICs":28}`)
		updates := 0
		clustersClient := newManagedClustersTestClient(t, &armcontainerservicefake.ManagedClustersServer{
			Get: func(ctx context.Context, resourceGroupName, resourceName string, options *armcontainerservice.ManagedClustersClientGetOptions) (resp azfake.Responder[armcontainerservice.ManagedClustersClientGetResponse], errResp azfake.ErrorResponder) {
				resp.SetResponse(http.StatusOK, armcontainerservice.ManagedClustersClientGetResponse{ManagedCluster: live}, nil)
				return
			},
			BeginCreateOrUpdate: func(_ context.Context, _, _ string, parameters armcontainerservice.ManagedCluster, options *armcontainerservice.ManagedClustersClientBeginCreateOrUpdateOptions) (resp azfake.PollerResponder[armcontainerservice.ManagedClustersClientCreateOrUpdateResponse], errResp azfake.ErrorResponder) {
				updates++
				require.Equal(t, live.ETag, options.IfMatch)
				require.Nil(t, parameters.Properties.AgentPoolProfiles)
				require.Nil(t, parameters.Properties.KubernetesVersion)
				parameters.Properties.KubernetesVersion = live.Properties.KubernetesVersion
				live = parameters
				resp.SetTerminalResponse(http.StatusOK, armcontainerservice.ManagedClustersClientCreateOrUpdateResponse{ManagedCluster: live}, nil)
				return
			},
		})
		completed := &completedOptions{
			validatedOptions: o,
			clustersClient:   clustersClient,
			skuCache:         newRunTestSKUCache(t, nil),
		}
		require.NoError(t, completed.run(context.Background(), logr.Discard()))
		require.Equal(t, o.etcdKMSKeyURI, *live.Properties.SecurityProfile.AzureKeyVaultKms.KeyID)
		require.Equal(t, "1.35.2", *live.Properties.KubernetesVersion)
		require.NoError(t, completed.run(context.Background(), logr.Discard()))
		require.Equal(t, 1, updates, "a converged rerun must not submit another cluster update")
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
					PoolMode:       compute.PoolModeRegional,
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

		err := completed.run(context.Background(), logr.Discard())

		require.Error(t, err)
		assert.Contains(t, err.Error(), "required tier allocation failed")
	})
}

func TestRunCapacityBaseline(t *testing.T) {
	tests := []struct {
		name      string
		existing  bool
		resume    bool
		baseline  bool
		lowerPlan bool
		partial   bool
		poolState string
		wantErr   string
	}{
		{name: "new_cluster"},
		{name: "adopt_existing_cluster", existing: true},
		{name: "resume_without_baseline", existing: true, resume: true},
		{name: "resume_preserves_baseline", existing: true, resume: true, baseline: true},
		{name: "resume_accepts_full_lower_plan", existing: true, resume: true, baseline: true, lowerPlan: true},
		{name: "resume_accepts_partial_lower_plan", existing: true, resume: true, baseline: true, partial: true},
		{name: "resume_waits_for_pool_completion", existing: true, resume: true, poolState: "Updating", wantErr: "must be successfully provisioned"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			o := testValidatedOptions()
			o.profile = compute.Profile{BudgetStrategy: compute.UnlimitedBudget, Tiers: []compute.TierConfig{{Name: "sys", Role: compute.PoolRoleSystem, PoolMode: compute.PoolModeRegional, PoolCount: 1, Cores: 4, MaxNodes: 3, OSDiskSizeGB: 32, FamilyPriority: []compute.VMFamily{"family"}, Required: true}}}
			o.zones = []string{"1"}
			bootstrap := testSystemPool()
			live, _, err := buildClusterSpec(o, nil, &bootstrap)
			require.NoError(t, err)
			live.Tags = map[string]*string{"clusterType": ptr.To("mgmt"), "persist": ptr.To("true")}
			live.ETag = ptr.To("e1")
			live.Tags["owner"] = ptr.To("team")
			live.Properties.ProvisioningState = ptr.To("Succeeded")
			exists := test.existing
			if test.resume {
				live.Tags[provisioningTagKey] = ptr.To(provisioningTagValue)
			}
			if test.baseline {
				live.Tags["arohcp-capacity-system"] = ptr.To(`{"vcpus":12,"memoryGiB":48,"swiftNICs":0}`)
				live.Tags["arohcp-capacity-infra"] = ptr.To(`{"vcpus":0,"memoryGiB":0,"swiftNICs":0}`)
				live.Tags["arohcp-capacity-worker"] = ptr.To(`{"vcpus":0,"memoryGiB":0,"swiftNICs":0}`)
			}
			if test.lowerPlan {
				o.profile.Tiers[0].MaxNodes = 2
			}
			if test.partial {
				o.profile.BudgetStrategy = func(context.Context, sets.Set[compute.VMFamily], compute.FetchQuotaUsageFunc) (map[compute.VMFamily]compute.QuotaUsage, error) {
					return map[compute.VMFamily]compute.QuotaUsage{"family": {Limit: 12}}, nil
				}
			}
			clustersClient := newManagedClustersTestClient(t, &armcontainerservicefake.ManagedClustersServer{
				Get: func(context.Context, string, string, *armcontainerservice.ManagedClustersClientGetOptions) (resp azfake.Responder[armcontainerservice.ManagedClustersClientGetResponse], errResp azfake.ErrorResponder) {
					if !exists {
						errResp.SetResponseError(http.StatusNotFound, "NotFound")
						return
					}
					resp.SetResponse(http.StatusOK, armcontainerservice.ManagedClustersClientGetResponse{ManagedCluster: live}, nil)
					return
				},
				BeginCreateOrUpdate: func(_ context.Context, _, _ string, parameters armcontainerservice.ManagedCluster, options *armcontainerservice.ManagedClustersClientBeginCreateOrUpdateOptions) (resp azfake.PollerResponder[armcontainerservice.ManagedClustersClientCreateOrUpdateResponse], errResp azfake.ErrorResponder) {
					require.False(t, exists, "resuming provisioning must not resubmit an inline bootstrap pool")
					require.Equal(t, ptr.To("*"), options.IfNoneMatch)
					live.Tags = parameters.Tags
					exists = true
					resp.SetTerminalResponse(http.StatusOK, armcontainerservice.ManagedClustersClientCreateOrUpdateResponse{ManagedCluster: live}, nil)
					return
				},
				BeginUpdateTags: func(_ context.Context, _, _ string, parameters armcontainerservice.TagsObject, options *armcontainerservice.ManagedClustersClientBeginUpdateTagsOptions) (resp azfake.PollerResponder[armcontainerservice.ManagedClustersClientUpdateTagsResponse], errResp azfake.ErrorResponder) {
					require.Equal(t, "e1", *options.IfMatch)
					live.Tags = parameters.Tags
					resp.SetTerminalResponse(http.StatusOK, armcontainerservice.ManagedClustersClientUpdateTagsResponse{ManagedCluster: live}, nil)
					return
				},
			})
			poolServer := armcontainerservicefake.AgentPoolsServer{
				Get: func(_ context.Context, _, _, poolName string, _ *armcontainerservice.AgentPoolsClientGetOptions) (resp azfake.Responder[armcontainerservice.AgentPoolsClientGetResponse], errResp azfake.ErrorResponder) {
					state := test.poolState
					if state == "" {
						state = "Succeeded"
					}
					resp.SetResponse(http.StatusOK, armcontainerservice.AgentPoolsClientGetResponse{AgentPool: armcontainerservice.AgentPool{
						Name: ptr.To(poolName), Properties: &armcontainerservice.ManagedClusterAgentPoolProfileProperties{ETag: ptr.To("pool-e1"), ProvisioningState: ptr.To(state)},
					}}, nil)
					return
				},
				NewListPager: func(string, string, *armcontainerservice.AgentPoolsClientListOptions) (resp azfake.PagerResponder[armcontainerservice.AgentPoolsClientListResponse]) {
					poolState := test.poolState
					if len(poolState) == 0 {
						poolState = "Succeeded"
					}
					resp.AddPage(http.StatusOK, armcontainerservice.AgentPoolsClientListResponse{AgentPoolListResult: armcontainerservice.AgentPoolListResult{Value: []*armcontainerservice.AgentPool{{Name: ptr.To("sys"), Properties: &armcontainerservice.ManagedClusterAgentPoolProfileProperties{
						VMSize: ptr.To("sku"), Count: ptr.To[int32](1), MaxCount: ptr.To[int32](3), EnableAutoScaling: ptr.To(true), ProvisioningState: ptr.To(poolState), NodeLabels: map[string]*string{compute.RoleLabel: ptr.To("system")},
					}}}}}, nil)
					return
				},
			}
			poolsClient, err := armcontainerservice.NewAgentPoolsClient("sub1", &azfake.TokenCredential{}, &azcorearm.ClientOptions{ClientOptions: policy.ClientOptions{Transport: armcontainerservicefake.NewAgentPoolsServerTransport(&poolServer)}})
			require.NoError(t, err)
			skuCache := newRunTestSKUCache(t, []*armcompute.ResourceSKU{{Name: ptr.To("sku"), Family: ptr.To("family"), ResourceType: ptr.To("virtualMachines"), LocationInfo: []*armcompute.ResourceSKULocationInfo{{Zones: []*string{ptr.To("1")}}}, Capabilities: []*armcompute.ResourceSKUCapabilities{
				{Name: ptr.To("vCPUs"), Value: ptr.To("4")}, {Name: ptr.To("MemoryGB"), Value: ptr.To("16")}, {Name: ptr.To("EphemeralOSDiskSupported"), Value: ptr.To("True")}, {Name: ptr.To("CachedDiskBytes"), Value: ptr.To("107374182400")},
			}}})
			err = (&completedOptions{validatedOptions: o, clustersClient: clustersClient, poolsClient: poolsClient, skuCache: skuCache}).run(context.Background(), logr.Discard())
			if len(test.wantErr) > 0 {
				require.ErrorContains(t, err, test.wantErr)
				require.Contains(t, live.Tags, provisioningTagKey)
				require.NotContains(t, live.Tags, "arohcp-capacity-system", "unfinished provisioning must not establish a baseline")
			} else {
				require.NoError(t, err)
				require.JSONEq(t, `{"vcpus":12,"memoryGiB":48,"swiftNICs":0}`, *live.Tags["arohcp-capacity-system"])
				require.JSONEq(t, `{"vcpus":0,"memoryGiB":0,"swiftNICs":0}`, *live.Tags["arohcp-capacity-worker"])
				require.NotContains(t, live.Tags, provisioningTagKey)
			}
		})
	}
}
