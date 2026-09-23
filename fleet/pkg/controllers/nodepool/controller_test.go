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
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/go-logr/logr/funcr"
	"github.com/stretchr/testify/require"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	azfake "github.com/Azure/azure-sdk-for-go/sdk/azcore/fake"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v6"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v8"

	"github.com/Azure/ARO-HCP/fleet/pkg/azure/agentpools"
	"github.com/Azure/ARO-HCP/fleet/pkg/azure/agentpoolspec"
	"github.com/Azure/ARO-HCP/fleet/pkg/azure/skucache"
	"github.com/Azure/ARO-HCP/fleet/pkg/compute"
	fleetcontrollers "github.com/Azure/ARO-HCP/fleet/pkg/controllers/base"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/apihelpers/fleetapihelpers"
	"github.com/Azure/ARO-HCP/internal/utils"
)

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

func syncOnceTestManagementCluster(aksResourceID string) *fleetapi.ManagementCluster {
	mc := &fleetapi.ManagementCluster{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID: metadataapi.Must(fleetapihelpers.ToManagementClusterResourceID(syncOnceTestStampID)),
		},
	}
	if len(aksResourceID) > 0 {
		mc.Status.AKSResourceID = metadataapi.Must(azcorearm.ParseResourceID(aksResourceID))
	}
	return mc
}

func syncOnceTestProfile() compute.Profile {
	return compute.Profile{
		Tiers: []compute.TierConfig{{
			Name: "wrk", Role: compute.PoolRoleWorker, PoolMode: compute.PoolModeRegional,
			Cores: 4, OSDiskSizeGB: 32, MaxNodes: 2, MaxPods: 100,
			FamilyPriority: []compute.VMFamily{syncOnceTestVMFamily},
		}},
		BudgetStrategy: compute.SubscriptionQuotaBudget,
	}
}

func testSyncOnceContext() context.Context {
	return utils.ContextWithLogger(context.Background(), logr.Discard())
}

type shadowTransportFunc func(*http.Request) (*http.Response, error)

func (f shadowTransportFunc) Do(req *http.Request) (*http.Response, error) {
	return f(req)
}

// shadowAzure serves ARM wire responses through real SDK clients. Every request,
// including SKU and quota discovery, passes the GET-only guard before routing.
// There are deliberately no write handlers or successful mutation responses.
type shadowAzure struct {
	cluster armcontainerservice.ManagedCluster
	pools   []*armcontainerservice.AgentPool
	limit   int64
	usage   int64
	paths   []string
	readErr string
}

func (a *shadowAzure) transport(t *testing.T) policy.Transporter {
	t.Helper()
	return shadowTransportFunc(func(req *http.Request) (*http.Response, error) {
		require.Equal(t, http.MethodGet, req.Method, "shadow controller attempted an ARM mutation: %s", req.URL)
		path := strings.ToLower(req.URL.Path)
		a.paths = append(a.paths, path)
		var body any
		switch {
		case strings.HasSuffix(path, "/agentpools"):
			body = armcontainerservice.AgentPoolListResult{Value: a.pools}
		case strings.HasSuffix(path, "/mc"):
			body = a.cluster
		case strings.HasSuffix(path, "/skus"):
			body = armcompute.ResourceSKUsResult{Value: []*armcompute.ResourceSKU{{
				Name: ptr.To(syncOnceTestVMSize), Family: ptr.To(syncOnceTestVMFamily), ResourceType: ptr.To("virtualMachines"),
				LocationInfo: []*armcompute.ResourceSKULocationInfo{{Zones: []*string{ptr.To("1"), ptr.To("2"), ptr.To("3")}}},
				Capabilities: []*armcompute.ResourceSKUCapabilities{
					{Name: ptr.To("vCPUs"), Value: ptr.To("4")},
					{Name: ptr.To("MemoryGB"), Value: ptr.To("16")},
					{Name: ptr.To("EphemeralOSDiskSupported"), Value: ptr.To("True")},
					{Name: ptr.To("CachedDiskBytes"), Value: ptr.To("107374182400")},
				},
			}}}
		case strings.HasSuffix(path, "/usages"):
			body = map[string]any{"value": []any{map[string]any{
				"name": map[string]string{"value": syncOnceTestVMFamily}, "limit": a.limit, "currentValue": a.usage,
			}}}
		default:
			t.Fatalf("unexpected Azure read: %s", req.URL)
		}
		status := http.StatusOK
		if a.readErr != "" && strings.HasSuffix(path, a.readErr) {
			status = http.StatusForbidden
			body = map[string]any{"error": map[string]string{"code": "AuthorizationFailed", "message": "read forbidden"}}
		}
		data, err := json.Marshal(body)
		require.NoError(t, err)
		return &http.Response{
			StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}},
			Body: io.NopCloser(strings.NewReader(string(data))), Request: req,
		}, nil
	})
}

