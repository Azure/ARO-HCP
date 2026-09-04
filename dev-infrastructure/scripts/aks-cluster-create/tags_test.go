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

	"github.com/stretchr/testify/require"

	"k8s.io/utils/ptr"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	azfake "github.com/Azure/azure-sdk-for-go/sdk/azcore/fake"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v6"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v8"
	armcontainerservicefake "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v8/fake"

	"github.com/Azure/ARO-HCP/fleet/pkg/azure/agentpools"
	"github.com/Azure/ARO-HCP/fleet/pkg/azure/agentpoolspec"
	"github.com/Azure/ARO-HCP/fleet/pkg/compute"
)

func TestProvisioningMarkerPresence(t *testing.T) {
	for _, test := range []struct {
		name string
		tags map[string]*string
		want bool
	}{
		{name: "absent"},
		{name: "unrelated", tags: map[string]*string{"other": ptr.To("true")}},
		{name: "true", tags: map[string]*string{provisioningTagKey: ptr.To("true")}, want: true},
		{name: "false", tags: map[string]*string{provisioningTagKey: ptr.To("false")}, want: true},
		{name: "empty", tags: map[string]*string{provisioningTagKey: ptr.To("")}, want: true},
		{name: "nil", tags: map[string]*string{provisioningTagKey: nil}, want: true},
		{name: "mixed case", tags: map[string]*string{"ARO-HCP-Provisioning": nil}, want: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.want, hasProvisioningTag(test.tags))
		})
	}
}

func TestInitialClusterTags(t *testing.T) {
	o := testValidatedOptions()
	o.clusterTags = map[string]string{
		"owningTeam": "dedicated-team", "custom": "value",
		"ARO-HCP-PROVISIONING": "false", "ARO HCP": "ordinary",
		"AROHCp-Capacity-worker": "untrusted", "arohcp-capacity-future": "untrusted",
	}
	require.Equal(t, map[string]*string{
		"owningTeam": ptr.To("dedicated-team"), "custom": ptr.To("value"),
		"ARO HCP": ptr.To("ordinary"), provisioningTagKey: ptr.To(provisioningTagValue),
	}, initialClusterTags(o))
}

type tagReconcileFixture struct {
	o            *completedOptions
	cluster      *armcontainerservice.ManagedCluster
	pages        [][]*armcontainerservice.AgentPool
	writes       int
	poolLists    int
	written      map[string]*string
	updateStatus int
}

