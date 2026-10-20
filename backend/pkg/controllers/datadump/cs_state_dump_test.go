// Copyright 2025 Microsoft Corporation
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

package datadump

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	clocktesting "k8s.io/utils/clock/testing"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/corelistertesting"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func TestCSStateDumpBackfill(t *testing.T) {
	for _, failure := range []string{"", "get", "list", "missing cluster", "missing cs id"} {
		t.Run(failure, func(t *testing.T) {
			var logs bytes.Buffer
			key := controllerutils.HCPClusterKey{SubscriptionID: "sub", ResourceGroupName: "rg", HCPClusterName: "cluster"}
			logger := key.AddLoggerValues(logr.FromSlogHandler(slog.NewJSONHandler(&logs, nil))).WithValues(utils.LogValues{}.AddControllerName(CSStateDumpControllerName)...)
			ctx := utils.ContextWithLogger(t.Context(), logger)
			db := corecosmosstoragetesting.NewMockResourcesDBClient()
			mock := ocm.NewMockClusterServiceClientSpec(gomock.NewController(t))
			clock := clocktesting.NewFakePassiveClock(time.Now())
			cooldown := controllerutil.NewTimeBasedCooldownChecker(csStateDumpPeriod)
			cooldown.SetClock(clock)
			syncer := &csStateDump{
				clusterLister:   &corelistertesting.DBClusterLister{ResourcesDBClient: db},
				csClient:        ocm.NewClusterServiceClientWithLogging(mock),
				nextDumpChecker: cooldown,
			}
			id := metadataapi.Must(metadataapi.NewInternalID("/api/aro_hcp/v1alpha1/clusters/cs-cluster"))
			if failure != "missing cluster" {
				cluster := &coreapi.Cluster{
					CosmosMetadata:  coreapi.CosmosMetadata{ResourceID: key.GetResourceID(), PartitionKey: "sub"},
					TrackedResource: coreapi.TrackedResource{Resource: coreapi.Resource{ID: key.GetResourceID()}},
				}
				if failure != "missing cs id" {
					cluster.ServiceProviderProperties.ClusterServiceID = &id
				}
				_, err := db.HCPClusters("sub", "rg").Create(ctx, cluster, nil)
				require.NoError(t, err)
			}
			if !strings.HasPrefix(failure, "missing") {
				cluster := metadataapi.Must(arohcpv1alpha1.NewCluster().ID("cs-cluster").Build())
				var getErr, listErr error
				if failure == "get" {
					cluster, getErr = nil, fmt.Errorf("get failed")
				}
				if failure == "list" {
					listErr = fmt.Errorf("later page failed")
				}
				// No Cosmos node pools: all returned CS documents must still be logged.
				nodePool := metadataapi.Must(arohcpv1alpha1.NewNodePool().ID("np").Version(arohcpv1alpha1.NewVersion().ID("openshift-v4.18.0").RawID("4.18.0")).Build())
				mock.EXPECT().GetCluster(gomock.Any(), id).Return(cluster, getErr).Times(2)
				mock.EXPECT().ListNodePools(id, "").DoAndReturn(func(ocm.InternalID, string) ocm.NodePoolListIterator {
					return ocm.NewSimpleNodePoolListIterator([]*arohcpv1alpha1.NodePool{nodePool}, listErr)
				}).Times(2)
			}
			require.NoError(t, syncer.SyncOnce(ctx, key))
			first := logs.String()
			clock.SetTime(clock.Now().Add(29 * time.Minute))
			require.NoError(t, syncer.SyncOnce(ctx, key))
			require.Equal(t, first, logs.String(), "events during cooldown must not fetch or log again")
			clock.SetTime(clock.Now().Add(time.Minute + time.Second))
			require.NoError(t, syncer.SyncOnce(ctx, key))
			if strings.HasPrefix(failure, "missing") {
				require.Empty(t, logs.String())
				return
			}
			counts := map[string]int{}
			decoder := json.NewDecoder(&logs)
			for decoder.More() {
				var entry map[string]any
				require.NoError(t, decoder.Decode(&entry))
				if entry["snapshotType"] == "clusterService" {
					counts[entry["msg"].(string)]++
					if nodePool, ok := entry["csNodePool"].(map[string]any); ok {
						require.Equal(t, "4.18.0", nodePool["version"].(map[string]any)["raw_id"])
					}
				}
			}
			require.Equal(t, 2, counts["cluster-service node pool state dump"], "one emission per returned node pool, including partial lists")
			if failure == "get" {
				require.Zero(t, counts["cluster-service state dump"])
			} else {
				require.Equal(t, 2, counts["cluster-service state dump"], "the controller must not duplicate client snapshots")
			}
		})
	}
}
