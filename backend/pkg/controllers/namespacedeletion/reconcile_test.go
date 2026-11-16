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

package namespacedeletion

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/cache"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"

	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
	backendcontrollerutils "github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/arohcp-cosmos-client/api/coreapi"
	"github.com/Azure/ARO-HCP/arohcp-cosmos-client/api/fleetapi"
	"github.com/Azure/ARO-HCP/arohcp-cosmos-client/api/kubeapplierapi"
	"github.com/Azure/ARO-HCP/arohcp-cosmos-client/apihelpers/coreapihelpers"
	"github.com/Azure/ARO-HCP/arohcp-cosmos-client/apihelpers/fleetapihelpers"
	"github.com/Azure/ARO-HCP/arohcp-cosmos-client/apihelpers/kubeapplierapihelpers"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/fleetcosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/fleetcosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/kubeappliercosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listers/kubeapplierlisters"
	"github.com/Azure/ARO-HCP/internal/utils"
)

var errInjectedFleetWrite = errors.New("injected Fleet status write failure")

type failingFleetClient struct {
	fleetcosmosstorage.FleetDBClient
}

func (c failingFleetClient) Stamps() fleetcosmosstorage.StampsCRUD {
	return failingStampsCRUD{StampsCRUD: c.FleetDBClient.Stamps()}
}

type failingStampsCRUD struct{ fleetcosmosstorage.StampsCRUD }

func (c failingStampsCRUD) ManagementClusters(stamp string) fleetcosmosstorage.ManagementClustersCRUD {
	return failingManagementClustersCRUD{ManagementClustersCRUD: c.StampsCRUD.ManagementClusters(stamp)}
}

type failingManagementClustersCRUD struct {
	fleetcosmosstorage.ManagementClustersCRUD
}

func (c failingManagementClustersCRUD) NamespaceDeletions() cosmosstorageutils.ValidatingResourceCRUD[fleetapi.NamespaceDeletion, *fleetapi.NamespaceDeletion] {
	return failingNamespaceDeletionCRUD{ValidatingResourceCRUD: c.ManagementClustersCRUD.NamespaceDeletions()}
}

type failingNamespaceDeletionCRUD struct {
	cosmosstorageutils.ValidatingResourceCRUD[fleetapi.NamespaceDeletion, *fleetapi.NamespaceDeletion]
}

func (failingNamespaceDeletionCRUD) Replace(context.Context, *fleetapi.NamespaceDeletion, *fleetapi.NamespaceDeletion, *azcosmos.ItemOptions) (*fleetapi.NamespaceDeletion, error) {
	return nil, errInjectedFleetWrite
}

func observedReadDesire(t *testing.T, stamp, name string, target kubeapplierapi.ResourceReference, object any) *kubeapplierapi.ReadDesire {
	t.Helper()
	managementCluster, err := fleetapihelpers.ToManagementClusterResourceID(stamp)
	require.NoError(t, err)
	desire := controllerutil.BuildReadDesire(
		kubeapplierapihelpers.ToManagementClusterScopedReadDesireResourceIDString(stamp, name), managementCluster, target)
	meta.SetStatusCondition(&desire.Status.Conditions, metav1.Condition{
		Type: kubeapplierapi.ConditionTypeSuccessful, Status: metav1.ConditionTrue, Reason: "NoErrors", Message: "observed",
	})
	if object != nil {
		content, err := json.Marshal(object)
		require.NoError(t, err)
		desire.Status.KubeContent = &runtime.RawExtension{Raw: content}
	}
	return desire
}

