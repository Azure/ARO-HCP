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
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	azfake "github.com/Azure/azure-sdk-for-go/sdk/azcore/fake"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v6"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v8"
	armcontainerservicefake "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v8/fake"

	"github.com/Azure/ARO-HCP/fleet/pkg/azure/agentpoolspec"
	"github.com/Azure/ARO-HCP/fleet/pkg/compute"
)

// lifecycleServer models a single allocated system pool. Requests cross the SDK
// fake transport, so returned observations are independent of later server writes.
// Hooks inject a concurrent actor immediately before an optimistic write commits.
type lifecycleServer struct {
	t       *testing.T
	ctx     context.Context
	options *completedOptions
	cluster *armcontainerservice.ManagedCluster
	pool    armcontainerservice.AgentPool

	clusterGets   int
	clusterWrites int
	tagWrites     int
	poolGets      int
	poolLists     int
	poolWrites    int
	revision      int

	beforeClusterWrite func(armcontainerservice.ManagedCluster) int
	beforeTagWrite     func(armcontainerservice.TagsObject) int
	beforePoolWrite    func(armcontainerservice.AgentPool) int
	beforePoolRead     func()
}

func newLifecycleServer(t *testing.T) *lifecycleServer {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	o := testValidatedOptions()
	o.zones = []string{"1"}
	o.profile = compute.Profile{
		BudgetStrategy: compute.UnlimitedBudget,
		Tiers: []compute.TierConfig{{
			Name: "sys", Role: compute.PoolRoleSystem, PoolMode: compute.PoolModeRegional,
			PoolCount: 1, Cores: 4, MaxNodes: 3, OSDiskSizeGB: 32,
			FamilyPriority: []compute.VMFamily{"family"}, Required: true,
		}},
	}
	s := &lifecycleServer{t: t, ctx: ctx, revision: 1}
	s.options = &completedOptions{
		validatedOptions: o,
		skuCache: newRunTestSKUCache(t, []*armcompute.ResourceSKU{{
			Name: ptr.To("sku"), Family: ptr.To("family"), ResourceType: ptr.To("virtualMachines"),
			LocationInfo: []*armcompute.ResourceSKULocationInfo{{Zones: []*string{ptr.To("1")}}},
			Capabilities: []*armcompute.ResourceSKUCapabilities{
				{Name: ptr.To("vCPUs"), Value: ptr.To("4")},
				{Name: ptr.To("MemoryGB"), Value: ptr.To("16")},
				{Name: ptr.To("EphemeralOSDiskSupported"), Value: ptr.To("True")},
				{Name: ptr.To("CachedDiskBytes"), Value: ptr.To("107374182400")},
			},
		}}),
	}
	pools, _, err := s.options.computeDesiredPools(ctx, logr.Discard())
	require.NoError(t, err)
	require.Len(t, pools, 1)
	desired := pools[0]
	props := agentpoolspec.Build(desired, compute.NetworkConfig{VnetSubnetID: o.nodeSubnetID, PodSubnetID: o.podSubnetID})
	props.Count = ptr.To[int32](7)
	props.MaxCount = ptr.To[int32](11)
	props.ProvisioningState = ptr.To("Succeeded")
	props.ETag = ptr.To("pool-1")
	s.pool = armcontainerservice.AgentPool{Name: ptr.To(desired.Name), Properties: props}
	cluster, _, err := buildClusterSpec(o, nil, &desired)
	require.NoError(t, err)
	cluster.Name = ptr.To(o.clusterName)
	cluster.ETag = ptr.To("cluster-1")
	cluster.Tags = map[string]*string{
		"clusterType": ptr.To("mgmt"), "persist": ptr.To("true"), "owner": ptr.To("fleet"),
		"arohcp-capacity-system": ptr.To(`{"vcpus":44,"memoryGiB":176,"swiftNICs":0}`),
		"arohcp-capacity-infra":  ptr.To(`{"vcpus":0,"memoryGiB":0,"swiftNICs":0}`),
		"arohcp-capacity-worker": ptr.To(`{"vcpus":0,"memoryGiB":0,"swiftNICs":0}`),
	}
	cluster.Properties.ProvisioningState = ptr.To("Succeeded")
	cluster.Properties.KubernetesVersion = ptr.To("1.35.2")
	cluster.Properties.AgentPoolProfiles = []*armcontainerservice.ManagedClusterAgentPoolProfile{toClusterAgentPoolProfile(desired.Name, props)}
	s.cluster = &cluster
	s.options.clustersClient = newManagedClustersTestClient(t, &armcontainerservicefake.ManagedClustersServer{
		Get:                 s.getCluster,
		BeginCreateOrUpdate: s.writeCluster,
		BeginUpdateTags:     s.writeTags,
	})
	poolServer := &armcontainerservicefake.AgentPoolsServer{
		Get:                 s.getPool,
		NewListPager:        s.listPools,
		BeginCreateOrUpdate: s.writePool,
	}
	s.options.poolsClient, err = armcontainerservice.NewAgentPoolsClient("sub1", &azfake.TokenCredential{}, &azcorearm.ClientOptions{
		ClientOptions: policy.ClientOptions{Transport: armcontainerservicefake.NewAgentPoolsServerTransport(poolServer)},
	})
	require.NoError(t, err)
	return s
}

