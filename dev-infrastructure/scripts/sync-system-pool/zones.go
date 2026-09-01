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

package main

// locationAvailabilityZonesTable mirrors dev-infrastructure/modules/common.bicep's
// _locationAvailabilityZones map. mgmt-cluster.bicep defaults the system pool's
// zones param to this per-location list whenever config.yaml's
// mgmt.aks.systemAgentPool.zones is an empty CSV, so desiredAvailabilityZones
// must reproduce the same defaulting to avoid a permanent (and destructive)
// drift signal in every zonal region where zones is left empty.
//
// https://learn.microsoft.com/en-us/azure/reliability/availability-zones-region-support
//
// Keep this in sync with common.bicep's _locationAvailabilityZones if that
// table changes.
var locationAvailabilityZonesTable = map[string][]string{
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

// locationAvailabilityZones returns the availability zones for the given
// ARM location, mirroring common.bicep's getLocationAvailabilityZones.
// Unlike the bicep function (which indexes _locationAvailabilityZones
// directly and fails the deployment on an unrecognized region), this is a
// plain map lookup that returns a nil slice for an unknown location.
// Callers MUST validate isKnownLocation(location) first (see main.go's
// runWith, which does this right after reading the live cluster's
// location) so an unrecognized region fails closed with a clear error
// instead of silently being treated as non-zonal.
func locationAvailabilityZones(location string) []string {
	return locationAvailabilityZonesTable[location]
}

// isKnownLocation reports whether location is present in
// locationAvailabilityZonesTable, i.e. whether this script's mirror of
// common.bicep's _locationAvailabilityZones table has an entry for it.
func isKnownLocation(location string) bool {
	_, ok := locationAvailabilityZonesTable[location]
	return ok
}