func TestNamespaceDeletionRejectsOtherEnvironmentBeforePublishingDesires(t *testing.T) {
	ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
	const stamp = "stamp"
	namespaceName := "ocm-arohcpstg-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	key := backendcontrollerutils.NamespaceDeletionKey{StampIdentifier: stamp, Name: namespaceName}
	resourceID, err := fleetapihelpers.ToNamespaceDeletionResourceID(stamp, namespaceName)
	require.NoError(t, err)
	request := &fleetapi.NamespaceDeletion{
		CosmosMetadata: coreapi.CosmosMetadata{ResourceID: resourceID, PartitionKey: stamp},
		Spec: fleetapi.NamespaceDeletionSpec{
			ManagementClusterName:      fleetapi.ManagementClusterResourceName,
			HostedClusterNamespaceName: namespaceName,
			HostedClusterName:          "hcp1",
			RequestReason:              "test environment guard",
		},
	}
	fleetDB, err := fleetcosmosstoragetesting.NewMockFleetDBClientWithResources(ctx, []any{request})
	require.NoError(t, err)
	managementCluster, err := fleetapihelpers.ToManagementClusterResourceID(stamp)
	require.NoError(t, err)
	client := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClient()
	clients := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClients()
	clients.Register(managementCluster, client)
	syncer := &namespaceDeletionSyncer{
		fleetDBClient: fleetDB, kubeApplierDBClients: clients,
		config: Config{EnvIdentifier: "arohcpprod"},
	}
	require.NoError(t, syncer.SyncOnce(ctx, key))
	stored, err := fleetDB.Stamps().ManagementClusters(stamp).NamespaceDeletions().Get(ctx, namespaceName)
	require.NoError(t, err)
	require.True(t, meta.IsStatusConditionTrue(stored.Status.Conditions, fleetapi.ConditionTypeNamespaceDeletionStopped))
	readCRUD, err := client.ReadDesiresForManagementCluster(stamp)
	require.NoError(t, err)
	_, err = readCRUD.Get(ctx, ReadDesireNameFor(namespaceName))
	require.True(t, cosmosstorageutils.IsNotFoundError(err), "a wrong-environment request must not publish a read desire")
	applyCRUD, err := client.ApplyDesiresForManagementCluster(stamp)
	require.NoError(t, err)
	_, err = applyCRUD.Get(ctx, UnblockHostedClusterDesireNameFor(namespaceName))
	require.True(t, cosmosstorageutils.IsNotFoundError(err), "a wrong-environment request must not publish a mutation desire")
}

func TestNamespaceDeletionUnavailableOwnershipIsRetryable(t *testing.T) {
	request := &fleetapi.NamespaceDeletion{}
	stop := &ownershipStop{degraded: true, reason: reasonOwnershipCheckFailed, message: "Cosmos lookup failed"}
	err := stop.apply(request)
	require.ErrorContains(t, err, "Cosmos lookup failed")
	require.True(t, meta.IsStatusConditionTrue(request.Status.Conditions, fleetapi.ConditionTypeNamespaceDeletionDegraded))
	require.False(t, meta.IsStatusConditionTrue(request.Status.Conditions, fleetapi.ConditionTypeNamespaceDeletionStopped))
}

func TestNamespaceDeletionFinalizeLegacyOwnerOutcomes(t *testing.T) {
	for _, tc := range []struct {
		name, reason, message string
		wantStopped           bool
	}{
		{name: "owner present", reason: kubeapplierapi.ConditionReasonLegacyOwnerPresent,
			message: "ManifestWork still references the namespace", wantStopped: true},
		{name: "lookup failed", reason: kubeapplierapi.ConditionReasonLegacyOwnerLookupFailed,
			message: "list ManifestWorks: temporary timeout"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := &fleetapi.NamespaceDeletion{}
			condition := &metav1.Condition{
				Type: kubeapplierapi.ConditionTypeNamespaceFinalized, Status: metav1.ConditionFalse,
				Reason: tc.reason, Message: tc.message,
			}
			handled, err := handleFinalizeCondition(request, condition)
			require.True(t, handled)
			if tc.wantStopped {
				require.NoError(t, err)
				require.True(t, meta.IsStatusConditionTrue(request.Status.Conditions, fleetapi.ConditionTypeNamespaceDeletionStopped))
				return
			}
			require.Error(t, err, "a transient owner lookup failure must requeue the request")
			require.True(t, meta.IsStatusConditionTrue(request.Status.Conditions, fleetapi.ConditionTypeNamespaceDeletionDegraded))
			require.False(t, meta.IsStatusConditionTrue(request.Status.Conditions, fleetapi.ConditionTypeNamespaceDeletionStopped))
		})
	}
}