func (s *lifecycleServer) getCluster(context.Context, string, string, *armcontainerservice.ManagedClustersClientGetOptions) (resp azfake.Responder[armcontainerservice.ManagedClustersClientGetResponse], errResp azfake.ErrorResponder) {
	s.clusterGets++
	if s.cluster == nil {
		errResp.SetResponseError(http.StatusNotFound, "NotFound")
		return
	}
	resp.SetResponse(http.StatusOK, armcontainerservice.ManagedClustersClientGetResponse{ManagedCluster: *s.cluster}, nil)
	return
}

func (s *lifecycleServer) writeCluster(_ context.Context, _, _ string, parameters armcontainerservice.ManagedCluster, options *armcontainerservice.ManagedClustersClientBeginCreateOrUpdateOptions) (resp azfake.PollerResponder[armcontainerservice.ManagedClustersClientCreateOrUpdateResponse], errResp azfake.ErrorResponder) {
	s.clusterWrites++
	require.NotNil(s.t, options)
	if s.cluster == nil {
		require.Equal(s.t, ptr.To("*"), options.IfNoneMatch, "creation must not replace a concurrently created cluster")
		require.Nil(s.t, options.IfMatch)
	} else {
		require.Equal(s.t, s.cluster.ETag, options.IfMatch, "configuration updates must use the latest cluster observation")
		require.Nil(s.t, options.IfNoneMatch)
		require.Nil(s.t, parameters.Properties.AgentPoolProfiles, "resuming must not rewrite the bootstrap pool inline")
		require.Nil(s.t, parameters.Properties.KubernetesVersion, "configuration reconciliation does not own Kubernetes versions")
		require.Equal(s.t, s.cluster.Tags, parameters.Tags, "configuration PUT must preserve live tags until finalization")
	}
	if s.beforeClusterWrite != nil {
		if status := s.beforeClusterWrite(parameters); status != 0 {
			errResp.SetResponseError(status, "ConcurrentWrite")
			return
		}
	}
	if s.cluster != nil {
		// ARM retains properties omitted by the configuration-only PUT.
		parameters.Properties.AgentPoolProfiles = s.cluster.Properties.AgentPoolProfiles
		parameters.Properties.KubernetesVersion = s.cluster.Properties.KubernetesVersion
	}
	s.cluster = &parameters
	s.advanceCluster()
	resp.SetTerminalResponse(http.StatusOK, armcontainerservice.ManagedClustersClientCreateOrUpdateResponse{ManagedCluster: *s.cluster}, nil)
	return
}

