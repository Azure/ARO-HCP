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

package sharedingress

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

	fleetcontrollers "github.com/Azure/ARO-HCP/fleet/pkg/controllers/base"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/kubeappliercosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/fleetcosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/kubeappliercosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/kubeapplierlistertesting"
)

const testStampIdentifier = "s1"

func testKey() fleetcontrollers.StampKey {
	return fleetcontrollers.StampKey{StampIdentifier: testStampIdentifier}
}

func testManagementClusterResourceID() *azcorearm.ResourceID {
	return metadataapi.Must(fleetapi.ToManagementClusterResourceID(testStampIdentifier))
}

// testManagementCluster builds a valid ManagementCluster (all required status
// fields populated so the mock DB accepts it on create), with optional
// pre-existing shared-ingress IPs and conditions.
func testManagementCluster(existingIPs []string, conditions ...metav1.Condition) *fleetapi.ManagementCluster {
	aksResourceID := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.ContainerService/managedClusters/mc"))
	dnsResourceID := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/dns-rg/providers/Microsoft.Network/dnszones/example.com"))
	placeholderShardID := metadataapi.Must(metadataapi.NewInternalID("/api/aro_hcp/v1alpha1/provision_shards/placeholder"))
	return &fleetapi.ManagementCluster{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   testManagementClusterResourceID(),
			PartitionKey: strings.ToLower(testStampIdentifier),
		},
		Spec: fleetapi.ManagementClusterSpec{
			SchedulingPolicy: fleetapi.ManagementClusterSchedulingPolicySchedulable,
		},
		Status: fleetapi.ManagementClusterStatus{
			AKSResourceID:                                        aksResourceID,
			PublicDNSZoneResourceID:                              dnsResourceID,
			HostedClustersSecretsKeyVaultURL:                     "https://kv-secrets.vault.azure.net",
			HostedClustersManagedIdentitiesKeyVaultURL:           "https://kv-mi.vault.azure.net",
			HostedClustersSecretsKeyVaultManagedIdentityClientID: "12345678-1234-1234-1234-123456789012",
			ClusterServiceProvisionShardID:                       &placeholderShardID,
			MaestroConsumerName:                                  "consumer-1",
			MaestroRESTAPIURL:                                    "http://maestro:8000",
			MaestroGRPCTarget:                                    "maestro:8090",
			KubeApplierCosmosContainerName:                       "kube-applier-test",
			SharedIngressIPAddresses:                             existingIPs,
			Conditions:                                           conditions,
		},
	}
}

func buildTestReadDesire(service *corev1.Service) *kubeapplierapi.ReadDesire {
	desireIDString := kubeapplierapi.ToManagementClusterScopedReadDesireResourceIDString(testStampIdentifier, ReadDesireName)
	desired := controllerutil.BuildReadDesire(desireIDString, testManagementClusterResourceID(), SharedIngressTarget)
	if service != nil {
		raw, _ := json.Marshal(service)
		desired.Status.KubeContent = &runtime.RawExtension{Raw: raw}
	}
	return desired
}

func serviceWithIngressIPs(ips ...string) *corev1.Service {
	svc := &corev1.Service{}
	for _, ip := range ips {
		svc.Status.LoadBalancer.Ingress = append(svc.Status.LoadBalancer.Ingress, corev1.LoadBalancerIngress{IP: ip})
	}
	return svc
}

// --- EnsureReadDesire tests ---

func TestEnsureReadDesire_NilClient(t *testing.T) {
	clients := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClients()
	syncer := &ensureReadDesireSyncer{kubeApplierDBClients: clients}

	err := syncer.SyncOnce(context.Background(), testKey())
	require.NoError(t, err)
}

func TestEnsureReadDesire_CreatesReadDesireOnFirstCall(t *testing.T) {
	mockClient := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClient()
	clients := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClients()
	clients.Register(testManagementClusterResourceID(), mockClient)

	syncer := &ensureReadDesireSyncer{kubeApplierDBClients: clients}

	err := syncer.SyncOnce(context.Background(), testKey())
	require.NoError(t, err)

	crud, err := mockClient.ReadDesiresForManagementCluster(testStampIdentifier)
	require.NoError(t, err)

	existing, err := crud.Get(context.Background(), ReadDesireName)
	require.NoError(t, err)
	assert.Equal(t, SharedIngressTarget, existing.Spec.TargetItem)
}

