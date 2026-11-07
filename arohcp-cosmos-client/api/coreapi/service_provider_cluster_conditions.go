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

package coreapi

// Status-condition vocabulary for ServiceProviderCluster placement. These live on
// ServiceProviderClusterStatus.Placement.Conditions, not on the top-level
// Status.Conditions.

const (
	// CapacityAvailableConditionType describes capacity for this HCP's placement
	// attempt, not ongoing fleet capacity. False means evaluation established that
	// no usable capacity exists; Unknown means required observations or configuration
	// were unavailable.
	CapacityAvailableConditionType = "CapacityAvailable"
)

// Known Reason values for the CapacityAvailable condition.
const (
	// CapacityReasonAvailable means a suitable management cluster was found.
	CapacityReasonAvailable = "Available"
	// CapacityReasonInsufficientCapacity means eligible management clusters were
	// evaluated, but none had enough capacity.
	CapacityReasonInsufficientCapacity = "InsufficientCapacity"
	// CapacityReasonNoEligibleManagementCluster means all candidates were evaluated
	// and none were eligible (or there were no candidates).
	CapacityReasonNoEligibleManagementCluster = "NoEligibleManagementCluster"
	// CapacityReasonEvaluationIncomplete means observations or configuration are
	// missing, so capacity availability is Unknown.
	CapacityReasonEvaluationIncomplete = "EvaluationIncomplete"
)