func (s *lifecycleServer) writeTags(_ context.Context, _, _ string, parameters armcontainerservice.TagsObject, options *armcontainerservice.ManagedClustersClientBeginUpdateTagsOptions) (resp azfake.PollerResponder[armcontainerservice.ManagedClustersClientUpdateTagsResponse], errResp azfake.ErrorResponder) {
	s.tagWrites++
	require.NotNil(s.t, options)
	require.Equal(s.t, s.cluster.ETag, options.IfMatch, "finalization must use the cluster ETag after configuration and pool writes")
	if s.beforeTagWrite != nil {
		if status := s.beforeTagWrite(parameters); status != 0 {
			errResp.SetResponseError(status, "ConcurrentWrite")
			return
		}
	}
	s.cluster.Tags = parameters.Tags
	s.advanceCluster()
	resp.SetTerminalResponse(http.StatusOK, armcontainerservice.ManagedClustersClientUpdateTagsResponse{ManagedCluster: *s.cluster}, nil)
	return
}

func (s *lifecycleServer) getPool(_ context.Context, _, _, name string, _ *armcontainerservice.AgentPoolsClientGetOptions) (resp azfake.Responder[armcontainerservice.AgentPoolsClientGetResponse], errResp azfake.ErrorResponder) {
	s.poolGets++
	if s.beforePoolRead != nil {
		s.beforePoolRead()
	}
	require.Equal(s.t, *s.pool.Name, name)
	resp.SetResponse(http.StatusOK, armcontainerservice.AgentPoolsClientGetResponse{AgentPool: s.pool}, nil)
	return
}

func (s *lifecycleServer) listPools(string, string, *armcontainerservice.AgentPoolsClientListOptions) (resp azfake.PagerResponder[armcontainerservice.AgentPoolsClientListResponse]) {
	s.poolLists++
	if s.beforePoolRead != nil {
		s.beforePoolRead()
	}
	resp.AddPage(http.StatusOK, armcontainerservice.AgentPoolsClientListResponse{AgentPoolListResult: armcontainerservice.AgentPoolListResult{Value: []*armcontainerservice.AgentPool{&s.pool}}}, nil)
	return
}

func (s *lifecycleServer) writePool(_ context.Context, _, _, name string, parameters armcontainerservice.AgentPool, options *armcontainerservice.AgentPoolsClientBeginCreateOrUpdateOptions) (resp azfake.PollerResponder[armcontainerservice.AgentPoolsClientCreateOrUpdateResponse], errResp azfake.ErrorResponder) {
	s.poolWrites++
	require.NotNil(s.t, options)
	require.Equal(s.t, *s.pool.Name, name)
	require.Equal(s.t, s.pool.Properties.ETag, options.IfMatch, "failed-pool recovery must use a fresh pool ETag")
	require.Nil(s.t, options.IfNoneMatch)
	if s.beforePoolWrite != nil {
		if status := s.beforePoolWrite(parameters); status != 0 {
			errResp.SetResponseError(status, "ConcurrentWrite")
			return
		}
	}
	parameters.Name = ptr.To(name)
	parameters.Properties.ETag = ptr.To(fmt.Sprintf("pool-written-%d", s.poolWrites))
	parameters.Properties.ProvisioningState = ptr.To("Succeeded")
	s.pool = parameters
	s.cluster.Properties.AgentPoolProfiles = []*armcontainerservice.ManagedClusterAgentPoolProfile{toClusterAgentPoolProfile(name, parameters.Properties)}
	s.advanceCluster()
	resp.SetTerminalResponse(http.StatusOK, armcontainerservice.AgentPoolsClientCreateOrUpdateResponse{AgentPool: s.pool}, nil)
	return
}

func (s *lifecycleServer) advanceCluster() {
	s.revision++
	s.cluster.ETag = ptr.To(fmt.Sprintf("cluster-%d", s.revision))
	s.cluster.Properties.ProvisioningState = ptr.To("Succeeded")
}

