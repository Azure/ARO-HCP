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

package slots

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"k8s.io/apimachinery/pkg/util/sets"
)

const (
	DefaultCatalogRelPath    = "test/e2e-config/e2e-slots.yaml"
	defaultEnvFileName       = "aro-hcp-slot.env"
	defaultSlotStateFileName = "aro-hcp-slot-state.yaml"

	defaultSlotIndexWidth      = 2
	defaultContainerIndexWidth = 2

	RegionModeFixed           = "fixed"
	RegionModeRuntimeSelected = "runtime-selected"
)

type Catalog struct {
	Version      int                    `yaml:"version"`
	Environments map[string]Environment `yaml:"environments"`
}

type Environment struct {
	DeployEnvs []string `yaml:"deploy_envs"`
	Pools      []Pool   `yaml:"pools"`
}

type Pool struct {
	SubscriptionName           string `yaml:"subscription_name"`
	Region                     string `yaml:"region"`
	RegionMode                 string `yaml:"region_mode,omitempty"`
	IdentityProvisioningRegion string `yaml:"identity_provisioning_region,omitempty"`
	IdentityProvisioning       string `yaml:"identity_provisioning,omitempty"`
	ResourceType               string `yaml:"resource_type"`
	SlotCount                  int    `yaml:"slot_count"`
	IdentityContainerPrefix    string `yaml:"identity_container_prefix"`
	IdentityContainerCount     int    `yaml:"identity_container_count"`
}

const (
	IdentityProvisioningUnmanaged = "unmanaged"
)

type ExpandedSlot struct {
	Environment             string `yaml:"environment"`
	SubscriptionName        string `yaml:"subscription_name"`
	Region                  string `yaml:"region"`
	ResourceType            string `yaml:"resource_type"`
	ResourceName            string `yaml:"resource_name"`
	SlotIndex               int    `yaml:"slot_index"`
	IdentityContainerPrefix string `yaml:"identity_container_prefix"`
	IdentityContainerCount  int    `yaml:"identity_container_count"`
}

func LoadCatalog(path string) (*Catalog, error) {
	if path == "" {
		resolvedPath, err := ResolveCatalogPath()
		if err != nil {
			return nil, err
		}
		path = resolvedPath
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read slot catalog %q: %w", path, err)
	}

	catalog := &Catalog{}
	if err := yaml.Unmarshal(data, catalog); err != nil {
		return nil, fmt.Errorf("failed to unmarshal slot catalog %q: %w", path, err)
	}
	if err := catalog.Validate(); err != nil {
		return nil, fmt.Errorf("invalid slot catalog %q: %w", path, err)
	}

	return catalog, nil
}

func ResolveCatalogPath() (string, error) {
	return ResolveCatalogPathFrom("")
}

// ResolveCatalogPathFrom walks upward from startDir looking for the catalog
// file. When startDir is empty it defaults to the current working directory.
func ResolveCatalogPathFrom(startDir string) (string, error) {
	return resolveRepoFile(DefaultCatalogRelPath, startDir)
}

func resolveRepoFile(relPath, startDir string) (string, error) {
	if startDir == "" {
		wd, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("failed to get current working directory: %w", err)
		}
		startDir = wd
	}

	dir := startDir
	for {
		candidate := filepath.Join(dir, relPath)
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}

	return "", fmt.Errorf("failed to find %q from %q", relPath, startDir)
}

