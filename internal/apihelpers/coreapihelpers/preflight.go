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

package coreapihelpers

import (
	"encoding/json"
	"net/http"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
)

// UnmarshalDeploymentPreflight unmarshals JSON-encoded data and returns
// either a DeploymentPreflight instance or an appropriate CloudError with
// a 200 OK HTTP status code.
func UnmarshalDeploymentPreflight(data []byte) (*coreapi.DeploymentPreflight, error) {
	deploymentPreflight := &coreapi.DeploymentPreflight{}
	err := json.Unmarshal(data, deploymentPreflight)
	if err != nil {
		cloudError := coreapi.NewInvalidRequestContentError(err)
		// Status code for preflight content errors must always be OK.
		cloudError.StatusCode = http.StatusOK
		return nil, cloudError
	}
	return deploymentPreflight, nil
}

// WriteDeploymentPreflightResponse writes an appropriately structured
// response body to a deployment preflight request using the given error
// slice. An empty error slice indicates successful validation.
func WriteDeploymentPreflightResponse(w http.ResponseWriter, preflightErrors []coreapi.CloudErrorBody) {
	var response *coreapi.DeploymentPreflightResponse

	if len(preflightErrors) == 0 {
		response = &coreapi.DeploymentPreflightResponse{
			Status: coreapi.DeploymentPreflightStatusSucceeded,
		}
	} else {
		response = &coreapi.DeploymentPreflightResponse{
			Status: coreapi.DeploymentPreflightStatusFailed,
			Error:  coreapi.NewCloudErrorBodyFromSlice(preflightErrors, "Preflight validation failed on multiple resources"),
		}
	}

	_, _ = WriteJSONResponse(w, http.StatusOK, response)
}
