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
)

const (
	DefaultCatalogRelPath    = "test/e2e-config/e2e-slots.yaml"
	defaultEnvFileName       = "aro-hcp-slot-env.sh"
	defaultSlotStateFileName = "aro-hcp-slot-state.yaml"

	defaultSlotIndexWidth      = 2
	defaultContainerIndexWidth = 2
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
	SubscriptionName        string `yaml:"subscription_name"`
	Region                  string `yaml:"region"`
	ResourceType            string `yaml:"resource_type"`
	SlotCount               int    `yaml:"slot_count"`
	IdentityContainerPrefix string `yaml:"identity_container_prefix"`
	IdentityContainerCount  int    `yaml:"identity_container_count"`
}

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
	return resolveRepoFile(DefaultCatalogRelPath)
}

func resolveRepoFile(relPath string) (string, error) {
	workingDir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("failed to get current working directory: %w", err)
	}

	// Commands can run from nested directories within the repo, so walk upward
	// until we find the requested path or hit the filesystem root.
	dir := workingDir
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

	return "", fmt.Errorf("failed to find %q from %q", relPath, workingDir)
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
		for _, pool := range environment.Pools {
			switch {
			case strings.TrimSpace(pool.SubscriptionName) == "":
				return fmt.Errorf("environment %q has a pool with empty subscription_name", environmentName)
			case strings.TrimSpace(pool.Region) == "":
				return fmt.Errorf("environment %q has a pool with empty region", environmentName)
			case strings.TrimSpace(pool.ResourceType) == "":
				return fmt.Errorf("environment %q has a pool with empty resource_type", environmentName)
			case pool.SlotCount <= 0:
				return fmt.Errorf("environment %q pool %s has invalid slot_count %d", environmentName, describePool(pool), pool.SlotCount)
			case strings.TrimSpace(pool.IdentityContainerPrefix) == "":
				return fmt.Errorf("environment %q pool %s has empty identity_container_prefix", environmentName, describePool(pool))
			case pool.IdentityContainerCount <= 0:
				return fmt.Errorf("environment %q pool %s has invalid identity_container_count %d", environmentName, describePool(pool), pool.IdentityContainerCount)
			}

			poolKey := poolIdentity(pool)
			if _, found := seenPoolKeys[poolKey]; found {
				return fmt.Errorf("environment %q declares duplicate pool %s", environmentName, describePool(pool))
			}
			seenPoolKeys[poolKey] = struct{}{}

			if previous, exists := resourceTypes[pool.ResourceType]; exists {
				return fmt.Errorf("resource type %q is declared by both %s and %s", pool.ResourceType, previous, qualifiedPoolName(environmentName, pool))
			}
			resourceTypes[pool.ResourceType] = qualifiedPoolName(environmentName, pool)
		}
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
	deployEnv = strings.TrimSpace(deployEnv)
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

func (c *Catalog) ResolvePool(environment, subscriptionName, region string) (Pool, error) {
	environmentConfig, found := c.Environments[environment]
	if !found {
		return Pool{}, fmt.Errorf("unknown environment %q", environment)
	}

	subscriptionName = strings.TrimSpace(subscriptionName)
	region = strings.TrimSpace(region)

	matches := make([]Pool, 0, len(environmentConfig.Pools))
	for _, pool := range environmentConfig.Pools {
		if subscriptionName != "" && pool.SubscriptionName != subscriptionName {
			continue
		}
		if region != "" && pool.Region != region {
			continue
		}
		matches = append(matches, pool)
	}

	switch len(matches) {
	case 0:
		selectors := []string{}
		if subscriptionName != "" {
			selectors = append(selectors, fmt.Sprintf("subscription_name=%q", subscriptionName))
		}
		if region != "" {
			selectors = append(selectors, fmt.Sprintf("region=%q", region))
		}
		if len(selectors) == 0 {
			return Pool{}, fmt.Errorf("environment %q has no pools", environment)
		}
		return Pool{}, fmt.Errorf("no pool found for environment %q matching %s", environment, strings.Join(selectors, ", "))
	case 1:
		return matches[0], nil
	default:
		return Pool{}, fmt.Errorf("environment %q has %d matching pools; specify both --subscription-name and --region to disambiguate", environment, len(matches))
	}
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

func (s ExpandedSlot) IdentityContainerNames() []string {
	names := make([]string, 0, s.IdentityContainerCount)
	for i := 0; i < s.IdentityContainerCount; i++ {
		names = append(names, fmt.Sprintf("%s-%0*d", s.IdentityContainerPrefix, defaultContainerIndexWidth, i))
	}
	return names
}

func SharedStateDir(sharedDir string) (string, error) {
	if strings.TrimSpace(sharedDir) == "" {
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
	return fmt.Sprintf("(subscription_name=%q, region=%q)", pool.SubscriptionName, pool.Region)
}

func poolIdentity(pool Pool) string {
	return fmt.Sprintf("%s/%s", pool.SubscriptionName, pool.Region)
}

func qualifiedPoolName(environment string, pool Pool) string {
	return fmt.Sprintf("%s/%s", environment, poolIdentity(pool))
}
