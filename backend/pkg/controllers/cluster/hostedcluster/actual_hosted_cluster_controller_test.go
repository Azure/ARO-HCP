// Copyright 2026 Microsoft Corporation
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package hostedcluster

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kruntime "k8s.io/apimachinery/pkg/runtime"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	configv1 "github.com/openshift/api/config/v1"
	hsv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/Azure/ARO-HCP/backend/pkg/kubeapplierhelpers"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/apihelpers/kubeapplierapihelpers"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/corelistertesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/kubeapplierlistertesting"
	"github.com/Azure/ARO-HCP/internal/utils"
	"github.com/Azure/ARO-HCP/internal/utils/apihelpers"
)

const (
	testSubscriptionID    = "00000000-0000-0000-0000-000000000001"
	testResourceGroupName = "test-rg"
	testClusterName       = "test-cluster"
	testCSClusterIDStr    = "/api/aro_hcp/v1alpha1/clusters/" + testClusterName
)

var testKey = controllerutils.HCPClusterKey{
	SubscriptionID:    testSubscriptionID,
	ResourceGroupName: testResourceGroupName,
	HCPClusterName:    testClusterName,
}

func TestActualHostedClusterSyncer_MirrorsImageContentSources(t *testing.T) {
	ctx := utils.ContextWithLogger(context.Background(), logr.Discard())
	mockResourcesDBClient := corecosmosstoragetesting.NewMockResourcesDBClient()
	createTestHCPCluster(t, ctx, mockResourcesDBClient)

	hostedCluster := newHostedCluster()
	hostedCluster.Spec.ImageContentSources = []hsv1beta1.ImageContentSource{{
		Source:  apihelpers.OcpV5ArtDevMirrorSource,
		Mirrors: []string{"registry.example.com/v5", "registry.example.com/backup"},
	}}
	syncer := newTestSyncer(t, mockResourcesDBClient, hostedCluster)

	require.NoError(t, syncer.SyncOnce(ctx, testKey))

	stored := getServiceProviderCluster(t, ctx, mockResourcesDBClient)
	require.NotNil(t, stored.Status.ImageContentSources)
	assert.Equal(t, hostedCluster.Spec.ImageContentSources, *stored.Status.ImageContentSources)
}

