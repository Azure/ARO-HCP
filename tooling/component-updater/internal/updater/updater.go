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

package updater

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Azure/ARO-HCP/tooling/component-updater/internal/config"
	"github.com/Azure/ARO-HCP/tooling/component-updater/internal/providers"
)

type ComponentResult struct {
	Name          string
	Current       string
	Available     []string
	Candidate     string
	Next          string
	Compatibility string
	// ConstraintVersions holds the constraint values read at discovery time
	// (for example every AKS kubernetes version the environments run). The
	// first entry is read from the target file; the rest are configured
	// literals. Empty when unused.
	ConstraintVersions []string
	Locations          []string
	Detail             string
	Error              error
	Regions            []providers.RegionResult
	Updated            bool
	Paused             bool
}

// ProviderFactory resolves a VersionProvider for a component's configured
// provider name (for example azure-istio). Callers typically wrap providers.New.
type ProviderFactory func(providerName string) (providers.VersionProvider, error)

func ValidateComponents(cfg *config.Config, components []string) error {
	for _, requested := range components {
		found := false
		for name := range cfg.Components {
			if strings.EqualFold(requested, name) {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("unknown component %q", requested)
		}
	}
	return nil
}

func Check(ctx context.Context, cfg *config.Config, configDir string, factory ProviderFactory, components []string) ([]ComponentResult, error) {
	return checkComponents(ctx, cfg, configDir, factory, components, false)
}

func checkComponents(ctx context.Context, cfg *config.Config, configDir string, factory ProviderFactory, components []string, skipPaused bool) ([]ComponentResult, error) {
	if err := ValidateComponents(cfg, components); err != nil {
		return nil, err
	}
	var results []ComponentResult
	var names []string
	for name := range cfg.Components {
		names = append(names, name)
	}
	sort.Strings(names)
	providersByName := map[string]providers.VersionProvider{}
	var failures []error
	for _, name := range names {
		if len(components) > 0 && !containsString(components, name) {
			continue
		}
		comp := cfg.Components[name]
		if skipPaused && comp.Paused {
			results = append(results, ComponentResult{
				Name: name, Paused: true, Detail: "automatic updates paused in component-updater configuration",
			})
			continue
		}
		provider, err := providerFor(factory, providersByName, comp.Provider)
		if err != nil {
			result := ComponentResult{Name: name, Locations: comp.Locations, Error: err}
			results = append(results, result)
			failures = append(failures, fmt.Errorf("%s: %w", name, err))
			continue
		}
		result := checkComponent(ctx, name, comp, configDir, provider)
		results = append(results, result)
		if result.Error != nil {
			failures = append(failures, fmt.Errorf("%s: %w", name, result.Error))
		}
	}
	return results, errors.Join(failures...)
}

func providerFor(factory ProviderFactory, cache map[string]providers.VersionProvider, providerName string) (providers.VersionProvider, error) {
	if provider, ok := cache[providerName]; ok {
		return provider, nil
	}
	if factory == nil {
		return nil, fmt.Errorf("provider factory is required")
	}
	provider, err := factory(providerName)
	if err != nil {
		return nil, err
	}
	cache[providerName] = provider
	return provider, nil
}

func checkComponent(ctx context.Context, name string, comp config.ComponentConfig, configDir string, provider providers.VersionProvider) ComponentResult {
	result := ComponentResult{Name: name, Locations: comp.Locations}
	if len(comp.Targets) != 1 {
		result.Error = fmt.Errorf("exactly one target is supported")
		return result
	}
	target := comp.Targets[0]
	filePath := resolveFilePath(configDir, target.FilePath)
	current, err := config.ReadCurrentVersion(filePath, target.JsonPath)
	if err != nil {
		result.Error = fmt.Errorf("reading configured version: %w", err)
		return result
	}
	result.Current = current
	req := providers.CheckRequest{
		Current:   current,
		Locations: comp.Locations,
	}
	if comp.ConstraintVersionPath != "" {
		constraint, err := config.ReadCurrentVersion(filePath, comp.ConstraintVersionPath)
		if err != nil {
			result.Error = fmt.Errorf("reading constraint version: %w", err)
			return result
		}
		// The live value always leads; configured literals (values pinned by
		// out-of-repo overlays) extend the set a candidate must satisfy. A
		// literal that matches the live value means the overlay it tracked has
		// caught up: fail loudly so the stale entry is removed instead of
		// silently deduplicated and forgotten.
		req.ConstraintVersions = []string{constraint}
		for _, version := range comp.ConstraintVersions {
			if version == constraint {
				result.Error = fmt.Errorf("stale constraintVersions entry %q equals the live value at %s; remove it from the component-updater configuration", version, comp.ConstraintVersionPath)
				return result
			}
			req.ConstraintVersions = append(req.ConstraintVersions, version)
		}
		result.ConstraintVersions = req.ConstraintVersions
	}
	discovery, err := provider.Check(ctx, req)
	result.Compatibility = discovery.Compatibility
	result.Available = discovery.Available
	result.Candidate = discovery.Candidate
	result.Regions = discovery.Regions
	result.Detail = discovery.Detail
	if err != nil {
		result.Error = err
		return result
	}
	result.Next = discovery.Next
	return result
}

func Update(ctx context.Context, cfg *config.Config, configDir string, factory ProviderFactory, components []string) ([]ComponentResult, error) {
	if err := ValidateComponents(cfg, components); err != nil {
		return nil, err
	}
	providersByName := map[string]providers.VersionProvider{}
	for name, component := range cfg.Components {
		if component.Paused || len(components) > 0 && !containsString(components, name) {
			continue
		}
		provider, err := providerFor(factory, providersByName, component.Provider)
		if err != nil {
			err = fmt.Errorf("%s: %w", name, err)
			return []ComponentResult{{Name: name, Error: err}}, err
		}
		if validator, ok := provider.(providers.UpdateValidator); ok {
			if err := validator.ValidateUpdate(providers.UpdateRequest{Locations: component.Locations}); err != nil {
				err = fmt.Errorf("%s: %w", name, err)
				return []ComponentResult{{Name: name, Error: err}}, err
			}
		}
	}
	results, err := checkComponents(ctx, cfg, configDir, factory, components, true)
	if err != nil {
		return results, err
	}
	var failures []error
	for i, r := range results {
		if r.Paused || r.Next == "" {
			continue
		}
		if err := ctx.Err(); err != nil {
			results[i].Error = err
			return results, err
		}
		comp := cfg.Components[r.Name]
		target := comp.Targets[0]
		filePath := resolveFilePath(configDir, target.FilePath)
		if comp.ConstraintVersionPath != "" {
			constraint, err := config.ReadCurrentVersion(filePath, comp.ConstraintVersionPath)
			if err != nil {
				results[i].Error = fmt.Errorf("re-reading constraint version before write: %w", err)
				failures = append(failures, results[i].Error)
				continue
			}
			if len(r.ConstraintVersions) == 0 || constraint != r.ConstraintVersions[0] {
				results[i].Error = fmt.Errorf("constraint %q changed since discovery (was %q, now %q); refusing to write", comp.ConstraintVersionPath, r.ConstraintVersions, constraint)
				failures = append(failures, results[i].Error)
				continue
			}
		}
		if err := config.WriteVersion(filePath, target.JsonPath, r.Current, r.Next); err != nil {
			results[i].Error = fmt.Errorf("updating %s in %s: %w", target.JsonPath, target.FilePath, err)
			failures = append(failures, results[i].Error)
		} else {
			results[i].Updated = true
		}
	}
	return results, errors.Join(failures...)
}

func resolveFilePath(configDir, filePath string) string {
	if filepath.IsAbs(filePath) {
		return filePath
	}
	return filepath.Join(configDir, filePath)
}

func containsString(ss []string, s string) bool {
	for _, v := range ss {
		if strings.EqualFold(v, s) {
			return true
		}
	}
	return false
}
