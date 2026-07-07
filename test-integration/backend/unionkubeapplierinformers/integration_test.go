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

package unionkubeapplierinformers_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/cache"
	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/fleet"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/database"
	dbinformers "github.com/Azure/ARO-HCP/internal/database/informers"
	unionkubeapplier "github.com/Azure/ARO-HCP/internal/database/unioninformers/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
	"github.com/Azure/ARO-HCP/internal/utils"
	"github.com/Azure/ARO-HCP/internal/utils/armhelpers"
	"github.com/Azure/ARO-HCP/test-integration/utils/integrationutils"
)

const (
	testSubscriptionID = "00000000-0000-0000-0000-000000000001"
	testResourceGroup  = "rg"
	testClusterName    = "cluster-1"
	testNodePoolName   = "np-1"

	// fastRelistDuration keeps the informer loop tight enough that test
	// budgets remain reasonable. Cosmos relists in the mock are
	// instantaneous.
	fastRelistDuration = 200 * time.Millisecond

	eventuallyTimeout = 15 * time.Second
	eventuallyTick    = 50 * time.Millisecond
)

// TestUnionKubeApplierInformersController_E2E exercises the dynamic-
// management-cluster reactor end to end:
//
//  1. Pre-load two ApplyDesires for management cluster "s1" into the
//     mock kube-applier DB. These will be the "initial state" the per-MC
//     informer delivers when it starts.
//  2. Register an event recorder on the union ApplyDesire informer.
//  3. Start the fleet informers and the controller. No management
//     clusters yet, so the union stays empty and the recorder sees nothing.
//  4. Add management cluster s1 to the fleet store. The controller
//     observes the add, calls the factory, gets per-MC informers, and
//     wires them into the union. The pre-loaded ApplyDesires are then
//     delivered through the union to the recorder.
//  5. Load another ApplyDesire under s1 after the per-MC informer is
//     running. Verify it lands at the recorder via the normal relist loop.
//  6. Cancel ctx; assert clean shutdown and no goroutine leaks.
//
// Runs once with the mock-cosmos storage backend, and a second time
// against the cosmos emulator when FRONTEND_SIMULATION_TESTING=true.
// In both modes the fleet and kube-applier sides use in-memory mocks —
// this test focuses on the controller wiring, not the cosmos data path
// (which has its own tests).
func TestUnionKubeApplierInformersController_E2E(t *testing.T) {
	defer integrationutils.VerifyNoNewGoLeaks(t)

	integrationutils.WithAndWithoutCosmos(t, func(t *testing.T, withMock bool) {
		ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
		ctx = utils.ContextWithLogger(ctx, integrationutils.DefaultLogger(t))
		defer cancel()

		// Cosmos storage — wired up so the framework can clean up artifacts
		// and run in both modes, even though the kube-applier *Desire data
		// path is exercised through the mock below.
		var (
			storageInfo integrationutils.StorageIntegrationTestInfo
			err         error
		)
		if withMock {
			storageInfo, err = integrationutils.NewMockCosmosFromTestingEnv(ctx, t)
		} else {
			storageInfo, err = integrationutils.NewCosmosFromTestingEnv(ctx, t)
		}
		require.NoError(t, err)
		cleanupCtx := utils.ContextWithLogger(context.Background(), integrationutils.DefaultLogger(t))
		defer storageInfo.Cleanup(cleanupCtx)

		// --- step 1: pre-load *Desires for the test stamp --------------------
		// Stamp identifiers are constrained to [0-9a-z]{1,3} by validation.
		stampIdentifier := "s1"
		managementClusterResourceID := api.Must(fleet.ToManagementClusterResourceID(stampIdentifier))

		initialClusterScopedApply := newApplyDesire(t,
			kubeapplier.ToClusterScopedApplyDesireResourceIDString(testSubscriptionID, testResourceGroup, testClusterName, "initial-a"),
			managementClusterResourceID)
		initialNodePoolScopedApply := newApplyDesire(t,
			kubeapplier.ToNodePoolScopedApplyDesireResourceIDString(testSubscriptionID, testResourceGroup, testClusterName, testNodePoolName, "initial-b"),
			managementClusterResourceID)

		mockKubeApplierClient, err := databasetesting.NewMockKubeApplierDBClientWithResources(ctx, []any{
			initialClusterScopedApply, initialNodePoolScopedApply,
		})
		require.NoError(t, err, "pre-load ApplyDesires")

		// MockKubeApplierDBClients is the registry the factory consults; we
		// register the per-MC client up front so it's ready when the
		// controller calls For() after the management cluster appears.
		kubeApplierClients := databasetesting.NewMockKubeApplierDBClients()
		kubeApplierClients.Register(managementClusterResourceID, mockKubeApplierClient)

		// --- step 2: set up fleet informers + listener ----------------------
		// FleetDBClient is the source of truth for management clusters.
		// Pre-create the stamp; we add the management cluster itself only
		// after the controller is running, so we can observe the reactor's
		// Add path.
		fleetClient := databasetesting.NewMockFleetDBClient()
		require.NoError(t, createStamp(ctx, fleetClient, stampIdentifier))

		relistDuration := fastRelistDuration
		fleetInformers := dbinformers.NewFleetInformersWithRelistDuration(ctx, fleetClient.GlobalListers(), &relistDuration)
		managementClusterInformer, managementClusterLister := fleetInformers.ManagementClusters()

		// Factory bridges the controller to the kube-applier registry.
		factory := &cosmosKubeApplierFactory{
			kubeApplierClients: kubeApplierClients,
			relistDuration:     relistDuration,
		}

		// Create the controller. Register an event recorder BEFORE Run so
		// the recorder's HasSynced lights up at the same time as the union's.
		controller := unionkubeapplier.NewUnionKubeApplierInformersController(
			managementClusterInformer, managementClusterLister, factory,
		)
		recorder := &applyDesireRecorder{}
		applyDesireInformer, applyDesireLister := controller.Union().ApplyDesires()
		_, err = applyDesireInformer.AddEventHandler(recorder.handlers())
		require.NoError(t, err, "AddEventHandler")

		// --- step 3: start fleet informers + controller ---------------------
		var waitGroup sync.WaitGroup
		waitGroup.Add(2)
		go func() { defer waitGroup.Done(); fleetInformers.RunWithContext(ctx) }()
		go func() { defer waitGroup.Done(); controller.Run(ctx, 1) }()
		defer waitGroup.Wait()

		require.True(t,
			cache.WaitForCacheSync(ctx.Done(), managementClusterInformer.HasSynced),
			"fleet management-cluster informer did not sync")

		// No MCs yet — recorder should be empty.
		require.Equal(t, 0, recorder.count(), "before adding any MC")

		// --- step 4: add the MC; observe initial *Desires delivered ---------
		require.NoError(t, createManagementCluster(ctx, fleetClient, stampIdentifier))

		require.Eventuallyf(t, func() bool {
			return recorder.count() >= 2
		}, eventuallyTimeout, eventuallyTick,
			"expected 2 initial ApplyDesires delivered (got %d)", recorder.count())

		// Cross-check via the union lister: both desires are visible there too.
		require.Eventually(t, func() bool {
			desires, err := applyDesireLister.ListForManagementCluster(ctx, managementClusterResourceID)
			return err == nil && len(desires) == 2
		}, eventuallyTimeout, eventuallyTick,
			"union ListForManagementCluster should return 2 desires for the registered MC")

		// --- step 5: add another *Desire and watch it propagate -------------
		secondClusterScopedApply := newApplyDesire(t,
			kubeapplier.ToClusterScopedApplyDesireResourceIDString(testSubscriptionID, testResourceGroup, "cluster-2", "initial-c"),
			managementClusterResourceID)
		require.NoError(t, createApplyDesire(ctx, mockKubeApplierClient, secondClusterScopedApply))

		require.Eventuallyf(t, func() bool {
			return recorder.count() >= 3
		}, eventuallyTimeout, eventuallyTick,
			"expected 3rd ApplyDesire delivered after late insert (got %d)", recorder.count())
		require.Eventually(t, func() bool {
			desires, err := applyDesireLister.ListForManagementCluster(ctx, managementClusterResourceID)
			return err == nil && len(desires) == 3
		}, eventuallyTimeout, eventuallyTick, "union sees 3 desires for the MC")

		// --- step 6: clean shutdown via context cancel ----------------------
		cancel()
		// waitGroup.Wait via defer.
	})
}