func TestActualHostedClusterSyncer_MirrorsControlPlaneVersionHistory(t *testing.T) {
	ctx := utils.ContextWithLogger(context.Background(), logr.Discard())
	mockResourcesDBClient := corecosmosstoragetesting.NewMockResourcesDBClient()
	createTestHCPCluster(t, ctx, mockResourcesDBClient)
	hostedCluster := newHostedCluster()
	hostedCluster.Status.ControlPlaneVersion.History = []hsv1beta1.ControlPlaneUpdateHistory{
		{Version: "4.22.2", State: configv1.PartialUpdate, Image: "registry.example.com/release:4.22.2", StartedTime: metav1.NewTime(time.Unix(100, 0))},
		{Version: "4.22.1", State: configv1.CompletedUpdate, CompletionTime: metav1.NewTime(time.Unix(90, 0))},
	}
	hostedCluster.Status.Version = &hsv1beta1.ClusterVersionStatus{History: []configv1.UpdateHistory{
		{Version: "4.21.9", State: configv1.CompletedUpdate},
	}}
	syncer := newTestSyncer(t, mockResourcesDBClient, hostedCluster)
	require.NoError(t, syncer.SyncOnce(ctx, testKey))
	observed := getServiceProviderCluster(t, ctx, mockResourcesDBClient)
	require.NotNil(t, observed.Status.ControlPlaneVersionHistory)
	assert.Equal(t, hostedCluster.Status.ControlPlaneVersion.History, *observed.Status.ControlPlaneVersionHistory)
	copied := observed.DeepCopy()
	(*copied.Status.ControlPlaneVersionHistory)[0].Version = "4.22.3"
	assert.Equal(t, "4.22.2", (*observed.Status.ControlPlaneVersionHistory)[0].Version)
	require.NoError(t, syncer.SyncOnce(ctx, testKey))
	assert.Equal(t, observed.CosmosETag, getServiceProviderCluster(t, ctx, mockResourcesDBClient).CosmosETag)

	hostedCluster.Status.ControlPlaneVersion.History[0].State = configv1.CompletedUpdate
	require.NoError(t, newTestSyncer(t, mockResourcesDBClient, hostedCluster).SyncOnce(ctx, testKey))
	updated := getServiceProviderCluster(t, ctx, mockResourcesDBClient)
	assert.NotEqual(t, observed.CosmosETag, updated.CosmosETag)
	assert.Equal(t, hostedCluster.Status.ControlPlaneVersion.History, *updated.Status.ControlPlaneVersionHistory)
	assert.Equal(t, observed.Status.ImageContentSources, updated.Status.ImageContentSources)

	updated.Status.ControlPlaneVersionHistory = nil
	legacy, err := mockResourcesDBClient.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Replace(ctx, updated, nil)
	require.NoError(t, err)
	require.NoError(t, newTestSyncer(t, mockResourcesDBClient, hostedCluster).SyncOnce(ctx, testKey))
	backfilled := getServiceProviderCluster(t, ctx, mockResourcesDBClient)
	assert.NotEqual(t, legacy.CosmosETag, backfilled.CosmosETag)
	require.NotNil(t, backfilled.Status.ControlPlaneVersionHistory)
	assert.Equal(t, hostedCluster.Status.ControlPlaneVersion.History, *backfilled.Status.ControlPlaneVersionHistory)

	hostedCluster.Status.ControlPlaneVersion.History = nil
	require.NoError(t, newTestSyncer(t, mockResourcesDBClient, hostedCluster).SyncOnce(ctx, testKey))
	empty := getServiceProviderCluster(t, ctx, mockResourcesDBClient)
	require.NotNil(t, empty.Status.ControlPlaneVersionHistory)
	assert.Equal(t, []hsv1beta1.ControlPlaneUpdateHistory{}, *empty.Status.ControlPlaneVersionHistory)
	serialized, err := json.Marshal(empty.Status)
	require.NoError(t, err)
	var restored coreapi.ServiceProviderClusterStatus
	require.NoError(t, json.Unmarshal(serialized, &restored))
	require.NotNil(t, restored.ControlPlaneVersionHistory)
	assert.Empty(t, *restored.ControlPlaneVersionHistory)
	require.NoError(t, newTestSyncer(t, mockResourcesDBClient, hostedCluster).SyncOnce(ctx, testKey))
	assert.Equal(t, empty.CosmosETag, getServiceProviderCluster(t, ctx, mockResourcesDBClient).CosmosETag)

	hostedCluster.Status.ControlPlaneVersion.History = *observed.Status.ControlPlaneVersionHistory
	require.NoError(t, newTestSyncer(t, mockResourcesDBClient, hostedCluster).SyncOnce(ctx, testKey))
	require.NoError(t, newTestSyncer(t, mockResourcesDBClient, nil).SyncOnce(ctx, testKey))
	retracted := getServiceProviderCluster(t, ctx, mockResourcesDBClient)
	assert.Nil(t, retracted.Status.ControlPlaneVersionHistory)
	require.NoError(t, newTestSyncer(t, mockResourcesDBClient, nil).SyncOnce(ctx, testKey))
	assert.Equal(t, retracted.CosmosETag, getServiceProviderCluster(t, ctx, mockResourcesDBClient).CosmosETag)
}

