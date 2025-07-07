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
	"fmt"
	"net/http"
	"time"

	validator "github.com/go-playground/validator/v10"

	"github.com/Azure/ARO-HCP/internal/api/arm"
)

// HCPOpenShiftVersion represents a location-based available HCP OpenShift version resource.
type HCPOpenShiftVersion struct {
	arm.ProxyResource
	Properties HCPOpenShiftVersionProperties `json:"properties,omitempty" validate:"required"`
}

// HCPOpenShiftVersionProperties contains details of an available HCP OpenShift version.
type HCPOpenShiftVersionProperties struct {
	ChannelGroup       string    `json:"channelGroup" validate:"required"`
	Enabled            bool      `json:"enabled" validate:"required"`
	EndOfLifeTimestamp time.Time `json:"endOfLifeTimestamp" validate:"required"`
}

// NewDefaultHCPOpenShiftVersion creates a default HCPOpenShiftVersion with sensible defaults.
func NewDefaultHCPOpenShiftVersion() *HCPOpenShiftVersion {
	return &HCPOpenShiftVersion{
		Properties: HCPOpenShiftVersionProperties{
			ChannelGroup:       "stable",
			Enabled:            true,
			EndOfLifeTimestamp: time.Now().AddDate(1, 0, 0), // Default EOL 1 year from now
		},
	}
}

// Validate performs validation on the HCPOpenShiftVersion.
func (version *HCPOpenShiftVersion) Validate(validate *validator.Validate, request *http.Request) []arm.CloudErrorBody {
	errorDetails := ValidateRequest(validate, request, version)

	// Only perform complex validation if single-field validation passed.
	if len(errorDetails) == 0 {
		errorDetails = append(errorDetails, version.validateChannelGroup()...)
		errorDetails = append(errorDetails, version.validateEndOfLife()...)
	}

	return errorDetails
}

// validateChannelGroup validates the ChannelGroup value.
func (version *HCPOpenShiftVersion) validateChannelGroup() []arm.CloudErrorBody {
	var errorDetails []arm.CloudErrorBody

	// For now, only "stable" is allowed. Extend this when AFEC or other mechanisms are introduced.
	if version.Properties.ChannelGroup != "stable" {
		errorDetails = append(errorDetails, arm.CloudErrorBody{
			Code:    arm.CloudErrorCodeInvalidRequestContent,
			Message: "ChannelGroup must be 'stable'",
			Target:  "properties.channelGroup",
		})
	}

	return errorDetails
}

// validateEndOfLife validates the EndOfLifeTimestamp.
func (version *HCPOpenShiftVersion) validateEndOfLife() []arm.CloudErrorBody {
	var errorDetails []arm.CloudErrorBody

	// Ensure EOL is in the future.
	if !version.Properties.EndOfLifeTimestamp.After(time.Now()) {
		errorDetails = append(errorDetails, arm.CloudErrorBody{
			Code:    arm.CloudErrorCodeInvalidRequestContent,
			Message: fmt.Sprintf("EndOfLifeTimestamp '%s' must be a future date", version.Properties.EndOfLifeTimestamp.Format(time.RFC3339)),
			Target:  "properties.endOfLifeTimestamp",
		})
	}

	return errorDetails
}