// --- helpers ------------------------------------------------------------------

// cosmosKubeApplierFactory adapts a database.KubeApplierDBClients into the
// controller's PerMCKubeApplierInformerFactory shape. Production code can
// use exactly this construction; we keep it inside the test to avoid
// committing to a package-level helper before the backend's wiring layer
// has settled.
type cosmosKubeApplierFactory struct {
	kubeApplierClients database.KubeApplierDBClients
	relistDuration     time.Duration
}

func (factory *cosmosKubeApplierFactory) NewKubeApplierInformers(
	ctx context.Context, managementClusterResourceID *azcorearm.ResourceID,
) dbinformers.KubeApplierInformers {
	client := factory.kubeApplierClients.For(ctx, managementClusterResourceID)
	if client == nil {
		return nil
	}
	return dbinformers.NewKubeApplierInformersWithRelistDuration(ctx, client.Listers(), &factory.relistDuration)
}

// applyDesireRecorder records the ApplyDesires that arrive through the
// union ApplyDesire informer. We only care about Adds for this test — the
// per-MC informer delivers existing state as Add events on startup, and
// subsequent inserts come through as Adds via the relist loop (the
// expiring-watcher protocol drops events between relists, so a fresh
// insert becomes a synthetic Add when the next list runs).
type applyDesireRecorder struct {
	mu    sync.Mutex
	items []*kubeapplier.ApplyDesire
	seen  map[string]struct{}
}

