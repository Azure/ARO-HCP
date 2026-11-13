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
	"iter"
	"path"
	"strings"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"

	"github.com/Azure/ARO-HCP/internal/utils"
)

// NewClusterServiceClientWithLogging logs every successful cluster and node pool
// observation, including unchanged results. It adds no requests or cache, and
// preserves the caller's logger and the wrapped client's results and errors.
func NewClusterServiceClientWithLogging(client ClusterServiceClientSpec) ClusterServiceClientSpec {
	return &clusterServiceClientWithLogging{ClusterServiceClientSpec: client}
}

type clusterServiceClientWithLogging struct {
	// Other response types (including credentials) deliberately pass through:
	// only the documents historically emitted by CSStateDump are snapshots.
	ClusterServiceClientSpec
}

func (c *clusterServiceClientWithLogging) GetCluster(ctx context.Context, id InternalID) (*arohcpv1alpha1.Cluster, error) {
	obj, err := c.ClusterServiceClientSpec.GetCluster(ctx, id)
	if err == nil {
		logCluster(ctx, obj, id.String())
	}
	return obj, err
}

func (c *clusterServiceClientWithLogging) PostCluster(ctx context.Context, builder *arohcpv1alpha1.ClusterBuilder) (*arohcpv1alpha1.Cluster, error) {
	obj, err := c.ClusterServiceClientSpec.PostCluster(ctx, builder)
	if err == nil {
		logCluster(ctx, obj, "")
	}
	return obj, err
}

func (c *clusterServiceClientWithLogging) UpdateCluster(ctx context.Context, id InternalID, builder *arohcpv1alpha1.ClusterBuilder) (*arohcpv1alpha1.Cluster, error) {
	obj, err := c.ClusterServiceClientSpec.UpdateCluster(ctx, id, builder)
	if err == nil {
		logCluster(ctx, obj, id.String())
	}
	return obj, err
}

func (c *clusterServiceClientWithLogging) ListClusters(search string) ClusterListIterator {
	return &loggingIterator[arohcpv1alpha1.Cluster]{
		ListIterator: c.ClusterServiceClientSpec.ListClusters(search),
		log:          func(ctx context.Context, obj *arohcpv1alpha1.Cluster) { logCluster(ctx, obj, "") },
	}
}

func (c *clusterServiceClientWithLogging) GetNodePool(ctx context.Context, id InternalID) (*arohcpv1alpha1.NodePool, error) {
	obj, err := c.ClusterServiceClientSpec.GetNodePool(ctx, id)
	if err == nil {
		logNodePool(ctx, obj, id.String(), "")
	}
	return obj, err
}

func (c *clusterServiceClientWithLogging) PostNodePool(ctx context.Context, clusterID InternalID, builder *arohcpv1alpha1.NodePoolBuilder) (*arohcpv1alpha1.NodePool, error) {
	obj, err := c.ClusterServiceClientSpec.PostNodePool(ctx, clusterID, builder)
	if err == nil {
		logNodePool(ctx, obj, "", clusterID.String())
	}
	return obj, err
}

func (c *clusterServiceClientWithLogging) UpdateNodePool(ctx context.Context, id InternalID, builder *arohcpv1alpha1.NodePoolBuilder) (*arohcpv1alpha1.NodePool, error) {
	obj, err := c.ClusterServiceClientSpec.UpdateNodePool(ctx, id, builder)
	if err == nil {
		logNodePool(ctx, obj, id.String(), "")
	}
	return obj, err
}

func (c *clusterServiceClientWithLogging) ListNodePools(clusterID InternalID, search string) NodePoolListIterator {
	return &loggingIterator[arohcpv1alpha1.NodePool]{
		ListIterator: c.ClusterServiceClientSpec.ListNodePools(clusterID, search),
		log:          func(ctx context.Context, obj *arohcpv1alpha1.NodePool) { logNodePool(ctx, obj, "", clusterID.String()) },
	}
}

type loggingIterator[T any] struct {
	ListIterator[T]
	log func(context.Context, *T)
}

func (i *loggingIterator[T]) Items(ctx context.Context) iter.Seq[*T] {
	return func(yield func(*T) bool) {
		for obj := range i.ListIterator.Items(ctx) {
			// Log the fully resolved result before the caller can modify it. Do not
			// advance the iterator past an early break or hide a later page error.
			if obj != nil {
				i.log(ctx, obj)
			}
			if !yield(obj) {
				return
			}
		}
	}
}

func logCluster(ctx context.Context, obj *arohcpv1alpha1.Cluster, id string) {
	if obj == nil {
		return
	}
	if obj.HREF() != "" {
		id = obj.HREF()
	} else if id == "" && obj.ID() != "" {
		id = GenerateAROHCPClusterHREF(obj.ID())
	}
	var buf bytes.Buffer
	if err := arohcpv1alpha1.MarshalCluster(obj, &buf); err != nil {
		utils.LoggerFromContext(ctx).Error(err, "failed to serialize cluster-service cluster snapshot")
		return
	}
	logCSData(ctx, "cluster-service state dump", "csCluster", buf.Bytes(),
		"clusterServiceID", strings.ToLower(id))
}

func logNodePool(ctx context.Context, obj *arohcpv1alpha1.NodePool, id, clusterID string) {
	if obj == nil {
		return
	}
	if obj.HREF() != "" {
		id = obj.HREF()
	} else if id == "" && clusterID != "" && obj.ID() != "" {
		id = path.Join(clusterID, nodePoolKey, obj.ID())
	}
	if clusterID == "" && matchClusterPath(id) != "" {
		clusterID = path.Dir(path.Dir(id))
	}
	var buf bytes.Buffer
	if err := arohcpv1alpha1.MarshalNodePool(obj, &buf); err != nil {
		utils.LoggerFromContext(ctx).Error(err, "failed to serialize cluster-service node pool snapshot")
		return
	}
	logCSData(ctx, "cluster-service node pool state dump", "csNodePool", buf.Bytes(),
		"clusterServiceID", strings.ToLower(clusterID), "nodePoolClusterServiceID", strings.ToLower(id))
}

func logCSData(ctx context.Context, message, field string, data []byte, values ...any) {
	// The SDK types do not implement json.Marshaler. Decode their custom JSON
	// into a structured value so every logr sink emits the same snapshot shape.
	var content map[string]any
	if err := json.Unmarshal(data, &content); err != nil {
		utils.LoggerFromContext(ctx).Error(err, "failed to decode cluster-service snapshot")
		return
	}
	// Consumers accept this marker or the legacy CSStateDump controller name.
	// Keep real controller attribution and all caller resource/trace fields intact.
	values = append(values, "snapshotType", "clusterService", field, content)
	utils.LoggerFromContext(ctx).Info(message, values...)
}
