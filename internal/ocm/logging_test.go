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

package ocm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"iter"
	"log/slog"
	"testing"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"

	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func snapshotTestContext(t *testing.T) (context.Context, *bytes.Buffer) {
	t.Helper()
	buf := &bytes.Buffer{}
	logger := logr.FromSlogHandler(slog.NewJSONHandler(buf, nil)).WithValues(
		"controller_name", "caller", "resource_id", "original-resource", "request_id", "request")
	return utils.ContextWithLogger(t.Context(), logger), buf
}

func TestClientSnapshots(t *testing.T) {
	clusterID := metadataapi.Must(metadataapi.NewInternalID("/api/aro_hcp/v1alpha1/clusters/cluster"))
	nodePoolID := metadataapi.Must(metadataapi.NewInternalID(clusterID.String() + "/node_pools/np"))
	cluster := metadataapi.Must(arohcpv1alpha1.NewCluster().ID("cluster").HREF(clusterID.String()).Autoscaler(arohcpv1alpha1.NewClusterAutoscaler().MaxPodGracePeriod(42)).Build())
	nodePool := metadataapi.Must(arohcpv1alpha1.NewNodePool().ID("np").HREF(nodePoolID.String()).Version(arohcpv1alpha1.NewVersion().ID("openshift-v4.18.0").RawID("4.18.0")).Build())
	clusterBuilder := arohcpv1alpha1.NewCluster()
	nodePoolBuilder := arohcpv1alpha1.NewNodePool()
	for _, method := range []string{"GetCluster", "PostCluster", "UpdateCluster", "GetNodePool", "PostNodePool", "UpdateNodePool"} {
		for _, outcome := range []string{"success", "nil", "error"} {
			t.Run(method+"/"+outcome, func(t *testing.T) {
				ctx, logs := snapshotTestContext(t)
				mock := NewMockClusterServiceClientSpec(gomock.NewController(t))
				client := NewClusterServiceClientWithLogging(mock)
				var wantErr error
				if outcome == "error" {
					wantErr = errors.New("CS failed")
				}
				clusterResult, nodePoolResult := cluster, nodePool
				if outcome == "nil" {
					clusterResult, nodePoolResult = nil, nil
				}
				var call func() (any, error)
				var want any
				field := "csCluster"
				switch method {
				case "GetCluster":
					mock.EXPECT().GetCluster(ctx, clusterID).Return(clusterResult, wantErr).Times(2)
					call = func() (any, error) { return client.GetCluster(ctx, clusterID) }
					want = clusterResult
				case "PostCluster":
					mock.EXPECT().PostCluster(ctx, clusterBuilder).Return(clusterResult, wantErr).Times(2)
					call = func() (any, error) { return client.PostCluster(ctx, clusterBuilder) }
					want = clusterResult
				case "UpdateCluster":
					mock.EXPECT().UpdateCluster(ctx, clusterID, clusterBuilder).Return(clusterResult, wantErr).Times(2)
					call = func() (any, error) { return client.UpdateCluster(ctx, clusterID, clusterBuilder) }
					want = clusterResult
				case "GetNodePool":
					mock.EXPECT().GetNodePool(ctx, nodePoolID).Return(nodePoolResult, wantErr).Times(2)
					call = func() (any, error) { return client.GetNodePool(ctx, nodePoolID) }
					want, field = nodePoolResult, "csNodePool"
				case "PostNodePool":
					mock.EXPECT().PostNodePool(ctx, clusterID, nodePoolBuilder).Return(nodePoolResult, wantErr).Times(2)
					call = func() (any, error) { return client.PostNodePool(ctx, clusterID, nodePoolBuilder) }
					want, field = nodePoolResult, "csNodePool"
				case "UpdateNodePool":
					mock.EXPECT().UpdateNodePool(ctx, nodePoolID, nodePoolBuilder).Return(nodePoolResult, wantErr).Times(2)
					call = func() (any, error) { return client.UpdateNodePool(ctx, nodePoolID, nodePoolBuilder) }
					want, field = nodePoolResult, "csNodePool"
				}
				for range 2 {
					got, err := call()
					require.True(t, got == want, "wrapper must preserve the returned pointer")
					require.True(t, err == wantErr, "wrapper must preserve the original error")
				}
				if outcome != "success" {
					require.Empty(t, logs.String(), "nil and error results must not emit snapshots")
					return
				}
				decoder := json.NewDecoder(logs)
				for range 2 {
					var entry map[string]any
					require.NoError(t, decoder.Decode(&entry), "identical responses must each log")
					require.Equal(t, "clusterService", entry["snapshotType"])
					require.Equal(t, "caller", entry["controller_name"])
					require.Equal(t, "original-resource", entry["resource_id"])
					require.Equal(t, "request", entry["request_id"])
					require.Equal(t, clusterID.String(), entry["clusterServiceID"])
					payload := entry[field].(map[string]any)
					if field == "csCluster" {
						require.Equal(t, float64(42), payload["autoscaler"].(map[string]any)["max_pod_grace_period"])
					} else {
						require.Equal(t, nodePoolID.String(), entry["nodePoolClusterServiceID"])
						require.Equal(t, "4.18.0", payload["version"].(map[string]any)["raw_id"])
					}
				}
				require.False(t, decoder.More(), "exactly one snapshot per response")
			})
		}
	}
}

