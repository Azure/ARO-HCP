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

//go:generate $MOCKGEN -typed -source=role_definitions.go -destination=mock_role_definitions_cached_reader.go -package cachedreader RoleDefinitionsCachedReader

import (
	"context"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	utilsclock "k8s.io/utils/clock"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v2"

	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// roleDefinitionResourceIDCacheKeySuccessTTL defines how long a cached successful GetByID is valid.
// After this TTL (6 hours), the cached entry is stale and the next GetCachedByID refreshes it from Azure.
const roleDefinitionResourceIDCacheKeySuccessTTL = 6 * time.Hour

// roleDefinitionResourceIDCacheKeyErrorTTL defines how long a cached GetByID that returned an error is valid.
// After this TTL (5 minutes), the cached entry is stale and the next GetCachedByID retries Azure.
const roleDefinitionResourceIDCacheKeyErrorTTL = 5 * time.Minute

// RoleDefinitionsCachedReader exposes cached reads of role definitions by ARM resource ID.
type RoleDefinitionsCachedReader interface {
	GetCachedByID(ctx context.Context, roleDefinitionResourceID string, options *armauthorization.RoleDefinitionsClientGetByIDOptions) (armauthorization.RoleDefinitionsClientGetByIDResponse, error)
}

// roleDefinitionsCachedReader wraps an uncached azureclient.RoleDefinitionsClient. GetCachedByID
// responses are cached in memory with a TTL and singleflight deduplication; other RoleDefinitionsClient
// methods are not exposed on this type.
// TTLs used for cached role definition entries.
// Successful lookups are cached for 6 hours (roleDefinitionResourceIDCacheKeySuccessTTL).
// Error responses are cached for 5 minutes (roleDefinitionResourceIDCacheKeyErrorTTL).
type roleDefinitionsCachedReader struct {
	// inner is the uncached Azure Role Definitions client used for live GetByID calls.
	inner azureclient.RoleDefinitionsClient
	// clock controls cache staleness and update timestamps.
	clock utilsclock.PassiveClock

	roleDefinitionsCacheLock sync.RWMutex
	// roleDefinitionsCache maps role definition ARM resource ID -> cached GetByID payload and metadata.
	// Keys are full resource IDs as returned by Azure, for example:
	//   /subscriptions/11111111-1111-1111-1111-111111111111/providers/Microsoft.Authorization/roleDefinitions/b24988ac-6180-42a0-ab88-20f7382dd24c
	roleDefinitionsCache map[string]cachedGetByIDResponse
	// sfGroup deduplicates concurrent refreshes for the same role definition resource ID (see ensureCachedGetByID).
	sfGroup singleflight.Group
}

type cachedGetByIDResponse struct {
	response armauthorization.RoleDefinitionsClientGetByIDResponse
	// err contains the error returned by the GetByID call made to Azure. nil if no error in the GetByID call.
	err error
	// lastUpdate is the wall-clock instant (UTC) when this cache entry was written, from time.Now().UTC().
	// It is compared with time.Since for TTL / staleness checks (same interpretation as time.Now).
	lastUpdate time.Time
}

// NewRoleDefinitionsCachedReader returns a RoleDefinitionsCachedReader that caches GetByID results from inner.
func NewRoleDefinitionsCachedReader(inner azureclient.RoleDefinitionsClient) RoleDefinitionsCachedReader {
	return &roleDefinitionsCachedReader{
		inner:                inner,
		clock:                utilsclock.RealClock{},
		roleDefinitionsCache: make(map[string]cachedGetByIDResponse),
	}
}

// GetCachedByID returns a role definition by resource ID, using the cache when the entry is fresh.
func (c *roleDefinitionsCachedReader) GetCachedByID(ctx context.Context, roleDefinitionResourceID string, options *armauthorization.RoleDefinitionsClientGetByIDOptions) (armauthorization.RoleDefinitionsClientGetByIDResponse, error) {
	if err := c.ensureCachedGetByID(ctx, roleDefinitionResourceID, options); err != nil {
		return armauthorization.RoleDefinitionsClientGetByIDResponse{}, err
	}
	c.roleDefinitionsCacheLock.RLock()
	entry := c.roleDefinitionsCache[roleDefinitionResourceID]
	c.roleDefinitionsCacheLock.RUnlock()
	return entry.response, entry.err
}

// ensureCachedGetByID loads a fresh role definition when missing or stale. Concurrent callers for the
// same roleDefinitionResourceID coalesce on a single inner.GetByID via singleflight.Group.Do, so only
// one Azure request runs while others wait on the shared result.
func (c *roleDefinitionsCachedReader) ensureCachedGetByID(ctx context.Context, roleDefinitionResourceID string, options *armauthorization.RoleDefinitionsClientGetByIDOptions) error {
	c.roleDefinitionsCacheLock.RLock()
	value, exists := c.roleDefinitionsCache[roleDefinitionResourceID]
	c.roleDefinitionsCacheLock.RUnlock()
	if exists && !c.isStale(value) {
		return nil
	}
	_, err, _ := c.sfGroup.Do(roleDefinitionResourceID, func() (interface{}, error) {
		resp, err := c.inner.GetByID(ctx, roleDefinitionResourceID, options)
		c.roleDefinitionsCacheLock.Lock()
		defer c.roleDefinitionsCacheLock.Unlock()
		c.roleDefinitionsCache[roleDefinitionResourceID] = cachedGetByIDResponse{
			response:   resp,
			err:        err,
			lastUpdate: c.clock.Now().UTC(),
		}
		return nil, nil
	})
	if err != nil {
		return utils.TrackError(err)
	}
	return nil
}

func (c *roleDefinitionsCachedReader) isStale(entry cachedGetByIDResponse) bool {
	ttl := roleDefinitionResourceIDCacheKeySuccessTTL
	if entry.err != nil {
		ttl = roleDefinitionResourceIDCacheKeyErrorTTL
	}
	return c.clock.Since(entry.lastUpdate) > ttl
}

var _ RoleDefinitionsCachedReader = (*roleDefinitionsCachedReader)(nil)
