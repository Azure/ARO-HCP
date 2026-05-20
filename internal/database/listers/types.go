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
package listers

import (
	"fmt"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/tools/cache"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/utils"
)

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

// listAll retrieves all items from a store, casting each to *T.
func listAll[T any](store cache.Store) ([]*T, error) {
	items := store.List()
	result := make([]*T, 0, len(items))
	for _, item := range items {
		typed, ok := item.(*T)
		if !ok {
			return nil, utils.TrackError(fmt.Errorf("expected *%T, got %T", *new(T), item))
		}
		result = append(result, typed)
	}
	return result, nil
}

// getByKey retrieves a single item from an indexer by key, casting it to *T.
func getByKey[T any](indexer cache.Indexer, key string) (*T, error) {
	item, exists, err := indexer.GetByKey(key)
	if apierrors.IsNotFound(err) {
		return nil, database.NewNotFoundError()
	}
	if err != nil {
		return nil, utils.TrackError(err)
	}
	if !exists {
		return nil, database.NewNotFoundError()
	}
	typed, ok := item.(*T)
	if !ok {
		return nil, utils.TrackError(fmt.Errorf("expected *%T, got %T", *new(T), item))
	}
	return typed, nil
}

// clusterIndexKey returns the canonical (lower-cased) ByCluster index key for an
// HCPOpenShiftCluster identified by subscription, resource group, and name.
func clusterIndexKey(subscriptionID, resourceGroupName, clusterName string) string {
	return strings.ToLower(api.ToClusterResourceIDString(subscriptionID, resourceGroupName, clusterName))
}

// nodePoolIndexKey returns the canonical (lower-cased) ByNodePool index key for a
// NodePool identified by its containing cluster plus its own name.
func nodePoolIndexKey(subscriptionID, resourceGroupName, clusterName, nodePoolName string) string {
	return strings.ToLower(api.ToNodePoolResourceIDString(subscriptionID, resourceGroupName, clusterName, nodePoolName))
}

// listFromIndex retrieves items from an indexer by index name and key, casting each to *T.
func listFromIndex[T any](indexer cache.Indexer, indexName, key string) ([]*T, error) {
	items, err := indexer.ByIndex(indexName, key)
	if err != nil {
		return nil, utils.TrackError(err)
	}
	result := make([]*T, 0, len(items))
	for _, item := range items {
		typed, ok := item.(*T)
		if !ok {
			return nil, utils.TrackError(fmt.Errorf("expected *%T, got %T", *new(T), item))
		}
		result = append(result, typed)
	}
	return result, nil
}
