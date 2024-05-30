package database

import (
	"context"
	"log"
)

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
	log.Printf("GET CALL: Resource ID: %s  cluster: %v", resourceID, c.cluster[resourceID])
	if _, ok := c.cluster[resourceID]; ok {
		return c.cluster[resourceID], nil
	}

	return nil, ErrNotFound
}

func (c *Cache) SetClusterDoc(ctx context.Context, doc *HCPOpenShiftClusterDocument) error {
	log.Printf("BEFORE SET: Resource ID: %s   clusters: %v", doc.Key, c.cluster)
	c.cluster[doc.Key] = doc
	log.Printf("AFTER SET: Resource ID: %s   clusters: %v", doc.Key, c.cluster[doc.Key])
	return nil
}

func (c *Cache) DeleteClusterDoc(ctx context.Context, resourceID string, partitionKey string) error {
	delete(c.cluster, resourceID)
	return nil
}

func (c *Cache) GetSubscriptionDoc(ctx context.Context, partitionKey string) (*SubscriptionDocument, error) {
	log.Print("partition key: ", partitionKey, "Subs: ", c.subscription)
	if _, ok := c.subscription[partitionKey]; ok {
		return c.subscription[partitionKey], nil
	}

	return nil, ErrNotFound
}

func (c *Cache) SetSubscriptionDoc(ctx context.Context, doc *SubscriptionDocument) error {
	log.Printf("BEFORE SET: partition key: %s   subs: %v", doc.PartitionKey, c.subscription)
	c.subscription[doc.PartitionKey] = doc
	log.Printf("AFTER SET: partition key: %s   subs: %v", doc.PartitionKey, c.subscription[doc.PartitionKey])
	return nil
}
