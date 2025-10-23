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

package api

import (
	"time"

	"github.com/Azure/ARO-HCP/internal/api/arm"
)

// HCPOpenShiftVersion represents a location-based available HCP OpenShift version resource.
type HCPOpenShiftVersion struct {
	arm.ProxyResource
	Properties HCPOpenShiftVersionProperties `json:"properties,omitempty"`
}

// HCPOpenShiftVersionProperties contains details of an available HCP OpenShift version.
type HCPOpenShiftVersionProperties struct {
	ChannelGroup       string    `json:"channelGroup"`
	Enabled            bool      `json:"enabled"`
	EndOfLifeTimestamp time.Time `json:"endOfLifeTimestamp"`
}