func (recorder *applyDesireRecorder) handlers() cache.ResourceEventHandler {
	return cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj any) {
			desire, ok := obj.(*kubeapplier.ApplyDesire)
			if !ok {
				return
			}
			recorder.mu.Lock()
			defer recorder.mu.Unlock()
			if recorder.seen == nil {
				recorder.seen = map[string]struct{}{}
			}
			key := desire.GetResourceID().String()
			if _, alreadySeen := recorder.seen[key]; alreadySeen {
				return
			}
			recorder.seen[key] = struct{}{}
			recorder.items = append(recorder.items, desire)
		},
	}
}

func (recorder *applyDesireRecorder) count() int {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	return len(recorder.items)
}

func newApplyDesire(t *testing.T, resourceIDString string, managementClusterResourceID *azcorearm.ResourceID) *kubeapplier.ApplyDesire {
	t.Helper()
	resourceID, err := azcorearm.ParseResourceID(resourceIDString)
	require.NoError(t, err, "parse %q", resourceIDString)
	return &kubeapplier.ApplyDesire{
		CosmosMetadata: api.CosmosMetadata{ResourceID: resourceID, PartitionKey: strings.ToLower(managementClusterResourceID.String())},
		Spec: kubeapplier.ApplyDesireSpec{
			ManagementCluster: managementClusterResourceID,
			Type:              kubeapplier.ApplyDesireTypeServerSideApply,
			ServerSideApply:   &kubeapplier.ServerSideApplyConfig{KubeContent: &runtime.RawExtension{Raw: []byte(`{"apiVersion":"v1","kind":"ConfigMap"}`)}},
		},
	}
}

func createStamp(ctx context.Context, fleetClient database.FleetDBClient, stampIdentifier string) error {
	stampResourceID := api.Must(fleet.ToStampResourceID(stampIdentifier))
	stamp := &fleet.Stamp{
		CosmosMetadata: api.CosmosMetadata{ResourceID: stampResourceID, PartitionKey: strings.ToLower(stampIdentifier)},
		ResourceID:     stampResourceID,
	}
	_, err := fleetClient.Stamps().Create(ctx, stamp, nil)
	return err
}