func TestNamespaceDeletionPersistsIdentityAndIntentBeforeMutation(t *testing.T) {
	ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
	const (
		stamp             = "stamp"
		hostedClusterName = "hcp1"
		clusterID         = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		subscriptionID    = "11111111-1111-1111-1111-111111111111"
	)
	namespaceName := "ocm-arohcpprod-" + clusterID
	cpNamespaceName := namespaceName + "-" + hostedClusterName
	key := backendcontrollerutils.NamespaceDeletionKey{StampIdentifier: stamp, Name: namespaceName}
	requestResourceID, err := fleetapihelpers.ToNamespaceDeletionResourceID(stamp, namespaceName)
	require.NoError(t, err)
	request := &fleetapi.NamespaceDeletion{
		CosmosMetadata: coreapi.CosmosMetadata{ResourceID: requestResourceID, PartitionKey: stamp},
		Spec: fleetapi.NamespaceDeletionSpec{
			ManagementClusterName:      fleetapi.ManagementClusterResourceName,
			HostedClusterNamespaceName: namespaceName, HostedClusterName: hostedClusterName,
			RequestReason: "test stuck cluster cleanup",
		},
	}
	fleetDB, err := fleetcosmosstoragetesting.NewMockFleetDBClientWithResources(ctx, []any{request})
	require.NoError(t, err)
	resourcesDB := corecosmosstoragetesting.NewMockResourcesDBClient()
	subscriptionARMID, err := azcorearm.ParseResourceID("/subscriptions/" + subscriptionID)
	require.NoError(t, err)
	tenantID := "tenant"
	_, err = resourcesDB.Subscriptions().Create(ctx, &coreapi.Subscription{
		CosmosMetadata: coreapi.CosmosMetadata{ResourceID: subscriptionARMID, PartitionKey: subscriptionID},
		Properties:     &coreapi.SubscriptionProperties{TenantId: &tenantID},
	}, nil)
	require.NoError(t, err)

	managementCluster, err := fleetapihelpers.ToManagementClusterResourceID(stamp)
	require.NoError(t, err)
	client := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClient()
	clients := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClients()
	clients.Register(managementCluster, client)
	readIndexer := cache.NewIndexer(func(obj interface{}) (string, error) {
		return strings.ToLower(obj.(*kubeapplierapi.ReadDesire).ResourceID.String()), nil
	}, cache.Indexers{})
	applyIndexer := cache.NewIndexer(func(obj interface{}) (string, error) {
		return strings.ToLower(obj.(*kubeapplierapi.ApplyDesire).ResourceID.String()), nil
	}, cache.Indexers{})
	deletedAt := time.Now().Add(-2 * time.Hour).UTC().Format(time.RFC3339)
	objects := []struct {
		name   string
		target kubeapplierapi.ResourceReference
		object any
	}{
		{ReadDesireNameFor(namespaceName), namespaceTargetFor(namespaceName), map[string]any{
			"apiVersion": "v1", "kind": "Namespace", "metadata": map[string]any{
				"name": namespaceName, "uid": "namespace-uid", "deletionTimestamp": deletedAt,
			},
		}},
		{HostedClusterReadDesireNameFor(namespaceName), hostedClusterTargetFor(namespaceName, hostedClusterName), map[string]any{
			"apiVersion": "hypershift.openshift.io/v1beta1", "kind": "HostedCluster", "metadata": map[string]any{
				"name": hostedClusterName, "namespace": namespaceName, "uid": "hosted-cluster-uid", "deletionTimestamp": deletedAt,
			},
			"spec": map[string]any{"platform": map[string]any{"azure": map[string]any{
				"subscriptionID": subscriptionID, "resourceGroup": "managed-rg",
			}}},
		}},
		{ControlPlaneNamespaceReadDesireNameFor(namespaceName), namespaceTargetFor(cpNamespaceName), map[string]any{
			"apiVersion": "v1", "kind": "Namespace", "metadata": map[string]any{
				"name": cpNamespaceName, "uid": "control-plane-uid", "deletionTimestamp": deletedAt,
			},
		}},
	}
	readCRUD, err := client.ReadDesiresForManagementCluster(stamp)
	require.NoError(t, err)
	for _, item := range objects {
		desire := observedReadDesire(t, stamp, item.name, item.target, item.object)
		_, err := readCRUD.Create(ctx, desire, nil)
		require.NoError(t, err)
		require.NoError(t, readIndexer.Add(desire))
	}

	ctrl := gomock.NewController(t)
	builder := azureclient.NewMockFirstPartyApplicationClientBuilder(ctrl)
	rgClient := azureclient.NewMockResourceGroupsClient(ctrl)
	builder.EXPECT().ResourceGroupsClient(tenantID, subscriptionID).Return(rgClient, nil).AnyTimes()
	rgClient.EXPECT().Get(gomock.Any(), "managed-rg", nil).
		Return(armresources.ResourceGroupsClientGetResponse{}, &azcore.ResponseError{StatusCode: 404}).AnyTimes()
	syncer := &namespaceDeletionSyncer{
		fleetDBClient: fleetDB, resourcesDBClient: resourcesDB, kubeApplierDBClients: clients,
		readDesireLister:      kubeapplierlisters.NewReadDesireLister(readIndexer),
		applyDesireLister:     kubeapplierlisters.NewApplyDesireLister(applyIndexer),
		azureFPAClientBuilder: builder,
		config:                Config{EnvIdentifier: "arohcpprod", MinimumDwell: time.Hour},
	}
	applyCRUD, err := client.ApplyDesiresForManagementCluster(stamp)
	require.NoError(t, err)
	for pass := 1; pass <= 3; pass++ {
		require.NoError(t, syncer.SyncOnce(ctx, key))
		_, err := applyCRUD.Get(ctx, UnblockHostedClusterDesireNameFor(namespaceName))
		require.Error(t, err, "pass %d must only persist identity or intent", pass)
	}
	// The intent write fails after all UIDs were persisted. No action may be
	// published from the in-memory intent. A fresh controller must recover by
	// persisting that intent before it creates the desire.
	syncer.fleetDBClient = failingFleetClient{FleetDBClient: fleetDB}
	require.ErrorIs(t, syncer.SyncOnce(ctx, key), errInjectedFleetWrite)
	_, err = applyCRUD.Get(ctx, UnblockHostedClusterDesireNameFor(namespaceName))
	require.Error(t, err, "failed Fleet status write must not publish a cleanup desire")
	persisted, err := fleetDB.Stamps().ManagementClusters(stamp).NamespaceDeletions().Get(ctx, namespaceName)
	require.NoError(t, err)
	require.Empty(t, persisted.Status.CleanupIntent)
	restarted := *syncer
	restarted.fleetDBClient = fleetDB
	syncer = &restarted
	require.NoError(t, syncer.SyncOnce(ctx, key))
	_, err = applyCRUD.Get(ctx, UnblockHostedClusterDesireNameFor(namespaceName))
	require.Error(t, err, "restart pass must persist intent before publishing the desire")
	persisted, err = fleetDB.Stamps().ManagementClusters(stamp).NamespaceDeletions().Get(ctx, namespaceName)
	require.NoError(t, err)
	require.Equal(t, "namespace-uid", persisted.Status.NamespaceUID)
	require.Equal(t, "hosted-cluster-uid", persisted.Status.HostedClusterUID)
	require.Equal(t, "control-plane-uid", persisted.Status.ControlPlaneNamespaceUID)
	require.Equal(t, "UnblockHostedCluster", persisted.Status.CleanupIntent)
	// Reconstruct the controller after the intent was committed. The next
	// reconcile must use that durable binding to publish the action.
	afterIntentRestart := *syncer
	syncer = &afterIntentRestart
	require.NoError(t, syncer.SyncOnce(ctx, key))
	desire, err := applyCRUD.Get(ctx, UnblockHostedClusterDesireNameFor(namespaceName))
	require.NoError(t, err)
	require.Equal(t, persisted.Status.NamespaceUID, desire.Spec.UnblockHostedCluster.NamespaceUID)
	require.Equal(t, persisted.Status.HostedClusterUID, desire.Spec.UnblockHostedCluster.HostedClusterUID)

	// A newly appearing Cosmos owner must stop this request and retire the
	// already published cleanup desire before the stopped status is persisted.
	spcResourceID, err := azcorearm.ParseResourceID(coreapihelpers.ToServiceProviderClusterResourceIDString(subscriptionID, "rg", "cluster"))
	require.NoError(t, err)
	spc := &coreapi.ServiceProviderCluster{
		CosmosMetadata: coreapi.CosmosMetadata{ResourceID: spcResourceID, PartitionKey: subscriptionID},
	}
	spc.Spec.ManagementClusterResourceID = managementCluster
	spc.Status.HostedClusterNamespace = namespaceName
	_, err = resourcesDB.ServiceProviderClusters(subscriptionID, "rg", "cluster").Create(ctx, spc, nil)
	require.NoError(t, err)
	require.NoError(t, syncer.SyncOnce(ctx, key))
	persisted, err = fleetDB.Stamps().ManagementClusters(stamp).NamespaceDeletions().Get(ctx, namespaceName)
	require.NoError(t, err)
	require.True(t, meta.IsStatusConditionTrue(persisted.Status.Conditions, fleetapi.ConditionTypeNamespaceDeletionStopped))
	_, err = applyCRUD.Get(ctx, UnblockHostedClusterDesireNameFor(namespaceName))
	require.Error(t, err, "the stopped request must retire its destructive desire")
}

