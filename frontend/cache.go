package main

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

import "github.com/Azure/ARO-HCP/internal/api"

type cache struct {
	cluster map[string]*api.HCPOpenShiftCluster
}

// NewCache returns a new cache.
func NewCache() *cache {
	return &cache{
		cluster: make(map[string]*api.HCPOpenShiftCluster),
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