func newShadowTestSyncer(t *testing.T, azure *shadowAzure, profile compute.Profile) *nodePoolSyncer {
	t.Helper()
	credential := &azfake.TokenCredential{}
	options := &policy.ClientOptions{Transport: azure.transport(t)}
	factory, armOptions := agentpools.NewClientFactory(credential, options)
	return &nodePoolSyncer{
		managementClusterLister: &fakeManagementClusterLister{mc: syncOnceTestManagementCluster(syncOnceTestAKSResourceID)},
		profile:                 profile, zones: []string{"1", "2", "3"}, region: "eastus",
		agentPoolClientFactory: factory, credential: credential, armClientOptions: armOptions,
		skuCache: skucache.NewSKUCache("eastus", credential, options, nil),
	}
}

func shadowReportContext(t *testing.T) (context.Context, *[]map[string]any) {
	t.Helper()
	reports := []map[string]any{}
	logger := funcr.NewJSON(func(line string) {
		var record map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &record))
		if _, ok := record["projectedTrace"]; ok {
			reports = append(reports, record)
		}
	}, funcr.Options{})
	return utils.ContextWithLogger(context.Background(), logger), &reports
}

func TestNodePoolSyncerSyncOnce(t *testing.T) {
	key := fleetcontrollers.ManagementClusterKey{StampIdentifier: syncOnceTestStampID}
	t.Run("lister failures remain operational errors", func(t *testing.T) {
		readErr := errors.New("not found")
		syncer := &nodePoolSyncer{managementClusterLister: &fakeManagementClusterLister{err: readErr}}
		require.ErrorIs(t, syncer.SyncOnce(testSyncOnceContext(), key), readErr)
	})
	t.Run("missing AKS ID reports waiting without Azure access", func(t *testing.T) {
		syncer := &nodePoolSyncer{managementClusterLister: &fakeManagementClusterLister{mc: syncOnceTestManagementCluster("")}}
		ctx, reports := shadowReportContext(t)
		require.NoError(t, syncer.SyncOnce(ctx, key))
		require.Len(t, *reports, 1)
		require.Equal(t, "waiting", (*reports)[0]["outcome"])
	})
}

