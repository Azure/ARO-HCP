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

// Package poolnaming implements the AKS node-pool naming algorithm used in
// pool.bicep. It derives expected pool names from base name, count, zone
// redundancy mode, and availability zones so pipeline scripts can compute
// the desired pool set without parsing the bicep source.
package poolnaming

import "fmt"

// maxPoolNameLen mirrors the take(pool.name, 12) truncation in pool.bicep.
// AKS agent-pool names must be ≤12 lowercase alphanumeric chars.
const maxPoolNameLen = 12

// Expected computes the set of pool names that the current rendered
// configuration expects. The algorithm mirrors pool.bicep exactly, including
// the take(pool.name, 12) truncation applied before the ARM resource is created:
//
// useZonalPools = mode == "Enabled" || (mode == "Auto" && len(zones) > 0)
// zonal pools:     {baseName}{zone}     for the first min(len(zones), count) zones
// non-zonal pools: {baseName}nz{1..N}   for the remainder
// all names are truncated to 12 characters (pool.bicep: take(pool.name, 12))
func Expected(baseName string, count int, mode string, zones []string) []string {
	useZonal := mode == "Enabled" || (mode == "Auto" && len(zones) > 0)

	var names []string
	if useZonal {
		zonal := minInt(len(zones), count)
		for i := 0; i < zonal; i++ {
			names = append(names, truncate(baseName+zones[i]))
		}
		for i := 1; i <= count-zonal; i++ {
			names = append(names, truncate(fmt.Sprintf("%snz%d", baseName, i)))
		}
	} else {
		for i := 1; i <= count; i++ {
			names = append(names, truncate(fmt.Sprintf("%snz%d", baseName, i)))
		}
	}
	return names
}

// truncate applies the same take(name, 12) truncation as pool.bicep.
func truncate(name string) string {
	if len(name) > maxPoolNameLen {
		return name[:maxPoolNameLen]
	}
	return name
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