func (c *Catalog) Validate() error {
	if c.Version != 1 {
		return fmt.Errorf("unsupported catalog version %d", c.Version)
	}
	if len(c.Environments) == 0 {
		return errors.New("catalog has no environments")
	}

	resourceTypes := map[string]string{}
	for _, environmentName := range c.EnvironmentNames() {
		environment := c.Environments[environmentName]
		if len(environment.DeployEnvs) == 0 {
			return fmt.Errorf("environment %q has no deploy_envs", environmentName)
		}
		if len(environment.Pools) == 0 {
			return fmt.Errorf("environment %q has no pools", environmentName)
		}

		seenPoolKeys := map[string]struct{}{}
		environmentRegionMode := ""
		for i := range environment.Pools {
			pool := &environment.Pools[i]
			pool.SubscriptionName = strings.TrimSpace(pool.SubscriptionName)
			pool.Region = strings.TrimSpace(pool.Region)
			pool.RegionMode = strings.TrimSpace(pool.RegionMode)
			if pool.RegionMode == "" {
				pool.RegionMode = RegionModeFixed
			}
			pool.IdentityProvisioningRegion = strings.TrimSpace(pool.IdentityProvisioningRegion)
			pool.ResourceType = strings.TrimSpace(pool.ResourceType)
			pool.IdentityContainerPrefix = strings.TrimSpace(pool.IdentityContainerPrefix)

			switch {
			case pool.SubscriptionName == "":
				return fmt.Errorf("environment %q has a pool with empty subscription_name", environmentName)
			case pool.Region == "":
				return fmt.Errorf("environment %q has a pool with empty region", environmentName)
			case pool.RegionMode != RegionModeFixed && pool.RegionMode != RegionModeRuntimeSelected:
				return fmt.Errorf("environment %q pool %s has invalid region_mode %q", environmentName, describePool(*pool), pool.RegionMode)
			case pool.ResourceType == "":
				return fmt.Errorf("environment %q has a pool with empty resource_type", environmentName)
			case pool.SlotCount <= 0:
				return fmt.Errorf("environment %q pool %s has invalid slot_count %d", environmentName, describePool(*pool), pool.SlotCount)
			case pool.IdentityContainerPrefix == "":
				return fmt.Errorf("environment %q pool %s has empty identity_container_prefix", environmentName, describePool(*pool))
			case pool.IdentityContainerCount <= 0:
				return fmt.Errorf("environment %q pool %s has invalid identity_container_count %d", environmentName, describePool(*pool), pool.IdentityContainerCount)
			}

			if environmentRegionMode == "" {
				environmentRegionMode = pool.RegionMode
			} else if pool.RegionMode != environmentRegionMode {
				return fmt.Errorf(
					"environment %q mixes region_mode values %q and %q; keep a single selection mode per environment",
					environmentName,
					environmentRegionMode,
					pool.RegionMode,
				)
			}

			poolKey := poolIdentity(*pool)
			if _, found := seenPoolKeys[poolKey]; found {
				return fmt.Errorf("environment %q declares duplicate pool %s", environmentName, describePool(*pool))
			}
			seenPoolKeys[poolKey] = struct{}{}

			if previous, exists := resourceTypes[pool.ResourceType]; exists {
				return fmt.Errorf("resource type %q is declared by both %s and %s", pool.ResourceType, previous, qualifiedPoolName(environmentName, *pool))
			}
			resourceTypes[pool.ResourceType] = qualifiedPoolName(environmentName, *pool)
		}

		c.Environments[environmentName] = environment
	}

	return nil
}

