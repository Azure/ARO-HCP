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

	"k8s.io/apimachinery/pkg/util/sets"
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
	networkConfig := compute.NetworkConfig{VnetSubnetID: o.nodeSubnetID, PodSubnetID: o.podSubnetID}

	got := buildManagedCluster(o, bootstrap, networkConfig, provisioningTags(o, nil))

	b, err := json.MarshalIndent(got, "", "  ")
	require.NoError(t, err)
	assertGolden(t, string(b)+"\n")
}

func TestBuildManagedClusterNonCiliumSkipsAdvancedNetworking(t *testing.T) {
	o := testValidatedOptions()
	o.networkDataplane = "azure"
	bootstrap := testSystemPool()
	networkConfig := compute.NetworkConfig{VnetSubnetID: o.nodeSubnetID, PodSubnetID: o.podSubnetID}

	got := buildManagedCluster(o, bootstrap, networkConfig, provisioningTags(o, nil))

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

func TestProvisioningTags(t *testing.T) {
	tests := []struct {
		name     string
		existing *armcontainerservice.ManagedCluster
		want     map[string]*string
	}{
		{
			name: "new cluster",
			want: map[string]*string{"clusterType": ptr.To("mgmt"), "persist": ptr.To("true"), provisioningTagKey: ptr.To(provisioningTagValue)},
		},
		{
			name:     "resume preserves baseline",
			existing: &armcontainerservice.ManagedCluster{Tags: map[string]*string{"ARO HCP": ptr.To("unused"), "AROHCP-Capacity-worker": ptr.To(`{"vcpus":8,"memoryGiB":32,"swiftNICs":0}`)}},
			want:     map[string]*string{"clusterType": ptr.To("mgmt"), "persist": ptr.To("true"), provisioningTagKey: ptr.To(provisioningTagValue), "AROHCP-Capacity-worker": ptr.To(`{"vcpus":8,"memoryGiB":32,"swiftNICs":0}`)},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			o := testValidatedOptions()
			got := provisioningTags(o, test.existing)
			require.Equal(t, test.want, got)
			delete(got, "clusterType")
			require.Equal(t, "mgmt", o.clusterTags["clusterType"], "payload tags must not mutate configured tags")
		})
	}
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
	tests := []struct {
		name             string
		liveTags         map[string]*string
		etag             string
		getErr           bool
		updateErr        bool
		wantErr          bool
		wantUpdateCalled bool
		wantTags         map[string]string // asserted only when wantUpdateCalled
		wantIfMatch      string
	}{
		{
			name:             "drops only the provisioning tag and preserves the rest under if-match",
			liveTags:         map[string]*string{"clusterType": ptr.To("mgmt"), "aks-managed-foo": ptr.To("bar"), provisioningTagKey: ptr.To(provisioningTagValue)},
			etag:             "etag-123",
			wantUpdateCalled: true,
			wantTags:         map[string]string{"clusterType": "mgmt", "aks-managed-foo": "bar"},
			wantIfMatch:      "etag-123",
		},
		{
			name:     "no update when provisioning tag absent",
			liveTags: map[string]*string{"clusterType": ptr.To("mgmt")},
		},
		{
			name:    "get error is returned",
			getErr:  true,
			wantErr: true,
		},
		{
			name:      "update error is returned",
			liveTags:  map[string]*string{provisioningTagKey: ptr.To(provisioningTagValue)},
			etag:      "e1",
			updateErr: true,
			wantErr:   true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var gotTags map[string]*string
			var gotIfMatch *string
			updateCalled := false
			client := newManagedClustersTestClient(t, &armcontainerservicefake.ManagedClustersServer{
				Get: func(ctx context.Context, resourceGroupName, resourceName string, options *armcontainerservice.ManagedClustersClientGetOptions) (resp azfake.Responder[armcontainerservice.ManagedClustersClientGetResponse], errResp azfake.ErrorResponder) {
					if test.getErr {
						errResp.SetResponseError(http.StatusInternalServerError, "InternalError")
						return
					}
					resp.SetResponse(http.StatusOK, armcontainerservice.ManagedClustersClientGetResponse{
						ManagedCluster: armcontainerservice.ManagedCluster{ETag: ptr.To(test.etag), Tags: test.liveTags},
					}, nil)
					return
				},
				BeginUpdateTags: func(ctx context.Context, resourceGroupName, resourceName string, parameters armcontainerservice.TagsObject, options *armcontainerservice.ManagedClustersClientBeginUpdateTagsOptions) (resp azfake.PollerResponder[armcontainerservice.ManagedClustersClientUpdateTagsResponse], errResp azfake.ErrorResponder) {
					updateCalled = true
					gotTags = parameters.Tags
					if options != nil {
						gotIfMatch = options.IfMatch
					}
					if test.updateErr {
						errResp.SetResponseError(http.StatusInternalServerError, "InternalError")
						return
					}
					resp.SetTerminalResponse(http.StatusOK, armcontainerservice.ManagedClustersClientUpdateTagsResponse{}, nil)
					return
				},
			})

			err := removeProvisioningTag(context.Background(), client, "rg1", "cluster1")

			if test.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, test.wantUpdateCalled, updateCalled, "UpdateTags call expectation")
			if !test.wantUpdateCalled {
				return
			}
			gotTagValues := make(map[string]string, len(gotTags))
			for k, v := range gotTags {
				gotTagValues[k] = *v
			}
			assert.Equal(t, test.wantTags, gotTagValues, "sent tags")
			require.NotNil(t, gotIfMatch)
			assert.Equal(t, test.wantIfMatch, *gotIfMatch, "if-match")
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
	t.Run("cluster already fully provisioned plans but applies nothing", func(t *testing.T) {
		clustersClient := newManagedClustersTestClient(t, &armcontainerservicefake.ManagedClustersServer{
			Get: func(ctx context.Context, resourceGroupName, resourceName string, options *armcontainerservice.ManagedClustersClientGetOptions) (resp azfake.Responder[armcontainerservice.ManagedClustersClientGetResponse], errResp azfake.ErrorResponder) {
				resp.SetResponse(http.StatusOK, armcontainerservice.ManagedClustersClientGetResponse{
					ManagedCluster: armcontainerservice.ManagedCluster{Name: ptr.To("cluster1"), Tags: map[string]*string{
						"arohcp-capacity-system": ptr.To(`{"vcpus":12,"memoryGiB":48,"swiftNICs":0}`),
						"arohcp-capacity-infra":  ptr.To(`{"vcpus":8,"memoryGiB":32,"swiftNICs":0}`),
						"arohcp-capacity-worker": ptr.To(`{"vcpus":64,"memoryGiB":512,"swiftNICs":28}`),
					}},
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

		err := run(context.Background(), completed, logr.Discard())

		require.Error(t, err)
		assert.Contains(t, err.Error(), "required tier allocation failed")
	})
}

func TestInitializeCapacityTags(t *testing.T) {
	tests := []struct {
		name           string
		existingWorker bool
		allPresent     bool
		malformed      bool
		poolState      string
		unknownSKU     bool
		conflict       bool
		wantWrite      bool
		wantErr        string
	}{
		{name: "missing", poolState: "Succeeded", wantWrite: true},
		{name: "preserve_existing_role", poolState: "Succeeded", existingWorker: true, wantWrite: true},
		{name: "already_initialized", allPresent: true},
		{name: "malformed", malformed: true, wantErr: "invalid capacity tag"},
		{name: "provisioning", poolState: "Updating", wantErr: "must be successfully provisioned"},
		{name: "unknown_sku", poolState: "Succeeded", unknownSKU: true, wantErr: "missing SKU metadata"},
		{name: "concurrent_update", poolState: "Succeeded", conflict: true, wantWrite: true, wantErr: "PreconditionFailed"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tags := map[string]*string{"owner": ptr.To("team")}
			if test.existingWorker || test.allPresent {
				tags["arohcp-capacity-worker"] = ptr.To(`{"vcpus":999,"memoryGiB":9999,"swiftNICs":999}`)
			}
			if test.allPresent {
				tags["arohcp-capacity-system"] = ptr.To(`{"vcpus":12,"memoryGiB":48,"swiftNICs":0}`)
				tags["arohcp-capacity-infra"] = ptr.To(`{"vcpus":0,"memoryGiB":0,"swiftNICs":0}`)
			}
			if test.malformed {
				tags["arohcp-capacity-worker"] = ptr.To(`{"vcpus":1}`)
			}
			writes := 0
			clustersClient := newManagedClustersTestClient(t, &armcontainerservicefake.ManagedClustersServer{
				Get: func(context.Context, string, string, *armcontainerservice.ManagedClustersClientGetOptions) (resp azfake.Responder[armcontainerservice.ManagedClustersClientGetResponse], errResp azfake.ErrorResponder) {
					resp.SetResponse(http.StatusOK, armcontainerservice.ManagedClustersClientGetResponse{ManagedCluster: armcontainerservice.ManagedCluster{Tags: tags, ETag: ptr.To("e1"), Properties: &armcontainerservice.ManagedClusterProperties{ProvisioningState: ptr.To("Succeeded")}}}, nil)
					return
				},
				BeginUpdateTags: func(_ context.Context, _, _ string, parameters armcontainerservice.TagsObject, options *armcontainerservice.ManagedClustersClientBeginUpdateTagsOptions) (resp azfake.PollerResponder[armcontainerservice.ManagedClustersClientUpdateTagsResponse], errResp azfake.ErrorResponder) {
					writes++
					require.Equal(t, "e1", *options.IfMatch)
					if test.conflict {
						errResp.SetResponseError(http.StatusPreconditionFailed, "PreconditionFailed")
						return
					}
					b, err := json.MarshalIndent(parameters.Tags, "", "  ")
					require.NoError(t, err)
					assertGolden(t, string(b)+"\n")
					resp.SetTerminalResponse(http.StatusOK, armcontainerservice.ManagedClustersClientUpdateTagsResponse{}, nil)
					return
				},
			})
			poolsServer := armcontainerservicefake.AgentPoolsServer{
				NewListPager: func(string, string, *armcontainerservice.AgentPoolsClientListOptions) (resp azfake.PagerResponder[armcontainerservice.AgentPoolsClientListResponse]) {
					require.False(t, test.allPresent, "initialized baselines do not require pool observation")
					var pools []*armcontainerservice.AgentPool
					for _, role := range []string{"system", "worker"} {
						maximum := int32(3)
						if role == "worker" {
							maximum = 5
						}
						properties := &armcontainerservice.ManagedClusterAgentPoolProfileProperties{
							VMSize: ptr.To("sku"), Count: ptr.To[int32](1), MaxCount: ptr.To(maximum), EnableAutoScaling: ptr.To(true), ProvisioningState: ptr.To(test.poolState),
							NodeLabels: map[string]*string{compute.RoleLabel: ptr.To(role)},
						}
						if role == "worker" {
							properties.Tags = map[string]*string{agentpoolspec.SwiftMultiTenancyTag: ptr.To(agentpoolspec.SwiftMultiTenancyEnabledValue), agentpoolspec.SwiftSecondaryNICCountTag: ptr.To("2")}
						}
						pools = append(pools, &armcontainerservice.AgentPool{Name: ptr.To(role), Properties: properties})
					}
					resp.AddPage(http.StatusOK, armcontainerservice.AgentPoolsClientListResponse{AgentPoolListResult: armcontainerservice.AgentPoolListResult{Value: pools}}, nil)
					return
				},
			}
			poolsClient, err := armcontainerservice.NewAgentPoolsClient("sub1", &azfake.TokenCredential{}, &azcorearm.ClientOptions{ClientOptions: policy.ClientOptions{Transport: armcontainerservicefake.NewAgentPoolsServerTransport(&poolsServer)}})
			require.NoError(t, err)
			skus := []*armcompute.ResourceSKU{{Name: ptr.To("sku"), ResourceType: ptr.To("virtualMachines"), Capabilities: []*armcompute.ResourceSKUCapabilities{
				{Name: ptr.To("vCPUs"), Value: ptr.To("4")}, {Name: ptr.To("MemoryGB"), Value: ptr.To("16")},
			}}}
			if test.unknownSKU {
				skus = nil
			}
			o := &completedOptions{validatedOptions: testValidatedOptions(), clustersClient: clustersClient, poolsClient: poolsClient, skuCache: newRunTestSKUCache(t, skus)}
			err = initializeCapacityTags(context.Background(), o)
			if len(test.wantErr) > 0 {
				require.ErrorContains(t, err, test.wantErr)
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, test.wantWrite, writes > 0)
		})
	}
}

func TestRunCapacityBaseline(t *testing.T) {
	tests := []struct {
		name       string
		existing   bool
		resume     bool
		baseline   bool
		lowerPlan  bool
		partial    bool
		poolState  string
		wantCreate bool
		wantErr    string
	}{
		{name: "new_cluster", wantCreate: true},
		{name: "adopt_existing_cluster", existing: true},
		{name: "resume_without_baseline", existing: true, resume: true, wantCreate: true},
		{name: "resume_preserves_baseline", existing: true, resume: true, baseline: true, wantCreate: true},
		{name: "resume_accepts_full_lower_plan", existing: true, resume: true, baseline: true, lowerPlan: true, wantCreate: true},
		{name: "resume_accepts_partial_lower_plan", existing: true, resume: true, baseline: true, partial: true, wantCreate: true},
		{name: "resume_waits_for_pool_completion", existing: true, resume: true, poolState: "Updating", wantCreate: true, wantErr: "must be successfully provisioned"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			o := testValidatedOptions()
			o.profile = compute.Profile{BudgetStrategy: compute.UnlimitedBudget, Tiers: []compute.TierConfig{{Name: "sys", Role: compute.PoolRoleSystem, PoolMode: compute.PoolModeRegional, PoolCount: 1, Cores: 4, MaxNodes: 3, OSDiskSizeGB: 32, FamilyPriority: []compute.VMFamily{"family"}, Required: true}}}
			o.zones = []string{"1"}
			live := armcontainerservice.ManagedCluster{ETag: ptr.To("e1"), Tags: map[string]*string{"owner": ptr.To("team")}, Properties: &armcontainerservice.ManagedClusterProperties{ProvisioningState: ptr.To("Succeeded")}}
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
			createCalls := 0
			clustersClient := newManagedClustersTestClient(t, &armcontainerservicefake.ManagedClustersServer{
				Get: func(context.Context, string, string, *armcontainerservice.ManagedClustersClientGetOptions) (resp azfake.Responder[armcontainerservice.ManagedClustersClientGetResponse], errResp azfake.ErrorResponder) {
					if !exists {
						errResp.SetResponseError(http.StatusNotFound, "NotFound")
						return
					}
					resp.SetResponse(http.StatusOK, armcontainerservice.ManagedClustersClientGetResponse{ManagedCluster: live}, nil)
					return
				},
				BeginCreateOrUpdate: func(_ context.Context, _, _ string, parameters armcontainerservice.ManagedCluster, _ *armcontainerservice.ManagedClustersClientBeginCreateOrUpdateOptions) (resp azfake.PollerResponder[armcontainerservice.ManagedClustersClientCreateOrUpdateResponse], errResp azfake.ErrorResponder) {
					createCalls++
					if test.resume {
						require.Equal(t, live.Tags["arohcp-capacity-system"], parameters.Tags["arohcp-capacity-system"], "resume must carry the baseline through the cluster PUT")
					}
					live.Tags = parameters.Tags
					exists = true
					resp.SetTerminalResponse(http.StatusOK, armcontainerservice.ManagedClustersClientCreateOrUpdateResponse{ManagedCluster: live}, nil)
					return
				},
				BeginUpdateTags: func(_ context.Context, _, _ string, parameters armcontainerservice.TagsObject, options *armcontainerservice.ManagedClustersClientBeginUpdateTagsOptions) (resp azfake.PollerResponder[armcontainerservice.ManagedClustersClientUpdateTagsResponse], errResp azfake.ErrorResponder) {
					require.Equal(t, test.wantCreate, createCalls > 0, "capacity tags are written only after provisioning or when adopting a finished cluster")
					require.Equal(t, "e1", *options.IfMatch)
					live.Tags = parameters.Tags
					resp.SetTerminalResponse(http.StatusOK, armcontainerservice.ManagedClustersClientUpdateTagsResponse{ManagedCluster: live}, nil)
					return
				},
			})
			poolServer := armcontainerservicefake.AgentPoolsServer{
				NewListPager: func(string, string, *armcontainerservice.AgentPoolsClientListOptions) (resp azfake.PagerResponder[armcontainerservice.AgentPoolsClientListResponse]) {
					require.Equal(t, test.wantCreate, createCalls > 0, "capacity is observed only after provisioning or when adopting a finished cluster")
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
			err = run(context.Background(), &completedOptions{validatedOptions: o, clustersClient: clustersClient, poolsClient: poolsClient, skuCache: skuCache}, logr.Discard())
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
			require.Equal(t, test.wantCreate, createCalls > 0)
		})
	}
}

func TestFinalizeClusterTags(t *testing.T) {
	tests := []struct {
		name      string
		malformed bool
		wantCalls []string
		wantErr   string
	}{
		{name: "baseline ready before marker removal", wantCalls: []string{"get", "get", "update"}},
		{name: "invalid baseline preserves marker", malformed: true, wantCalls: []string{"get"}, wantErr: "initializing capacity baseline"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tags := map[string]*string{
				provisioningTagKey:       ptr.To(provisioningTagValue),
				"arohcp-capacity-system": ptr.To(`{"vcpus":12,"memoryGiB":48,"swiftNICs":0}`),
				"arohcp-capacity-infra":  ptr.To(`{"vcpus":0,"memoryGiB":0,"swiftNICs":0}`),
				"arohcp-capacity-worker": ptr.To(`{"vcpus":0,"memoryGiB":0,"swiftNICs":0}`),
				"owner":                  ptr.To("team"),
			}
			if test.malformed {
				tags["arohcp-capacity-system"] = ptr.To("invalid")
			}
			var calls []string
			client := newManagedClustersTestClient(t, &armcontainerservicefake.ManagedClustersServer{
				Get: func(context.Context, string, string, *armcontainerservice.ManagedClustersClientGetOptions) (resp azfake.Responder[armcontainerservice.ManagedClustersClientGetResponse], errResp azfake.ErrorResponder) {
					calls = append(calls, "get")
					resp.SetResponse(http.StatusOK, armcontainerservice.ManagedClustersClientGetResponse{ManagedCluster: armcontainerservice.ManagedCluster{Tags: tags, ETag: ptr.To("e1")}}, nil)
					return
				},
				BeginUpdateTags: func(_ context.Context, _, _ string, parameters armcontainerservice.TagsObject, options *armcontainerservice.ManagedClustersClientBeginUpdateTagsOptions) (resp azfake.PollerResponder[armcontainerservice.ManagedClustersClientUpdateTagsResponse], errResp azfake.ErrorResponder) {
					calls = append(calls, "update")
					require.Equal(t, "e1", *options.IfMatch)
					require.NotContains(t, parameters.Tags, provisioningTagKey)
					require.Equal(t, tags["arohcp-capacity-system"], parameters.Tags["arohcp-capacity-system"])
					require.Equal(t, "team", *parameters.Tags["owner"])
					tags = parameters.Tags
					resp.SetTerminalResponse(http.StatusOK, armcontainerservice.ManagedClustersClientUpdateTagsResponse{}, nil)
					return
				},
			})
			err := finalizeClusterTags(context.Background(), &completedOptions{validatedOptions: testValidatedOptions(), clustersClient: client})
			if len(test.wantErr) > 0 {
				require.ErrorContains(t, err, test.wantErr)
				require.Contains(t, tags, provisioningTagKey)
			} else {
				require.NoError(t, err)
				require.NotContains(t, tags, provisioningTagKey)
			}
			require.Equal(t, test.wantCalls, calls)
		})
	}
}