func TestActualHostedClusterSyncer_NoWriteWhenUnchanged(t *testing.T) {
	ctx := utils.ContextWithLogger(context.Background(), logr.Discard())
	mockResourcesDBClient := corecosmosstoragetesting.NewMockResourcesDBClient()
	createTestHCPCluster(t, ctx, mockResourcesDBClient)

	syncer := newTestSyncer(t, mockResourcesDBClient, newHostedCluster())

	require.NoError(t, syncer.SyncOnce(ctx, testKey))
	afterFirstSync := getServiceProviderCluster(t, ctx, mockResourcesDBClient)
	require.NotNil(t, afterFirstSync.Status.ImageContentSources)
	assert.Equal(t, []hsv1beta1.ImageContentSource{}, *afterFirstSync.Status.ImageContentSources)
	serialized, err := json.Marshal(afterFirstSync.Status)
	require.NoError(t, err)
	var restored coreapi.ServiceProviderClusterStatus
	require.NoError(t, json.Unmarshal(serialized, &restored))
	require.NotNil(t, restored.ImageContentSources, "an observed empty slice must survive serialization")
	assert.Empty(t, *restored.ImageContentSources)

	require.NoError(t, syncer.SyncOnce(ctx, testKey))
	afterSecondSync := getServiceProviderCluster(t, ctx, mockResourcesDBClient)

	assert.Equal(t, afterFirstSync.CosmosETag, afterSecondSync.CosmosETag,
		"ServiceProviderCluster was rewritten despite an identical observed HostedCluster; every needless Replace costs RUs and wakes the changefeed")
}

func TestActualHostedClusterSyncer_WritesWhenHostedClusterChanges(t *testing.T) {
	ctx := utils.ContextWithLogger(context.Background(), logr.Discard())
	mockResourcesDBClient := corecosmosstoragetesting.NewMockResourcesDBClient()
	createTestHCPCluster(t, ctx, mockResourcesDBClient)

	require.NoError(t, newTestSyncer(t, mockResourcesDBClient, newHostedCluster()).SyncOnce(ctx, testKey))
	beforeETag := getServiceProviderCluster(t, ctx, mockResourcesDBClient).CosmosETag

	changed := newHostedCluster()
	changed.Spec.ImageContentSources = []hsv1beta1.ImageContentSource{{
		Source: apihelpers.OcpV5ArtDevMirrorSource,
	}}
	require.NoError(t, newTestSyncer(t, mockResourcesDBClient, changed).SyncOnce(ctx, testKey))

	stored := getServiceProviderCluster(t, ctx, mockResourcesDBClient)
	assert.NotEqual(t, beforeETag, stored.CosmosETag, "expected a Replace after the observed HostedCluster changed")
	require.NotNil(t, stored.Status.ImageContentSources)
	assert.Equal(t, changed.Spec.ImageContentSources, *stored.Status.ImageContentSources)
}

func TestActualHostedClusterSyncer_BackfillsImageContentSources(test *testing.T) {
	ctx := utils.ContextWithLogger(context.Background(), logr.Discard())
	mockResourcesDBClient := corecosmosstoragetesting.NewMockResourcesDBClient()
	createTestHCPCluster(test, ctx, mockResourcesDBClient)
	hostedCluster := newHostedCluster()
	hostedCluster.Spec.ImageContentSources = []hsv1beta1.ImageContentSource{{Source: apihelpers.OcpV5ArtDevMirrorSource}}
	syncer := newTestSyncer(test, mockResourcesDBClient, hostedCluster)
	require.NoError(test, syncer.SyncOnce(ctx, testKey))

	legacy := getServiceProviderCluster(test, ctx, mockResourcesDBClient)
	legacy.Status.ImageContentSources = nil
	_, err := mockResourcesDBClient.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Replace(ctx, legacy, nil)
	require.NoError(test, err)
	before := getServiceProviderCluster(test, ctx, mockResourcesDBClient)

	require.NoError(test, syncer.SyncOnce(ctx, testKey))
	backfilled := getServiceProviderCluster(test, ctx, mockResourcesDBClient)
	assert.NotEqual(test, before.CosmosETag, backfilled.CosmosETag)
	require.NotNil(test, backfilled.Status.ImageContentSources)
	assert.Equal(test, hostedCluster.Spec.ImageContentSources, *backfilled.Status.ImageContentSources)

	require.NoError(test, syncer.SyncOnce(ctx, testKey))
	assert.Equal(test, backfilled.CosmosETag, getServiceProviderCluster(test, ctx, mockResourcesDBClient).CosmosETag)
}

