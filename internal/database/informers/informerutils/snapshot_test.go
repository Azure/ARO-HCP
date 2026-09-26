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

package informerutils

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-logr/logr/funcr"
	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	utilsclock "k8s.io/utils/clock"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
	"github.com/Azure/ARO-HCP/internal/apitesting/coreapitesting"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type snapshotCapture struct {
	mu    sync.Mutex
	lines []string
}

func snapshotContext(t *testing.T) (context.Context, *snapshotCapture) {
	capture := &snapshotCapture{}
	logger := funcr.NewJSON(func(line string) {
		capture.mu.Lock()
		defer capture.mu.Unlock()
		capture.lines = append(capture.lines, line)
	}, funcr.Options{}).WithValues("request_id", "parent-request")
	return utils.ContextWithLogger(t.Context(), logger), capture
}

func (c *snapshotCapture) snapshots(t *testing.T) []map[string]any {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	var result []map[string]any
	for _, line := range c.lines {
		var entry map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &entry))
		if entry["snapshotType"] == "cosmos" {
			result = append(result, entry)
		}
	}
	return result
}

type snapshotLister[T any] struct{ items []*T }

func (l snapshotLister[T]) List(context.Context, *cosmosstorageutils.DBClientListResourceDocsOptions) (cosmosstorageutils.DBClientIterator[T], error) {
	return l, nil
}
func (l snapshotLister[T]) Items(context.Context) cosmosstorageutils.DBClientIteratorItem[T] {
	return func(yield func(string, *T) bool) {
		for _, obj := range l.items {
			if !yield("", obj) {
				return
			}
		}
	}
}
func (snapshotLister[T]) GetContinuationToken() string { return "" }
func (snapshotLister[T]) GetError() error              { return nil }

func TestListAndWatchSnapshots(t *testing.T) {
	ctx, capture := snapshotContext(t)
	cluster := &coreapi.Cluster{}
	cluster.ResourceID = mustParseResourceIDForTest(t, coreapitesting.TestClusterResourceID)
	cluster.PartitionKey = strings.ToLower(cluster.ResourceID.SubscriptionID)
	cluster.InstanceVersion = 1
	cluster.SystemData = &coreapi.SystemData{CreatedBy: "private-creator", LastModifiedBy: "private-modifier"}
	other := cluster.DeepCopy()
	other.ResourceID = mustParseResourceIDForTest(t, coreapitesting.TestClusterResourceID+"-other")
	original := cluster.DeepCopy()
	lw := NewChangeFeedListWatcher[coreapi.Cluster, *coreapi.Cluster, cosmosstorageutils.GenericDocument[coreapi.Cluster]](
		[]azcorearm.ResourceType{coreapi.ClusterResourceType}, utilsclock.RealClock{},
		snapshotLister[coreapi.Cluster]{items: []*coreapi.Cluster{cluster, other}},
		fakeChangeFeedClient{}, time.Hour, "resources")
	defer lw.Stop()
	for range 2 {
		listed, err := lw.List(ctx, metav1.ListOptions{})
		require.NoError(t, err)
		require.Len(t, listed.(*metav1.List).Items, 2)
		require.Same(t, cluster, listed.(*metav1.List).Items[0].Object)
	}
	entries := capture.snapshots(t)
	require.Len(t, entries, 4, "unchanged objects must be snapshotted again on relist")
	for i, entry := range entries {
		expectedID := []*azcorearm.ResourceID{cluster.ResourceID, other.ResourceID}[i%2]
		require.Equal(t, "dumping resourceID "+expectedID.String(), entry["msg"])
		assertSnapshotIdentity(t, entry, "resources", expectedID, expectedID)
		properties := entry["content"].(map[string]any)["properties"].(map[string]any)
		systemData := properties["systemData"].(map[string]any)
		require.Equal(t, cosmosstorageutils.RedactStr, systemData["createdBy"])
		require.Equal(t, cosmosstorageutils.RedactStr, systemData["lastModifiedBy"])
	}
	require.Equal(t, original, cluster, "redaction must not mutate informer objects")

	updated := cluster.DeepCopy()
	updated.InstanceVersion++
	stored, err := cosmosstorageutils.InternalToCosmosGeneric(updated)
	require.NoError(t, err)
	data, err := json.Marshal(stored)
	require.NoError(t, err)
	require.NoError(t, lw.currentWatcher.processItem(ctx, data))
	entries = capture.snapshots(t)
	require.Len(t, entries, 5)
	assertSnapshotIdentity(t, entries[4], "resources", cluster.ResourceID, cluster.ResourceID)
	event := <-lw.currentWatcher.ResultChan()
	require.Equal(t, watch.Modified, event.Type)
	require.Equal(t, "private-creator", event.Object.(*coreapi.Cluster).SystemData.CreatedBy)
}

