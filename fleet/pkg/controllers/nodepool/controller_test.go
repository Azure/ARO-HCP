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

package nodepool

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/require"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
	fleetcontrollers "github.com/Azure/ARO-HCP/fleet/pkg/controllers/base"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// fakeManagementClusterLister is a minimal ManagementClusterLister backed by a
// single canned response, since SyncOnce only ever calls Get.
type fakeManagementClusterLister struct {
	mc  *fleetapi.ManagementCluster
	err error
}

func (f *fakeManagementClusterLister) List(_ context.Context) ([]*fleetapi.ManagementCluster, error) {
	return nil, nil
}

func (f *fakeManagementClusterLister) Get(_ context.Context, _ string) (*fleetapi.ManagementCluster, error) {
	return f.mc, f.err
}

func (f *fakeManagementClusterLister) GetByCSProvisionShardID(_ context.Context, _ string) (*fleetapi.ManagementCluster, error) {
	return nil, nil
}

const (
	syncOnceTestStampID        = "s1"
	syncOnceTestSubscriptionID = "00000000-0000-0000-0000-000000000000"
	syncOnceTestAKSResourceID  = "/subscriptions/" + syncOnceTestSubscriptionID + "/resourceGroups/rg/providers/Microsoft.ContainerService/managedClusters/mc"
	syncOnceTestVMSize         = "Standard_TestW4"
	syncOnceTestVMFamily       = "testFamily"
)

// syncOnceTestManagementCluster returns a ManagementCluster with aksResourceID
// as its AKS resource ID, or with no AKS resource ID when aksResourceID is empty.
func syncOnceTestManagementCluster(aksResourceID string) *fleetapi.ManagementCluster {
	mc := &fleetapi.ManagementCluster{
		ResourceID: metadataapi.Must(fleetapi.ToManagementClusterResourceID(syncOnceTestStampID)),
	}
	if len(aksResourceID) > 0 {
		mc.Status.AKSResourceID = metadataapi.Must(azcorearm.ParseResourceID(aksResourceID))
	}
	return mc
}

// syncOnceTestProfile is a minimal single-tier, single-family, unlimited-budget
// profile, chosen so the desired pool set (and its convergence with the fake
// AKS state below) is easy to reason about by hand.
func syncOnceTestProfile() compute.Profile {
	return compute.Profile{
		Tiers: []compute.TierConfig{
			{
				Name:           "wrk",
				Role:           compute.PoolRoleWorker,
				PoolMode:       compute.PoolModeRegional,
				Cores:          4,
				OSDiskSizeGB:   32,
				MaxNodes:       2,
				FamilyPriority: []compute.VMFamily{syncOnceTestVMFamily},
				MaxPods:        100,
				Labels:         map[string]string{compute.RoleLabel: string(compute.PoolRoleWorker)},
			},
		},
		BudgetStrategy: compute.UnlimitedBudget,
	}
}

// syncOnceTestSKUCache returns a SKUCache serving a single eligible SKU for
// syncOnceTestProfile's family, via the real Azure SDK fake transport.
func syncOnceTestSKUCache(t *testing.T) *skucache.SKUCache {
	t.Helper()
	srv := armcomputefake.ResourceSKUsServer{
		NewListPager: func(_ *armcompute.ResourceSKUsClientListOptions) (resp azfake.PagerResponder[armcompute.ResourceSKUsClientListResponse]) {
			resp.AddPage(http.StatusOK, armcompute.ResourceSKUsClientListResponse{
				ResourceSKUsResult: armcompute.ResourceSKUsResult{
					Value: []*armcompute.ResourceSKU{
						{
							Name:         ptr.To(syncOnceTestVMSize),
							Family:       ptr.To(syncOnceTestVMFamily),
							ResourceType: ptr.To("virtualMachines"),
							LocationInfo: []*armcompute.ResourceSKULocationInfo{
								{Zones: []*string{ptr.To("1"), ptr.To("2"), ptr.To("3")}},
							},
							Capabilities: []*armcompute.ResourceSKUCapabilities{
								{Name: ptr.To("vCPUs"), Value: ptr.To("4")},
								{Name: ptr.To("MemoryGB"), Value: ptr.To("16")},
								{Name: ptr.To("EphemeralOSDiskSupported"), Value: ptr.To("True")},
								{Name: ptr.To("CachedDiskBytes"), Value: ptr.To("107374182400")},
							},
						},
					},
				},
			}, nil)
			return
		},
	}
	transport := armcomputefake.NewResourceSKUsServerTransport(&srv)
	return skucache.NewSKUCache("eastus", &azfake.TokenCredential{}, &policy.ClientOptions{Transport: transport}, nil)
}

