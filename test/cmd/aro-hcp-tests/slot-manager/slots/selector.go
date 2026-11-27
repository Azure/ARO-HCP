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
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/validation"
)

const poolNameLabel = "pool.name"
const customLabelPrefix = "slots.aro-hcp.azure.com/"

func validatePoolLabels(environment string, pool Pool) error {
	if problems := validation.IsValidLabelValue(pool.Name); len(problems) != 0 {
		return fmt.Errorf("environment %q pool.name %q: %s", environment, pool.Name, strings.Join(problems, "; "))
	}
	for key, value := range pool.Labels {
		if key == poolNameLabel || !strings.HasPrefix(key, customLabelPrefix) {
			return fmt.Errorf("environment %q pool %q label key %q must use prefix %q and cannot override pool.name", environment, pool.Name, key, customLabelPrefix)
		}
		if problems := validation.IsQualifiedName(key); len(problems) != 0 {
			return fmt.Errorf("environment %q pool %q label key %q: %s", environment, pool.Name, key, strings.Join(problems, "; "))
		}
		if problems := validation.IsValidLabelValue(value); len(problems) != 0 {
			return fmt.Errorf("environment %q pool %q label %q: %s", environment, pool.Name, key, strings.Join(problems, "; "))
		}
	}
	return nil
}

// SelectPools applies scalar selectors only within the resolved environment, then a hard
// placement: in a fixed-region environment only pools of that region remain (weighted and
// runtime-selected environments validate placement when the region is resolved).
// Negative predicates retain Kubernetes absent-key semantics; use an existence
// guard when a negative match must also require presence.
func (c *Catalog) SelectPools(environment, expression, placement string) ([]Pool, error) {
	matches, err := c.selectPoolsByLabels(environment, expression)
	if err != nil || placement == "" {
		return matches, err
	}
	mode, err := c.RegionModeForEnvironment(environment)
	if err != nil || mode != RegionModeFixed {
		return matches, err
	}
	placed := matches[:0:0]
	for _, pool := range matches {
		if pool.Region == placement {
			placed = append(placed, pool)
		}
	}
	if len(placed) == 0 {
		return nil, fmt.Errorf("no pool matching selector %q in environment %q is placed in %q", expression, environment, placement)
	}
	return placed, nil
}

func (c *Catalog) selectPoolsByLabels(environment, expression string) ([]Pool, error) {
	env, found := c.Environments[environment]
	if !found {
		return nil, fmt.Errorf("unknown environment %q", environment)
	}
	selector, err := labels.Parse(expression)
	if err != nil {
		return nil, fmt.Errorf("environment %q pool selector %q: %w", environment, expression, err)
	}
	known := sets.New(poolNameLabel)
	for _, pool := range env.Pools {
		for key := range pool.Labels {
			known.Insert(key)
		}
	}
	requirements, _ := selector.Requirements()
	for _, requirement := range requirements {
		if !known.Has(requirement.Key()) {
			return nil, fmt.Errorf("environment %q selector requirement %q uses unknown key %q", environment, requirement.String(), requirement.Key())
		}
		switch requirement.Operator() {
		case selection.Equals, selection.DoubleEquals, selection.NotEquals, selection.In, selection.NotIn, selection.Exists, selection.DoesNotExist:
		default:
			return nil, fmt.Errorf("environment %q selector requirement %q uses unsupported operator %q", environment, requirement.String(), requirement.Operator())
		}
	}
	matches := make([]Pool, 0, len(env.Pools))
	for _, pool := range env.Pools {
		if selector.Matches(selectionLabels{name: pool.Name, custom: pool.Labels}) {
			matches = append(matches, pool)
		}
	}
	if len(matches) == 0 {
		exclusions := make([]string, 0, len(env.Pools))
		for _, pool := range env.Pools {
			projected := selectionLabels{name: pool.Name, custom: pool.Labels}
			var reasons []string
			for _, requirement := range requirements {
				if !requirement.Matches(projected) {
					reasons = append(reasons, requirement.String())
				}
			}
			exclusions = append(exclusions, fmt.Sprintf("pool %q: selector mismatch [%s]", pool.Name, strings.Join(reasons, ", ")))
		}
		return nil, fmt.Errorf("no pool found for environment %q matching selector %q; exclusions: %s", environment, expression, strings.Join(exclusions, "; "))
	}
	return matches, nil
}

// selectionLabels projects the reserved name without allocating or copying labels.
type selectionLabels struct {
	name   string
	custom map[string]string
}

func (p selectionLabels) Has(key string) bool {
	if key == poolNameLabel {
		return true
	}
	_, found := p.custom[key]
	return found
}

func (p selectionLabels) Get(key string) string {
	if key == poolNameLabel {
		return p.name
	}
	return p.custom[key]
}

func (p selectionLabels) Lookup(key string) (string, bool) {
	if key == poolNameLabel {
		return p.name, true
	}
	value, found := p.custom[key]
	return value, found
}
