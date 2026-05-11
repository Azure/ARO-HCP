// Copyright 2026 Microsoft Corporation
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package cincinnati

//go:generate $MOCKGEN -typed -source=cache.go -destination=mock_cache.go -package cincinnati ClientCache

import (
	"net/http"
	"sync"

	"github.com/google/uuid"

	"k8s.io/utils/lru"

	"github.com/openshift/cluster-version-operator/pkg/cincinnati"
)

// ClientCache provides cached Client instances keyed by cluster UUID.
type ClientCache interface {
	GetOrCreateClient(clusterUUID uuid.UUID) Client
}

// clientCache implements ClientCache with LRU caching.
type clientCache struct {
	mu        sync.Mutex
	cache     *lru.Cache
	transport *http.Transport
	userAgent string
}

// Ensure clientCache implements ClientCache
var _ ClientCache = (*clientCache)(nil)

// NewClientCache creates a new ClientCache.
func NewClientCache() ClientCache {
	return &clientCache{
		cache:     lru.New(100000),
		transport: http.DefaultTransport.(*http.Transport).Clone(),
		userAgent: "ARO-HCP",
	}
}

// GetOrCreateClient returns a Cincinnati client for the given cluster UUID, using a cache.
// The clusterUUID is the external ID of the cluster from Cluster Service.
func (c *clientCache) GetOrCreateClient(clusterUUID uuid.UUID) Client {
	// Fast path: check cache without lock (k8s lru is thread-safe for reads)
	if client, ok := c.cache.Get(clusterUUID); ok {
		return client.(Client)
	}

	// Slow path: cache miss, acquire lock to create client
	c.mu.Lock()
	defer c.mu.Unlock()

	// Double-check: another goroutine might have created it while we waited
	if client, ok := c.cache.Get(clusterUUID); ok {
		return client.(Client)
	}

	newClient := cincinnati.NewClient(clusterUUID, c.transport, c.userAgent, NewAlwaysConditionRegistry())
	c.cache.Add(clusterUUID, newClient)
	return newClient
}
