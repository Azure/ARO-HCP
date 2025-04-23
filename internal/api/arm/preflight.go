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

package arm

import (
	"encoding/json"
	"net/http"
	"path"
)

// See https://learn.microsoft.com/en-us/rest/api/datareplication/deployment-preflight/deployment-preflight?view=rest-datareplication-2021-02-16-preview&tabs=Go

// DeploymentPreflight represents the body of a deployment preflight request.
// We use a RawMessage slice here because preflight validation is best effort.
// So if one resource cannot be unmarshaled we move on to the next instead of
// failing the whole operation.
type DeploymentPreflight struct {
	Resources []json.RawMessage `json:"resources"`
}

// UnmarshalDeploymentPreflight unmarshals JSON-encoded data and returns
// either a DeploymentPreflight instance or an appropriate CloudError with
// a 200 OK HTTP status code.
func UnmarshalDeploymentPreflight(data []byte) (*DeploymentPreflight, *CloudError) {
	deploymentPreflight := &DeploymentPreflight{}
	err := json.Unmarshal(data, deploymentPreflight)
	if err != nil {
		cloudError := NewInvalidRequestContentError(err)
		// Status code for preflight content errors must always be OK.
		cloudError.StatusCode = http.StatusOK
		return nil, cloudError
	}
	return deploymentPreflight, nil
}

// DeploymentPreflightResource represents a desired resource in a deployment preflight request.
type DeploymentPreflightResource struct {
	Name       string `json:"name"                 validate:"required"`
	Type       string `json:"type"                 validate:"required"`
	Location   string `json:"location"             validate:"required"`
	APIVersion string `json:"apiVersion,omitempty" validate:"required,api_version"`

	// Preserve other tracked resource fields as raw data.
	Properties json.RawMessage `json:"properties,omitempty"`
	SystemData json.RawMessage `json:"systemData,omitempty"`
	Tags       json.RawMessage `json:"tags,omitempty"`
}

// Convert discards the APIVersion, marshals itself back to raw JSON,
// and then unmarshals the raw JSON to the given value, which should
// be an extension of the TrackedResource type.
func (r *DeploymentPreflightResource) Convert(v any) error {
	var clone = *r

	// Omit APIVersion from the clone.
	clone.APIVersion = ""

	data, err := json.Marshal(&clone)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}

// ResourceID returns a resource ID string for the resource.
func (r *DeploymentPreflightResource) ResourceID(subscriptionID, resourceGroup string) string {
	return path.Join("/subscriptions", subscriptionID, "resourcegroups", resourceGroup, "providers", r.Type, r.Name)
}

// DeploymentPreflightStatus is used in a DeploymentPreflightResponse.
type DeploymentPreflightStatus string

const (
	DeploymentPreflightStatusSucceeded DeploymentPreflightStatus = "Succeeded"
	DeploymentPreflightStatusFailed    DeploymentPreflightStatus = "Failed"
)

// DeploymentPreflightResponse represents the JSON response structure
// for a deployment preflight request.
type DeploymentPreflightResponse struct {
	Status DeploymentPreflightStatus `json:"status"`
	Error  *CloudErrorBody           `json:"error,omitempty"`
}

// WriteDeploymentPreflightResponse writes an appropriately structured
// response body to a deployment preflight request using the given error
// slice. An empty error slice indicates successful validation.
func WriteDeploymentPreflightResponse(w http.ResponseWriter, preflightErrors []CloudErrorBody) {
	var response *DeploymentPreflightResponse

	switch len(preflightErrors) {
	case 0:
		response = &DeploymentPreflightResponse{
			Status: DeploymentPreflightStatusSucceeded,
		}
	case 1:
		response = &DeploymentPreflightResponse{
			Status: DeploymentPreflightStatusFailed,
			Error:  &preflightErrors[0],
		}
	default:
		response = &DeploymentPreflightResponse{
			Status: DeploymentPreflightStatusFailed,
			Error: &CloudErrorBody{
				Code:    CloudErrorCodeMultipleErrorsOccurred,
				Message: "Preflight validation failed on multiple resources",
				Details: preflightErrors,
			},
		}
	}

	_, _ = WriteJSONResponse(w, http.StatusOK, response)
}