func TestShadowSyncOnceReadOnly(t *testing.T) {
	tests := []struct {
		name        string
		change      func(*shadowAzure, *compute.Profile)
		wantOutcome string
		wantErr     bool
		clusterOnly bool
		wantSteps   int
	}{
		{name: "converged without ownership or baseline tags", wantOutcome: "converged"},
		{name: "actionable downsize projects without changing ARM", change: func(a *shadowAzure, _ *compute.Profile) { a.pools[0].Properties.MaxCount = ptr.To[int32](3) }, wantOutcome: "converged", wantSteps: 1},
		{name: "partial desired plan rejects reducing live capacity", change: func(a *shadowAzure, p *compute.Profile) {
			a.pools[0].Properties.MaxCount = ptr.To[int32](3)
			p.Tiers[0].MaxNodes = 4
			a.limit = 12
		}, wantOutcome: "rejected"},
		{name: "allocation failure reports rejected not degraded", change: func(_ *shadowAzure, p *compute.Profile) { p.Tiers[0].Cores = 8 }, wantOutcome: "rejected"},
		{name: "exhausted quota reports blocked not degraded", change: func(a *shadowAzure, _ *compute.Profile) {
			a.pools[0].Properties.MaxCount = ptr.To[int32](1)
			a.usage = a.limit
		}, wantOutcome: "blocked"},
		{name: "pool operation reports waiting", change: func(a *shadowAzure, _ *compute.Profile) { a.pools[0].Properties.ProvisioningState = ptr.To("Updating") }, wantOutcome: "waiting", wantSteps: 1},
		{name: "pool operation precedes allocation rejection", change: func(a *shadowAzure, p *compute.Profile) {
			a.pools[0].Properties.ProvisioningState = ptr.To("Updating")
			p.Tiers[0].Cores = 8
		}, wantOutcome: "waiting"},
		{name: "cluster operation reports waiting", change: func(a *shadowAzure, _ *compute.Profile) { a.cluster.Properties.ProvisioningState = ptr.To("Updating") }, wantOutcome: "waiting", clusterOnly: true},
		{name: "missing cluster properties reports waiting", change: func(a *shadowAzure, _ *compute.Profile) { a.cluster.Properties = nil }, wantOutcome: "waiting", clusterOnly: true},
		{name: "optional provisioning marker reports waiting", change: func(a *shadowAzure, _ *compute.Profile) {
			a.cluster.Tags = map[string]*string{agentpools.ProvisioningTagKey: ptr.To(agentpools.ProvisioningTagValue)}
		}, wantOutcome: "waiting", clusterOnly: true},
		{name: "invalid configured NICs remain errors", change: func(a *shadowAzure, _ *compute.Profile) {
			a.pools[0].Properties.Tags = map[string]*string{agentpoolspec.SwiftMultiTenancyTag: ptr.To("true"), agentpoolspec.SwiftSecondaryNICCountTag: ptr.To("invalid")}
		}, wantErr: true},
		{name: "incomplete managed configuration remains error", change: func(a *shadowAzure, _ *compute.Profile) { a.pools[0].Properties.OSDiskSizeGB = nil }, wantErr: true},
		{name: "incomplete managed capacity remains error", change: func(a *shadowAzure, _ *compute.Profile) { a.pools[0].Properties.Count = nil }, wantErr: true},
		{name: "unknown observed SKU remains error", change: func(a *shadowAzure, _ *compute.Profile) { a.pools[0].Properties.VMSize = ptr.To("Standard_Unknown") }, wantErr: true},
		{name: "Azure read failure remains error", change: func(a *shadowAzure, _ *compute.Profile) { a.readErr = "/agentpools" }, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			profile := syncOnceTestProfile()
			azure := &shadowAzure{cluster: armcontainerservice.ManagedCluster{Properties: &armcontainerservice.ManagedClusterProperties{ProvisioningState: ptr.To("Succeeded")}}, limit: 100}
			syncer := newShadowTestSyncer(t, azure, profile)
			resolved, err := compute.ResolveDesiredPools(testSyncOnceContext(), syncer.skuCache, syncOnceTestSubscriptionID, profile, syncer.zones, syncer.usageFetcher(syncOnceTestSubscriptionID))
			require.NoError(t, err)
			require.Len(t, resolved.Pools, 1)
			properties := agentpoolspec.Build(resolved.Pools[0], compute.NetworkConfig{})
			properties.Count = ptr.To[int32](1)
			properties.ProvisioningState = ptr.To("Succeeded")
			azure.pools = []*armcontainerservice.AgentPool{{Name: ptr.To(resolved.Pools[0].Name), Properties: properties}}
			if test.change != nil {
				test.change(azure, &profile)
			}
			// Use a fresh cache so this sync exercises SKU discovery as well.
			syncer = newShadowTestSyncer(t, azure, profile)
			azure.paths = nil
			ctx, reports := shadowReportContext(t)
			key := fleetcontrollers.ManagementClusterKey{StampIdentifier: syncOnceTestStampID}
			err = syncer.SyncOnce(ctx, key)
			if test.wantErr {
				require.Error(t, err)
				require.Empty(t, *reports, "read/computation failures must not publish a successful projection")
				return
			}
			require.NoError(t, err)
			status := key.InitialController(NodePoolControllerName)
			fleetcontrollers.ReportSyncError(err)(status)
			condition := apimeta.FindStatusCondition(status.Status.Conditions, "Degraded")
			require.NotNil(t, condition)
			require.Equal(t, metav1.ConditionFalse, condition.Status)
			require.Len(t, *reports, 1)
			report := (*reports)[0]
			require.Equal(t, test.wantOutcome, report["outcome"])
			require.NotEmpty(t, report["reason"])
			require.Equal(t, syncOnceTestAKSResourceID, report["aksResourceID"])
			require.Equal(t, syncOnceTestStampID, report["stampIdentifier"])
			require.Equal(t, true, report["shadowOnly"])
			_, err = time.Parse(time.RFC3339Nano, report["observedAt"].(string))
			require.NoError(t, err)
			if test.clusterOnly {
				require.Len(t, azure.paths, 1)
			} else {
				require.Len(t, azure.paths, 4, "cluster, pools, SKU and quota must all be observed")
				require.Contains(t, report["projectedTrace"], resolved.Pools[0].Name)
				if test.wantSteps == 1 {
					require.Regexp(t, `steps:\s+1\b`, report["projectedTrace"])
				}
			}
		})
	}
}

