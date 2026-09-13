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

package fleetapi

import (
	"github.com/blang/semver/v4"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
)

// ControlPlaneVersionRollout coordinates a fleet-wide control-plane version
// rollout for a single y-stream channel (e.g. "stable-4.21"). It is a
// region-wide (fleet-scoped) resource: the top-level resource name is the
// y-stream channel it is associated with, and all rollouts live in a single
// shared partition keyed by the provider namespace.
//
// The Rollout Seeding controller creates one document per y-stream channel; the
// Best Version Selection controller computes Spec.BestExactVersion from the
// upgrade graph; the Status Collector aggregates per-cluster progress; and the
// Normal/Forced Cluster Desired Version Assignment controllers drive individual
// ServiceProviderCluster desired versions toward Spec.BestExactVersion.
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type ControlPlaneVersionRollout struct {
	// The top-level resource name is the y-stream channel, e.g. "stable-4.21".
	// All ControlPlaneVersionRollout documents share a single partition keyed by
	// the provider namespace (see ProviderNamespacePartitionKeyDeriver).
	coreapi.CosmosMetadata `json:"cosmosMetadata"`

	Spec   ControlPlaneVersionRolloutSpec   `json:"spec"`
	Status ControlPlaneVersionRolloutStatus `json:"status"`
}

// ControlPlaneVersionRolloutSpec contains the desired state of the rollout.
type ControlPlaneVersionRolloutSpec struct {
	// BestExactVersion is the most recent z-stream without a platform+controlplane
	// risk in this y-stream channel, offset by zStreamOffset from the latest
	// available z-stream. It is the exact version the rollout drives clusters
	// toward. Nil means no version has been selected yet.
	BestExactVersion *semver.Version `json:"bestExactVersion,omitempty"`
}

// ControlPlaneVersionRolloutStatus contains the observed state of the rollout.
//
// The count maps are keyed by exact-version string (semver.Version.String()).
type ControlPlaneVersionRolloutStatus struct {
	// Conditions tracks the rollout's progression. Known condition types:
	// "Progressing" (rollout is advancing), "Degraded" (failure budget exceeded).
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// ClusterCountByDesiredExactVersion counts clusters whose
	// Spec.ControlPlaneVersion.DesiredVersion equals the keyed exact version.
	ClusterCountByDesiredExactVersion map[string]int64 `json:"clusterCountByDesiredExactVersion,omitempty"`

	// MismatchedClusterCountByDesiredExactVersion counts clusters that desire the
	// keyed exact version but do not yet have it as the earliest version in their
	// activeVersions (i.e. the upgrade is in flight).
	MismatchedClusterCountByDesiredExactVersion map[string]int64 `json:"mismatchedClusterCountByDesiredExactVersion,omitempty"`

	// FailedClusterCountByDesiredExactVersion counts clusters that have been
	// mismatched for the keyed exact version longer than the maxUpgradeDuration
	// for that minor version.
	FailedClusterCountByDesiredExactVersion map[string]int64 `json:"failedClusterCountByDesiredExactVersion,omitempty"`

	// ClusterCountByAchievedExactVersion counts clusters that have the keyed exact
	// version as the earliest version in their activeVersions.
	ClusterCountByAchievedExactVersion map[string]int64 `json:"clusterCountByAchievedExactVersion,omitempty"`

	// SuccessfulClusterCountByAchievedExactVersion counts clusters that have
	// achieved the keyed exact version and have been at that level longer than
	// minVersionReadyDuration.
	SuccessfulClusterCountByAchievedExactVersion map[string]int64 `json:"successfulClusterCountByAchievedExactVersion,omitempty"`
}