func newTagReconcileFixture(t *testing.T) *tagReconcileFixture {
	t.Helper()
	f := &tagReconcileFixture{
		cluster: &armcontainerservice.ManagedCluster{
			ETag:       ptr.To("observed-etag"),
			Tags:       map[string]*string{"ARO-HCP-Provisioning": ptr.To("false"), "external": ptr.To("keep")},
			Properties: &armcontainerservice.ManagedClusterProperties{ProvisioningState: ptr.To("Succeeded")},
		},
	}
	for _, role := range []string{"system", "worker"} {
		maximum := int32(3)
		if role == "worker" {
			maximum = 5
		}
		properties := &armcontainerservice.ManagedClusterAgentPoolProfileProperties{
			VMSize: ptr.To("sku"), Count: ptr.To[int32](1), MaxCount: ptr.To(maximum),
			EnableAutoScaling: ptr.To(true), ProvisioningState: ptr.To("Succeeded"),
			NodeLabels: map[string]*string{compute.RoleLabel: ptr.To(role)},
		}
		if role == "worker" {
			properties.Tags = map[string]*string{
				agentpoolspec.SwiftMultiTenancyTag:      ptr.To(agentpoolspec.SwiftMultiTenancyEnabledValue),
				agentpoolspec.SwiftSecondaryNICCountTag: ptr.To("2"),
			}
		}
		f.pages = append(f.pages, []*armcontainerservice.AgentPool{{Name: ptr.To(role), Properties: properties}})
	}
	clustersClient := newManagedClustersTestClient(t, &armcontainerservicefake.ManagedClustersServer{
		Get: func(context.Context, string, string, *armcontainerservice.ManagedClustersClientGetOptions) (resp azfake.Responder[armcontainerservice.ManagedClustersClientGetResponse], errResp azfake.ErrorResponder) {
			t.Fatal("tag reconciliation must use its supplied cluster snapshot")
			return
		},
		BeginUpdateTags: func(_ context.Context, _, _ string, parameters armcontainerservice.TagsObject, options *armcontainerservice.ManagedClustersClientBeginUpdateTagsOptions) (resp azfake.PollerResponder[armcontainerservice.ManagedClustersClientUpdateTagsResponse], errResp azfake.ErrorResponder) {
			f.writes++
			require.NotNil(t, options)
			require.Equal(t, "observed-etag", ptr.Deref(options.IfMatch, ""))
			f.written = parameters.Tags
			if f.updateStatus != 0 {
				errResp.SetResponseError(f.updateStatus, "ConcurrentUpdate")
				return
			}
			resp.SetTerminalResponse(http.StatusOK, armcontainerservice.ManagedClustersClientUpdateTagsResponse{}, nil)
			return
		},
	})
	poolsServer := &armcontainerservicefake.AgentPoolsServer{
		NewListPager: func(string, string, *armcontainerservice.AgentPoolsClientListOptions) (resp azfake.PagerResponder[armcontainerservice.AgentPoolsClientListResponse]) {
			f.poolLists++
			for _, pools := range f.pages {
				resp.AddPage(http.StatusOK, armcontainerservice.AgentPoolsClientListResponse{AgentPoolListResult: armcontainerservice.AgentPoolListResult{Value: pools}}, nil)
			}
			return
		},
	}
	poolsClient, err := armcontainerservice.NewAgentPoolsClient("sub1", &azfake.TokenCredential{}, &azcorearm.ClientOptions{ClientOptions: policy.ClientOptions{Transport: armcontainerservicefake.NewAgentPoolsServerTransport(poolsServer)}})
	require.NoError(t, err)
	f.o = &completedOptions{
		validatedOptions: testValidatedOptions(), clustersClient: clustersClient, poolsClient: poolsClient,
		skuCache: newRunTestSKUCache(t, []*armcompute.ResourceSKU{{
			Name: ptr.To("sku"), ResourceType: ptr.To("virtualMachines"),
			Capabilities: []*armcompute.ResourceSKUCapabilities{
				{Name: ptr.To("vCPUs"), Value: ptr.To("4")}, {Name: ptr.To("MemoryGB"), Value: ptr.To("16")},
			},
		}}),
	}
	f.o.clusterTags = nil
	return f
}

func completeTagBaseline() map[string]*string {
	return map[string]*string{
		"arohcp-capacity-system": ptr.To(`{"vcpus":12,"memoryGiB":48,"swiftNICs":0}`),
		"arohcp-capacity-infra":  ptr.To(`{"vcpus":0,"memoryGiB":0,"swiftNICs":0}`),
		"arohcp-capacity-worker": ptr.To(`{"vcpus":999,"memoryGiB":9999,"swiftNICs":999}`),
	}
}

func TestReconcileClusterTagsAtomicFinalize(t *testing.T) {
	f := newTagReconcileFixture(t)
	f.o.clusterTags = map[string]string{"owner": "new-team", "arohcp-capacity-worker": "ignored", "ARO-HCP-PROVISIONING": "ignored"}
	require.NoError(t, f.o.reconcileClusterTags(context.Background(), f.cluster))
	require.Equal(t, 1, f.writes)
	require.False(t, hasProvisioningTag(f.written))
	require.Equal(t, "keep", *f.written["external"])
	require.Equal(t, "new-team", *f.written["owner"])
	capacities, err := agentpools.ReadCapacityTags(f.written)
	require.NoError(t, err)
	require.Equal(t, compute.CapacityByRole{
		compute.PoolRoleSystem: {VCPUs: 12, MemoryGiB: 48},
		compute.PoolRoleWorker: {VCPUs: 20, MemoryGiB: 80, SwiftNICs: 10},
		compute.PoolRoleInfra:  {},
	}, capacities)
	require.True(t, hasProvisioningTag(f.cluster.Tags), "pre-update snapshot remains intact")
}