func TestEnsureReadDesire_UpdatesStaleSpec(t *testing.T) {
	mockClient := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClient()
	clients := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClients()
	clients.Register(testManagementClusterResourceID(), mockClient)

	staleTarget := kubeapplierapi.ResourceReference{
		Group:    "old.group",
		Version:  "v1",
		Resource: "oldresources",
		Name:     "old",
	}
	desireIDString := kubeapplierapi.ToManagementClusterScopedReadDesireResourceIDString(testStampIdentifier, ReadDesireName)
	stale := controllerutil.BuildReadDesire(desireIDString, testManagementClusterResourceID(), staleTarget)

	crud, err := mockClient.ReadDesiresForManagementCluster(testStampIdentifier)
	require.NoError(t, err)
	_, err = crud.Create(context.Background(), stale, nil)
	require.NoError(t, err)

	syncer := &ensureReadDesireSyncer{kubeApplierDBClients: clients}

	err = syncer.SyncOnce(context.Background(), testKey())
	require.NoError(t, err)

	updated, err := crud.Get(context.Background(), ReadDesireName)
	require.NoError(t, err)
	assert.Equal(t, SharedIngressTarget, updated.Spec.TargetItem)
}

func TestEnsureReadDesire_ConflictOnCreateIsSwallowed(t *testing.T) {
	clients := &conflictOnCreateDBClients{}
	syncer := &ensureReadDesireSyncer{kubeApplierDBClients: clients}

	err := syncer.SyncOnce(context.Background(), testKey())
	require.NoError(t, err)
}

// --- SharedIngressReporting SyncOnce tests ---

func TestSyncOnce_NotFoundReadDesire(t *testing.T) {
	ctx := context.Background()
	mockDB, err := fleetcosmosstoragetesting.NewMockFleetDBClientWithResources(ctx, []any{testManagementCluster(nil)})
	require.NoError(t, err)

	lister := &kubeapplierlistertesting.SliceReadDesireLister{}
	syncer := &sharedIngressReportingSyncer{
		fleetDBClient:    mockDB,
		readDesireLister: lister,
	}

	require.NoError(t, syncer.SyncOnce(ctx, testKey()))

	mc, err := mockDB.Stamps().ManagementClusters(testStampIdentifier).Get(ctx, fleetapi.ManagementClusterResourceName)
	require.NoError(t, err)
	// ReadDesire not found: controller is a no-op, so no condition is set.
	assert.Nil(t, apimeta.FindStatusCondition(mc.Status.Conditions, string(fleetapi.ManagementClusterConditionSharedIngressAvailable)))
	assert.Nil(t, mc.Status.SharedIngressIPAddresses)
}

func TestSyncOnce_NilKubeContent_SetsNotMirroredAndClears(t *testing.T) {
	ctx := context.Background()
	// Seed with pre-existing IPs so we can prove they get cleared.
	mockDB, err := fleetcosmosstoragetesting.NewMockFleetDBClientWithResources(ctx, []any{testManagementCluster([]string{"10.0.0.1"})})
	require.NoError(t, err)

	lister := &kubeapplierlistertesting.SliceReadDesireLister{
		Desires: []*kubeapplierapi.ReadDesire{buildTestReadDesire(nil)},
	}
	syncer := &sharedIngressReportingSyncer{
		fleetDBClient:    mockDB,
		readDesireLister: lister,
	}

	require.NoError(t, syncer.SyncOnce(ctx, testKey()))

	mc, err := mockDB.Stamps().ManagementClusters(testStampIdentifier).Get(ctx, fleetapi.ManagementClusterResourceName)
	require.NoError(t, err)

	assert.Nil(t, mc.Status.SharedIngressIPAddresses, "IPs must be cleared when content is not mirrored")
	cond := apimeta.FindStatusCondition(mc.Status.Conditions, string(fleetapi.ManagementClusterConditionSharedIngressAvailable))
	require.NotNil(t, cond)
	assert.Equal(t, metav1.ConditionFalse, cond.Status)
	assert.Equal(t, string(fleetapi.ManagementClusterConditionReasonSharedIngressNotMirrored), cond.Reason)
}

