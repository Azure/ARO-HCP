package database

import (
	"context"
	"strings"
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
	// Make sure lookup keys are lowercase.
	key := strings.ToLower(resourceID)

	if doc, ok := c.cluster[key]; ok {
		return doc, nil
	}

	return nil, ErrNotFound
}

func (c *Cache) SetClusterDoc(ctx context.Context, doc *HCPOpenShiftClusterDocument) error {
	// Make sure lookup keys are lowercase.
	key := strings.ToLower(doc.Key)

	c.cluster[key] = doc
	return nil
}

func (c *Cache) DeleteClusterDoc(ctx context.Context, resourceID string, subscriptionID string) error {
	// Make sure lookup keys are lowercase.
	key := strings.ToLower(resourceID)

	delete(c.cluster, key)
	return nil
}

func (c *Cache) GetNodePoolDoc(ctx context.Context, resourceID string) (*NodePoolDocument, error) {
	// Make sure lookup keys are lowercase.
	key := strings.ToLower(resourceID)

	if doc, ok := c.nodePool[key]; ok {
		return doc, nil
	}

	return nil, ErrNotFound
}

func (c *Cache) SetNodePoolDoc(ctx context.Context, doc *NodePoolDocument) error {
	// Make sure lookup keys are lowercase.
	key := strings.ToLower(doc.Key)

	c.nodePool[key] = doc
	return nil
}

func (c *Cache) DeleteNodePoolDoc(ctx context.Context, resourceID string) error {
	// Make sure lookup keys are lowercase.
	key := strings.ToLower(resourceID)

	delete(c.nodePool, key)
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
