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
	"bytes"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"slices"
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
)

const (
	RegionModeFixed RegionMode = iota
	RegionModeRuntimeSelected
	RegionModeWeighted
)

type RegionMode int

func (m RegionMode) String() string {
	switch m {
	case RegionModeFixed:
		return "fixed"
	case RegionModeRuntimeSelected:
		return "runtime-selected"
	case RegionModeWeighted:
		return "weighted"
	default:
		return fmt.Sprintf("RegionMode(%d)", m)
	}
}

func (m RegionMode) IsValid() bool {
	switch m {
	case RegionModeFixed, RegionModeRuntimeSelected, RegionModeWeighted:
		return true
	default:
		return false
	}
}

func (m *RegionMode) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.ScalarNode || node.Tag != "!!str" {
		return fmt.Errorf("region_mode must be a string")
	}

	switch value := strings.TrimSpace(node.Value); value {
	case "", RegionModeFixed.String():
		*m = RegionModeFixed
	case RegionModeRuntimeSelected.String():
		*m = RegionModeRuntimeSelected
	case RegionModeWeighted.String():
		*m = RegionModeWeighted
	default:
		return fmt.Errorf("invalid region_mode %q", value)
	}
	return nil
}

func (m RegionMode) MarshalYAML() (any, error) {
	return m.String(), nil
}

type Catalog struct {
	Version      int                    `yaml:"version"`
	AssetPools   []AssetPool            `yaml:"asset_pools,omitempty"`
	Environments map[string]Environment `yaml:"environments"`
}

type Environment struct {
	DeploymentEnvironment DeploymentEnvironment `yaml:"deployment_environment,omitempty"`
	DeployEnvs            []string              `yaml:"deploy_envs"`
	Pools                 []Pool                `yaml:"pools"`
}

type DeploymentEnvironment struct {
	Name                       string `yaml:"name"`
	InfrastructureSubscription string `yaml:"infrastructure_subscription,omitempty"`
}

type PoolSubscriptions struct {
	E2E            string `yaml:"e2e"`
	Infrastructure string `yaml:"-"`
}

type E2EIdentitiesAsset struct {
	Allocation          Allocation `yaml:"allocation"`
	Provisioning        string     `yaml:"provisioning,omitempty"`
	ProvisioningRegion  string     `yaml:"provisioning_region,omitempty"`
	ResourceGroupPrefix string     `yaml:"resource_group_prefix"`
	ResourceGroupCount  int        `yaml:"resource_group_count"`
}

type SlotAssets struct {
	E2EIdentities            *E2EIdentitiesAsset `yaml:"e2e_identities,omitempty"`
	InfrastructureIdentities *LeasedAsset        `yaml:"infrastructure_identities,omitempty"`
}

type Pool struct {
	Name                       string            `yaml:"name,omitempty"`
	DeployEnv                  string            `yaml:"-"`
	Subscriptions              PoolSubscriptions `yaml:"subscriptions,omitempty"`
	SlotAssets                 SlotAssets        `yaml:"slot_assets,omitempty"`
	SubscriptionName           string            `yaml:"subscription_name"`
	Region                     string            `yaml:"region,omitempty"`
	Regions                    []string          `yaml:"regions,omitempty"`
	RegionMode                 RegionMode        `yaml:"region_mode,omitempty"`
	IdentityProvisioningRegion string            `yaml:"identity_provisioning_region,omitempty"`
	IdentityProvisioning       string            `yaml:"identity_provisioning,omitempty"`
	ResourceType               string            `yaml:"resource_type"`
	SlotCount                  int               `yaml:"slot_count"`
	IdentityContainerPrefix    string            `yaml:"identity_container_prefix"`
	IdentityContainerCount     int               `yaml:"identity_container_count"`
}

const (
	AssetProvisioningManaged      = "managed"
	IdentityProvisioningUnmanaged = "unmanaged"
)

type ResolvedSubscription struct {
	Name string `yaml:"name"`
	ID   string `yaml:"id"`
}

