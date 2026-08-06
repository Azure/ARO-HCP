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

// Package listerutils provides the generic, reusable helpers shared by the
// cache.Indexer-backed listers in both internal/database/listers and
// backend/pkg/listers. The generic store/indexer accessors and the canonical
// index-key builders previously lived (duplicated) in each package's types.go;
// centralizing them here keeps a single implementation.
package listerutils

import (
	"fmt"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/tools/cache"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// ListAll retrieves all items from a store, casting each to *T.
func ListAll[T any](store cache.Store) ([]*T, error) {
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

// GetByKey retrieves a single item from an indexer by key, casting it to *T.
func GetByKey[T any](indexer cache.Indexer, key string) (*T, error) {
	item, exists, err := indexer.GetByKey(key)
	if apierrors.IsNotFound(err) {
		return nil, cosmosstorageutils.NewNotFoundError()
	}
	if err != nil {
		return nil, utils.TrackError(err)
	}
	if !exists {
		return nil, cosmosstorageutils.NewNotFoundError()
	}
	typed, ok := item.(*T)
	if !ok {
		return nil, utils.TrackError(fmt.Errorf("expected *%T, got %T", *new(T), item))
	}
	return typed, nil
}

// ListFromIndex retrieves items from an indexer by index name and key, casting each to *T.
func ListFromIndex[T any](indexer cache.Indexer, indexName, key string) ([]*T, error) {
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

// ClusterIndexKey returns the canonical (lower-cased) ByCluster index key for an
// HCPOpenShiftCluster identified by subscription, resource group, and name.
func ClusterIndexKey(subscriptionID, resourceGroupName, clusterName string) string {
	return strings.ToLower(api.ToClusterResourceIDString(subscriptionID, resourceGroupName, clusterName))
}

// NodePoolIndexKey returns the canonical (lower-cased) ByNodePool index key for a
// NodePool identified by its containing cluster plus its own name.
func NodePoolIndexKey(subscriptionID, resourceGroupName, clusterName, nodePoolName string) string {
	return strings.ToLower(api.ToNodePoolResourceIDString(subscriptionID, resourceGroupName, clusterName, nodePoolName))
}
