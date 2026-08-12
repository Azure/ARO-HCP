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

package validationutils

import (
	"context"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
)

// ClusterValidation represents a validation that can be performed on a cluster.
type ClusterValidation interface {
	// Name returns the name of the validation.
	Name() string
	// Validate validates the Cluster and returns a ValidationResult describing the outcome.
	Validate(ctx context.Context, clusterSubscription *coreapi.Subscription, cluster *coreapi.HCPOpenShiftCluster) ValidationResult
}

// InputKeyedClusterValidation is an optional extension of ClusterValidation
// for validations whose input can change on day-2 updates. Implementations
// must call PassedValidation(reason, InputKey(cluster), internalMessage) on
// success, storing the key directly in the condition's Message field. The
// controller re-runs when InputKey(cluster) != condition.Message.
//
// This stores a cache key (e.g., a resource ID) in Message instead of a
// human-readable string. This is acceptable because:
//  1. Status=True conditions are filtered by AggregateRequirementsValidCondition
//     and never shown to end users.
//  2. The optimization prevents stale validation results: without it, a day-2
//     input change (e.g., changing the ACR pull MI) would be gated by the 12-hour
//     Passed retry cooldown before the new MI is validated.
type InputKeyedClusterValidation interface {
	ClusterValidation
	// InputKey returns a string representing the validation-relevant input.
	// When this value changes between reconciles, the existing True condition
	// is considered stale and the validation re-runs.
	InputKey(cluster *coreapi.HCPOpenShiftCluster) string
}