func createManagementCluster(ctx context.Context, fleetClient database.FleetDBClient, stampIdentifier string) error {
	managementClusterResourceID := api.Must(fleet.ToManagementClusterResourceID(stampIdentifier))
	aksResourceID := api.Must(azcorearm.ParseResourceID(
		fmt.Sprintf("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.ContainerService/managedClusters/aks-%s", stampIdentifier)))
	dnsZoneResourceID := api.Must(azcorearm.ParseResourceID(
		"/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/dns-rg/providers/Microsoft.Network/dnszones/test.example.com"))
	provisionShardID := ptr.To(api.Must(api.NewInternalID(
		fmt.Sprintf("/api/aro_hcp/v1alpha1/provision_shards/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeee%s", stampIdentifier))))
	managementCluster := &fleet.ManagementCluster{
		CosmosMetadata: api.CosmosMetadata{ResourceID: managementClusterResourceID, PartitionKey: strings.ToLower(stampIdentifier)},
		ResourceID:     managementClusterResourceID,
		Spec: fleet.ManagementClusterSpec{
			SchedulingPolicy: fleet.ManagementClusterSchedulingPolicySchedulable,
		},
		Status: fleet.ManagementClusterStatus{
			AKSResourceID:                                        aksResourceID,
			PublicDNSZoneResourceID:                              dnsZoneResourceID,
			HostedClustersSecretsKeyVaultURL:                     "https://cx-kv.vault.azure.net/",
			HostedClustersManagedIdentitiesKeyVaultURL:           "https://mi-kv.vault.azure.net/",
			HostedClustersSecretsKeyVaultManagedIdentityClientID: "c2bde1aa-d904-48cd-a728-9de33e3ddca9",
			ClusterServiceProvisionShardID:                       provisionShardID,
			MaestroConsumerName:                                  "consumer-" + stampIdentifier,
			MaestroRESTAPIURL:                                    "http://maestro.maestro.svc.cluster.local:8000",
			MaestroGRPCTarget:                                    "maestro-grpc.maestro.svc.cluster.local:8090",
			KubeApplierCosmosContainerName:                       "Manifests-MC-" + stampIdentifier,
		},
	}
	_, err := fleetClient.Stamps().ManagementClusters(stampIdentifier).Create(ctx, managementCluster, nil)
	return err
}

// createApplyDesire writes a new ApplyDesire to a per-MC kube-applier mock
// using the desire's parent resource hierarchy.
func createApplyDesire(ctx context.Context, mockClient *databasetesting.MockKubeApplierDBClient, desire *kubeapplier.ApplyDesire) error {
	id := desire.GetResourceID()
	if id == nil || id.Parent == nil {
		return fmt.Errorf("desire %v has no parent in its resource ID", id)
	}
	parentType := id.Parent.ResourceType
	var applyDesireCRUD database.ResourceCRUD[kubeapplier.ApplyDesire, *kubeapplier.ApplyDesire]
	var err error
	switch {
	case armhelpers.ResourceTypeEqual(parentType, api.ClusterResourceType):
		applyDesireCRUD, err = mockClient.ApplyDesiresForCluster(id.SubscriptionID, id.ResourceGroupName, id.Parent.Name)
	case armhelpers.ResourceTypeEqual(parentType, api.NodePoolResourceType):
		applyDesireCRUD, err = mockClient.ApplyDesiresForNodePool(id.SubscriptionID, id.ResourceGroupName, id.Parent.Parent.Name, id.Parent.Name)
	default:
		return fmt.Errorf("unsupported *Desire parent resource type: %s", parentType)
	}
	if err != nil {
		return err
	}
	_, err = applyDesireCRUD.Create(ctx, desire, nil)
	return err
}
