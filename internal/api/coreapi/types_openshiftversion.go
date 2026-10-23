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

package coreapi

import (
	"time"
)

// MinimumBackendVersion bounds prospective seeding and obsolete rollout retirement.
// Raise it only after the retired minor's clusters have drained.
const MinimumBackendVersion = "4.20"

// MinimumPublicVersion bounds public version advertisement and cluster requests
// independently of backend seeding and retirement.
const MinimumPublicVersion = "4.20"

// OpenShiftVersion represents a location-based available HCP OpenShift version resource.
type OpenShiftVersion struct {
	ProxyResource
	Properties OpenShiftVersionProperties `json:"properties,omitempty"`
}

// OpenShiftVersionProperties contains details of an available HCP OpenShift version.
type OpenShiftVersionProperties struct {
	// ChannelGroup identifies the release channel of the advertised version.
	ChannelGroup string `json:"channelGroup"`
	// Enabled is retained for API compatibility and is always true for returned
	// versions. It should be retired in a future API version.
	Enabled bool `json:"enabled"`
	// EndOfLifeTimestamp is the known end of support for this version. Its zero
	// value represents an unknown date and is omitted from public responses.
	EndOfLifeTimestamp time.Time `json:"endOfLifeTimestamp"`
}
