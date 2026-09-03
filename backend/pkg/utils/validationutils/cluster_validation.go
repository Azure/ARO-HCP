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
// for validations whose input can change on day-2 updates. The controller
// stores InputKey(cluster) in ServiceProviderClusterStatus.ValidationInputKeys
// (not in condition.Message) after a successful validation, and re-runs the
// validation when the stored key differs from the current InputKey.
//
// This prevents stale validation results: without it, a day-2 input change
// (e.g., changing the ACR pull MI) would be gated by the 12-hour Passed retry
// cooldown before the new MI is validated.
//
// Implementations should pass a descriptive human-readable userMessage to
// PassedValidation; the input key is stored separately by the controller.
type InputKeyedClusterValidation interface {
	ClusterValidation
	// InputKey returns a string representing the validation-relevant input.
	// When this value changes between reconciles, the existing True condition
	// is considered stale and the validation re-runs.
	InputKey(cluster *coreapi.HCPOpenShiftCluster) string
}
