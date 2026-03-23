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

package roledefinitions

import (
	"context"
	"fmt"
	"time"

	cmap "github.com/orcaman/concurrent-map/v2"
	"golang.org/x/sync/singleflight"

	"k8s.io/utils/set"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v2"

	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
)

// roleDefinitionResourceIdCacheKeyTtl defines how long a cached role definition is considered valid.
// After this TTL (6 hours), the cached value is considered stale and will be refreshed
// on the next request via the Azure Role Definitions API.
//
// The 6-hour TTL was decided arbitrarily, to find a balance between frequent calls to fetch the role definition
// and having the latest role definition.
// This value could change in the future if needed.
const roleDefinitionResourceIdCacheKeyTtl = 6 * time.Hour

// cachedRoleDefinition holds an Azure role definition along with the timestamp of
// when it was last updated in the cache.
type cachedRoleDefinition struct {
	roleDefinition armauthorization.RoleDefinition
	lastUpdate     time.Time
}

// CachedRoleDefinitionsGetter provides lazy, cached access to Azure role definitions.
type CachedRoleDefinitionsGetter struct {
	roleDefinitionsClient azureclient.RoleDefinitionsClient
	// roleDefinitionsCache is an in-memory, thread-safe cache that maps
	// Azure Role Definition Resource IDs to their corresponding role definition data.
	//
	// The cache supports lazy evaluation: role definitions are not fetched from Azure
	// until they are requested via the Get method. If a requested role definition is not in the cache
	// or has expired based on the TTL, it will be fetched and stored for future use.
	//
	// The cache expects a limited number of role definitions, based on azure operators managed identities config.
	// Due to the type and limited number of entries, the cache isn't expected to grow big, if that changes in the
	// future, this cache should be redesigned.
	roleDefinitionsCache cmap.ConcurrentMap[string, cachedRoleDefinition]
	sfGroup              singleflight.Group
}

// NewCachedRoleDefinitionsGetter creates a new CachedRoleDefinitionsGetter.
func NewCachedRoleDefinitionsGetter(roleDefinitionsClient azureclient.RoleDefinitionsClient) *CachedRoleDefinitionsGetter {
	roleDefinitionsCache := cmap.New[cachedRoleDefinition]()

	return &CachedRoleDefinitionsGetter{
		roleDefinitionsClient: roleDefinitionsClient,
		roleDefinitionsCache:  roleDefinitionsCache,
	}
}

// GetActions returns the list of allowed actions for the given roleDefinitionResourceId.
//
// If the role definition is found in the cache and is not stale, the cached actions
// are returned immediately. Otherwise, it fetches the definition from Azure,
// extracts the role definition, updates the cache, and returns the actions.
//
// To avoid redundant network calls, concurrent calls for the same ID are
// deduplicated using single flight, ensuring that only one fetch is in flight
// per roleDefinitionResourceId.
func (s *CachedRoleDefinitionsGetter) GetActions(ctx context.Context, roleDefinitionResourceId string) ([]string, error) {
	if err := s.ensureCached(ctx, roleDefinitionResourceId); err != nil {
		return nil, err
	}
	value, _ := s.roleDefinitionsCache.Get(roleDefinitionResourceId)
	return s.extractRoleDefinitionActions(value.roleDefinition)
}

// GetDataActions returns the list of allowed data actions for the given roleDefinitionResourceId.
//
// Data actions are data-plane operations (e.g. reading blob storage). The role definition
// is served from cache when present and not stale; otherwise it is fetched from Azure.
func (s *CachedRoleDefinitionsGetter) GetDataActions(ctx context.Context, roleDefinitionResourceId string) ([]string, error) {
	if err := s.ensureCached(ctx, roleDefinitionResourceId); err != nil {
		return nil, err
	}
	value, _ := s.roleDefinitionsCache.Get(roleDefinitionResourceId)
	return s.extractRoleDefinitionDataActions(value.roleDefinition)
}

// ensureCached fetches the role definition from Azure and stores it in the cache when
// it is missing or stale. Concurrent calls for the same ID are deduplicated via singleflight.
func (s *CachedRoleDefinitionsGetter) ensureCached(ctx context.Context, roleDefinitionResourceId string) error {
	value, exists := s.roleDefinitionsCache.Get(roleDefinitionResourceId)
	if exists && !s.isStale(value) {
		return nil
	}
	_, err, _ := s.sfGroup.Do(roleDefinitionResourceId, func() (interface{}, error) {
		roleDefinition, err := s.fetchRoleDefinition(ctx, roleDefinitionResourceId)
		if err != nil {
			return nil, err
		}
		s.roleDefinitionsCache.Set(roleDefinitionResourceId, cachedRoleDefinition{
			roleDefinition: roleDefinition,
			lastUpdate:     time.Now().UTC(),
		})
		return nil, nil
	})
	return err
}