// The mirror exists so admission can distinguish "we have not looked" from
// "we looked and the mirror is not there". Leaving it nil while the
// HostedCluster is unobserved is what keeps that distinction meaningful.
func TestActualHostedClusterSyncer_LeavesMirrorNilWhenHostedClusterUnobserved(t *testing.T) {
	ctx := utils.ContextWithLogger(context.Background(), logr.Discard())
	mockResourcesDBClient := corecosmosstoragetesting.NewMockResourcesDBClient()
	createTestHCPCluster(t, ctx, mockResourcesDBClient)

	syncer := &actualHostedClusterSyncer{
		resourcesDBClient:            mockResourcesDBClient,
		clusterLister:                &corelistertesting.DBClusterLister{ResourcesDBClient: mockResourcesDBClient},
		readDesireLister:             &kubeapplierlistertesting.SliceReadDesireLister{},
		serviceProviderClusterLister: &corelistertesting.DBServiceProviderClusterLister{ResourcesDBClient: mockResourcesDBClient},
	}
	require.NoError(t, syncer.SyncOnce(ctx, testKey))

	stored := getServiceProviderCluster(t, ctx, mockResourcesDBClient)
	assert.Nil(t, stored.Status.ImageContentSources)
	assert.Nil(t, stored.Status.ControlPlaneVersionHistory)
}

func TestActualHostedClusterSyncer_ClearsMirrorAfterSuccessfulEmptyObservation(t *testing.T) {
	ctx := utils.ContextWithLogger(context.Background(), logr.Discard())
	mockResourcesDBClient := corecosmosstoragetesting.NewMockResourcesDBClient()
	createTestHCPCluster(t, ctx, mockResourcesDBClient)

	syncer := newTestSyncer(t, mockResourcesDBClient, newHostedCluster())
	require.NoError(t, syncer.SyncOnce(ctx, testKey))
	require.NotNil(t, getServiceProviderCluster(t, ctx, mockResourcesDBClient).Status.ImageContentSources)

	syncer.readDesireLister = &kubeapplierlistertesting.SliceReadDesireLister{
		Desires: []*kubeapplierapi.ReadDesire{newTestHostedClusterReadDesire(t, nil)},
	}
	require.NoError(t, syncer.SyncOnce(ctx, testKey))

	retracted := getServiceProviderCluster(t, ctx, mockResourcesDBClient)
	assert.Nil(t, retracted.Status.ImageContentSources)
	assert.Nil(t, retracted.Status.ControlPlaneVersionHistory)
	require.NoError(t, syncer.SyncOnce(ctx, testKey))
	assert.Equal(t, retracted.CosmosETag, getServiceProviderCluster(t, ctx, mockResourcesDBClient).CosmosETag)
}

func TestActualHostedClusterSyncer_RetainsMirrorWhenUnobserved(t *testing.T) {
	changed := newHostedCluster()
	changed.Spec.ImageContentSources = []hsv1beta1.ImageContentSource{{Source: apihelpers.OcpV5ArtDevMirrorSource}}

	tests := []struct {
		name              string
		missingReadDesire bool
		hostedCluster     *hsv1beta1.HostedCluster
		conditionStatus   metav1.ConditionStatus
	}{
		{name: "cold cache", missingReadDesire: true},
		{name: "not yet observed"},
		{name: "failed empty read", conditionStatus: metav1.ConditionFalse},
		{name: "failed read with cached content", hostedCluster: changed, conditionStatus: metav1.ConditionFalse},
		{name: "unknown observation", hostedCluster: changed, conditionStatus: metav1.ConditionUnknown},
	}
	for _, state := range []struct {
		name     string
		deleting bool
	}{
		{name: "active"},
		{name: "deleting", deleting: true},
	} {
		t.Run(state.name, func(t *testing.T) {
			for _, tt := range tests {
				t.Run(tt.name, func(t *testing.T) {
					ctx := utils.ContextWithLogger(context.Background(), logr.Discard())
					mockResourcesDBClient := corecosmosstoragetesting.NewMockResourcesDBClient()
					createTestHCPCluster(t, ctx, mockResourcesDBClient)

					syncer := newTestSyncer(t, mockResourcesDBClient, newHostedCluster())
					require.NoError(t, syncer.SyncOnce(ctx, testKey))
					before := getServiceProviderCluster(t, ctx, mockResourcesDBClient)
					if state.deleting {
						markClusterDeleting(t, ctx, mockResourcesDBClient)
					}

					lister := &kubeapplierlistertesting.SliceReadDesireLister{}
					if !tt.missingReadDesire {
						desire := newTestHostedClusterReadDesire(t, tt.hostedCluster)
						desire.Status.Conditions = nil
						if tt.conditionStatus != "" {
							desire.Status.Conditions = []metav1.Condition{{Type: kubeapplierapi.ConditionTypeSuccessful, Status: tt.conditionStatus}}
						}
						lister.Desires = []*kubeapplierapi.ReadDesire{desire}
					}
					syncer.readDesireLister = lister
					require.NoError(t, syncer.SyncOnce(ctx, testKey))

					after := getServiceProviderCluster(t, ctx, mockResourcesDBClient)
					assert.Equal(t, before.Status.ImageContentSources, after.Status.ImageContentSources)
					assert.Equal(t, before.Status.ControlPlaneVersionHistory, after.Status.ControlPlaneVersionHistory)
					assert.Equal(t, before.CosmosETag, after.CosmosETag, "an unobserved result must not rewrite the ServiceProviderCluster")
				})
			}
		})
	}
}

