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

const OpenShiftVersionCatalogName = "default"

// OpenShiftVersionCatalog is the internal regional version snapshot in Resources.
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type OpenShiftVersionCatalog struct {
	// PartitionKey is the lowercased provider namespace. The Resources account is regional.
	CosmosMetadata `json:"cosmosMetadata"`

	// Entries is the regional snapshot of known minor/channel choices and their
	// readiness, projected from Fleet rollouts for frontend version discovery.
	// Written by: OpenShiftVersionCatalog
	Entries []OpenShiftVersionCatalogEntry `json:"entries"`
}

type OpenShiftVersionCatalogEntry struct {
	// Version identifies the minor version and channel group advertised by this entry.
	// Written by: OpenShiftVersionCatalog
	Version VersionProfile `json:"version"`
	// Available is true once the backend has selected an exact version for this
	// rollout. False identifies a known choice whose selection is still pending,
	// allowing the frontend to distinguish unavailable choices from unknown ones.
	// Written by: OpenShiftVersionCatalog
	Available bool `json:"available"`
}
