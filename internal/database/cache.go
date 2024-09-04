package database

import (
	"context"
	"strings"

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

func (c *Cache) GetResourceDoc(ctx context.Context, resourceID *arm.ResourceID) (*ResourceDocument, error) {
	// Make sure lookup keys are lowercase.
	key := strings.ToLower(resourceID.String())

	if doc, ok := c.resource[key]; ok {
		return doc, nil
	}

	return nil, ErrNotFound
}

func (c *Cache) SetResourceDoc(ctx context.Context, doc *ResourceDocument) error {
	// Make sure lookup keys are lowercase.
	key := strings.ToLower(doc.Key.String())

	c.resource[key] = doc
	return nil
}

func (c *Cache) DeleteResourceDoc(ctx context.Context, resourceID *arm.ResourceID) error {
	// Make sure lookup keys are lowercase.
	key := strings.ToLower(resourceID.String())

	delete(c.resource, key)
	return nil
}

func (c *Cache) GetOperationDoc(ctx context.Context, operationID string) (*OperationDocument, error) {
	// Make sure lookup keys are lowercase.
	key := strings.ToLower(operationID)

	if doc, ok := c.operation[key]; ok {
		return doc, nil
	}

	return nil, ErrNotFound
}

func (c *Cache) SetOperationDoc(ctx context.Context, doc *OperationDocument) error {
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

func (c *Cache) SetSubscriptionDoc(ctx context.Context, doc *SubscriptionDocument) error {
	// Make sure lookup keys are lowercase.
	key := strings.ToLower(doc.PartitionKey)

	c.subscription[key] = doc
	return nil
}
