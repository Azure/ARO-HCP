package database

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

import (
	"context"
	"strings"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/arm"
)

var _ DBClient = &Cache{}

// Cache is a simple DBClient that allows us to perform simple tests without needing a real CosmosDB. For production,
// use CosmosDBClient instead. Call NewCache() to initialize a Cache correctly.
type Cache struct {
	resource     map[string]*ResourceDocument
	operation    map[string]*OperationDocument
	subscription map[string]*SubscriptionDocument
}

// NewCache initializes a new Cache to allow for simple tests without needing a real CosmosDB. For production, use
// NewCosmosDBConfig instead.
func NewCache() DBClient {
	return &Cache{
		resource:     make(map[string]*ResourceDocument),
		operation:    make(map[string]*OperationDocument),
		subscription: make(map[string]*SubscriptionDocument),
	}
}

func (c *Cache) DBConnectionTest(ctx context.Context) error {
	return nil
}

func (c *Cache) GetLockClient() *LockClient {
	return nil
}

func (c *Cache) GetResourceDoc(ctx context.Context, resourceID *arm.ResourceID) (*ResourceDocument, error) {
	// Make sure lookup keys are lowercase.
	key := strings.ToLower(resourceID.String())

	if doc, ok := c.resource[key]; ok {
		return doc, nil
	}

	return nil, ErrNotFound
}

func (c *Cache) CreateResourceDoc(ctx context.Context, doc *ResourceDocument) error {
	// Make sure lookup keys are lowercase.
	key := strings.ToLower(doc.Key.String())

	c.resource[key] = doc
	return nil
}

func (c *Cache) UpdateResourceDoc(ctx context.Context, resourceID *arm.ResourceID, callback func(*ResourceDocument) bool) (bool, error) {
	// Make sure lookup keys are lowercase.
	key := strings.ToLower(resourceID.String())

	if doc, ok := c.resource[key]; ok {
		return callback(doc), nil
	}

	return false, ErrNotFound
}

func (c *Cache) DeleteResourceDoc(ctx context.Context, resourceID *arm.ResourceID) error {
	// Make sure lookup keys are lowercase.
	key := strings.ToLower(resourceID.String())

	delete(c.resource, key)
	return nil
}

func (c *Cache) ListResourceDocs(ctx context.Context, prefix *arm.ResourceID, resourceType *azcorearm.ResourceType, pageSizeHint int32, continuationToken *string) ([]*ResourceDocument, *string, error) {
	var resourceList []*ResourceDocument

	// Make sure key prefix is lowercase.
	prefixString := strings.ToLower(prefix.String() + "/")

	for key, doc := range c.resource {
		if strings.HasPrefix(key, prefixString) {
			if resourceType == nil || strings.EqualFold(resourceType.String(), doc.Key.ResourceType.String()) {
				resourceList = append(resourceList, doc)
			}
		}
	}

	return resourceList, nil, nil
}

func (c *Cache) GetOperationDoc(ctx context.Context, operationID string) (*OperationDocument, error) {
	// Make sure lookup keys are lowercase.
	key := strings.ToLower(operationID)

	if doc, ok := c.operation[key]; ok {
		return doc, nil
	}

	return nil, ErrNotFound
}

func (c *Cache) CreateOperationDoc(ctx context.Context, doc *OperationDocument) error {
	// Make sure lookup keys are lowercase.
	key := strings.ToLower(doc.ID)

	c.operation[key] = doc
	return nil
}

func (c *Cache) DeleteOperationDoc(ctx context.Context, operationID string) error {
	// Make sure lookup keys are lowercase.
	key := strings.ToLower(operationID)

	delete(c.operation, key)
	return nil
}

func (c *Cache) GetSubscriptionDoc(ctx context.Context, subscriptionID string) (*SubscriptionDocument, error) {
	// Make sure lookup keys are lowercase.
	key := strings.ToLower(subscriptionID)

	if doc, ok := c.subscription[key]; ok {
		return doc, nil
	}

	return nil, ErrNotFound
}

func (c *Cache) CreateSubscriptionDoc(ctx context.Context, doc *SubscriptionDocument) error {
	// Make sure lookup keys are lowercase.
	key := strings.ToLower(doc.ID)

	c.subscription[key] = doc
	return nil
}

func (c *Cache) UpdateSubscriptionDoc(ctx context.Context, subscriptionID string, callback func(*SubscriptionDocument) bool) (bool, error) {
	// Make sure lookup keys are lowercase.
	key := strings.ToLower(subscriptionID)

	if doc, ok := c.subscription[key]; ok {
		return callback(doc), nil
	}

	return false, ErrNotFound
}