// GetActionsMultipleIds returns a union of the allowed actions for the given roleDefinitionResourceIds.
//
// The union takes only the actions into consideration regardless of the role definition scope.
func (s *CachedRoleDefinitionsGetter) GetActionsMultipleIds(ctx context.Context, roleDefinitionResourceIds []string) ([]string, error) {
	actionsUnion := set.Set[string]{}

	for _, roleDefinitionResourceId := range roleDefinitionResourceIds {
		actions, err := s.GetActions(ctx, roleDefinitionResourceId)
		if err != nil {
			return nil, err
		}
		actionsUnion.Insert(actions...)
	}

	return actionsUnion.UnsortedList(), nil
}

// GetDataActionsMultipleIds returns a union of the allowed data actions for the given roleDefinitionResourceIds.
//
// The union takes only the data actions into consideration regardless of the role definition scope.
func (s *CachedRoleDefinitionsGetter) GetDataActionsMultipleIds(ctx context.Context, roleDefinitionResourceIds []string) ([]string, error) {
	dataActionsUnion := set.Set[string]{}

	for _, roleDefinitionResourceId := range roleDefinitionResourceIds {
		dataActions, err := s.GetDataActions(ctx, roleDefinitionResourceId)
		if err != nil {
			return nil, err
		}
		dataActionsUnion.Insert(dataActions...)
	}

	return dataActionsUnion.UnsortedList(), nil
}

func (s *CachedRoleDefinitionsGetter) isStale(roleDefinition cachedRoleDefinition) bool {
	return time.Since(roleDefinition.lastUpdate) > roleDefinitionResourceIdCacheKeyTtl
}

func (s *CachedRoleDefinitionsGetter) extractRoleDefinitionActions(roleDefinition armauthorization.RoleDefinition) ([]string, error) {
	if roleDefinition.Properties == nil || roleDefinition.Properties.Permissions == nil {
		return nil, fmt.Errorf("role definition '%s' doesn't contain permissions", stringValue(roleDefinition.ID))
	}

	var actions []string
	for _, permission := range roleDefinition.Properties.Permissions {
		for _, action := range permission.Actions {
			actions = append(actions, stringValue(action))
		}
	}

	return actions, nil
}

func (s *CachedRoleDefinitionsGetter) extractRoleDefinitionDataActions(roleDefinition armauthorization.RoleDefinition) ([]string, error) {
	if roleDefinition.Properties == nil || roleDefinition.Properties.Permissions == nil {
		return nil, fmt.Errorf("role definition '%s' doesn't contain permissions", stringValue(roleDefinition.ID))
	}

	var dataActions []string
	for _, permission := range roleDefinition.Properties.Permissions {
		for _, dataAction := range permission.DataActions {
			dataActions = append(dataActions, stringValue(dataAction))
		}
	}

	return dataActions, nil
}

func (s *CachedRoleDefinitionsGetter) fetchRoleDefinition(ctx context.Context, roleDefinitionResourceId string) (armauthorization.RoleDefinition, error) {
	response, err := s.roleDefinitionsClient.GetByID(ctx, roleDefinitionResourceId, nil)
	if err != nil {
		return armauthorization.RoleDefinition{}, fmt.Errorf("failed to get role definition for '%s': %w", roleDefinitionResourceId, err)
	}

	return response.RoleDefinition, nil
}

func stringValue(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// RoleDefinitionsGetter interface is thread-safe, future implementations of this interface should ensure thread-safety.
type RoleDefinitionsGetter interface {
	// GetActions returns the list of allowed actions for the given roleDefinitionResourceId.
	//
	// Returns an error if the role definition cannot be fetched or is missing required permissions.
	GetActions(ctx context.Context, roleDefinitionResourceId string) ([]string, error)

	// GetDataActions returns the list of allowed data actions for the given roleDefinitionResourceId.
	//
	// Returns an error if the role definition cannot be fetched or is missing required permissions.
	GetDataActions(ctx context.Context, roleDefinitionResourceId string) ([]string, error)

	// GetActionsMultipleIds returns a union of the allowed actions for the given roleDefinitionResourceIds.
	//
	// The union takes only the actions into consideration regardless of the role definition scope.
	GetActionsMultipleIds(ctx context.Context, roleDefinitionResourceIds []string) ([]string, error)

	// GetDataActionsMultipleIds returns a union of the allowed data actions for the given roleDefinitionResourceIds.
	//
	// The union takes only the data actions into consideration regardless of the role definition scope.
	GetDataActionsMultipleIds(ctx context.Context, roleDefinitionResourceIds []string) ([]string, error)
}

var _ RoleDefinitionsGetter = (*CachedRoleDefinitionsGetter)(nil)
