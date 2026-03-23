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

package arm

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armlocks"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"

	"github.com/Azure/ARO-HCP/tooling/cleanup-sweeper/pkg/engine/runner"
)

type ARMDeletionStep struct {
	runner.DeletionStep
	apiVersionCache *apiVersionCache
}

type apiVersionCache struct {
	mu    sync.Mutex
	cache map[string]string
}

func newAPIVersionCache() *apiVersionCache {
	return &apiVersionCache{cache: make(map[string]string)}
}

func (c *apiVersionCache) Get(key string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.cache[key]
	return v, ok
}

func (c *apiVersionCache) Set(key, value string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cache[key] = value
}

type ResourceSelector struct {
	IncludedResourceTypes []string
	ExcludedResourceTypes []string
}

type DeletionStepConfig struct {
	ResourceGroupName string
	Client            *armresources.Client
	LocksClient       *armlocks.ManagementLocksClient
	ProvidersClient   *armresources.ProvidersClient
	Selector          ResourceSelector

	Name            string
	Retries         int
	ContinueOnError bool
	Verify          runner.VerifyFn
}

var _ runner.StepOptionsProvider = DeletionStepConfig{}

func (c DeletionStepConfig) StepOptions() runner.StepOptions {
	return runner.StepOptions{
		Name:            c.Name,
		Retries:         c.Retries,
		ContinueOnError: c.ContinueOnError,
		Verify:          c.Verify,
	}
}

func NewDeletionStep(cfg DeletionStepConfig) *ARMDeletionStep {
	selector := cfg.Selector
	hasIncluded := len(selector.IncludedResourceTypes) > 0
	hasExcluded := len(selector.ExcludedResourceTypes) > 0
	if hasIncluded == hasExcluded {
		panic("exactly one of IncludedResourceTypes or ExcludedResourceTypes must be set")
	}

	stepOptions := cfg.StepOptions()
	if stepOptions.Name == "" {
		switch {
		case hasIncluded && len(selector.IncludedResourceTypes) == 1:
			stepOptions.Name = fmt.Sprintf("Delete %s", selector.IncludedResourceTypes[0])
		case hasIncluded:
			stepOptions.Name = "Delete selected resources"
		default:
			stepOptions.Name = "Delete resources excluding selected types"
		}
	}

	armds := &ARMDeletionStep{
		DeletionStep: runner.DeletionStep{
			ResourceType: "",
			Options:      stepOptions,
		},
		apiVersionCache: newAPIVersionCache(),
	}

	armds.DiscoverFn = func(ctx context.Context, _ string) ([]runner.Target, error) {
		targets := []runner.Target{}
		seenByID := map[string]struct{}{}

		appendTarget := func(resource *armresources.GenericResourceExpanded) {
			if resource == nil || resource.ID == nil || resource.Name == nil || resource.Type == nil {
				return
			}
			id := *resource.ID
			if _, exists := seenByID[id]; exists {
				return
			}
			seenByID[id] = struct{}{}
			targets = append(targets, runner.Target{
				ID:   id,
				Name: *resource.Name,
				Type: *resource.Type,
			})
		}

		if hasIncluded {
			for _, resourceType := range selector.IncludedResourceTypes {
				resources, err := ListByType(ctx, cfg.Client, cfg.ResourceGroupName, resourceType)
				if err != nil {
					return nil, err
				}
				for _, resource := range resources {
					appendTarget(resource)
				}
			}
			return targets, nil
		}

		excluded := map[string]struct{}{}
		for _, t := range selector.ExcludedResourceTypes {
			excluded[strings.ToLower(t)] = struct{}{}
		}

		pager := cfg.Client.NewListByResourceGroupPager(cfg.ResourceGroupName, nil)
		for pager.More() {
			page, err := pager.NextPage(ctx)
			if err != nil {
				return nil, fmt.Errorf("failed to list resources: %w", err)
			}
			for _, resource := range page.Value {
				if resource == nil || resource.Type == nil {
					continue
				}
				if _, isExcluded := excluded[strings.ToLower(*resource.Type)]; isExcluded {
					continue
				}
				appendTarget(resource)
			}
		}
		return targets, nil
	}
	armds.DeleteFn = func(ctx context.Context, target runner.Target, wait bool) error {
		return DeleteByIDWithCache(ctx, cfg.Client, cfg.ProvidersClient, target.ID, target.Type, wait, armds.apiVersionCache)
	}
	armds.VerifyFn = func(ctx context.Context) error {
		if stepOptions.Verify == nil {
			return nil
		}
		return stepOptions.Verify(ctx)
	}
	armds.SkipFn = func(ctx context.Context, target runner.Target) (bool, string, error) {
		if HasLocks(ctx, cfg.LocksClient, target.ID) {
			return true, "locked", nil
		}
		return false, "", nil
	}

	return armds
}