func TestNamespaceDeletionCompletesOnlyAfterObservedNamespaceAbsence(t *testing.T) {
	ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
	const stamp = "stamp"
	const hostedClusterName = "hcp1"
	namespaceName := "ocm-arohcpprod-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	key := backendcontrollerutils.NamespaceDeletionKey{StampIdentifier: stamp, Name: namespaceName}
	requestResourceID, err := fleetapihelpers.ToNamespaceDeletionResourceID(stamp, namespaceName)
	require.NoError(t, err)
	request := &fleetapi.NamespaceDeletion{
		CosmosMetadata: coreapi.CosmosMetadata{ResourceID: requestResourceID, PartitionKey: stamp},
		Spec: fleetapi.NamespaceDeletionSpec{
			ManagementClusterName:      fleetapi.ManagementClusterResourceName,
			HostedClusterNamespaceName: namespaceName,
			HostedClusterName:          hostedClusterName,
			RequestReason:              "complete an orphaned namespace cleanup",
		},
	}
	fleetDB, err := fleetcosmosstoragetesting.NewMockFleetDBClientWithResources(ctx, []any{request})
	require.NoError(t, err)
	managementCluster, err := fleetapihelpers.ToManagementClusterResourceID(stamp)
	require.NoError(t, err)
	client := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClient()
	clients := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClients()
	clients.Register(managementCluster, client)
	readIndexer := cache.NewIndexer(func(obj interface{}) (string, error) {
		return strings.ToLower(obj.(*kubeapplierapi.ReadDesire).ResourceID.String()), nil
	}, cache.Indexers{})
	applyIndexer := cache.NewIndexer(func(obj interface{}) (string, error) {
		return strings.ToLower(obj.(*kubeapplierapi.ApplyDesire).ResourceID.String()), nil
	}, cache.Indexers{})
	readCRUD, err := client.ReadDesiresForManagementCluster(stamp)
	require.NoError(t, err)
	deletingNamespace := map[string]any{
		"apiVersion": "v1", "kind": "Namespace", "metadata": map[string]any{
			"name": namespaceName, "uid": "namespace-uid",
			"deletionTimestamp": time.Now().Add(-2 * time.Hour).UTC().Format(time.RFC3339),
		},
	}
	readTargets := []struct {
		name   string
		target kubeapplierapi.ResourceReference
		object any
	}{
		{ReadDesireNameFor(namespaceName), namespaceTargetFor(namespaceName), deletingNamespace},
		{HostedClusterReadDesireNameFor(namespaceName), hostedClusterTargetFor(namespaceName, hostedClusterName), nil},
		{ControlPlaneNamespaceReadDesireNameFor(namespaceName), namespaceTargetFor(namespaceName + "-" + hostedClusterName), nil},
	}
	for _, item := range readTargets {
		desire := observedReadDesire(t, stamp, item.name, item.target, item.object)
		_, err := readCRUD.Create(ctx, desire, nil)
		require.NoError(t, err)
		require.NoError(t, readIndexer.Add(desire))
	}
	syncer := &namespaceDeletionSyncer{
		fleetDBClient:        fleetDB,
		resourcesDBClient:    corecosmosstoragetesting.NewMockResourcesDBClient(),
		kubeApplierDBClients: clients,
		readDesireLister:     kubeapplierlisters.NewReadDesireLister(readIndexer),
		applyDesireLister:    kubeapplierlisters.NewApplyDesireLister(applyIndexer),
		config:               Config{EnvIdentifier: "arohcpprod", MinimumDwell: time.Hour},
	}
	applyCRUD, err := client.ApplyDesiresForManagementCluster(stamp)
	require.NoError(t, err)
	for range 3 {
		require.NoError(t, syncer.SyncOnce(ctx, key))
	}
	_, err = applyCRUD.Get(ctx, FinalizeDesireNameFor(namespaceName))
	require.NoError(t, err, "finalization must be scheduled after the namespace UID and intent are persisted")
	persisted, err := fleetDB.Stamps().ManagementClusters(stamp).NamespaceDeletions().Get(ctx, namespaceName)
	require.NoError(t, err)
	require.False(t, meta.IsStatusConditionTrue(persisted.Status.Conditions, fleetapi.ConditionTypeNamespaceDeletionComplete),
		"publishing the finalization desire is not completion")

	// Kube-applier has now observed the namespace absent. Only this later
	// observation may turn the Fleet request Complete.
	absentRead := observedReadDesire(t, stamp, ReadDesireNameFor(namespaceName), namespaceTargetFor(namespaceName), nil)
	require.NoError(t, readIndexer.Update(absentRead))
	require.NoError(t, syncer.SyncOnce(ctx, key))
	persisted, err = fleetDB.Stamps().ManagementClusters(stamp).NamespaceDeletions().Get(ctx, namespaceName)
	require.NoError(t, err)
	require.True(t, meta.IsStatusConditionTrue(persisted.Status.Conditions, fleetapi.ConditionTypeNamespaceDeletionComplete))
	require.NoError(t, syncer.SyncOnce(ctx, key))
	_, err = applyCRUD.Get(ctx, FinalizeDesireNameFor(namespaceName))
	require.Error(t, err, "settled request must retire its finalization desire")
}

