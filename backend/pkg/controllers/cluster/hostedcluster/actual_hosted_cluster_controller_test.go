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

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kruntime "k8s.io/apimachinery/pkg/runtime"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

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

func TestActualHostedClusterSyncer_MirrorsObservedHostedCluster(t *testing.T) {
	ctx := utils.ContextWithLogger(context.Background(), logr.Discard())
	mockResourcesDBClient := corecosmosstoragetesting.NewMockResourcesDBClient()
	createTestHCPCluster(t, ctx, mockResourcesDBClient)

	hostedCluster := newHostedCluster()
	hostedCluster.Spec.ImageContentSources = []hsv1beta1.ImageContentSource{{
		Source: apihelpers.OcpV5ArtDevMirrorSource,
	}}
	syncer := newTestSyncer(t, mockResourcesDBClient, hostedCluster)

	require.NoError(t, syncer.SyncOnce(ctx, testKey))

	stored := getServiceProviderCluster(t, ctx, mockResourcesDBClient)
	require.NotNil(t, stored.Status.ActualHostedCluster, "expected the observed HostedCluster to be mirrored")
	assert.Equal(t, testClusterName, stored.Status.ActualHostedCluster.Name)
	assert.Equal(t, []hsv1beta1.ImageContentSource{{Source: apihelpers.OcpV5ArtDevMirrorSource}},
		stored.Status.ActualHostedCluster.Spec.ImageContentSources)
}

func TestActualHostedClusterSyncer_NoWriteWhenUnchanged(t *testing.T) {
	ctx := utils.ContextWithLogger(context.Background(), logr.Discard())
	mockResourcesDBClient := corecosmosstoragetesting.NewMockResourcesDBClient()
	createTestHCPCluster(t, ctx, mockResourcesDBClient)

	syncer := newTestSyncer(t, mockResourcesDBClient, newHostedCluster())

	require.NoError(t, syncer.SyncOnce(ctx, testKey))
	afterFirstSync := getServiceProviderCluster(t, ctx, mockResourcesDBClient)
	require.NotNil(t, afterFirstSync.Status.ActualHostedCluster, "first sync should have published the mirror")

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
	require.NotNil(t, stored.Status.ActualHostedCluster)
	assert.Len(t, stored.Status.ActualHostedCluster.Spec.ImageContentSources, 1)
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
	assert.Nil(t, stored.Status.ActualHostedCluster, "an unobserved HostedCluster must stay nil, not be mirrored as empty")
}

func TestActualHostedClusterSyncer_ClearsMirrorAfterSuccessfulEmptyObservation(t *testing.T) {
	ctx := utils.ContextWithLogger(context.Background(), logr.Discard())
	mockResourcesDBClient := corecosmosstoragetesting.NewMockResourcesDBClient()
	createTestHCPCluster(t, ctx, mockResourcesDBClient)

	syncer := newTestSyncer(t, mockResourcesDBClient, newHostedCluster())
	require.NoError(t, syncer.SyncOnce(ctx, testKey))
	require.NotNil(t, getServiceProviderCluster(t, ctx, mockResourcesDBClient).Status.ActualHostedCluster)

	syncer.readDesireLister = &kubeapplierlistertesting.SliceReadDesireLister{
		Desires: []*kubeapplierapi.ReadDesire{newHostedClusterReadDesire(t, nil)},
	}
	require.NoError(t, syncer.SyncOnce(ctx, testKey))

	assert.Nil(t, getServiceProviderCluster(t, ctx, mockResourcesDBClient).Status.ActualHostedCluster,
		"a successful empty observation must retract the old mirror")
}

