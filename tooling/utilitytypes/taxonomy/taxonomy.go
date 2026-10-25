// Copyright 2025 Microsoft Corporation
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

// Package taxonomy defines the CI failure classification vocabulary — L1
// categories and L2 subcategories — produced by the analysis agent. It is a
// dependency-light leaf so downstream consumers (e.g. the release dashboard)
// can share the exact contract without importing the agent runtime.
package taxonomy

// L1 failure taxonomy categories.
const (
	L1AzureProblems      = "Azure Problems"
	L1DeploymentFailures = "Deployment Failures"
	L1ProductFailures    = "Product Failures"
	L1TestReliability    = "Test Reliability"
)

// ValidL1Categories is the set of allowed L1 taxonomy values.
var ValidL1Categories = map[string]bool{
	L1AzureProblems:      true,
	L1DeploymentFailures: true,
	L1ProductFailures:    true,
	L1TestReliability:    true,
}

// L2 subcategories, valid only when L1 is "Product Failures".
const (
	L2Frontend       = "Frontend"
	L2ClusterService = "Cluster Service"
	L2Backend        = "Backend"
	L2Maestro        = "Maestro"
	L2HyperShift     = "HyperShift"
	L2RHUpstream     = "RH Upstream"
)

// ValidL2Subcategories is the set of allowed L2 taxonomy values.
var ValidL2Subcategories = map[string]bool{
	L2Frontend:       true,
	L2ClusterService: true,
	L2Backend:        true,
	L2Maestro:        true,
	L2HyperShift:     true,
	L2RHUpstream:     true,
}
