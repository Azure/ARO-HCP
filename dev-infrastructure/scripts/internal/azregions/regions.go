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

// Package azregions mirrors the static region -> availability-zone table in
// dev-infrastructure/modules/common.bicep (_locationAvailabilityZones, consumed
// via getLocationAvailabilityZonesCSV). pool.bicep falls back to this region
// zone list when a pool's configured zones are empty, so pipeline scripts must
// resolve the same effective zones to compute expected pool names that match
// what ARM actually deployed.
//
// IMPORTANT: keep this table in sync with dev-infrastructure/modules/common.bicep.
package azregions

import "strings"

// locationAvailabilityZones is a verbatim port of _locationAvailabilityZones in
// dev-infrastructure/modules/common.bicep. Regions absent from this map are
// unknown and callers must fail closed rather than assume non-zonal.
var locationAvailabilityZones = map[string][]string{
	"asia":                {},
	"asiapacific":         {},
	"australia":           {},
	"australiacentral":    {},
	"australiacentral2":   {},
	"australiaeast":       {"1", "2", "3"},
	"australiasoutheast":  {},
	"brazil":              {},
	"brazilsouth":         {"1", "2", "3"},
	"brazilsoutheast":     {},
	"brazilus":            {},
	"canada":              {},
	"canadacentral":       {"1", "2", "3"},
	"canadaeast":          {},
	"centralindia":        {"1", "2", "3"},
	"centralus":           {"1", "2", "3"},
	"centraluseuap":       {},
	"centralusstage":      {},
	"eastasia":            {"1", "2", "3"},
	"eastasiastage":       {},
	"eastus":              {"1", "2", "3"},
	"eastus2":             {"1", "2", "3"},
	"eastus2euap":         {"1", "3", "4"},
	"eastus2stage":        {},
	"eastusstage":         {},
	"eastusstg":           {},
	"europe":              {},
	"france":              {},
	"francecentral":       {"1", "2", "3"},
	"francesouth":         {},
	"germany":             {},
	"germanynorth":        {},
	"germanywestcentral":  {"1", "2", "3"},
	"global":              {},
	"india":               {},
	"israel":              {},
	"israelcentral":       {"1", "2", "3"},
	"italy":               {},
	"italynorth":          {"1", "2", "3"},
	"japan":               {},
	"japaneast":           {"1", "2", "3"},
	"japanwest":           {},
	"jioindiacentral":     {},
	"jioindiawest":        {},
	"korea":               {},
	"koreacentral":        {"1", "2", "3"},
	"koreasouth":          {},
	"mexicocentral":       {"1", "2", "3"},
	"newzealand":          {},
	"newzealandnorth":     {"1", "2", "3"},
	"northcentralus":      {},
	"northcentralusstage": {},
	"northeurope":         {"1", "2", "3"},
	"norway":              {},
	"norwayeast":          {"1", "2", "3"},
	"norwaywest":          {},
	"poland":              {},
	"polandcentral":       {"1", "2", "3"},
	"qatar":               {},
	"qatarcentral":        {"1", "2", "3"},
	"singapore":           {},
	"southafrica":         {},
	"southafricanorth":    {"1", "2", "3"},
	"southafricawest":     {},
	"southcentralus":      {"1", "2", "3"},
	"southcentralusstage": {},
	"southcentralusstg":   {},
	"southeastasia":       {"1", "2", "3"},
	"southeastasiastage":  {},
	"southindia":          {},
	"spaincentral":        {"1", "2", "3"},
	"sweden":              {},
	"swedencentral":       {"1", "2", "3"},
	"switzerland":         {},
	"switzerlandnorth":    {"1", "2", "3"},
	"switzerlandwest":     {},
	"uae":                 {},
	"uaecentral":          {},
	"uaenorth":            {"1", "2", "3"},
	"uk":                  {},
	"uksouth":             {"1", "2", "3"},
	"ukwest":              {},
	"unitedstates":        {},
	"unitedstateseuap":    {},
	"westcentralus":       {},
	"westeurope":          {"1", "2", "3"},
	"westindia":           {},
	"westus":              {},
	"westus2":             {"1", "2", "3"},
	"westus2stage":        {},
	"westus3":             {"1", "2", "3"},
	"westusstage":         {},
}

// normalizeLocation lowercases and strips spaces so ARM location forms like
// "UK South" and "uksouth" both resolve. The bicep map keys are already in the
// space-free lowercase form.
func normalizeLocation(location string) string {
	return strings.ToLower(strings.ReplaceAll(location, " ", ""))
}

// AvailabilityZones returns the availability zones for the given Azure region
// and whether the region is known. A known region with no zones returns an
// empty slice and true (mirroring bicep regions whose availabilityZones is []).
func AvailabilityZones(location string) ([]string, bool) {
	zones, ok := locationAvailabilityZones[normalizeLocation(location)]
	return zones, ok
}