func TestActualHostedClusterSyncer_MirrorsDeletingCluster(t *testing.T) {
	ctx := utils.ContextWithLogger(context.Background(), logr.Discard())
	mockResourcesDBClient := corecosmosstoragetesting.NewMockResourcesDBClient()
	createTestHCPCluster(t, ctx, mockResourcesDBClient)

	markClusterDeleting(t, ctx, mockResourcesDBClient)

	hostedCluster := newHostedCluster()
	require.NoError(t, newTestSyncer(t, mockResourcesDBClient, hostedCluster).SyncOnce(ctx, testKey))

	stored := getServiceProviderCluster(t, ctx, mockResourcesDBClient)
	require.NotNil(t, stored.Status.ImageContentSources)
	assert.Empty(t, *stored.Status.ImageContentSources)
}

// Deletion must not freeze the mirror while the HostedCluster still exists.
func TestActualHostedClusterSyncer_UpdatesMirrorWhileDeletingHostedClusterStillUp(t *testing.T) {
	ctx := utils.ContextWithLogger(context.Background(), logr.Discard())
	mockResourcesDBClient := corecosmosstoragetesting.NewMockResourcesDBClient()
	createTestHCPCluster(t, ctx, mockResourcesDBClient)

	require.NoError(t, newTestSyncer(t, mockResourcesDBClient, newHostedCluster()).SyncOnce(ctx, testKey))
	before := getServiceProviderCluster(t, ctx, mockResourcesDBClient)
	require.NotNil(t, before.Status.ImageContentSources, "first sync should have published the mirror")

	markClusterDeleting(t, ctx, mockResourcesDBClient)

	changed := newHostedCluster()
	changed.Spec.ImageContentSources = []hsv1beta1.ImageContentSource{{Source: apihelpers.OcpV5ArtDevMirrorSource}}
	changed.DeletionTimestamp = &metav1.Time{Time: time.Date(2026, time.September, 24, 12, 0, 0, 0, time.UTC)}
	require.NoError(t, newTestSyncer(t, mockResourcesDBClient, changed).SyncOnce(ctx, testKey))

	stored := getServiceProviderCluster(t, ctx, mockResourcesDBClient)
	require.NotNil(t, stored.Status.ImageContentSources)
	assert.Equal(t, changed.Spec.ImageContentSources, *stored.Status.ImageContentSources)
	assert.NotEqual(t, before.CosmosETag, stored.CosmosETag, "a changed observation must be written during deletion")
}