func TestRunResumedClusterReconcilesConfigurationWithoutInlinePoolRewrite(t *testing.T) {
	s := newLifecycleServer(t)
	s.cluster.Tags[provisioningTagKey] = ptr.To(provisioningTagValue)
	s.cluster.Tags["clusterType"] = ptr.To("old")
	delete(s.cluster.Tags, "arohcp-capacity-system")
	s.cluster.Properties.SecurityProfile.AzureKeyVaultKms.KeyID = ptr.To("https://kv1.vault.azure.net/keys/aks-etcd-encryption/old")
	wantPools := s.cluster.Properties.AgentPoolProfiles
	wantVersion := *s.cluster.Properties.KubernetesVersion
	s.beforePoolRead = func() {
		require.Equal(t, s.options.etcdKMSKeyURI, *s.cluster.Properties.SecurityProfile.AzureKeyVaultKms.KeyID, "configuration must converge before provisioning resumes")
	}
	s.beforeTagWrite = func(parameters armcontainerservice.TagsObject) int {
		require.NotContains(t, parameters.Tags, provisioningTagKey)
		require.Equal(t, ptr.To("mgmt"), parameters.Tags["clusterType"])
		require.Contains(t, parameters.Tags, "arohcp-capacity-system")
		require.JSONEq(t, `{"vcpus":44,"memoryGiB":176,"swiftNICs":0}`, *parameters.Tags["arohcp-capacity-system"], "baseline must use observed pool ceiling, not the smaller desired plan")
		return 0
	}

	require.NoError(t, s.options.run(s.ctx, logr.Discard()))
	assert.Equal(t, s.options.etcdKMSKeyURI, *s.cluster.Properties.SecurityProfile.AzureKeyVaultKms.KeyID)
	assert.Equal(t, wantPools, s.cluster.Properties.AgentPoolProfiles)
	assert.Equal(t, wantVersion, *s.cluster.Properties.KubernetesVersion)
	assert.Equal(t, 1, s.clusterWrites)
	assert.Equal(t, 1, s.clusterGets, "reuse the configuration update's snapshot and ETag without another GET")
	assert.Equal(t, 1, s.tagWrites, "configured tags, missing baseline, and marker removal commit atomically")
	assert.Zero(t, s.poolWrites, "a healthy bootstrap pool must remain untouched")
	assert.NotContains(t, s.cluster.Tags, provisioningTagKey)
	assert.Equal(t, ptr.To("fleet"), s.cluster.Tags["owner"])
}

func TestRunExistingClusterReconcilesConfigurationBeforeAllocationFailure(t *testing.T) {
	s := newLifecycleServer(t)
	s.cluster.Tags[provisioningTagKey] = ptr.To(provisioningTagValue)
	s.cluster.Properties.SecurityProfile.AzureKeyVaultKms.KeyID = ptr.To("https://kv1.vault.azure.net/keys/aks-etcd-encryption/old")
	s.options.profile.Tiers[0].FamilyPriority = []compute.VMFamily{"unavailable"}
	wantPools := s.cluster.Properties.AgentPoolProfiles
	wantTags := s.cluster.Tags

	require.ErrorContains(t, s.options.run(s.ctx, logr.Discard()), "required tier allocation failed")
	assert.Equal(t, s.options.etcdKMSKeyURI, *s.cluster.Properties.SecurityProfile.AzureKeyVaultKms.KeyID)
	assert.Equal(t, wantPools, s.cluster.Properties.AgentPoolProfiles)
	assert.Equal(t, wantTags, s.cluster.Tags, "allocation failure must not finalize the baseline or provisioning marker")
	assert.Zero(t, s.poolWrites)
}