type ResolvedSubscriptions struct {
	E2E            ResolvedSubscription `yaml:"e2e"`
	Infrastructure ResolvedSubscription `yaml:"infrastructure,omitempty"`
}

type ResolvedE2EIdentitiesAsset struct {
	Allocation         Allocation `yaml:"allocation"`
	ProvisioningRegion string     `yaml:"provisioning_region"`
	ResourceGroups     []string   `yaml:"resource_groups"`
}

type ResolvedAssets struct {
	E2EIdentities            *ResolvedE2EIdentitiesAsset       `yaml:"e2e_identities,omitempty"`
	InfrastructureIdentities *ResolvedInfrastructureIdentities `yaml:"infrastructure_identities,omitempty"`
}

type ExpandedSlot struct {
	Requirements            []AssetRequirement    `yaml:"requirements,omitempty"`
	Environment             string                `yaml:"environment"`
	PoolName                string                `yaml:"pool_name,omitempty"`
	DeployEnvironment       string                `yaml:"deploy_environment,omitempty"`
	SubscriptionName        string                `yaml:"subscription_name"`
	Region                  string                `yaml:"region"`
	ResourceType            string                `yaml:"resource_type"`
	ResourceName            string                `yaml:"resource_name"`
	SlotIndex               int                   `yaml:"slot_index"`
	IdentityContainerPrefix string                `yaml:"identity_container_prefix"`
	IdentityContainerCount  int                   `yaml:"identity_container_count"`
	Subscriptions           ResolvedSubscriptions `yaml:"subscriptions,omitempty"`
	Assets                  ResolvedAssets        `yaml:"assets,omitempty"`
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
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(catalog); err != nil {
		return nil, fmt.Errorf("failed to unmarshal slot catalog %q: %w", path, err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("slot catalog %q must contain exactly one YAML document", path)
	}
	if catalog.Version == 2 {
		if err := rejectV2LegacyFields(data); err != nil {
			return nil, err
		}
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
	if c.Version != 1 && c.Version != 2 {
		return fmt.Errorf("unsupported catalog version %d", c.Version)
	}
	if len(c.Environments) == 0 {
		return errors.New("catalog has no environments")
	}

	resourceTypes := map[string]string{}
	for _, environmentName := range c.EnvironmentNames() {
		environment := c.Environments[environmentName]
		if c.Version == 1 && len(environment.DeployEnvs) == 0 {
			return fmt.Errorf("environment %q has no deploy_envs", environmentName)
		}
		if c.Version == 2 && len(environment.DeployEnvs) > 0 {
			return fmt.Errorf("environment %q version 2 config must not declare deploy_envs", environmentName)
		}
		if c.Version == 2 {
			environment.DeploymentEnvironment.Name = strings.TrimSpace(environment.DeploymentEnvironment.Name)
			environment.DeploymentEnvironment.InfrastructureSubscription = strings.TrimSpace(environment.DeploymentEnvironment.InfrastructureSubscription)
			if environment.DeploymentEnvironment.Name == "" {
				return fmt.Errorf("environment %q must declare deployment_environment.name", environmentName)
			}
			if err := validateDeploymentEnvironmentName(environment.DeploymentEnvironment.Name); err != nil {
				return fmt.Errorf("environment %q: %w", environmentName, err)
			}
		}
		if len(environment.Pools) == 0 {
			return fmt.Errorf("environment %q has no pools", environmentName)
		}

		seenPoolKeys := map[string]struct{}{}
		seenPoolNames := map[string]struct{}{}
		environmentRegionMode := RegionModeFixed
		environmentRegionModeSet := false
		var environmentRegions []string
		for i := range environment.Pools {
			pool := &environment.Pools[i]
			pool.Name = strings.TrimSpace(pool.Name)
			pool.DeployEnv = strings.TrimSpace(pool.DeployEnv)
			pool.Subscriptions.E2E = strings.TrimSpace(pool.Subscriptions.E2E)
			pool.Subscriptions.Infrastructure = strings.TrimSpace(pool.Subscriptions.Infrastructure)
			pool.SubscriptionName = strings.TrimSpace(pool.SubscriptionName)
			pool.Region = strings.TrimSpace(pool.Region)
			pool.Regions = trimValues(pool.Regions)
			poolRegions := sets.New(pool.Regions...)
			pool.IdentityProvisioningRegion = strings.TrimSpace(pool.IdentityProvisioningRegion)
			pool.ResourceType = strings.TrimSpace(pool.ResourceType)
			pool.IdentityContainerPrefix = strings.TrimSpace(pool.IdentityContainerPrefix)
			pool.IdentityProvisioning = strings.TrimSpace(pool.IdentityProvisioning)

			if c.Version == 2 {
				pool.DeployEnv = environment.DeploymentEnvironment.Name
				pool.Subscriptions.Infrastructure = environment.DeploymentEnvironment.InfrastructureSubscription
				if err := normalizeV2Pool(environmentName, pool); err != nil {
					return err
				}
				if _, found := seenPoolNames[pool.Name]; found {
					return fmt.Errorf("environment %q declares duplicate pool name %q", environmentName, pool.Name)
				}
				seenPoolNames[pool.Name] = struct{}{}
			}

			switch {
			case pool.SubscriptionName == "":
				return fmt.Errorf("environment %q has a pool with empty subscription_name", environmentName)
			case pool.IdentityProvisioning != "" && pool.IdentityProvisioning != IdentityProvisioningUnmanaged:
				return fmt.Errorf("environment %q pool %s has invalid identity_provisioning %q (must be empty or %q)", environmentName, describePool(*pool), pool.IdentityProvisioning, IdentityProvisioningUnmanaged)
			case !pool.RegionMode.IsValid():
				return fmt.Errorf("environment %q pool %s has invalid region_mode %q", environmentName, describePool(*pool), pool.RegionMode)
			case pool.RegionMode == RegionModeWeighted && pool.Region != "":
				return fmt.Errorf("environment %q weighted pool %s must not declare region", environmentName, describePool(*pool))
			case pool.RegionMode == RegionModeWeighted && len(pool.Regions) == 0:
				return fmt.Errorf("environment %q weighted pool %s has no regions", environmentName, describePool(*pool))
			case pool.RegionMode == RegionModeWeighted && (poolRegions.Has("") || poolRegions.Len() != len(pool.Regions)):
				return fmt.Errorf("environment %q weighted pool %s has empty or duplicate regions", environmentName, describePool(*pool))
			case pool.RegionMode == RegionModeWeighted && pool.IdentityProvisioningRegion == "" && (c.Version == 1 || pool.SlotAssets.E2EIdentities != nil):
				return fmt.Errorf("environment %q weighted pool %s must declare identity_provisioning_region", environmentName, describePool(*pool))
			case pool.RegionMode != RegionModeWeighted && pool.Region == "":
				return fmt.Errorf("environment %q has a pool with empty region", environmentName)
			case pool.RegionMode != RegionModeWeighted && len(pool.Regions) > 0:
				return fmt.Errorf("environment %q non-weighted pool %s must not declare regions", environmentName, describePool(*pool))
			case pool.ResourceType == "":
				return fmt.Errorf("environment %q has a pool with empty resource_type", environmentName)
			case pool.SlotCount <= 0:
				return fmt.Errorf("environment %q pool %s has invalid slot_count %d", environmentName, describePool(*pool), pool.SlotCount)
			case pool.IdentityContainerPrefix == "" && (c.Version == 1 || pool.SlotAssets.E2EIdentities != nil):
				return fmt.Errorf("environment %q pool %s has empty identity_container_prefix", environmentName, describePool(*pool))
			case pool.IdentityContainerCount <= 0 && (c.Version == 1 || pool.SlotAssets.E2EIdentities != nil):
				return fmt.Errorf("environment %q pool %s has invalid identity_container_count %d", environmentName, describePool(*pool), pool.IdentityContainerCount)
			}

			if !environmentRegionModeSet {
				environmentRegionMode = pool.RegionMode
				environmentRegionModeSet = true
				environmentRegions = append([]string(nil), pool.Regions...)
			} else if pool.RegionMode != environmentRegionMode {
				return fmt.Errorf(
					"environment %q mixes region_mode values %q and %q; keep a single selection mode per environment",
					environmentName,
					environmentRegionMode,
					pool.RegionMode,
				)
			} else if pool.RegionMode == RegionModeWeighted && !slices.Equal(pool.Regions, environmentRegions) {
				return fmt.Errorf(
					"environment %q weighted pools must declare the same ordered regions; got %q and %q",
					environmentName,
					strings.Join(environmentRegions, ","),
					strings.Join(pool.Regions, ","),
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

	return c.validateAssetPools(resourceTypes)
}

func validateDeploymentEnvironmentName(name string) error {
	if !inventoryName.MatchString(name) {
		return fmt.Errorf("invalid deployment environment name %q: must match [A-Za-z0-9][A-Za-z0-9_-]*", name)
	}
	return nil
}

func normalizeV2Pool(environmentName string, pool *Pool) error {
	switch {
	case pool.Name == "":
		return fmt.Errorf("environment %q has a pool with empty name", environmentName)
	case pool.DeployEnv == "":
		return fmt.Errorf("environment %q has empty deployment_environment.name for pool %q", environmentName, pool.Name)
	case pool.Subscriptions.E2E == "":
		return fmt.Errorf("environment %q pool %q has empty subscriptions.e2e", environmentName, pool.Name)
	case pool.SlotAssets.InfrastructureIdentities != nil && pool.Subscriptions.Infrastructure == "":
		return fmt.Errorf("environment %q has empty deployment_environment.infrastructure_subscription for pool %q", environmentName, pool.Name)
	}

	if pool.SlotAssets.InfrastructureIdentities == nil {
		pool.Subscriptions.Infrastructure = ""
	}
	pool.SubscriptionName = pool.Subscriptions.E2E
	if pool.ResourceType == "" {
		pool.ResourceType = fmt.Sprintf("aro-hcp-%s-%s-slot", environmentName, pool.Name)
	}
	if pool.SlotAssets.InfrastructureIdentities != nil {
		asset := pool.SlotAssets.InfrastructureIdentities
		if asset.Allocation != AllocationLeased || strings.TrimSpace(asset.AssetPool) == "" || asset.UnitsPerSlot < 0 {
			return fmt.Errorf("pool %q infrastructure_identities requires allocation leased, asset_pool and positive units_per_slot", pool.Name)
		}
		if asset.UnitsPerSlot == 0 {
			asset.UnitsPerSlot = 1
		}
	}
	asset := pool.SlotAssets.E2EIdentities
	if asset == nil {
		pool.IdentityContainerPrefix = ""
		pool.IdentityContainerCount = 0
		pool.IdentityProvisioning = ""
		pool.IdentityProvisioningRegion = ""
		return nil
	}
	asset.Provisioning = strings.TrimSpace(asset.Provisioning)
	asset.ProvisioningRegion = strings.TrimSpace(asset.ProvisioningRegion)
	asset.ResourceGroupPrefix = strings.TrimSpace(asset.ResourceGroupPrefix)
	if asset.Provisioning == "" {
		asset.Provisioning = AssetProvisioningManaged
	}
	switch {
	case asset.Allocation != AllocationDedicated:
		return fmt.Errorf("pool %q e2e_identities requires allocation dedicated", pool.Name)
	case asset.Provisioning != AssetProvisioningManaged && asset.Provisioning != IdentityProvisioningUnmanaged:
		return fmt.Errorf("environment %q pool %q has invalid slot_assets.e2e_identities.provisioning %q", environmentName, pool.Name, asset.Provisioning)
	case asset.ResourceGroupPrefix == "":
		return fmt.Errorf("environment %q pool %q has empty slot_assets.e2e_identities.resource_group_prefix", environmentName, pool.Name)
	case asset.ResourceGroupCount <= 0:
		return fmt.Errorf("environment %q pool %q has invalid slot_assets.e2e_identities.resource_group_count %d", environmentName, pool.Name, asset.ResourceGroupCount)
	case pool.SlotCount > math.MaxInt/asset.ResourceGroupCount:
		return fmt.Errorf("environment %q pool %q dedicated identity demand overflows int", environmentName, pool.Name)
	}

	pool.IdentityProvisioning = asset.Provisioning
	if pool.IdentityProvisioning == AssetProvisioningManaged {
		pool.IdentityProvisioning = ""
	}
	pool.IdentityProvisioningRegion = asset.ProvisioningRegion
	pool.IdentityContainerPrefix = asset.ResourceGroupPrefix
	pool.IdentityContainerCount = asset.ResourceGroupCount
	return nil
}

func trimValues(values []string) []string {
	trimmed := make([]string, len(values))
	for i, value := range values {
		trimmed[i] = strings.TrimSpace(value)
	}
	return trimmed
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
		environment := c.Environments[environmentName]
		if c.Version == 1 {
			for _, candidate := range environment.DeployEnvs {
				if candidate == deployEnv {
					matches = append(matches, environmentName)
					break
				}
			}
			continue
		}
		for _, pool := range environment.Pools {
			if pool.DeployEnv == deployEnv {
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
		if environmentRegionMode == RegionModeRuntimeSelected || environmentRegionMode == RegionModeWeighted {
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
		if allowedSubscriptions.Len() > 0 && !allowedSubscriptions.Has(pool.E2ESubscriptionName()) {
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

func (c *Catalog) RegionModeForEnvironment(environment string) (RegionMode, error) {
	environmentConfig, found := c.Environments[environment]
	if !found {
		return RegionModeFixed, fmt.Errorf("unknown environment %q", environment)
	}
	if len(environmentConfig.Pools) == 0 {
		return RegionModeFixed, fmt.Errorf("environment %q has no pools", environment)
	}

	regionMode := environmentConfig.Pools[0].EffectiveRegionMode()
	for _, pool := range environmentConfig.Pools[1:] {
		if candidate := pool.EffectiveRegionMode(); candidate != regionMode {
			return RegionModeFixed, fmt.Errorf("environment %q mixes region_mode values %q and %q", environment, regionMode, candidate)
		}
	}
	return regionMode, nil
}

func (c *Catalog) RegionsForEnvironment(environment string) ([]string, error) {
	environmentConfig, found := c.Environments[environment]
	if !found {
		return nil, fmt.Errorf("unknown environment %q", environment)
	}
	if len(environmentConfig.Pools) == 0 {
		return nil, fmt.Errorf("environment %q has no pools", environment)
	}

	if environmentConfig.Pools[0].EffectiveRegionMode() != RegionModeWeighted {
		return nil, fmt.Errorf("environment %q is not in weighted region mode", environment)
	}
	return append([]string(nil), environmentConfig.Pools[0].Regions...), nil
}

func ExpandSlotsForPool(environment string, pool Pool) []ExpandedSlot {
	slots := make([]ExpandedSlot, 0, pool.SlotCount)
	for i := 0; i < pool.SlotCount; i++ {
		identityContainerPrefix := fmt.Sprintf("%s-%0*d", pool.IdentityContainerPrefix, defaultSlotIndexWidth, i)
		identityContainers := identityContainerNames(identityContainerPrefix, pool.IdentityContainerCount)
		slot := ExpandedSlot{
			Requirements:            pool.Requirements(),
			Environment:             environment,
			PoolName:                pool.Name,
			DeployEnvironment:       pool.DeployEnv,
			SubscriptionName:        pool.E2ESubscriptionName(),
			Region:                  pool.Region,
			ResourceType:            pool.ResourceType,
			ResourceName:            fmt.Sprintf("%s-%0*d", pool.ResourceType, defaultSlotIndexWidth, i),
			SlotIndex:               i,
			IdentityContainerPrefix: identityContainerPrefix,
			IdentityContainerCount:  pool.IdentityContainerCount,
		}
		if pool.Name != "" {
			slot.Subscriptions = ResolvedSubscriptions{
				E2E:            ResolvedSubscription{Name: pool.E2ESubscriptionName()},
				Infrastructure: ResolvedSubscription{Name: pool.InfrastructureSubscriptionName()},
			}
		}
		if pool.SlotAssets.E2EIdentities != nil {
			slot.Assets = ResolvedAssets{
				E2EIdentities: &ResolvedE2EIdentitiesAsset{
					Allocation:         AllocationDedicated,
					ProvisioningRegion: pool.EffectiveIdentityProvisioningRegion(),
					ResourceGroups:     identityContainers,
				},
			}
		}
		slots = append(slots, slot)
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

func (p Pool) E2ESubscriptionName() string {
	if p.Subscriptions.E2E != "" {
		return p.Subscriptions.E2E
	}
	return p.SubscriptionName
}

func (p Pool) InfrastructureSubscriptionName() string {
	if p.SlotAssets.InfrastructureIdentities == nil {
		return ""
	}
	return p.Subscriptions.Infrastructure
}

func (p Pool) EffectiveDeployEnvironment(requested string) string {
	if p.DeployEnv != "" {
		return p.DeployEnv
	}
	return requested
}

func (p Pool) EffectiveRegionMode() RegionMode {
	return p.RegionMode
}

func (p Pool) EffectiveIdentityProvisioningRegion() string {
	if p.IdentityProvisioningRegion != "" {
		return p.IdentityProvisioningRegion
	}
	return p.Region
}

func (s ExpandedSlot) IdentityContainerNames() []string {
	if s.Assets.E2EIdentities != nil {
		return append([]string(nil), s.Assets.E2EIdentities.ResourceGroups...)
	}
	return identityContainerNames(s.IdentityContainerPrefix, s.IdentityContainerCount)
}

func identityContainerNames(prefix string, count int) []string {
	names := make([]string, 0, count)
	for i := 0; i < count; i++ {
		names = append(names, fmt.Sprintf("%s-%0*d", prefix, defaultContainerIndexWidth, i))
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
	if pool.Name != "" {
		return fmt.Sprintf("(name=%q, subscription_name=%q, region_mode=%q)", pool.Name, pool.E2ESubscriptionName(), pool.EffectiveRegionMode())
	}
	switch pool.EffectiveRegionMode() {
	case RegionModeRuntimeSelected:
		return fmt.Sprintf("(subscription_name=%q, region_mode=%q, default_region=%q)", pool.SubscriptionName, pool.EffectiveRegionMode(), pool.Region)
	case RegionModeWeighted:
		return fmt.Sprintf("(subscription_name=%q, region_mode=%q, regions=%q)", pool.SubscriptionName, pool.EffectiveRegionMode(), strings.Join(pool.Regions, ","))
	default:
		return fmt.Sprintf("(subscription_name=%q, region=%q)", pool.SubscriptionName, pool.Region)
	}
}

func poolIdentity(pool Pool) string {
	if pool.Name != "" {
		return pool.Name
	}
	if pool.EffectiveRegionMode() == RegionModeRuntimeSelected || pool.EffectiveRegionMode() == RegionModeWeighted {
		return pool.E2ESubscriptionName()
	}
	return fmt.Sprintf("%s/%s", pool.E2ESubscriptionName(), pool.Region)
}

func qualifiedPoolName(environment string, pool Pool) string {
	return fmt.Sprintf("%s/%s", environment, poolIdentity(pool))
}
