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

package api

// ExperimentalFeatures captures experimental feature state evaluated from
// AFEC and per-resource tags. This is stored in Cosmos as part of the
// cluster's desired state and read during internal spec to CS transformation.
type ExperimentalFeatures struct {
	// SingleReplica, when true, causes CS to configure the cluster with
	// AvailabilityPolicy set to SingleReplica for control plane components.
	SingleReplica bool `json:"singleReplica,omitempty"`

	// SizeOverride, when true, causes CS to set the ClusterSizeOverride
	// annotation for reduced resource requests on hosted control plane components.
	SizeOverride bool `json:"sizeOverride,omitempty"`
}