func TestRunTagConflictRechecksOwnershipAndPreservesConcurrentBaselines(t *testing.T) {
	s := newLifecycleServer(t)
	s.cluster.Tags[provisioningTagKey] = ptr.To(provisioningTagValue)
	s.cluster.Tags["clusterType"] = ptr.To("old")
	delete(s.cluster.Tags, "arohcp-capacity-system")
	concurrentBaselines := map[string]string{
		"arohcp-capacity-system": `{ "vcpus": 400, "memoryGiB": 1600, "swiftNICs": 0 }`,
		"arohcp-capacity-infra":  `{ "vcpus": 800, "memoryGiB": 3200, "swiftNICs": 0 }`,
		"arohcp-capacity-worker": `{ "vcpus": 1600, "memoryGiB": 12800, "swiftNICs": 400 }`,
	}
	actorFinished := false
	getsAtConflict := 0
	s.beforePoolRead = func() {
		require.False(t, actorFinished, "a fresh read of the cleared marker must stop all pool management and baseline discovery")
	}
	s.beforeTagWrite = func(parameters armcontainerservice.TagsObject) int {
		if !actorFinished {
			require.Positive(t, s.poolLists, "conflict occurs after observing capacity")
			getsAtConflict = s.clusterGets
			delete(s.cluster.Tags, provisioningTagKey)
			for key, value := range concurrentBaselines {
				s.cluster.Tags[key] = ptr.To(value)
			}
			s.cluster.Tags["actor"] = ptr.To("finished")
			s.advanceCluster()
			actorFinished = true
			return http.StatusPreconditionFailed
		}
		require.Greater(t, s.clusterGets, getsAtConflict, "a tag conflict must restart from a fresh cluster GET")
		for key, value := range concurrentBaselines {
			require.Equal(t, ptr.To(value), parameters.Tags[key], "retry must not replay its stale baseline")
		}
		return 0
	}

	require.NoError(t, s.options.run(s.ctx, logr.Discard()))
	require.True(t, actorFinished)
	assert.Greater(t, s.clusterGets, getsAtConflict)
	assert.Equal(t, 2, s.tagWrites)
	assert.Zero(t, s.clusterWrites)
	assert.Zero(t, s.poolWrites)
	assert.NotContains(t, s.cluster.Tags, provisioningTagKey)
	assert.Equal(t, ptr.To("mgmt"), s.cluster.Tags["clusterType"])
	assert.Equal(t, ptr.To("finished"), s.cluster.Tags["actor"])
	for key, value := range concurrentBaselines {
		assert.Equal(t, ptr.To(value), s.cluster.Tags[key], "preserve the concurrent baseline byte-for-byte")
	}
}

func TestRunClusterCreateConflictAdoptsExistingCluster(t *testing.T) {
	for _, status := range []int{http.StatusPreconditionFailed, http.StatusConflict} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			s := newLifecycleServer(t)
			concurrent := s.cluster
			concurrent.Properties.SecurityProfile.AzureKeyVaultKms.KeyID = ptr.To("https://kv1.vault.azure.net/keys/aks-etcd-encryption/old")
			wantPools := concurrent.Properties.AgentPoolProfiles
			wantTags := concurrent.Tags
			s.cluster = nil
			s.beforeClusterWrite = func(parameters armcontainerservice.ManagedCluster) int {
				if s.clusterWrites == 1 {
					require.Len(t, parameters.Properties.AgentPoolProfiles, 1, "only initial creation embeds a bootstrap pool")
					require.Contains(t, parameters.Tags, provisioningTagKey)
					s.cluster = concurrent
					s.advanceCluster()
					return status
				}
				require.Equal(t, 2, s.clusterWrites)
				require.GreaterOrEqual(t, s.clusterGets, 2, "create race recovery must read the winner before reconciling")
				return 0
			}

			require.NoError(t, s.options.run(s.ctx, logr.Discard()))
			assert.Equal(t, 2, s.clusterWrites, "one rejected create followed by one configuration-only update")
			assert.Equal(t, s.options.etcdKMSKeyURI, *s.cluster.Properties.SecurityProfile.AzureKeyVaultKms.KeyID)
			assert.Equal(t, wantPools, s.cluster.Properties.AgentPoolProfiles)
			assert.Equal(t, "1.35.2", *s.cluster.Properties.KubernetesVersion)
			assert.Equal(t, wantTags, s.cluster.Tags)
			assert.Zero(t, s.poolGets+s.poolLists+s.poolWrites, "the winning creator already completed provisioning")
			assert.Zero(t, s.tagWrites)
		})
	}
}

