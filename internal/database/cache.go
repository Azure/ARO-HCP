package database

import (
	"context"
)

var _ DBClient = &Cache{}

// Cache is a simple DBClient that allows us to perform simple tests without needing a real CosmosDB. For production,
// use CosmosDBClient instead. Call NewCache() to initialize a Cache correctly.
type Cache struct {
	cluster      map[string]*HCPOpenShiftClusterDocument
	nodePool     map[string]*NodePoolDocument
	operation    map[string]*OperationDocument
	subscription map[string]*SubscriptionDocument
}

// NewCache initializes a new Cache to allow for simple tests without needing a real CosmosDB. For production, use
// NewCosmosDBConfig instead.
func NewCache() DBClient {
	return &Cache{
		cluster:      make(map[string]*HCPOpenShiftClusterDocument),
		nodePool:     make(map[string]*NodePoolDocument),
		subscription: make(map[string]*SubscriptionDocument),
	}
}

func (c *Cache) DBConnectionTest(ctx context.Context) error {
	return nil
}

func (c *Cache) GetClusterDoc(ctx context.Context, resourceID string, subscriptionID string) (*HCPOpenShiftClusterDocument, error) {
	if _, ok := c.cluster[resourceID]; ok {
		return c.cluster[resourceID], nil
	}

	return nil, ErrNotFound
}

func (c *Cache) SetClusterDoc(ctx context.Context, doc *HCPOpenShiftClusterDocument) error {
	c.cluster[doc.Key] = doc
	return nil
}

func (c *Cache) DeleteClusterDoc(ctx context.Context, resourceID string, subscriptionID string) error {
	delete(c.cluster, resourceID)
	return nil
}

func (c *Cache) GetNodePoolDoc(ctx context.Context, resourceID string) (*NodePoolDocument, error) {
	if _, ok := c.nodePool[resourceID]; ok {
		return c.nodePool[resourceID], nil
	}

	return nil, ErrNotFound
}

func (c *Cache) SetNodePoolDoc(ctx context.Context, doc *NodePoolDocument) error {
	c.nodePool[doc.Key] = doc
	return nil
}

func (c *Cache) DeleteNodePoolDoc(ctx context.Context, resourceID string) error {
	delete(c.nodePool, resourceID)
	return nil
}

func (c *Cache) GetOperationDoc(ctx context.Context, operationID string) (*OperationDocument, error) {
	if _, ok := c.operation[operationID]; ok {
		return c.operation[operationID], nil
	}

	return nil, ErrNotFound
}

func (c *Cache) SetOperationDoc(ctx context.Context, doc *OperationDocument) error {
	c.operation[doc.ID] = doc
	return nil
}

func (c *Cache) DeleteOperationDoc(ctx context.Context, operationID string) error {
	delete(c.operation, operationID)
	return nil
}

func (c *Cache) GetSubscriptionDoc(ctx context.Context, subscriptionID string) (*SubscriptionDocument, error) {
	if _, ok := c.subscription[subscriptionID]; ok {
		return c.subscription[subscriptionID], nil
	}

	return nil, ErrNotFound
}

func (c *Cache) SetSubscriptionDoc(ctx context.Context, doc *SubscriptionDocument) error {
	c.subscription[doc.PartitionKey] = doc
	return nil
}
