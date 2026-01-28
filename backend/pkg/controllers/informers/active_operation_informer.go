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

package informers

import (
	"context"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type activeOperationInformer struct {
	cosmosClient          database.DBClient
	subscriptionLister    listers.BasicReader[arm.Subscription]
	activeOperationLister listers.PerSubscriptionMaintainer[api.Operation]
}

// NewActiveOperationInformerController periodically lists all active operations and updates the cache
func NewActiveOperationInformerController(
	cosmosClient database.DBClient,
	subscriptionLister listers.SubscriptionLister,
	activeOperationLister listers.PerSubscriptionMaintainer[api.Operation],
) controllerutils.SubscriptionSyncer {
	c := &activeOperationInformer{
		cosmosClient:          cosmosClient,
		subscriptionLister:    subscriptionLister,
		activeOperationLister: activeOperationLister,
	}

	return c
}

func (c *activeOperationInformer) SyncOnce(ctx context.Context, keyObj controllerutils.SubscriptionKey) error {
	// Collect active operations for this subscription
	operations := []*api.Operation{}
	operationIterator := c.cosmosClient.Operations(keyObj.SubscriptionID).ListActiveOperations(nil)
	for _, operation := range operationIterator.Items(ctx) {
		operations = append(operations, operation)
	}
	if err := operationIterator.GetError(); err != nil {
		return utils.TrackError(err)
	}

	// Build the map by operation ID name
	operationsByName := make(map[string]*api.Operation, len(operations))
	for _, operation := range operations {
		if operation.OperationID != nil {
			operationsByName[operation.OperationID.Name] = operation
		}
	}

	// Update this subscription's operations in the per-subscription lister
	newLister := listers.NewReadOnlyContentLister(operations, operationsByName)
	c.activeOperationLister.SetSubscriptionValue(keyObj.SubscriptionID, newLister)

	return nil
}