func TestRunPoolConflictRefreshesClusterAndPoolETags(t *testing.T) {
	s := newLifecycleServer(t)
	s.cluster.Tags[provisioningTagKey] = ptr.To(provisioningTagValue)
	s.pool.Properties.ProvisioningState = ptr.To(provisioningStateFailed)
	getsAtConflict := 0
	poolGetsAtConflict := 0
	s.beforePoolWrite = func(armcontainerservice.AgentPool) int {
		if s.poolWrites == 1 {
			getsAtConflict = s.clusterGets
			poolGetsAtConflict = s.poolGets
			s.pool.Properties.ETag = ptr.To("pool-concurrent")
			s.cluster.Tags["actor"] = ptr.To("retained")
			s.advanceCluster()
			return http.StatusConflict
		}
		require.Equal(t, 2, s.poolWrites)
		require.Greater(t, s.clusterGets, getsAtConflict, "pool conflict must retry the lifecycle, not only the stale pool write")
		require.Greater(t, s.poolGets, poolGetsAtConflict)
		return 0
	}
	s.beforeTagWrite = func(armcontainerservice.TagsObject) int {
		require.Equal(t, "Succeeded", *s.pool.Properties.ProvisioningState)
		require.Equal(t, 2, s.poolWrites)
		return 0
	}

	require.NoError(t, s.options.run(s.ctx, logr.Discard()))
	assert.Equal(t, 2, s.poolWrites)
	assert.Equal(t, "Succeeded", *s.pool.Properties.ProvisioningState)
	assert.Equal(t, s.pool.Properties.MaxCount, s.cluster.Properties.AgentPoolProfiles[0].MaxCount)
	assert.Zero(t, s.clusterWrites, "bootstrap recovery uses the pool API, never an inline cluster update")
	assert.Equal(t, 1, s.tagWrites)
	assert.Equal(t, ptr.To("retained"), s.cluster.Tags["actor"])
	assert.NotContains(t, s.cluster.Tags, provisioningTagKey)
}

func TestRunCompletedConvergedClusterOnlyReadsCluster(t *testing.T) {
	s := newLifecycleServer(t)
	wantPools := s.cluster.Properties.AgentPoolProfiles
	wantTags := s.cluster.Tags

	require.NoError(t, s.options.run(s.ctx, logr.Discard()))
	assert.Equal(t, 1, s.clusterGets, "a converged cluster with complete baselines needs one cluster observation")
	assert.Zero(t, s.clusterWrites+s.tagWrites)
	assert.Zero(t, s.poolGets+s.poolLists+s.poolWrites, "completed ownership and full baselines require no pool API calls")
	assert.Equal(t, wantPools, s.cluster.Properties.AgentPoolProfiles)
	assert.Equal(t, wantTags, s.cluster.Tags)
}

func TestRunNonConflictFailureDoesNotRetry(t *testing.T) {
	s := newLifecycleServer(t)
	s.cluster.Properties.SecurityProfile.AzureKeyVaultKms.KeyID = ptr.To("https://kv1.vault.azure.net/keys/aks-etcd-encryption/old")
	s.beforeClusterWrite = func(armcontainerservice.ManagedCluster) int {
		return http.StatusForbidden
	}

	require.Error(t, s.options.run(s.ctx, logr.Discard()))
	assert.Equal(t, 1, s.clusterGets)
	assert.Equal(t, 1, s.clusterWrites, "authorization failures must not enter the lifecycle conflict retry loop")
	assert.Zero(t, s.tagWrites+s.poolGets+s.poolLists+s.poolWrites)
	assert.NotEqual(t, s.options.etcdKMSKeyURI, *s.cluster.Properties.SecurityProfile.AzureKeyVaultKms.KeyID)
}

func TestRunCancellationStopsConflictRetry(t *testing.T) {
	s := newLifecycleServer(t)
	ctx, cancel := context.WithCancel(s.ctx)
	defer cancel()
	s.cluster.Properties.SecurityProfile.AzureKeyVaultKms.KeyID = ptr.To("https://kv1.vault.azure.net/keys/aks-etcd-encryption/old")
	s.beforeClusterWrite = func(armcontainerservice.ManagedCluster) int {
		cancel()
		return http.StatusConflict
	}

	require.ErrorIs(t, s.options.run(ctx, logr.Discard()), context.Canceled)
	assert.Equal(t, 1, s.clusterGets)
	assert.Equal(t, 1, s.clusterWrites, "cancellation after a rejected write must stop the next lifecycle attempt")
	assert.Zero(t, s.tagWrites+s.poolGets+s.poolLists+s.poolWrites)
}
