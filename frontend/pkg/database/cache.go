package database

import "context"

var _ DBClient = &Cache{}

// Cache is a simple DBClient that allows us to perform simple tests without needing a real CosmosDB. For production,
// use CosmosDBClient instead. Call NewCache() to initialize a Cache correctly.
type Cache struct {
	cluster      map[string]*HCPOpenShiftClusterDocument
	subscription map[string]*SubscriptionDocument
}

// NewCache initializes a new Cache to allow for simple tests without needing a real CosmosDB. For production, use
// NewCosmosDBConfig instead.
func NewCache() DBClient {
	return &Cache{
		cluster:      make(map[string]*HCPOpenShiftClusterDocument),
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
	c.cluster[doc.ResourceID] = doc
	return nil
}

func (c *Cache) DeleteClusterDoc(ctx context.Context, resourceID string, subscriptionID string) error {
	delete(c.cluster, resourceID)
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