func TestActualHostedClusterSyncer_RetainsMirrorAfterFailedObservation(t *testing.T) {
	ctx := utils.ContextWithLogger(context.Background(), logr.Discard())
	mockResourcesDBClient := corecosmosstoragetesting.NewMockResourcesDBClient()
	createTestHCPCluster(t, ctx, mockResourcesDBClient)

	hostedCluster := newHostedCluster()
	syncer := newTestSyncer(t, mockResourcesDBClient, hostedCluster)
	require.NoError(t, syncer.SyncOnce(ctx, testKey))
	before := getServiceProviderCluster(t, ctx, mockResourcesDBClient)

	failed := newHostedClusterReadDesire(t, hostedCluster)
	failed.Status.Conditions = []metav1.Condition{{
		Type:   kubeapplierapi.ConditionTypeSuccessful,
		Status: metav1.ConditionFalse,
	}}
	syncer.readDesireLister = &kubeapplierlistertesting.SliceReadDesireLister{Desires: []*kubeapplierapi.ReadDesire{failed}}
	require.NoError(t, syncer.SyncOnce(ctx, testKey))

	after := getServiceProviderCluster(t, ctx, mockResourcesDBClient)
	assert.NotNil(t, after.Status.ActualHostedCluster, "a failed observation must not clear the mirror")
	assert.Equal(t, before.CosmosETag, after.CosmosETag, "a failed observation must not rewrite the ServiceProviderCluster")
}

func TestActualHostedClusterSyncer_SkipsDeletingCluster(t *testing.T) {
	ctx := utils.ContextWithLogger(context.Background(), logr.Discard())
	mockResourcesDBClient := corecosmosstoragetesting.NewMockResourcesDBClient()
	createTestHCPCluster(t, ctx, mockResourcesDBClient)

	markClusterDeleting(t, ctx, mockResourcesDBClient)

	require.NoError(t, newTestSyncer(t, mockResourcesDBClient, newHostedCluster()).SyncOnce(ctx, testKey))

	stored := getServiceProviderCluster(t, ctx, mockResourcesDBClient)
	assert.Nil(t, stored.Status.ActualHostedCluster, "a cluster being deleted should not be mirrored")
}