// syncOnceTestAgentPoolClientFactory returns an agentPoolClientFactory backed
// by the given already-existing agent pools, via the real Azure SDK fake
// transport.
func syncOnceTestAgentPoolClientFactory(existing []*armcontainerservice.AgentPool) func(string) (*armcontainerservice.AgentPoolsClient, error) {
	srv := armcontainerservicefake.AgentPoolsServer{
		NewListPager: func(_, _ string, _ *armcontainerservice.AgentPoolsClientListOptions) (resp azfake.PagerResponder[armcontainerservice.AgentPoolsClientListResponse]) {
			resp.AddPage(http.StatusOK, armcontainerservice.AgentPoolsClientListResponse{
				AgentPoolListResult: armcontainerservice.AgentPoolListResult{Value: existing},
			}, nil)
			return
		},
	}
	transport := armcontainerservicefake.NewAgentPoolsServerTransport(&srv)
	return func(subscriptionID string) (*armcontainerservice.AgentPoolsClient, error) {
		return armcontainerservice.NewAgentPoolsClient(subscriptionID, &azfake.TokenCredential{}, &azcorearm.ClientOptions{
			ClientOptions: policy.ClientOptions{Transport: transport},
		})
	}
}

func testSyncOnceContext() context.Context {
	return utils.ContextWithLogger(context.Background(), logr.Discard())
}

func TestNodePoolSyncerSyncOnce(t *testing.T) {
	key := fleetcontrollers.ManagementClusterKey{StampIdentifier: syncOnceTestStampID}

	t.Run("management cluster not found", func(t *testing.T) {
		syncer := &nodePoolSyncer{
			managementClusterLister: &fakeManagementClusterLister{err: errors.New("not found")},
		}

		err := syncer.SyncOnce(testSyncOnceContext(), key)
		require.Error(t, err)
	})

	t.Run("no AKS resource ID yet is a no-op", func(t *testing.T) {
		syncer := &nodePoolSyncer{
			managementClusterLister: &fakeManagementClusterLister{mc: syncOnceTestManagementCluster("")},
			agentPoolClientFactory: func(string) (*armcontainerservice.AgentPoolsClient, error) {
				t.Fatal("agent pool client should not be built without an AKS resource ID")
				return nil, nil
			},
		}

		err := syncer.SyncOnce(testSyncOnceContext(), key)
		require.NoError(t, err)
	})

	t.Run("converged state performs no action", func(t *testing.T) {
		skuCache := syncOnceTestSKUCache(t)

		// Precompute the desired pool the same way SyncOnce will, so the fake
		// AKS state below can be built to exactly match it (MaxCount, name,
		// zones) and force convergence (findNextAction returns nil).
		resolved, err := compute.ResolveDesiredPools(
			testSyncOnceContext(), skuCache, syncOnceTestSubscriptionID,
			syncOnceTestProfile(), []string{"1", "2", "3"},
			func(context.Context, sets.Set[compute.VMFamily]) (map[compute.VMFamily]compute.QuotaUsage, error) {
				t.Fatal("fetchQuotaUsage should not be called by UnlimitedBudget")
				return nil, nil
			},
		)
		require.NoError(t, err)
		require.Len(t, resolved.Pools, 1, "expected exactly one desired pool")
		desired := resolved.Pools[0]

		existingZones := make([]*string, len(desired.AvailabilityZones))
		for i, z := range desired.AvailabilityZones {
			existingZones[i] = ptr.To(z)
		}

		existingPool := &armcontainerservice.AgentPool{
			Name: ptr.To(desired.Name),
			Properties: &armcontainerservice.ManagedClusterAgentPoolProfileProperties{
				VMSize:            ptr.To(desired.Spec.Size),
				OSDiskSizeGB:      ptr.To(desired.OSDiskSizeGB),
				MaxPods:           ptr.To(desired.MaxPods),
				Count:             ptr.To(desired.MaxCount),
				MaxCount:          ptr.To(desired.MaxCount),
				MinCount:          ptr.To[int32](1),
				EnableAutoScaling: ptr.To(true),
				ProvisioningState: ptr.To("Succeeded"),
				AvailabilityZones: existingZones,
				NodeLabels:        map[string]*string{compute.RoleLabel: ptr.To(string(compute.PoolRoleWorker))},
			},
		}

		clusterServer := armcontainerservicefake.ManagedClustersServer{
			Get: func(context.Context, string, string, *armcontainerservice.ManagedClustersClientGetOptions) (resp azfake.Responder[armcontainerservice.ManagedClustersClientGetResponse], errResp azfake.ErrorResponder) {
				resp.SetResponse(http.StatusOK, armcontainerservice.ManagedClustersClientGetResponse{ManagedCluster: armcontainerservice.ManagedCluster{
					Properties: &armcontainerservice.ManagedClusterProperties{ProvisioningState: ptr.To("Succeeded")},
					Tags: map[string]*string{
						"arohcp-capacity-system": ptr.To(`{"vcpus":0,"memoryGiB":0,"swiftNICs":0}`),
						"arohcp-capacity-infra":  ptr.To(`{"vcpus":0,"memoryGiB":0,"swiftNICs":0}`),
						"arohcp-capacity-worker": ptr.To(`{"vcpus":8,"memoryGiB":32,"swiftNICs":0}`),
					},
				}}, nil)
				return
			},
		}
		syncer := &nodePoolSyncer{
			managementClusterLister: &fakeManagementClusterLister{mc: syncOnceTestManagementCluster(syncOnceTestAKSResourceID)},
			profile:                 syncOnceTestProfile(),
			zones:                   []string{"1", "2", "3"},
			agentPoolClientFactory:  syncOnceTestAgentPoolClientFactory([]*armcontainerservice.AgentPool{existingPool}),
			credential:              &azfake.TokenCredential{},
			armClientOptions:        &azcorearm.ClientOptions{ClientOptions: policy.ClientOptions{Transport: armcontainerservicefake.NewManagedClustersServerTransport(&clusterServer)}},
			skuCache:                skuCache,
		}

		err = syncer.SyncOnce(testSyncOnceContext(), key)
		require.NoError(t, err)
	})
}

