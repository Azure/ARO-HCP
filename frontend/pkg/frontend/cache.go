package frontend

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

import (
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
)

type Cache struct {
	cluster      map[string]*api.HCPOpenShiftCluster
	subscription map[string]*arm.Subscription
}

// NewCache returns a new cache.
func NewCache() *Cache {
	return &Cache{
		cluster:      make(map[string]*api.HCPOpenShiftCluster),
		subscription: make(map[string]*arm.Subscription),
	}
}

func (c *Cache) GetCluster(id string) (*api.HCPOpenShiftCluster, bool) {
	cluster, found := c.cluster[id]
	return cluster, found
}

func (c *Cache) SetCluster(id string, cluster *api.HCPOpenShiftCluster) {
	c.cluster[id] = cluster
}

func (c *Cache) DeleteCluster(id string) {
	delete(c.cluster, id)
}

func (c *Cache) GetSubscription(id string) (*arm.Subscription, bool) {
	subscription, found := c.subscription[id]
	return subscription, found
}

func (c *Cache) SetSubscription(id string, subscription *arm.Subscription) {
	c.subscription[id] = subscription
}

func (c *Cache) DeleteSubscription(id string) {
	delete(c.subscription, id)
}