// While the cluster is deleting but the HostedCluster is still up, its teardown
// churn is not worth publishing: that would race the deletion controllers and
// no reader benefits. The already-published mirror stays as it is.
func TestActualHostedClusterSyncer_KeepsMirrorWhileDeletingHostedClusterStillUp(t *testing.T) {
	ctx := utils.ContextWithLogger(context.Background(), logr.Discard())
	mockResourcesDBClient := corecosmosstoragetesting.NewMockResourcesDBClient()
	createTestHCPCluster(t, ctx, mockResourcesDBClient)

	require.NoError(t, newTestSyncer(t, mockResourcesDBClient, newHostedCluster()).SyncOnce(ctx, testKey))
	before := getServiceProviderCluster(t, ctx, mockResourcesDBClient)
	require.NotNil(t, before.Status.ActualHostedCluster, "first sync should have published the mirror")

	markClusterDeleting(t, ctx, mockResourcesDBClient)

	changed := newHostedCluster()
	changed.Spec.ImageContentSources = []hsv1beta1.ImageContentSource{{Source: apihelpers.OcpV5ArtDevMirrorSource}}
	require.NoError(t, newTestSyncer(t, mockResourcesDBClient, changed).SyncOnce(ctx, testKey))

	stored := getServiceProviderCluster(t, ctx, mockResourcesDBClient)
	require.NotNil(t, stored.Status.ActualHostedCluster, "the mirror must survive while the HostedCluster is still observed")
	assert.Equal(t, before.CosmosETag, stored.CosmosETag, "a deleting cluster should not be rewritten while its HostedCluster is still up")
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
	require.NotNil(t, getServiceProviderCluster(t, ctx, mockResourcesDBClient).Status.ActualHostedCluster,
		"first sync should have published the mirror")

	markClusterDeleting(t, ctx, mockResourcesDBClient)

	// A ReadDesire with no kubeContent is how the kube-applier records a target
	// object that is not on the management cluster.
	gone := &actualHostedClusterSyncer{
		resourcesDBClient: mockResourcesDBClient,
		clusterLister:     &corelistertesting.DBClusterLister{ResourcesDBClient: mockResourcesDBClient},
		readDesireLister: &kubeapplierlistertesting.SliceReadDesireLister{
			Desires: []*kubeapplierapi.ReadDesire{newHostedClusterReadDesire(t, nil)},
		},
		serviceProviderClusterLister: &corelistertesting.DBServiceProviderClusterLister{ResourcesDBClient: mockResourcesDBClient},
	}
	require.NoError(t, gone.SyncOnce(ctx, testKey))

	stored := getServiceProviderCluster(t, ctx, mockResourcesDBClient)
	assert.Nil(t, stored.Status.ActualHostedCluster,
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

// The mirror is stored verbatim: server-side bookkeeping that a consumer never
// reads still travels, because a mirror that edits what it mirrors forces every
// consumer to know the stripping policy.
func TestActualHostedClusterSyncer_MirrorsVerbatim(t *testing.T) {
	ctx := utils.ContextWithLogger(context.Background(), logr.Discard())
	mockResourcesDBClient := corecosmosstoragetesting.NewMockResourcesDBClient()
	createTestHCPCluster(t, ctx, mockResourcesDBClient)

	hostedCluster := newHostedCluster()
	hostedCluster.ResourceVersion = "12345"
	hostedCluster.ManagedFields = []metav1.ManagedFieldsEntry{{Manager: "control-plane-operator"}}
	hostedCluster.Annotations = map[string]string{"hypershift.openshift.io/cluster": "keep-me"}

	require.NoError(t, newTestSyncer(t, mockResourcesDBClient, hostedCluster).SyncOnce(ctx, testKey))

	stored := getServiceProviderCluster(t, ctx, mockResourcesDBClient)
	require.NotNil(t, stored.Status.ActualHostedCluster)
	assert.Equal(t, "12345", stored.Status.ActualHostedCluster.ResourceVersion, "the mirror must not strip resourceVersion")
	assert.Equal(t, []metav1.ManagedFieldsEntry{{Manager: "control-plane-operator"}}, stored.Status.ActualHostedCluster.ManagedFields,
		"the mirror must not strip managedFields")
	assert.Equal(t, "keep-me", stored.Status.ActualHostedCluster.Annotations["hypershift.openshift.io/cluster"])
}

func newTestSyncer(t *testing.T, mockResourcesDBClient *corecosmosstoragetesting.MockResourcesDBClient, hostedCluster *hsv1beta1.HostedCluster) *actualHostedClusterSyncer {
	t.Helper()
	return &actualHostedClusterSyncer{
		resourcesDBClient: mockResourcesDBClient,
		clusterLister:     &corelistertesting.DBClusterLister{ResourcesDBClient: mockResourcesDBClient},
		readDesireLister: &kubeapplierlistertesting.SliceReadDesireLister{
			Desires: []*kubeapplierapi.ReadDesire{newHostedClusterReadDesire(t, hostedCluster)},
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

// newHostedClusterReadDesire builds a ReadDesire whose Status.KubeContent.Raw
// carries the marshaled HostedCluster, as the kube-applier would record it.
func newHostedClusterReadDesire(t *testing.T, hostedCluster *hsv1beta1.HostedCluster) *kubeapplierapi.ReadDesire {
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

	cluster := &coreapi.HCPOpenShiftCluster{
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
		ServiceProviderProperties: coreapi.HCPOpenShiftClusterServiceProviderProperties{
			ProvisioningState: coreapi.ProvisioningStateSucceeded,
			ClusterServiceID:  &clusterInternalID,
		},
	}
	_, err = mockResourcesDBClient.HCPClusters(testSubscriptionID, testResourceGroupName).Create(ctx, cluster, nil)
	require.NoError(t, err)

	_, err = corecosmosstorage.GetOrCreateServiceProviderCluster(ctx, mockResourcesDBClient, clusterResourceID)
	require.NoError(t, err)
}