func assertSnapshotIdentity(t *testing.T, entry map[string]any, container string, resourceID, clusterID *azcorearm.ResourceID) {
	t.Helper()
	require.Equal(t, "parent-request", entry["request_id"])
	require.Equal(t, "cosmos", entry["snapshotType"])
	require.Equal(t, resourceID.String(), entry["currentResourceID"])
	require.Equal(t, strings.ToLower(resourceID.String()), entry["resource_id"])
	metadata := entry["objectMetadata"].(map[string]any)
	require.Equal(t, container, metadata["cosmosContainer"])
	require.Equal(t, resourceID.String(), metadata["resourceID"])
	if clusterID != nil {
		require.Equal(t, strings.ToLower(clusterID.SubscriptionID), entry["subscription_id"])
		require.Equal(t, strings.ToLower(clusterID.ResourceGroupName), entry["resource_group"])
		require.Equal(t, strings.ToLower(clusterID.String()), entry["hcp_cluster_name"])
		require.Equal(t, entry["hcp_cluster_name"], entry["cluster_id"])
		require.Equal(t, clusterID.String(), metadata["clusterResourceID"])
	}
}

func TestListSnapshotSpecialResourceIdentity(t *testing.T) {
	ctx, capture := snapshotContext(t)
	clusterID := mustParseResourceIDForTest(t, coreapitesting.TestClusterResourceID)
	operationID := mustParseResourceIDForTest(t, "/subscriptions/"+clusterID.SubscriptionID+"/providers/Microsoft.RedHatOpenShift/locations/eastus/hcpOperationStatuses/test-op")
	operation := &coreapi.Operation{ExternalID: clusterID}
	operation.ResourceID = operationID
	operation.PartitionKey = strings.ToLower(clusterID.SubscriptionID)
	LogCosmosListItem(ctx, "resources", operation)
	managementClusterID := mustParseResourceIDForTest(t, "/providers/Microsoft.RedHatOpenShift/stamps/TestStamp/managementClusters/default")
	desire := &kubeapplierapi.ReadDesire{}
	desire.ResourceID = mustParseResourceIDForTest(t, clusterID.String()+"/readDesires/test")
	desire.Spec.ManagementCluster = managementClusterID
	desire.PartitionKey = strings.ToLower(managementClusterID.String())
	LogCosmosListItem(ctx, "kubeApplier", desire)
	mc := &fleetapi.ManagementCluster{}
	mc.ResourceID = managementClusterID
	mc.PartitionKey = "teststamp"
	LogCosmosListItem(ctx, "fleet", mc)
	entries := capture.snapshots(t)
	require.Len(t, entries, 3)
	assertSnapshotIdentity(t, entries[0], "resources", operationID, clusterID)
	assertSnapshotIdentity(t, entries[1], "kubeApplier", desire.ResourceID, clusterID)
	require.Equal(t, strings.ToLower(managementClusterID.String()), entries[1]["managementCluster"])
	assertSnapshotIdentity(t, entries[2], "fleet", managementClusterID, nil)
	require.NotContains(t, entries[2], "hcp_cluster_name", "item identity must not leak to the next item")
}

type invalidSnapshot struct {
	coreapi.CosmosMetadata
	SystemData map[string]any `json:"systemData"`
}

func TestListSnapshotFailuresDoNotLogContent(t *testing.T) {
	for _, tc := range []struct {
		name      string
		createdBy any
	}{
		{"redaction failure", 123},
		{"serialization failure", func() {}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, capture := snapshotContext(t)
			obj := &invalidSnapshot{SystemData: map[string]any{"createdBy": tc.createdBy, "lastModifiedBy": "private-modifier"}}
			obj.ResourceID = mustParseResourceIDForTest(t, coreapitesting.TestClusterResourceID)
			obj.PartitionKey = strings.ToLower(obj.ResourceID.SubscriptionID)
			LogCosmosListItem(ctx, "resources", obj)
			require.Empty(t, capture.snapshots(t))
			require.NotEmpty(t, capture.lines, "failure must be logged")
			require.NotContains(t, strings.Join(capture.lines, "\n"), "private-modifier")
			require.Equal(t, "private-modifier", obj.SystemData["lastModifiedBy"])
		})
	}
}