func (c *Catalog) EnvironmentNames() []string {
	names := make([]string, 0, len(c.Environments))
	for name := range c.Environments {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (c *Catalog) ResolveEnvironmentForDeployEnv(deployEnv string) (string, error) {
	if deployEnv == "" {
		return "", errors.New("deploy environment is empty")
	}

	var matches []string
	for _, environmentName := range c.EnvironmentNames() {
		for _, candidate := range c.Environments[environmentName].DeployEnvs {
			if candidate == deployEnv {
				matches = append(matches, environmentName)
				break
			}
		}
	}

	switch len(matches) {
	case 0:
		return "", fmt.Errorf("failed to resolve deploy environment %q", deployEnv)
	case 1:
		return matches[0], nil
	default:
		return "", fmt.Errorf("deploy environment %q maps to multiple slot environments: %s", deployEnv, strings.Join(matches, ", "))
	}
}

func (c *Catalog) ResolvePool(environment string, allowedSubscriptions, allowedLocations sets.Set[string], selectedLocation string) (Pool, error) {
	matches, err := c.CandidatePools(environment, allowedSubscriptions, allowedLocations, selectedLocation)
	if err != nil {
		return Pool{}, err
	}

	switch len(matches) {
	case 1:
		return matches[0], nil
	default:
		environmentRegionMode, err := c.RegionModeForEnvironment(environment)
		if err != nil {
			return Pool{}, err
		}
		if environmentRegionMode == RegionModeRuntimeSelected {
			return Pool{}, fmt.Errorf("environment %q has %d matching pools; narrow ALLOWED_SUBSCRIPTIONS to a single pool", environment, len(matches))
		}
		return Pool{}, fmt.Errorf("environment %q has %d matching pools; narrow ALLOWED_SUBSCRIPTIONS and/or ALLOWED_LOCATIONS to a single pool", environment, len(matches))
	}
}

func (c *Catalog) CandidatePools(environment string, allowedSubscriptions, allowedLocations sets.Set[string], selectedLocation string) ([]Pool, error) {
	environmentConfig, found := c.Environments[environment]
	if !found {
		return nil, fmt.Errorf("unknown environment %q", environment)
	}

	environmentRegionMode, err := c.RegionModeForEnvironment(environment)
	if err != nil {
		return nil, err
	}

	matches := make([]Pool, 0, len(environmentConfig.Pools))
	for _, pool := range environmentConfig.Pools {
		if allowedSubscriptions.Len() > 0 && !allowedSubscriptions.Has(pool.SubscriptionName) {
			continue
		}
		if environmentRegionMode == RegionModeFixed {
			if selectedLocation != "" && pool.Region != selectedLocation {
				continue
			}
			if selectedLocation == "" && allowedLocations.Len() > 0 && !allowedLocations.Has(pool.Region) {
				continue
			}
		}
		matches = append(matches, pool)
	}

	if len(matches) > 0 {
		return matches, nil
	}

	selectors := []string{}
	if allowedSubscriptions.Len() > 0 {
		selectors = append(selectors, fmt.Sprintf("allowed_subscriptions=%q", strings.Join(sets.List(allowedSubscriptions), ",")))
	}
	if environmentRegionMode == RegionModeFixed {
		if selectedLocation != "" {
			selectors = append(selectors, fmt.Sprintf("selected_location=%q", selectedLocation))
		} else if allowedLocations.Len() > 0 {
			selectors = append(selectors, fmt.Sprintf("allowed_locations=%q", strings.Join(sets.List(allowedLocations), ",")))
		}
	}
	if len(selectors) == 0 {
		return nil, fmt.Errorf("environment %q has no pools", environment)
	}
	return nil, fmt.Errorf("no pool found for environment %q matching %s", environment, strings.Join(selectors, ", "))
}

func (c *Catalog) RegionModeForEnvironment(environment string) (string, error) {
	environmentConfig, found := c.Environments[environment]
	if !found {
		return "", fmt.Errorf("unknown environment %q", environment)
	}
	if len(environmentConfig.Pools) == 0 {
		return "", fmt.Errorf("environment %q has no pools", environment)
	}

	regionMode := environmentConfig.Pools[0].EffectiveRegionMode()
	for _, pool := range environmentConfig.Pools[1:] {
		if candidate := pool.EffectiveRegionMode(); candidate != regionMode {
			return "", fmt.Errorf("environment %q mixes region_mode values %q and %q", environment, regionMode, candidate)
		}
	}
	return regionMode, nil
}

func ExpandSlotsForPool(environment string, pool Pool) []ExpandedSlot {
	slots := make([]ExpandedSlot, 0, pool.SlotCount)
	for i := 0; i < pool.SlotCount; i++ {
		slots = append(slots, ExpandedSlot{
			Environment:             environment,
			SubscriptionName:        pool.SubscriptionName,
			Region:                  pool.Region,
			ResourceType:            pool.ResourceType,
			ResourceName:            fmt.Sprintf("%s-%0*d", pool.ResourceType, defaultSlotIndexWidth, i),
			SlotIndex:               i,
			IdentityContainerPrefix: fmt.Sprintf("%s-%0*d", pool.IdentityContainerPrefix, defaultSlotIndexWidth, i),
			IdentityContainerCount:  pool.IdentityContainerCount,
		})
	}

	return slots
}

func (c *Catalog) ExpandedSlotsForEnvironment(environment string) ([]ExpandedSlot, error) {
	environmentConfig, found := c.Environments[environment]
	if !found {
		return nil, fmt.Errorf("unknown environment %q", environment)
	}

	var slots []ExpandedSlot
	for _, pool := range environmentConfig.Pools {
		slots = append(slots, ExpandSlotsForPool(environment, pool)...)
	}
	return slots, nil
}

func (c *Catalog) FindSlotByResourceName(resourceName string) (*ExpandedSlot, error) {
	for _, environmentName := range c.EnvironmentNames() {
		slots, err := c.ExpandedSlotsForEnvironment(environmentName)
		if err != nil {
			return nil, err
		}
		for i := range slots {
			if slots[i].ResourceName == resourceName {
				slot := slots[i]
				return &slot, nil
			}
		}
	}

	return nil, fmt.Errorf("failed to find slot for leased resource %q", resourceName)
}

func (p Pool) IsUnmanaged() bool {
	return p.IdentityProvisioning == IdentityProvisioningUnmanaged
}

func (p Pool) EffectiveRegionMode() string {
	if p.RegionMode != "" {
		return p.RegionMode
	}
	return RegionModeFixed
}

func (p Pool) EffectiveIdentityProvisioningRegion() string {
	if p.IdentityProvisioningRegion != "" {
		return p.IdentityProvisioningRegion
	}
	return p.Region
}

func (s ExpandedSlot) IdentityContainerNames() []string {
	names := make([]string, 0, s.IdentityContainerCount)
	for i := 0; i < s.IdentityContainerCount; i++ {
		names = append(names, fmt.Sprintf("%s-%0*d", s.IdentityContainerPrefix, defaultContainerIndexWidth, i))
	}
	return names
}

func SharedStateDir(sharedDir string) (string, error) {
	if sharedDir == "" {
		return "", errors.New("SHARED_DIR is empty")
	}
	return sharedDir, nil
}

func EnvFile(sharedDir string) (string, error) {
	stateDir, err := SharedStateDir(sharedDir)
	if err != nil {
		return "", err
	}
	return filepath.Join(stateDir, defaultEnvFileName), nil
}

func SlotStateFile(sharedDir string) (string, error) {
	stateDir, err := SharedStateDir(sharedDir)
	if err != nil {
		return "", err
	}
	return filepath.Join(stateDir, defaultSlotStateFileName), nil
}

func describePool(pool Pool) string {
	if pool.EffectiveRegionMode() == RegionModeRuntimeSelected {
		return fmt.Sprintf("(subscription_name=%q, region_mode=%q, default_region=%q)", pool.SubscriptionName, pool.EffectiveRegionMode(), pool.Region)
	}
	return fmt.Sprintf("(subscription_name=%q, region=%q)", pool.SubscriptionName, pool.Region)
}

func poolIdentity(pool Pool) string {
	if pool.EffectiveRegionMode() == RegionModeRuntimeSelected {
		return pool.SubscriptionName
	}
	return fmt.Sprintf("%s/%s", pool.SubscriptionName, pool.Region)
}

func qualifiedPoolName(environment string, pool Pool) string {
	return fmt.Sprintf("%s/%s", environment, poolIdentity(pool))
}
