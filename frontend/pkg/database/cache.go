package database

import "context"

var _ DBClient = &Cache{}

type Cache struct {
	cluster      map[string]*HCPOpenShiftClusterDocument
	subscription map[string]*SubscriptionDocument
}

func NewCache() DBClient {
	return &Cache{
		cluster:      make(map[string]*HCPOpenShiftClusterDocument),
		subscription: make(map[string]*SubscriptionDocument),
	}
}

func (c *Cache) DBConnectionTest(ctx context.Context) (string, error) {
	return "using cache", nil
}

func (c *Cache) GetClusterDoc(ctx context.Context, resourceID string, partitionKey string) (*HCPOpenShiftClusterDocument, error) {
	if _, ok := c.cluster[resourceID]; ok {
		return c.cluster[resourceID], nil
	}

	return nil, ErrNotFound
}

func (c *Cache) SetClusterDoc(ctx context.Context, doc *HCPOpenShiftClusterDocument) error {
	c.cluster[doc.ResourceID] = doc
	return nil
}

func (c *Cache) DeleteClusterDoc(ctx context.Context, resourceID string, partitionKey string) error {
	delete(c.cluster, resourceID)
	return nil
}

func (c *Cache) GetSubscriptionDoc(ctx context.Context, partitionKey string) (*SubscriptionDocument, error) {
	if _, ok := c.subscription[partitionKey]; ok {
		return c.subscription[partitionKey], nil
	}

	return nil, ErrNotFound
}

func (c *Cache) SetSubscriptionDoc(ctx context.Context, doc *SubscriptionDocument) error {
	c.subscription[doc.PartitionKey] = doc
	return nil
}
