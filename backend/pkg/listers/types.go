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

package listers

import (
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/tools/cache"

	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type BackendListers struct {
	SubscriptionLister                    SubscriptionLister
	ActiveOperationLister                 ActiveOperationLister
	HCPOpenShiftClusterLister             ClusterLister
	HCPOpenShiftClusterNodePoolLister     NodePoolLister
	HCPOpenShiftClusterExternalAuthLister ExternalAuthLister
	ServiceProviderClusterLister          ServiceProviderClusterLister
	DNSReservationLister                  DNSReservationLister
}

const ByResourceGroup = "byResourceGroup"

const ByCluster = "byCluster"

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