func TestSyncOnce_WithIPs_SetsMirrored(t *testing.T) {
	ctx := context.Background()
	mockDB, err := fleetcosmosstoragetesting.NewMockFleetDBClientWithResources(ctx, []any{testManagementCluster(nil)})
	require.NoError(t, err)

	// Includes an empty IP that must be skipped, plus two real IPs.
	lister := &kubeapplierlistertesting.SliceReadDesireLister{
		Desires: []*kubeapplierapi.ReadDesire{buildTestReadDesire(serviceWithIngressIPs("20.1.2.3", "", "20.4.5.6"))},
	}
	syncer := &sharedIngressReportingSyncer{
		fleetDBClient:    mockDB,
		readDesireLister: lister,
	}

	require.NoError(t, syncer.SyncOnce(ctx, testKey()))

	mc, err := mockDB.Stamps().ManagementClusters(testStampIdentifier).Get(ctx, fleetapi.ManagementClusterResourceName)
	require.NoError(t, err)

	assert.Equal(t, []string{"20.1.2.3", "20.4.5.6"}, mc.Status.SharedIngressIPAddresses)
	cond := apimeta.FindStatusCondition(mc.Status.Conditions, string(fleetapi.ManagementClusterConditionSharedIngressAvailable))
	require.NotNil(t, cond)
	assert.Equal(t, metav1.ConditionTrue, cond.Status)
	assert.Equal(t, string(fleetapi.ManagementClusterConditionReasonSharedIngressMirrored), cond.Reason)
}

func TestSyncOnce_NoIPs_SetsUnavailableAndClears(t *testing.T) {
	ctx := context.Background()
	// Seed with pre-existing IPs so we can prove they get cleared.
	mockDB, err := fleetcosmosstoragetesting.NewMockFleetDBClientWithResources(ctx, []any{testManagementCluster([]string{"10.0.0.1"})})
	require.NoError(t, err)

	// Service present but with no (or only empty) load balancer ingress IPs.
	lister := &kubeapplierlistertesting.SliceReadDesireLister{
		Desires: []*kubeapplierapi.ReadDesire{buildTestReadDesire(serviceWithIngressIPs(""))},
	}
	syncer := &sharedIngressReportingSyncer{
		fleetDBClient:    mockDB,
		readDesireLister: lister,
	}

	require.NoError(t, syncer.SyncOnce(ctx, testKey()))

	mc, err := mockDB.Stamps().ManagementClusters(testStampIdentifier).Get(ctx, fleetapi.ManagementClusterResourceName)
	require.NoError(t, err)

	assert.Nil(t, mc.Status.SharedIngressIPAddresses, "IPs must be cleared when shared ingress is unavailable")
	cond := apimeta.FindStatusCondition(mc.Status.Conditions, string(fleetapi.ManagementClusterConditionSharedIngressAvailable))
	require.NotNil(t, cond)
	assert.Equal(t, metav1.ConditionFalse, cond.Status)
	assert.Equal(t, string(fleetapi.ManagementClusterConditionReasonSharedIngressUnavailable), cond.Reason)
}

// --- Test doubles for conflict-on-create scenario ---

// conflictOnCreateDBClients implements KubeApplierDBClients, returning a
// client whose ReadDesiresForManagementCluster CRUD returns NotFound on Get
// and Conflict on Create — simulating a race where another controller wins
// the create.
type conflictOnCreateDBClients struct{}

func (c *conflictOnCreateDBClients) For(_ context.Context, _ *azcorearm.ResourceID) kubeappliercosmosstorage.KubeApplierDBClient {
	return &conflictOnCreateDBClient{}
}

type conflictOnCreateDBClient struct {
	kubeappliercosmosstorage.KubeApplierDBClient // embedded nil — only ReadDesiresForManagementCluster is called
}

func (c *conflictOnCreateDBClient) ReadDesiresForManagementCluster(_ string) (cosmosstorageutils.ResourceCRUD[kubeapplierapi.ReadDesire, *kubeapplierapi.ReadDesire], error) {
	return &notFoundThenConflictCRUD{}, nil
}

type notFoundThenConflictCRUD struct {
	cosmosstorageutils.ResourceCRUD[kubeapplierapi.ReadDesire, *kubeapplierapi.ReadDesire] // embedded nil — only Get and Create are called
}

func (c *notFoundThenConflictCRUD) Get(_ context.Context, _ string) (*kubeapplierapi.ReadDesire, error) {
	return nil, cosmosstorageutils.NewNotFoundError()
}

func (c *notFoundThenConflictCRUD) Create(_ context.Context, _ *kubeapplierapi.ReadDesire, _ *azcosmos.ItemOptions) (*kubeapplierapi.ReadDesire, error) {
	return nil, &azcore.ResponseError{StatusCode: http.StatusConflict}
}