func TestSyncOnceCapacityBaseline(t *testing.T) {
	tests := []struct {
		name              string
		baseline          string
		missing           bool
		partial           bool
		allocationFailure bool
		invalidNICs       bool
		blocked           bool
		conflict          bool
		wantWrite         bool
		wantErr           string
	}{
		{name: "unchanged", baseline: `{"vcpus":8,"memoryGiB":32,"swiftNICs":0}`},
		{name: "advance after convergence", baseline: `{"vcpus":4,"memoryGiB":16,"swiftNICs":0}`, wantWrite: true},
		{name: "full CPU reduction after convergence", baseline: `{"vcpus":12,"memoryGiB":32,"swiftNICs":0}`, wantWrite: true},
		{name: "full memory reduction after convergence", baseline: `{"vcpus":8,"memoryGiB":64,"swiftNICs":0}`, wantWrite: true},
		{name: "full NIC reduction after convergence", baseline: `{"vcpus":8,"memoryGiB":32,"swiftNICs":1}`, wantWrite: true},
		{name: "partial allocation preserves baseline", partial: true, baseline: `{"vcpus":8,"memoryGiB":32,"swiftNICs":0}`},
		{name: "full downsize blocked before convergence does not lower tags", blocked: true, baseline: `{"vcpus":12,"memoryGiB":48,"swiftNICs":0}`, wantErr: "no safe action"},
		{name: "missing baseline", missing: true, wantErr: "missing worker capacity baseline"},
		{name: "malformed baseline", baseline: `{"vcpus":8}`, wantErr: "invalid capacity tag"},
		{name: "partial CPU reduction", partial: true, baseline: `{"vcpus":12,"memoryGiB":32,"swiftNICs":0}`, wantErr: "rejecting desired plan"},
		{name: "partial memory reduction", partial: true, baseline: `{"vcpus":8,"memoryGiB":64,"swiftNICs":0}`, wantErr: "rejecting desired plan"},
		{name: "partial NIC reduction", partial: true, baseline: `{"vcpus":8,"memoryGiB":32,"swiftNICs":1}`, wantErr: "rejecting desired plan"},
		{name: "invalid configured NIC count", baseline: `{"vcpus":8,"memoryGiB":32,"swiftNICs":0}`, invalidNICs: true, wantErr: "invalid Swift NIC count"},
		{name: "optional allocation failure", baseline: `{"vcpus":8,"memoryGiB":32,"swiftNICs":0}`, allocationFailure: true, wantErr: "tier allocation failed"},
		{name: "quota blocked is not convergence", baseline: `{"vcpus":4,"memoryGiB":16,"swiftNICs":0}`, blocked: true, wantErr: "pool configuration has not converged; no safe action"},
		{name: "concurrent tag update", baseline: `{"vcpus":4,"memoryGiB":16,"swiftNICs":0}`, wantWrite: true, conflict: true, wantErr: "PreconditionFailed"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			skuCache := syncOnceTestSKUCache(t)
			profile := syncOnceTestProfile()
			if test.blocked {
				profile.BudgetStrategy = func(context.Context, sets.Set[compute.VMFamily], compute.FetchQuotaUsageFunc) (map[compute.VMFamily]compute.QuotaUsage, error) {
					return map[compute.VMFamily]compute.QuotaUsage{syncOnceTestVMFamily: {Limit: 16, CurrentValue: 16}}, nil
				}
			}
			resolved, err := compute.ResolveDesiredPools(testSyncOnceContext(), skuCache, syncOnceTestSubscriptionID, profile, []string{"1", "2", "3"}, nil)
			require.NoError(t, err)
			desired := resolved.Pools[0]
			properties := agentpoolspec.Build(desired, compute.NetworkConfig{})
			properties.Count = ptr.To[int32](1)
			properties.ProvisioningState = ptr.To("Succeeded")
			if test.blocked {
				properties.MaxCount = ptr.To[int32](1)
			}
			if test.invalidNICs {
				properties.Tags = map[string]*string{agentpoolspec.SwiftMultiTenancyTag: ptr.To("true"), agentpoolspec.SwiftSecondaryNICCountTag: ptr.To("invalid")}
			}
			if test.partial {
				profile.Tiers[0].MaxNodes = 3
				profile.BudgetStrategy = func(context.Context, sets.Set[compute.VMFamily], compute.FetchQuotaUsageFunc) (map[compute.VMFamily]compute.QuotaUsage, error) {
					return map[compute.VMFamily]compute.QuotaUsage{syncOnceTestVMFamily: {Limit: 12}}, nil
				}
			}
			if test.allocationFailure {
				profile.Tiers[0].Cores = 8
			}
			tags := map[string]*string{
				"arohcp-capacity-system": ptr.To(`{"vcpus":0,"memoryGiB":0,"swiftNICs":0}`),
				"arohcp-capacity-infra":  ptr.To(`{"vcpus":0,"memoryGiB":0,"swiftNICs":0}`),
				"owner":                  ptr.To("test"),
			}
			if !test.missing {
				tags["arohcp-capacity-worker"] = ptr.To(test.baseline)
			}
			writes := 0
			server := armcontainerservicefake.ManagedClustersServer{
				Get: func(context.Context, string, string, *armcontainerservice.ManagedClustersClientGetOptions) (resp azfake.Responder[armcontainerservice.ManagedClustersClientGetResponse], errResp azfake.ErrorResponder) {
					resp.SetResponse(http.StatusOK, armcontainerservice.ManagedClustersClientGetResponse{ManagedCluster: armcontainerservice.ManagedCluster{ETag: ptr.To("e1"), Tags: tags, Properties: &armcontainerservice.ManagedClusterProperties{ProvisioningState: ptr.To("Succeeded")}}}, nil)
					return
				},
				BeginUpdateTags: func(_ context.Context, _, _ string, parameters armcontainerservice.TagsObject, options *armcontainerservice.ManagedClustersClientBeginUpdateTagsOptions) (resp azfake.PollerResponder[armcontainerservice.ManagedClustersClientUpdateTagsResponse], errResp azfake.ErrorResponder) {
					writes++
					require.Equal(t, "e1", *options.IfMatch)
					require.Equal(t, "test", *parameters.Tags["owner"])
					require.JSONEq(t, `{"vcpus":8,"memoryGiB":32,"swiftNICs":0}`, *parameters.Tags["arohcp-capacity-worker"])
					if test.conflict {
						errResp.SetResponseError(http.StatusPreconditionFailed, "PreconditionFailed")
						return
					}
					resp.SetTerminalResponse(http.StatusOK, armcontainerservice.ManagedClustersClientUpdateTagsResponse{}, nil)
					return
				},
			}
			syncer := &nodePoolSyncer{
				managementClusterLister: &fakeManagementClusterLister{mc: syncOnceTestManagementCluster(syncOnceTestAKSResourceID)},
				profile:                 profile, zones: []string{"1", "2", "3"}, credential: &azfake.TokenCredential{}, skuCache: skuCache,
				armClientOptions:       &azcorearm.ClientOptions{ClientOptions: policy.ClientOptions{Transport: armcontainerservicefake.NewManagedClustersServerTransport(&server)}},
				agentPoolClientFactory: syncOnceTestAgentPoolClientFactory([]*armcontainerservice.AgentPool{{Name: ptr.To(desired.Name), Properties: properties}}),
			}
			err = syncer.SyncOnce(testSyncOnceContext(), fleetcontrollers.ManagementClusterKey{StampIdentifier: syncOnceTestStampID})
			if len(test.wantErr) > 0 {
				require.ErrorContains(t, err, test.wantErr)
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, test.wantWrite, writes > 0)
			if test.blocked || test.name == "unchanged" {
				key := fleetcontrollers.ManagementClusterKey{StampIdentifier: syncOnceTestStampID}
				status := key.InitialController(NodePoolControllerName)
				fleetcontrollers.ReportSyncError(err)(status)
				condition := apimeta.FindStatusCondition(status.Status.Conditions, "Degraded")
				require.NotNil(t, condition)
				if test.blocked {
					var tracked *utils.LineTrackingError
					require.ErrorAs(t, err, &tracked)
					require.Equal(t, metav1.ConditionTrue, condition.Status)
					require.Contains(t, condition.Message, "no safe action")
				} else {
					require.Equal(t, metav1.ConditionFalse, condition.Status)
				}
			}
		})
	}
}
