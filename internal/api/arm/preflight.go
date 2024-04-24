package arm

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

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
		cloudError := NewUnmarshalCloudError(err)
		// Status code for preflight content errors must always be OK.
		cloudError.StatusCode = http.StatusOK
		return nil, cloudError
	}
	return deploymentPreflight, nil
}

// DeploymentPreflightResource represents a desired resource in a deployment preflight request.
type DeploymentPreflightResource struct {
	Name       string `json:"name"       validate:"required"`
	Type       string `json:"type"       validate:"required"`
	Location   string `json:"location"   validate:"required"`
	APIVersion string `json:"apiVersion" validate:"required,api_version"`
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

	w.Header()["Content-Type"] = []string{"application/json"}
	w.WriteHeader(http.StatusOK)
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "    ")
	_ = encoder.Encode(response)
}
