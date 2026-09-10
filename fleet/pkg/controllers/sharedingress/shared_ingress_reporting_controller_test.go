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

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/fleetcosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/fleetcosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/fleetlistertesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/kubeapplierlistertesting"
)

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

// seedReportingDB creates a mock fleet DB seeded with a ManagementCluster and a
// SliceManagementClusterLister backed by the stored object (carrying the current
// etag) so lister reads and optimistic-concurrency Replaces stay consistent.
func seedReportingDB(ctx context.Context, t *testing.T, existingIPs []string) (*fleetcosmosstoragetesting.MockFleetDBClient, *fleetlistertesting.SliceManagementClusterLister) {
	t.Helper()
	mockDB, err := fleetcosmosstoragetesting.NewMockFleetDBClientWithResources(ctx, []any{testManagementCluster(existingIPs)})
	require.NoError(t, err)
	stored, err := mockDB.Stamps().ManagementClusters(testStampIdentifier).Get(ctx, fleetapi.ManagementClusterResourceName)
	require.NoError(t, err)
	mcLister := &fleetlistertesting.SliceManagementClusterLister{ManagementClusters: []*fleetapi.ManagementCluster{stored}}
	return mockDB, mcLister
}

func TestSyncOnce_NotFoundReadDesire(t *testing.T) {
	ctx := context.Background()
	mockDB, mcLister := seedReportingDB(ctx, t, nil)

	syncer := &sharedIngressReportingSyncer{
		fleetDBClient:           mockDB,
		readDesireLister:        &kubeapplierlistertesting.SliceReadDesireLister{},
		managementClusterLister: mcLister,
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
	mockDB, mcLister := seedReportingDB(ctx, t, []string{"10.0.0.1"})

	syncer := &sharedIngressReportingSyncer{
		fleetDBClient:           mockDB,
		readDesireLister:        &kubeapplierlistertesting.SliceReadDesireLister{Desires: []*kubeapplierapi.ReadDesire{buildTestReadDesire(nil)}},
		managementClusterLister: mcLister,
	}

	require.NoError(t, syncer.SyncOnce(ctx, testKey()))

	mc, err := mockDB.Stamps().ManagementClusters(testStampIdentifier).Get(ctx, fleetapi.ManagementClusterResourceName)
	require.NoError(t, err)

	assert.Nil(t, mc.Status.SharedIngressIPAddresses, "IPs must be cleared when content is not mirrored")
	cond := apimeta.FindStatusCondition(mc.Status.Conditions, string(fleetapi.ManagementClusterConditionSharedIngressAvailable))
	require.NotNil(t, cond)
	assert.Equal(t, metav1.ConditionFalse, cond.Status)
	assert.Equal(t, string(fleetapi.ManagementClusterConditionReasonSharedIngressIPsNotMirrored), cond.Reason)
}

func TestSyncOnce_EmptyRawKubeContent_SetsNotMirroredAndClears(t *testing.T) {
	ctx := context.Background()
	// Seed with pre-existing IPs so we can prove they get cleared.
	mockDB, mcLister := seedReportingDB(ctx, t, []string{"10.0.0.1"})

	// KubeContent is non-nil but its Raw payload is empty — must be treated as
	// "not mirrored yet" and must not attempt to unmarshal empty bytes.
	desire := buildTestReadDesire(nil)
	desire.Status.KubeContent = &runtime.RawExtension{Raw: []byte{}}

	syncer := &sharedIngressReportingSyncer{
		fleetDBClient:           mockDB,
		readDesireLister:        &kubeapplierlistertesting.SliceReadDesireLister{Desires: []*kubeapplierapi.ReadDesire{desire}},
		managementClusterLister: mcLister,
	}

	require.NoError(t, syncer.SyncOnce(ctx, testKey()))

	mc, err := mockDB.Stamps().ManagementClusters(testStampIdentifier).Get(ctx, fleetapi.ManagementClusterResourceName)
	require.NoError(t, err)

	assert.Nil(t, mc.Status.SharedIngressIPAddresses, "IPs must be cleared when Raw is empty")
	cond := apimeta.FindStatusCondition(mc.Status.Conditions, string(fleetapi.ManagementClusterConditionSharedIngressAvailable))
	require.NotNil(t, cond)
	assert.Equal(t, metav1.ConditionFalse, cond.Status)
	assert.Equal(t, string(fleetapi.ManagementClusterConditionReasonSharedIngressIPsNotMirrored), cond.Reason)
}

func TestSyncOnce_WithIPs_SetsMirrored(t *testing.T) {
	ctx := context.Background()
	mockDB, mcLister := seedReportingDB(ctx, t, nil)

	// Includes an empty IP that must be skipped, plus two real IPs.
	syncer := &sharedIngressReportingSyncer{
		fleetDBClient:           mockDB,
		readDesireLister:        &kubeapplierlistertesting.SliceReadDesireLister{Desires: []*kubeapplierapi.ReadDesire{buildTestReadDesire(serviceWithIngressIPs("20.1.2.3", "", "20.4.5.6"))}},
		managementClusterLister: mcLister,
	}

	require.NoError(t, syncer.SyncOnce(ctx, testKey()))

	mc, err := mockDB.Stamps().ManagementClusters(testStampIdentifier).Get(ctx, fleetapi.ManagementClusterResourceName)
	require.NoError(t, err)

	assert.Equal(t, []string{"20.1.2.3", "20.4.5.6"}, mc.Status.SharedIngressIPAddresses)
	cond := apimeta.FindStatusCondition(mc.Status.Conditions, string(fleetapi.ManagementClusterConditionSharedIngressAvailable))
	require.NotNil(t, cond)
	assert.Equal(t, metav1.ConditionTrue, cond.Status)
	assert.Equal(t, string(fleetapi.ManagementClusterConditionReasonSharedIngressIPsAvailable), cond.Reason)
}

func TestSyncOnce_NoIPs_SetsUnavailableAndClears(t *testing.T) {
	ctx := context.Background()
	// Seed with pre-existing IPs so we can prove they get cleared.
	mockDB, mcLister := seedReportingDB(ctx, t, []string{"10.0.0.1"})

	// Service present but with no (or only empty) load balancer ingress IPs.
	syncer := &sharedIngressReportingSyncer{
		fleetDBClient:           mockDB,
		readDesireLister:        &kubeapplierlistertesting.SliceReadDesireLister{Desires: []*kubeapplierapi.ReadDesire{buildTestReadDesire(serviceWithIngressIPs(""))}},
		managementClusterLister: mcLister,
	}

	require.NoError(t, syncer.SyncOnce(ctx, testKey()))

	mc, err := mockDB.Stamps().ManagementClusters(testStampIdentifier).Get(ctx, fleetapi.ManagementClusterResourceName)
	require.NoError(t, err)

	assert.Nil(t, mc.Status.SharedIngressIPAddresses, "IPs must be cleared when shared ingress is unavailable")
	cond := apimeta.FindStatusCondition(mc.Status.Conditions, string(fleetapi.ManagementClusterConditionSharedIngressAvailable))
	require.NotNil(t, cond)
	assert.Equal(t, metav1.ConditionFalse, cond.Status)
	assert.Equal(t, string(fleetapi.ManagementClusterConditionReasonSharedIngressIPsUnavailable), cond.Reason)
}

func TestSyncOnce_PreconditionFailedOnReplaceReturnsNil(t *testing.T) {
	ctx := context.Background()
	mockDB, mcLister := seedReportingDB(ctx, t, nil)
	// Wrap the real mock so the ManagementCluster Replace fails with 412.
	db := &preconditionReplaceFleetDB{FleetDBClient: mockDB}

	// A service with IPs so the syncer computes an update and attempts a Replace.
	syncer := &sharedIngressReportingSyncer{
		fleetDBClient:           db,
		readDesireLister:        &kubeapplierlistertesting.SliceReadDesireLister{Desires: []*kubeapplierapi.ReadDesire{buildTestReadDesire(serviceWithIngressIPs("20.1.2.3"))}},
		managementClusterLister: mcLister,
	}

	// Replace returns 412 Precondition Failed → swallowed, SyncOnce returns nil.
	require.NoError(t, syncer.SyncOnce(ctx, testKey()))
}

// --- Test double: FleetDBClient whose ManagementCluster Replace fails with 412 ---

// preconditionReplaceFleetDB delegates every operation to a real mock except the
// ManagementCluster Replace, which returns PreconditionFailed to simulate a lost
// optimistic-concurrency race.
type preconditionReplaceFleetDB struct {
	fleetcosmosstorage.FleetDBClient
}

func (f *preconditionReplaceFleetDB) Stamps() fleetcosmosstorage.StampsCRUD {
	return &preconditionReplaceStamps{StampsCRUD: f.FleetDBClient.Stamps()}
}

type preconditionReplaceStamps struct {
	fleetcosmosstorage.StampsCRUD
}

func (s *preconditionReplaceStamps) ManagementClusters(stampIdentifier string) fleetcosmosstorage.ManagementClustersCRUD {
	return &preconditionReplaceMCCRUD{ManagementClustersCRUD: s.StampsCRUD.ManagementClusters(stampIdentifier)}
}

type preconditionReplaceMCCRUD struct {
	fleetcosmosstorage.ManagementClustersCRUD
}

func (m *preconditionReplaceMCCRUD) Replace(_ context.Context, _, _ *fleetapi.ManagementCluster, _ *azcosmos.ItemOptions) (*fleetapi.ManagementCluster, error) {
	return nil, &azcore.ResponseError{StatusCode: http.StatusPreconditionFailed}
}
