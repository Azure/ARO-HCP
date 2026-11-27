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

package acquire

import (
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager/catalog"
)

// Plan is the side-effect-free outcome of resolving an acquire request against
// the catalog: which environment, which candidate pools in catalog order, and
// how the runtime region is chosen. Every request validation that does not need
// the network or Azure happens in NewPlan.
type Plan struct {
	PoolEnvironment string
	CandidatePools  []catalog.Pool
	RegionSelection RegionSelection
}

// PlanInput holds the selectors an acquire request supplies. Empty values mean
// "not specified".
type PlanInput struct {
	Catalog              *catalog.Catalog
	Environment          string
	DeployEnv            string
	AllowedSubscriptions []string
	AllowedLocations     []string
	SelectedLocation     string
	LocationWeights      string
	BuildID              string
}

// NewPlan resolves the environment and candidate pools for in and chooses the
// runtime region.
func NewPlan(in PlanInput) (*Plan, error) {
	slotCatalog := in.Catalog
	selectedLocation := strings.TrimSpace(in.SelectedLocation)
	environment := strings.TrimSpace(in.Environment)
	var err error
	if environment == "" {
		environment, err = slotCatalog.ResolveEnvironmentForDeployEnv(in.DeployEnv)
		if err != nil {
			return nil, err
		}
	} else if _, found := slotCatalog.Environments[environment]; !found {
		return nil, fmt.Errorf("unknown environment %q", environment)
	}

	candidatePools, err := slotCatalog.CandidatePools(environment, sets.New(in.AllowedSubscriptions...), sets.New(in.AllowedLocations...), selectedLocation)
	if err != nil {
		return nil, err
	}
	if in.DeployEnv != "" && in.DeployEnv != slotCatalog.Environments[environment].DeploymentEnvironment.Name {
		return nil, fmt.Errorf("environment %q has no candidate pool with deploy_env %q", environment, in.DeployEnv)
	}

	regionMode, err := slotCatalog.RegionModeForEnvironment(environment)
	if err != nil {
		return nil, err
	}
	regionSelection, err := resolveRegionSelection(slotCatalog, environment, regionMode, selectedLocation, in.LocationWeights, in.BuildID)
	if err != nil {
		return nil, err
	}
	return &Plan{PoolEnvironment: environment, CandidatePools: candidatePools, RegionSelection: regionSelection}, nil
}

func (p *Plan) ResolveLeasedSlot(pool catalog.Pool, resourceName string) (*catalog.ExpandedSlot, error) {
	for _, slot := range catalog.ExpandSlotsForPool(p.PoolEnvironment, pool) {
		if slot.ResourceName == resourceName {
			resolvedSlot := slot
			return &resolvedSlot, nil
		}
	}

	return nil, fmt.Errorf(
		"leased resource %q is not part of selected pool %s in environment %q",
		resourceName,
		describePool(pool),
		p.PoolEnvironment,
	)
}

// RuntimeRegionForPool returns the region the slot runs in: the selected
// runtime region when one was chosen, otherwise the pool's own region.
func (p *Plan) RuntimeRegionForPool(pool catalog.Pool) string {
	if p.RegionSelection.RuntimeRegion != "" {
		return p.RegionSelection.RuntimeRegion
	}
	return pool.Region
}

func describePool(pool catalog.Pool) string {
	switch pool.EffectiveRegionMode() {
	case catalog.RegionModeRuntimeSelected:
		return fmt.Sprintf("subscription_name=%q, region_mode=%q, default_region=%q", pool.Subscriptions.E2E, pool.EffectiveRegionMode(), pool.Region)
	case catalog.RegionModeWeighted:
		return fmt.Sprintf("subscription_name=%q, region_mode=%q, regions=%q", pool.Subscriptions.E2E, pool.EffectiveRegionMode(), strings.Join(pool.Regions, ","))
	default:
		return fmt.Sprintf("subscription_name=%q, region=%q", pool.Subscriptions.E2E, pool.Region)
	}
}
