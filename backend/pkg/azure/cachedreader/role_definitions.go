// Copyright 2026 Microsoft Corporation
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package cachedreader provides Azure SDK-shaped clients that add caching on top of
// thin interfaces in backend/pkg/azure/client.
package cachedreader

import (
	"context"
	"fmt"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v2"

	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// roleDefinitionResourceIDCacheKeyTTL defines how long a cached role definition is considered valid.
// After this TTL (6 hours), the cached value is considered stale and will be refreshed
// on the next GetByID request via the Azure Role Definitions API.
const roleDefinitionResourceIDCacheKeyTTL = 6 * time.Hour

// CachedRoleDefinitions mirrors the method set of armauthorization.RoleDefinitionsClient.
// Implementations may add caching; callers should use a type from this package when they
// accept cached Azure reads.
type CachedRoleDefinitions interface {
	GetCachedByID(ctx context.Context, roleDefinitionResourceID string, options *armauthorization.RoleDefinitionsClientGetByIDOptions) (armauthorization.RoleDefinitionsClientGetByIDResponse, error)
}

// cachedRoleDefinitions implements CachedRoleDefinitions by delegating to azureclient.RoleDefinitionsClient.
// GetByID responses are cached in memory with a TTL and singleflight deduplication; Get and
// NewListPager are forwarded without caching.
type cachedRoleDefinitions struct {
	inner azureclient.RoleDefinitionsClient

	roleDefinitionsCacheLock sync.RWMutex
	roleDefinitionsCache     map[string]cachedGetByIDResponse
	sfGroup                  singleflight.Group
}

type cachedGetByIDResponse struct {
	response   armauthorization.RoleDefinitionsClientGetByIDResponse
	lastUpdate time.Time
}

// NewCachedRoleDefinitions returns a CachedRoleDefinitions implementation that caches GetByID.
func NewCachedRoleDefinitions(inner azureclient.RoleDefinitionsClient) CachedRoleDefinitions {
	return &cachedRoleDefinitions{
		inner:                inner,
		roleDefinitionsCache: make(map[string]cachedGetByIDResponse),
	}
}

// GetByID returns a role definition by resource ID, using the cache when the entry is fresh.
func (c *cachedRoleDefinitions) GetCachedByID(ctx context.Context, roleDefinitionResourceID string, options *armauthorization.RoleDefinitionsClientGetByIDOptions) (armauthorization.RoleDefinitionsClientGetByIDResponse, error) {
	if err := c.ensureCachedGetByID(ctx, roleDefinitionResourceID, options); err != nil {
		return armauthorization.RoleDefinitionsClientGetByIDResponse{}, err
	}
	c.roleDefinitionsCacheLock.RLock()
	entry := c.roleDefinitionsCache[roleDefinitionResourceID]
	c.roleDefinitionsCacheLock.RUnlock()
	return entry.response, nil
}

func (c *cachedRoleDefinitions) ensureCachedGetByID(ctx context.Context, roleDefinitionResourceID string, options *armauthorization.RoleDefinitionsClientGetByIDOptions) error {
	c.roleDefinitionsCacheLock.RLock()
	value, exists := c.roleDefinitionsCache[roleDefinitionResourceID]
	c.roleDefinitionsCacheLock.RUnlock()
	if exists && !c.isStale(value) {
		return nil
	}
	_, err, _ := c.sfGroup.Do(roleDefinitionResourceID, func() (interface{}, error) {
		resp, err := c.inner.GetByID(ctx, roleDefinitionResourceID, options)
		if err != nil {
			return nil, utils.TrackError(fmt.Errorf("failed to get role definition for '%s': %w", roleDefinitionResourceID, err))
		}
		c.roleDefinitionsCacheLock.Lock()
		defer c.roleDefinitionsCacheLock.Unlock()
		c.roleDefinitionsCache[roleDefinitionResourceID] = cachedGetByIDResponse{
			response:   resp,
			lastUpdate: time.Now().UTC(),
		}
		return nil, nil
	})
	if err != nil {
		return utils.TrackError(err)
	}
	return nil
}

func (c *cachedRoleDefinitions) isStale(entry cachedGetByIDResponse) bool {
	return time.Since(entry.lastUpdate) > roleDefinitionResourceIDCacheKeyTTL
}

var _ CachedRoleDefinitions = (*cachedRoleDefinitions)(nil)
