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
	"errors"
	"fmt"
	"hash/fnv"
	"math"
	"strconv"
	"strings"

	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager/catalog"
)

type RegionSelection struct {
	Mode                 catalog.RegionMode
	RuntimeRegion        string
	CatalogRegions       []string
	NormalizedWeights    []string
	LocationOverrideUsed bool
	SelectionKeySource   string
}

type locationWeight struct {
	Location string
	Weight   uint64
}

func resolveRegionSelection(slotCatalog *catalog.Catalog, environment string, regionMode catalog.RegionMode, override, rawWeights, buildID string) (RegionSelection, error) {
	selection := RegionSelection{
		Mode:                 regionMode,
		RuntimeRegion:        override,
		LocationOverrideUsed: override != "",
	}
	if regionMode != catalog.RegionModeWeighted {
		return selection, nil
	}

	regions, err := slotCatalog.RegionsForEnvironment(environment)
	if err != nil {
		return RegionSelection{}, err
	}
	selection.CatalogRegions = regions
	if override != "" {
		if !sets.New(regions...).Has(override) {
			return RegionSelection{}, fmt.Errorf("location override %q is not allowed for weighted environment %q; allowed locations: %s", override, environment, strings.Join(regions, ","))
		}
		return selection, nil
	}

	weights, totalWeight, err := parseLocationWeights(rawWeights, regions)
	if err != nil {
		return RegionSelection{}, fmt.Errorf("invalid LOCATION_WEIGHTS for weighted environment %q: %w", environment, err)
	}
	buildID = strings.TrimSpace(buildID)
	if buildID == "" {
		return RegionSelection{}, fmt.Errorf("BUILD_ID must not be empty for weighted environment %q without a location override", environment)
	}

	hasher := fnv.New64a()
	_, _ = hasher.Write([]byte(buildID))
	bucket := hasher.Sum64() % totalWeight

	var cumulative uint64
	for _, weight := range weights {
		cumulative += weight.Weight
		if bucket < cumulative {
			selection.RuntimeRegion = weight.Location
			selection.NormalizedWeights = normalizedWeightsForCatalog(weights)
			selection.SelectionKeySource = "BUILD_ID"
			return selection, nil
		}
	}

	return RegionSelection{}, errors.New("weighted location selection did not resolve a region")
}

func parseLocationWeights(raw string, regions []string) ([]locationWeight, uint64, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, 0, errors.New("LOCATION_WEIGHTS must not be empty")
	}

	allowedRegions := sets.New(regions...)
	parsedWeights := make(map[string]uint64, len(regions))
	normalized := strings.ReplaceAll(raw, "\r\n", "\n")
	normalized = strings.ReplaceAll(normalized, "\r", "\n")
	normalized = strings.ReplaceAll(normalized, "\n", ",")
	for _, entry := range strings.Split(normalized, ",") {
		location, rawWeight, found := strings.Cut(entry, "=")
		location = strings.TrimSpace(location)
		rawWeight = strings.TrimSpace(rawWeight)
		switch {
		case !found || location == "" || rawWeight == "":
			return nil, 0, fmt.Errorf("entry %q must use location=weight format", strings.TrimSpace(entry))
		case !allowedRegions.Has(location):
			return nil, 0, fmt.Errorf("unknown location %q; allowed locations: %s", location, strings.Join(regions, ","))
		}
		if _, found := parsedWeights[location]; found {
			return nil, 0, fmt.Errorf("location %q is declared more than once", location)
		}

		weight, err := strconv.ParseUint(rawWeight, 10, 64)
		if err != nil {
			return nil, 0, fmt.Errorf("weight for location %q must be a non-negative integer: %w", location, err)
		}
		parsedWeights[location] = weight
	}

	weights := make([]locationWeight, 0, len(regions))
	var totalWeight uint64
	for _, region := range regions {
		weight, found := parsedWeights[region]
		if !found {
			return nil, 0, fmt.Errorf("missing weight for location %q", region)
		}
		if math.MaxUint64-totalWeight < weight {
			return nil, 0, errors.New("location weight sum overflows uint64")
		}
		totalWeight += weight
		weights = append(weights, locationWeight{Location: region, Weight: weight})
	}
	if totalWeight == 0 {
		return nil, 0, errors.New("at least one location weight must be greater than zero")
	}
	return weights, totalWeight, nil
}

func normalizedWeightsForCatalog(weights []locationWeight) []string {
	normalized := make([]string, 0, len(weights))
	for _, weight := range weights {
		normalized = append(normalized, fmt.Sprintf("%s=%d", weight.Location, weight.Weight))
	}
	return normalized
}
