package main

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

import (
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/subscription"
)

type cache struct {
	cluster      map[string]*api.HCPOpenShiftCluster
	subscription map[string]*subscription.Subscription
}

// NewCache returns a new cache.
func NewCache() *cache {
	return &cache{
		cluster:      make(map[string]*api.HCPOpenShiftCluster),
		subscription: make(map[string]*subscription.Subscription),
	}
}

func (c *cache) GetCluster(id string) (*api.HCPOpenShiftCluster, bool) {
	cluster, found := c.cluster[id]
	return cluster, found
}

func (c *cache) SetCluster(id string, cluster *api.HCPOpenShiftCluster) {
	c.cluster[id] = cluster
}

func (c *cache) DeleteCluster(id string) {
	delete(c.cluster, id)
}

func (c *cache) GetSubscription(id string) (*subscription.Subscription, bool) {
	subscription, found := c.subscription[id]
	return subscription, found
}

func (c *cache) SetSubscription(id string, subscription *subscription.Subscription) {
	c.subscription[id] = subscription
}

func (c *cache) DeleteSubscription(id string) {
	delete(c.subscription, id)
}
