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

package database

//go:generate $MOCKGEN -typed -source=crud_subscription.go -destination=mock_crud_subscription.go -package database SubscriptionCRUD

import (
	"context"
	"fmt"
	"strings"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

	"github.com/Azure/ARO-HCP/internal/api/arm"
)

type SubscriptionCRUD interface {
	ResourceCRUD[arm.Subscription]
}

type subscriptionCRUD struct {
	containerClient *azcosmos.ContainerClient
}

var _ SubscriptionCRUD = (*subscriptionCRUD)(nil)

func NewSubscriptionCRUD(containerClient *azcosmos.ContainerClient) SubscriptionCRUD {
	return &subscriptionCRUD{
		containerClient: containerClient,
	}
}

func (d *subscriptionCRUD) GetByID(ctx context.Context, cosmosID string) (*arm.Subscription, error) {
	// for subscriptions, the cosmosID IS the partitionKey (at least for now)
	if strings.ToLower(cosmosID) != cosmosID {
		return nil, fmt.Errorf("cosmosID must be lowercase, not: %q", cosmosID)
	}
	partitionKey := strings.ToLower(cosmosID)

	return getByItemID[arm.Subscription, Subscription](ctx, d.containerClient, partitionKey, cosmosID)
}

func (d *subscriptionCRUD) Get(ctx context.Context, resourceName string) (*arm.Subscription, error) {
	// for subscriptions, the resourceName IS the partitionKey (at least for now).
	completeResourceID, err := arm.ToSubscriptionResourceID(resourceName)
	if err != nil {
		return nil, fmt.Errorf("failed to make ResourceID path for '%s': %w", resourceName, err)
	}
	partitionKey := strings.ToLower(completeResourceID.SubscriptionID)

	return get[arm.Subscription, Subscription](ctx, d.containerClient, partitionKey, completeResourceID)
}

func (d *subscriptionCRUD) List(ctx context.Context, options *DBClientListResourceDocsOptions) (DBClientIterator[arm.Subscription], error) {
	// prefix is intentionally nil so that we don't have a resource prefix until after we've written all reords with a resourceID
	var prefix *azcorearm.ResourceID
	// list must be across all partitions
	partitionKey := ""

	return list[arm.Subscription, Subscription](ctx, d.containerClient, partitionKey, &azcorearm.SubscriptionResourceType, prefix, options, false)
}

func (d *subscriptionCRUD) AddCreateToTransaction(ctx context.Context, transaction DBTransaction, newObj *arm.Subscription, opts *azcosmos.TransactionalBatchItemOptions) (string, error) {
	return addCreateToTransaction[arm.Subscription, Subscription](ctx, transaction, newObj, opts)
}

func (d *subscriptionCRUD) AddReplaceToTransaction(ctx context.Context, transaction DBTransaction, newObj *arm.Subscription, opts *azcosmos.TransactionalBatchItemOptions) (string, error) {
	return addReplaceToTransaction[arm.Subscription, Subscription](ctx, transaction, newObj, opts)
}

func (d *subscriptionCRUD) Create(ctx context.Context, newObj *arm.Subscription, options *azcosmos.ItemOptions) (*arm.Subscription, error) {
	partitionKey := strings.ToLower(newObj.ResourceID.SubscriptionID)
	return create[arm.Subscription, Subscription](ctx, d.containerClient, partitionKey, newObj, options)
}

func (d *subscriptionCRUD) Replace(ctx context.Context, newObj *arm.Subscription, options *azcosmos.ItemOptions) (*arm.Subscription, error) {
	partitionKey := strings.ToLower(newObj.ResourceID.SubscriptionID)
	return replace[arm.Subscription, Subscription](ctx, d.containerClient, partitionKey, newObj, options)
}

func (d *subscriptionCRUD) Delete(ctx context.Context, resourceName string) error {
	completeResourceID, err := arm.ToSubscriptionResourceID(resourceName)
	if err != nil {
		return fmt.Errorf("failed to make ResourceID path for '%s': %w", resourceName, err)
	}
	partitionKey := strings.ToLower(completeResourceID.SubscriptionID)

	return deleteResource(ctx, d.containerClient, partitionKey, completeResourceID)
}