func TestReconcileClusterTagsAdoptsOnlyMissingRoles(t *testing.T) {
	f := newTagReconcileFixture(t)
	delete(f.cluster.Tags, "ARO-HCP-Provisioning")
	const key = "AROHCp-Capacity-WORKER"
	const value = `{ "swiftNICs":999, "memoryGiB":9999, "vcpus":999 }`
	f.cluster.Tags[key] = ptr.To(value)
	require.NoError(t, f.o.reconcileClusterTags(context.Background(), f.cluster))
	require.Equal(t, 1, f.writes)
	require.Equal(t, value, *f.written[key])
	require.NotContains(t, f.written, "arohcp-capacity-worker")
	capacities, err := agentpools.ReadCapacityTags(f.written)
	require.NoError(t, err)
	require.Equal(t, compute.RoleCapacity{VCPUs: 12, MemoryGiB: 48}, capacities[compute.PoolRoleSystem])
	require.Equal(t, compute.RoleCapacity{}, capacities[compute.PoolRoleInfra])
	require.False(t, hasProvisioningTag(f.written))
}

func TestReconcileClusterTagsCompletedBaseline(t *testing.T) {
	for _, changeConfiguredTags := range []bool{false, true} {
		t.Run(map[bool]string{false: "no-op", true: "configured tag overlay"}[changeConfiguredTags], func(t *testing.T) {
			f := newTagReconcileFixture(t)
			f.cluster.Tags = completeTagBaseline()
			f.cluster.Tags["Owner"] = ptr.To("old-team")
			f.cluster.Tags["external"] = ptr.To("keep")
			f.o.poolsClient = nil
			f.o.skuCache = nil
			f.o.clusterTags = map[string]string{"owner": "old-team", "AROHCp-Capacity-worker": "ignored", "ARO-HCP-PROVISIONING": "ignored"}
			if changeConfiguredTags {
				f.o.clusterTags["owner"] = "new-team"
				f.o.clusterTags["owningTeam"] = "dedicated-team"
			}
			require.NoError(t, f.o.reconcileClusterTags(context.Background(), f.cluster))
			require.Zero(t, f.poolLists)
			if !changeConfiguredTags {
				require.Zero(t, f.writes)
				return
			}
			require.Equal(t, 1, f.writes)
			require.Equal(t, "new-team", *f.written["Owner"])
			require.NotContains(t, f.written, "owner")
			require.Equal(t, "dedicated-team", *f.written["owningTeam"])
			require.Equal(t, "keep", *f.written["external"])
			for key, value := range completeTagBaseline() {
				require.Equal(t, value, f.written[key])
			}
			require.False(t, hasProvisioningTag(f.written))
		})
	}
}

func TestReconcileClusterTagsMarkedCompleteBaseline(t *testing.T) {
	f := newTagReconcileFixture(t)
	f.cluster.Tags = completeTagBaseline()
	f.cluster.Tags["ARO-HCP-PROVISIONING"] = nil
	f.o.skuCache = nil
	require.NoError(t, f.o.reconcileClusterTags(context.Background(), f.cluster))
	require.Equal(t, 1, f.poolLists, "marker removal still requires observing pool success")
	require.Equal(t, 1, f.writes)
	require.Equal(t, completeTagBaseline(), f.written, "existing high baselines are not recalculated")
}