func TestNamespaceDeletionRetryArchivesStopAndWaitsForOldAuthorization(t *testing.T) {
	ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
	const stamp = "stamp"
	namespaceName := "ocm-arohcpprod-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	key := backendcontrollerutils.NamespaceDeletionKey{StampIdentifier: stamp, Name: namespaceName}
	rid, err := fleetapihelpers.ToNamespaceDeletionResourceID(stamp, namespaceName)
	require.NoError(t, err)
	request := &fleetapi.NamespaceDeletion{
		CosmosMetadata: coreapi.CosmosMetadata{ResourceID: rid, PartitionKey: stamp},
		Spec: fleetapi.NamespaceDeletionSpec{
			ManagementClusterName:      fleetapi.ManagementClusterResourceName,
			HostedClusterNamespaceName: namespaceName,
			HostedClusterName:          "hcp1",
			RequestReason:              "initial cleanup",
		},
	}
	fleetDB, err := fleetcosmosstoragetesting.NewMockFleetDBClientWithResources(ctx, []any{request})
	require.NoError(t, err)
	crud := fleetDB.Stamps().ManagementClusters(stamp).NamespaceDeletions()
	stored, err := crud.Get(ctx, namespaceName)
	require.NoError(t, err)
	stopped := stored.DeepCopy()
	stopped.Status.NamespaceUID = "namespace-uid"
	stopped.Status.HostedClusterUID = "hosted-cluster-uid"
	stopped.Status.CleanupIntent = "UnblockHostedCluster"
	meta.SetStatusCondition(&stopped.Status.Conditions, metav1.Condition{
		Type: fleetapi.ConditionTypeNamespaceDeletionStopped, Status: metav1.ConditionTrue,
		Reason: "ManifestWorkPresent", Message: "work still owns the namespace",
	})
	recent := metav1.Now()
	stopped.Status.CompletedAt = &recent
	stored, err = crud.Replace(ctx, stopped, stored, nil)
	require.NoError(t, err)
	retry := stored.DeepCopy()
	retry.Spec.RetryGeneration = 1
	retry.Spec.RetryReason = "removed stale ManifestWork"
	_, err = crud.Replace(ctx, retry, stored, nil)
	require.NoError(t, err)

	managementCluster, err := fleetapihelpers.ToManagementClusterResourceID(stamp)
	require.NoError(t, err)
	client := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClient()
	clients := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClients()
	clients.Register(managementCluster, client)
	syncer := &namespaceDeletionSyncer{fleetDBClient: fleetDB, kubeApplierDBClients: clients}
	require.NoError(t, syncer.SyncOnce(ctx, key))
	stored, err = crud.Get(ctx, namespaceName)
	require.NoError(t, err)
	require.True(t, meta.IsStatusConditionTrue(stored.Status.Conditions, fleetapi.ConditionTypeNamespaceDeletionStopped))
	require.Zero(t, stored.Status.AttemptGeneration, "old authorization must drain before a new attempt starts")

	aged := stored.DeepCopy()
	old := metav1.NewTime(time.Now().Add(-cleanupAuthorizationLifetime - time.Minute))
	aged.Status.CompletedAt = &old
	_, err = crud.Replace(ctx, aged, stored, nil)
	require.NoError(t, err)
	require.NoError(t, syncer.SyncOnce(ctx, key))
	stored, err = crud.Get(ctx, namespaceName)
	require.NoError(t, err)
	require.EqualValues(t, 1, stored.Status.AttemptGeneration)
	require.Equal(t, "removed stale ManifestWork", stored.Status.AttemptReason)
	require.Equal(t, "namespace-uid", stored.Status.NamespaceUID)
	require.Equal(t, "hosted-cluster-uid", stored.Status.HostedClusterUID)
	require.Empty(t, stored.Status.CleanupIntent)
	require.Empty(t, stored.Status.Conditions)
	require.Nil(t, stored.Status.CompletedAt)
	require.Len(t, stored.Status.PreviousAttempts, 1)
	require.Equal(t, "initial cleanup", stored.Status.PreviousAttempts[0].Reason)
	require.Equal(t, "ManifestWorkPresent", stored.Status.PreviousAttempts[0].StopReason)
	require.NotEqual(t, ReadDesireNameForAttempt(namespaceName, 0), ReadDesireNameForAttempt(namespaceName, 1))
	readIndexer := cache.NewIndexer(func(obj interface{}) (string, error) {
		return strings.ToLower(obj.(*kubeapplierapi.ReadDesire).ResourceID.String()), nil
	}, cache.Indexers{})
	syncer.readDesireLister = kubeapplierlisters.NewReadDesireLister(readIndexer)
	require.NoError(t, syncer.SyncOnce(ctx, key))
	readCRUD, err := client.ReadDesiresForManagementCluster(stamp)
	require.NoError(t, err)
	_, err = readCRUD.Get(ctx, ReadDesireNameForAttempt(namespaceName, 1))
	require.NoError(t, err, "retry must create a fresh ReadDesire")
	_, err = readCRUD.Get(ctx, ReadDesireNameForAttempt(namespaceName, 0))
	require.Error(t, err, "retired attempt must not supply an observation to the retry")
	replacement := map[string]any{
		"apiVersion": "v1", "kind": "Namespace", "metadata": map[string]any{
			"name": namespaceName, "uid": "replacement-uid",
			"deletionTimestamp": time.Now().Add(-2 * time.Hour).UTC().Format(time.RFC3339),
		},
	}
	for _, item := range []struct {
		name   string
		target kubeapplierapi.ResourceReference
		object any
	}{
		{ReadDesireNameForAttempt(namespaceName, 1), namespaceTargetFor(namespaceName), replacement},
		{HostedClusterReadDesireNameForAttempt(namespaceName, 1), hostedClusterTargetFor(namespaceName, "hcp1"), nil},
		{ControlPlaneNamespaceReadDesireNameForAttempt(namespaceName, 1), namespaceTargetFor(namespaceName + "-hcp1"), nil},
	} {
		require.NoError(t, readIndexer.Add(observedReadDesire(t, stamp, item.name, item.target, item.object)))
	}
	require.NoError(t, syncer.SyncOnce(ctx, key))
	stored, err = crud.Get(ctx, namespaceName)
	require.NoError(t, err)
	require.True(t, meta.IsStatusConditionTrue(stored.Status.Conditions, fleetapi.ConditionTypeNamespaceDeletionStopped),
		"a replacement namespace cannot inherit the retry")
	applyCRUD, err := client.ApplyDesiresForManagementCluster(stamp)
	require.NoError(t, err)
	_, err = applyCRUD.Get(ctx, FinalizeDesireNameForAttempt(namespaceName, 1))
	require.Error(t, err, "replacement namespace must not be finalized")
}
