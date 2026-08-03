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

// Package listers provides cache.Indexer-backed listers for the kube-applier
// *Desire resource types. Each lister is informer-fed: a SharedIndexInformer
// (see ../informers) populates the indexer, and these listers expose typed
// Get/List APIs over it.
//
// Both the kube-applier binary (single-partition view) and the backend
// (cross-partition view) use the same lister implementations. The difference
// is in which database.KubeApplierGlobalListers feeds the informer.
//
// The generic store/indexer helpers (ListAll, GetByKey, ListFromIndex) and the
// index-key builders (ClusterIndexKey, NodePoolIndexKey) live in the shared
// listerutils package and are used directly by the listers in this package.
package listers

// Index names registered on the *Desire informers as well as on the
// pre-existing cluster-service-shard index used by backend listers.
const (
	// ByCSProvisionShard groups documents by their Cluster Service
	// provision-shard ID. Used by backend controllers that fan out per
	// shard.
	ByCSProvisionShard = "byCSProvisionShard"
	// ByManagementCluster groups *Desires by their lower-cased
	// spec.managementCluster value. Used by the kube-applier binary.
	ByManagementCluster = "byManagementCluster"
	// ByCluster groups *Desires by the lower-cased resource ID of their
	// containing HCPOpenShiftCluster (covering both cluster- and
	// node-pool-scoped desires under that cluster).
	ByCluster = "byCluster"
	// ByNodePool groups node-pool-scoped *Desires by the lower-cased resource
	// ID of their containing NodePool. Cluster-scoped desires are not in this
	// index.
	ByNodePool = "byNodePool"
)