func TestReconcileClusterTagsRejectsUnsafeFinalization(t *testing.T) {
	for _, test := range []struct {
		name    string
		alter   func(*tagReconcileFixture)
		wantErr string
	}{
		{name: "malformed baseline", alter: func(f *tagReconcileFixture) { f.cluster.Tags["arohcp-capacity-worker"] = ptr.To(`{"vcpus":1}`) }, wantErr: "invalid capacity tag"},
		{name: "cluster busy", alter: func(f *tagReconcileFixture) { f.cluster.Properties.ProvisioningState = ptr.To("Updating") }, wantErr: "cluster must be successfully provisioned"},
		{name: "missing cluster properties", alter: func(f *tagReconcileFixture) { f.cluster.Properties = nil }, wantErr: "cluster must be successfully provisioned"},
		{name: "later pool busy", alter: func(f *tagReconcileFixture) { f.pages[1][0].Properties.ProvisioningState = ptr.To("Updating") }, wantErr: "pool \"worker\" must be successfully provisioned"},
		{name: "unmanaged pool busy", alter: func(f *tagReconcileFixture) {
			f.pages[1][0].Properties.NodeLabels = nil
			f.pages[1][0].Properties.ProvisioningState = ptr.To("Updating")
		}, wantErr: "must be successfully provisioned"},
		{name: "missing pool properties", alter: func(f *tagReconcileFixture) { f.pages[1][0].Properties = nil }, wantErr: "must be successfully provisioned"},
		{name: "nil pool", alter: func(f *tagReconcileFixture) { f.pages[1][0] = nil }, wantErr: "missing pool observation"},
		{name: "unknown SKU", alter: func(f *tagReconcileFixture) { f.o.skuCache = newRunTestSKUCache(t, nil) }, wantErr: "missing SKU metadata"},
		{name: "missing pool ceiling", alter: func(f *tagReconcileFixture) { f.pages[1][0].Properties.MaxCount = nil }, wantErr: "incomplete ARM capacity data"},
		{name: "missing system pool", alter: func(f *tagReconcileFixture) { f.pages = f.pages[1:] }, wantErr: "without a managed system pool"},
		{name: "invalid Swift capacity", alter: func(f *tagReconcileFixture) {
			f.pages[1][0].Properties.Tags[agentpoolspec.SwiftSecondaryNICCountTag] = ptr.To("invalid")
		}, wantErr: "invalid Swift NIC count"},
		{name: "nil ETag", alter: func(f *tagReconcileFixture) { f.cluster.ETag = nil }, wantErr: "no ETag"},
		{name: "empty ETag", alter: func(f *tagReconcileFixture) { f.cluster.ETag = ptr.To("") }, wantErr: "no ETag"},
		{name: "complete baseline busy pool", alter: func(f *tagReconcileFixture) {
			f.cluster.Tags = completeTagBaseline()
			f.cluster.Tags[provisioningTagKey] = ptr.To("")
			f.pages[1][0].Properties.ProvisioningState = ptr.To("Updating")
		}, wantErr: "must be successfully provisioned"},
		{name: "complete baseline busy cluster", alter: func(f *tagReconcileFixture) {
			f.cluster.Tags = completeTagBaseline()
			f.cluster.Tags[provisioningTagKey] = nil
			f.cluster.Properties.ProvisioningState = ptr.To("Updating")
		}, wantErr: "cluster must be successfully provisioned"},
		{name: "configured update without ETag", alter: func(f *tagReconcileFixture) {
			f.cluster.Tags = completeTagBaseline()
			f.o.clusterTags = map[string]string{"owner": "new-team"}
			f.cluster.ETag = nil
		}, wantErr: "no ETag"},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newTagReconcileFixture(t)
			test.alter(f)
			marked := hasProvisioningTag(f.cluster.Tags)
			require.ErrorContains(t, f.o.reconcileClusterTags(context.Background(), f.cluster), test.wantErr)
			require.Zero(t, f.writes, "unsafe observations must not remove markers or partially update tags")
			require.Equal(t, marked, hasProvisioningTag(f.cluster.Tags))
		})
	}
}

func TestReconcileClusterTagsPropagatesConflicts(t *testing.T) {
	for _, status := range []int{http.StatusConflict, http.StatusPreconditionFailed} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			f := newTagReconcileFixture(t)
			f.updateStatus = status
			err := f.o.reconcileClusterTags(context.Background(), f.cluster)
			var responseErr *azcore.ResponseError
			require.ErrorAs(t, err, &responseErr)
			require.Equal(t, status, responseErr.StatusCode)
			require.Equal(t, 1, f.writes, "the outer lifecycle owns retries from a fresh read")
			require.True(t, hasProvisioningTag(f.cluster.Tags))
			require.NotContains(t, f.cluster.Tags, "arohcp-capacity-system")
		})
	}
}