func TestShadowSyncOnceRefreshesLiveCapacity(t *testing.T) {
	profile := syncOnceTestProfile()
	azure := &shadowAzure{cluster: armcontainerservice.ManagedCluster{Properties: &armcontainerservice.ManagedClusterProperties{ProvisioningState: ptr.To("Succeeded")}}, limit: 12}
	syncer := newShadowTestSyncer(t, azure, profile)
	resolved, err := compute.ResolveDesiredPools(testSyncOnceContext(), syncer.skuCache, syncOnceTestSubscriptionID, profile, syncer.zones, syncer.usageFetcher(syncOnceTestSubscriptionID))
	require.NoError(t, err)
	require.Len(t, resolved.Pools, 1)
	properties := agentpoolspec.Build(resolved.Pools[0], compute.NetworkConfig{})
	properties.Count = ptr.To[int32](1)
	properties.ProvisioningState = ptr.To("Succeeded")
	azure.pools = []*armcontainerservice.AgentPool{{Name: ptr.To(resolved.Pools[0].Name), Properties: properties}}
	// Quota permits two nodes with surge, short of this three-node target.
	syncer.profile.Tiers[0].MaxNodes = 3
	ctx, reports := shadowReportContext(t)
	key := fleetcontrollers.ManagementClusterKey{StampIdentifier: syncOnceTestStampID}
	require.NoError(t, syncer.SyncOnce(ctx, key))
	// An external controller increases the live ceiling between periodic syncs.
	properties.MaxCount = ptr.To[int32](3)
	require.NoError(t, syncer.SyncOnce(ctx, key))
	require.Len(t, *reports, 2)
	require.Equal(t, "converged", (*reports)[0]["outcome"])
	require.Equal(t, "rejected", (*reports)[1]["outcome"])
	require.Equal(t, int32(3), *properties.MaxCount)
	first, err := time.Parse(time.RFC3339Nano, (*reports)[0]["observedAt"].(string))
	require.NoError(t, err)
	second, err := time.Parse(time.RFC3339Nano, (*reports)[1]["observedAt"].(string))
	require.NoError(t, err)
	require.True(t, second.After(first), "each periodic observation must produce a freshly timestamped report")
}

func TestShadowSyncOnceOptionalTierFailureStillProjects(t *testing.T) {
	profile := syncOnceTestProfile()
	profile.Tiers[0].Required = true
	// No 8-core SKU exists in the fake region, so this optional tier cannot
	// allocate. The required tier above it still has a complete, usable plan.
	profile.Tiers = append(profile.Tiers, compute.TierConfig{
		Name: "wrk8", Role: compute.PoolRoleWorker, PoolMode: compute.PoolModeRegional,
		Cores: 8, OSDiskSizeGB: 32, MaxNodes: 1, MaxPods: 100,
		FamilyPriority: []compute.VMFamily{syncOnceTestVMFamily},
	})

	azure := &shadowAzure{cluster: armcontainerservice.ManagedCluster{Properties: &armcontainerservice.ManagedClusterProperties{ProvisioningState: ptr.To("Succeeded")}}, limit: 100}
	syncer := newShadowTestSyncer(t, azure, profile)
	resolved, err := compute.ResolveDesiredPools(testSyncOnceContext(), syncer.skuCache, syncOnceTestSubscriptionID, profile, syncer.zones, syncer.usageFetcher(syncOnceTestSubscriptionID))
	require.NoError(t, err)
	require.Len(t, resolved.Pools, 1, "the required tier must allocate")
	require.Len(t, resolved.Failures, 1)
	require.False(t, resolved.Failures[0].Required, "the failing tier must be the optional one")

	properties := agentpoolspec.Build(resolved.Pools[0], compute.NetworkConfig{})
	properties.Count = ptr.To[int32](1)
	properties.ProvisioningState = ptr.To("Succeeded")
	azure.pools = []*armcontainerservice.AgentPool{{Name: ptr.To(resolved.Pools[0].Name), Properties: properties}}

	syncer = newShadowTestSyncer(t, azure, profile)
	ctx, reports := shadowReportContext(t)
	require.NoError(t, syncer.SyncOnce(ctx, fleetcontrollers.ManagementClusterKey{StampIdentifier: syncOnceTestStampID}))
	require.Len(t, *reports, 1)
	require.Equal(t, "converged", (*reports)[0]["outcome"],
		"an optional tier's allocation failure must not abandon the projection of the tiers that did allocate")
}