// The deletion flow specifically: the cluster is going away and its HostedCluster
// has already been torn down, so the successful empty observation must retract the
// mirror rather than let it outlive the object. This is the same path as
// ClearsMirrorAfterSuccessfulEmptyObservation, pinned separately because a
// DeletionTimestamp must not reintroduce the old blanket skip.
func TestActualHostedClusterSyncer_RetractsMirrorWhenDeletingAndHostedClusterGone(t *testing.T) {
	ctx := utils.ContextWithLogger(context.Background(), logr.Discard())
	mockResourcesDBClient := corecosmosstoragetesting.NewMockResourcesDBClient()
	createTestHCPCluster(t, ctx, mockResourcesDBClient)

	require.NoError(t, newTestSyncer(t, mockResourcesDBClient, newHostedCluster()).SyncOnce(ctx, testKey))
	require.NotNil(t, getServiceProviderCluster(t, ctx, mockResourcesDBClient).Status.ImageContentSources,
		"first sync should have published the mirror")

	markClusterDeleting(t, ctx, mockResourcesDBClient)

	// A ReadDesire with no kubeContent is how the kube-applier records a target
	// object that is not on the management cluster.
	gone := &actualHostedClusterSyncer{
		resourcesDBClient: mockResourcesDBClient,
		clusterLister:     &corelistertesting.DBClusterLister{ResourcesDBClient: mockResourcesDBClient},
		readDesireLister: &kubeapplierlistertesting.SliceReadDesireLister{
			Desires: []*kubeapplierapi.ReadDesire{newTestHostedClusterReadDesire(t, nil)},
		},
		serviceProviderClusterLister: &corelistertesting.DBServiceProviderClusterLister{ResourcesDBClient: mockResourcesDBClient},
	}
	require.NoError(t, gone.SyncOnce(ctx, testKey))

	stored := getServiceProviderCluster(t, ctx, mockResourcesDBClient)
	assert.Nil(t, stored.Status.ImageContentSources,
		"a deleting cluster whose HostedCluster is gone must have its mirror retracted, not left stale")
}

func markClusterDeleting(t *testing.T, ctx context.Context, mockResourcesDBClient *corecosmosstoragetesting.MockResourcesDBClient) {
	t.Helper()

	clusterCRUD := mockResourcesDBClient.HCPClusters(testSubscriptionID, testResourceGroupName)
	cluster, err := clusterCRUD.Get(ctx, testClusterName)
	require.NoError(t, err)
	cluster.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: metav1.Now().Time}
	_, err = clusterCRUD.Replace(ctx, cluster, nil)
	require.NoError(t, err)
}

func TestActualHostedClusterSyncer_NoWriteWhenOtherHostedClusterFieldsChange(test *testing.T) {
	ctx := utils.ContextWithLogger(context.Background(), logr.Discard())
	mockResourcesDBClient := corecosmosstoragetesting.NewMockResourcesDBClient()
	createTestHCPCluster(test, ctx, mockResourcesDBClient)

	hostedCluster := newHostedCluster()
	hostedCluster.Spec.ImageContentSources = []hsv1beta1.ImageContentSource{{
		Source:  apihelpers.OcpV5ArtDevMirrorSource,
		Mirrors: []string{"registry.example.com/v5"},
	}}
	require.NoError(test, newTestSyncer(test, mockResourcesDBClient, hostedCluster).SyncOnce(ctx, testKey))
	before := getServiceProviderCluster(test, ctx, mockResourcesDBClient)
	require.NotNil(test, before.Status.ImageContentSources)

	hostedCluster.ResourceVersion = "12345"
	hostedCluster.ManagedFields = []metav1.ManagedFieldsEntry{{Manager: "control-plane-operator"}}
	hostedCluster.Annotations = map[string]string{"hypershift.openshift.io/cluster": "keep-me"}
	hostedCluster.Spec.Release.Image = "registry.example.com/release:5.0"
	hostedCluster.Status.Conditions = []metav1.Condition{{Type: "Available", Status: metav1.ConditionTrue}}

	require.NoError(test, newTestSyncer(test, mockResourcesDBClient, hostedCluster).SyncOnce(ctx, testKey))

	stored := getServiceProviderCluster(test, ctx, mockResourcesDBClient)
	assert.Equal(test, before.CosmosETag, stored.CosmosETag, "unrelated HostedCluster changes must not cause a write")
	assert.Equal(test, before.Status.ImageContentSources, stored.Status.ImageContentSources)
}