type observedIterator[T any] struct {
	items    []*T
	err      error
	visited  int
	finished bool
}

func (i *observedIterator[T]) Items(context.Context) iter.Seq[*T] {
	return func(yield func(*T) bool) {
		for _, item := range i.items {
			i.visited++
			if !yield(item) {
				return
			}
		}
		i.finished = true
	}
}

func (i *observedIterator[T]) GetError() error {
	if i.finished {
		return i.err
	}
	return nil
}

func TestClientSnapshotLists(t *testing.T) {
	clusterID := metadataapi.Must(metadataapi.NewInternalID("/api/aro_hcp/v1alpha1/clusters/cluster"))
	cluster := metadataapi.Must(arohcpv1alpha1.NewCluster().ID("cluster").Build())
	nodePool := metadataapi.Must(arohcpv1alpha1.NewNodePool().ID("np").Build())
	for _, stopEarly := range []bool{false, true} {
		t.Run(fmtBool(stopEarly), func(t *testing.T) {
			ctx, logs := snapshotTestContext(t)
			mock := NewMockClusterServiceClientSpec(gomock.NewController(t))
			client := NewClusterServiceClientWithLogging(mock)
			pageErr := errors.New("later page failed")
			clusters := &observedIterator[arohcpv1alpha1.Cluster]{items: []*arohcpv1alpha1.Cluster{cluster, nil, cluster}, err: pageErr}
			nodePools := &observedIterator[arohcpv1alpha1.NodePool]{items: []*arohcpv1alpha1.NodePool{nodePool, nil, nodePool}, err: pageErr}
			mock.EXPECT().ListClusters("search").Return(clusters)
			mock.EXPECT().ListNodePools(clusterID, "search").Return(nodePools)
			ci := client.ListClusters("search")
			ni := client.ListNodePools(clusterID, "search")
			require.Empty(t, logs.String(), "constructing an iterator must not consume it")
			for item := range ci.Items(ctx) {
				require.True(t, item == nil || item == cluster)
				if stopEarly {
					break
				}
			}
			for item := range ni.Items(ctx) {
				require.True(t, item == nil || item == nodePool)
				if stopEarly {
					break
				}
			}
			wantCount := 4
			if stopEarly {
				wantCount = 2
				require.Equal(t, 1, clusters.visited)
				require.Equal(t, 1, nodePools.visited)
				require.NoError(t, ci.GetError())
				require.NoError(t, ni.GetError())
			} else {
				require.Same(t, pageErr, ci.GetError())
				require.Same(t, pageErr, ni.GetError())
			}
			require.Equal(t, wantCount, bytes.Count(logs.Bytes(), []byte(`"snapshotType":"clusterService"`)))
		})
	}
}

func fmtBool(v bool) string {
	if v {
		return "early break"
	}
	return "partial list error"
}

func TestClientSnapshotWithoutIdentity(t *testing.T) {
	ctx, logs := snapshotTestContext(t)
	logCluster(ctx, metadataapi.Must(arohcpv1alpha1.NewCluster().Build()), "")
	logNodePool(ctx, metadataapi.Must(arohcpv1alpha1.NewNodePool().Build()), "", "")
	require.Equal(t, 2, bytes.Count(logs.Bytes(), []byte(`"snapshotType":"clusterService"`)), "missing identity must not suppress data")
}

func TestClientSnapshotPassThrough(t *testing.T) {
	ctx, logs := snapshotTestContext(t)
	mock := NewMockClusterServiceClientSpec(gomock.NewController(t))
	client := NewClusterServiceClientWithLogging(mock)
	id := metadataapi.Must(metadataapi.NewInternalID("/api/aro_hcp/v1alpha1/clusters/cluster"))
	status := metadataapi.Must(arohcpv1alpha1.NewClusterStatus().Build())
	mock.EXPECT().GetClusterStatus(ctx, id).Return(status, nil)
	mock.EXPECT().DeleteCluster(ctx, id).Return(nil)
	got, err := client.GetClusterStatus(ctx, id)
	require.NoError(t, err)
	require.Same(t, status, got)
	require.NoError(t, client.DeleteCluster(ctx, id))
	require.Empty(t, logs.String(), "status responses and bodyless deletes are not full-document snapshots")
}

func TestMalformedSnapshotDoesNotEmitData(t *testing.T) {
	ctx, logs := snapshotTestContext(t)
	logCSData(ctx, "cluster-service state dump", "csCluster", []byte("invalid JSON"))
	require.NotContains(t, logs.String(), `"snapshotType"`)
	require.Contains(t, logs.String(), "failed to decode cluster-service snapshot")
}