func newTestSyncer(t *testing.T, mockResourcesDBClient *corecosmosstoragetesting.MockResourcesDBClient, hostedCluster *hsv1beta1.HostedCluster) *actualHostedClusterSyncer {
	t.Helper()
	return &actualHostedClusterSyncer{
		resourcesDBClient: mockResourcesDBClient,
		clusterLister:     &corelistertesting.DBClusterLister{ResourcesDBClient: mockResourcesDBClient},
		readDesireLister: &kubeapplierlistertesting.SliceReadDesireLister{
			Desires: []*kubeapplierapi.ReadDesire{newTestHostedClusterReadDesire(t, hostedCluster)},
		},
		serviceProviderClusterLister: &corelistertesting.DBServiceProviderClusterLister{ResourcesDBClient: mockResourcesDBClient},
	}
}

func newHostedCluster() *hsv1beta1.HostedCluster {
	hostedCluster := &hsv1beta1.HostedCluster{}
	hostedCluster.APIVersion = "hypershift.openshift.io/v1beta1"
	hostedCluster.Kind = "HostedCluster"
	hostedCluster.SetName(testClusterName)
	hostedCluster.SetNamespace("ocm-test")
	return hostedCluster
}

// newTestHostedClusterReadDesire builds a ReadDesire whose Status.KubeContent.Raw
// carries the marshaled HostedCluster, as the kube-applier would record it.
func newTestHostedClusterReadDesire(t *testing.T, hostedCluster *hsv1beta1.HostedCluster) *kubeapplierapi.ReadDesire {
	t.Helper()

	// A nil hostedCluster models a ReadDesire carrying no content, which is how
	// the kube-applier records a target object that is not on the cluster.
	var kubeContent *kruntime.RawExtension
	if hostedCluster != nil {
		raw, err := json.Marshal(hostedCluster)
		require.NoError(t, err)
		kubeContent = &kruntime.RawExtension{Raw: raw}
	}
	return &kubeapplierapi.ReadDesire{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID: metadataapi.Must(azcorearm.ParseResourceID(
				kubeapplierapihelpers.ToClusterScopedReadDesireResourceIDString(
					testSubscriptionID, testResourceGroupName, testClusterName, kubeapplierhelpers.ReadDesireNameReadonlyHostedCluster))),
			PartitionKey: strings.ToLower("management-cluster-resource-id"),
		},
		Status: kubeapplierapi.ReadDesireStatus{
			Conditions:  []metav1.Condition{{Type: kubeapplierapi.ConditionTypeSuccessful, Status: metav1.ConditionTrue}},
			KubeContent: kubeContent,
		},
	}
}

func getServiceProviderCluster(t *testing.T, ctx context.Context, mockResourcesDBClient *corecosmosstoragetesting.MockResourcesDBClient) *coreapi.ServiceProviderCluster {
	t.Helper()
	serviceProviderCluster, err := mockResourcesDBClient.
		ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).
		Get(ctx, coreapi.ServiceProviderClusterResourceName)
	require.NoError(t, err)
	return serviceProviderCluster
}

func createTestHCPCluster(t *testing.T, ctx context.Context, mockResourcesDBClient *corecosmosstoragetesting.MockResourcesDBClient) {
	t.Helper()

	clusterResourceID := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID +
		"/resourceGroups/" + testResourceGroupName +
		"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testClusterName))
	clusterInternalID, err := metadataapi.NewInternalID(testCSClusterIDStr)
	require.NoError(t, err)

	cluster := &coreapi.Cluster{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   clusterResourceID,
			PartitionKey: strings.ToLower(clusterResourceID.SubscriptionID),
		},
		TrackedResource: coreapi.TrackedResource{
			Resource: coreapi.Resource{
				ID:   clusterResourceID,
				Name: testClusterName,
				Type: coreapi.ClusterResourceType.String(),
			},
			Location: "eastus",
		},
		ServiceProviderProperties: coreapi.ClusterServiceProviderProperties{
			ProvisioningState: coreapi.ProvisioningStateSucceeded,
			ClusterServiceID:  &clusterInternalID,
		},
	}
	_, err = mockResourcesDBClient.HCPClusters(testSubscriptionID, testResourceGroupName).Create(ctx, cluster, nil)
	require.NoError(t, err)

	_, err = corecosmosstorage.GetOrCreateServiceProviderCluster(ctx, mockResourcesDBClient, clusterResourceID)
	require.NoError(t, err)
}
