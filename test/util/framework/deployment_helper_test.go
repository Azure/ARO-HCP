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

package framework

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
)

// deploymentFailedError mirrors the error PollUntilDone returns for a failed
// ARM deployment: a 200 operation-status response whose body nests the
// resource-level failure details.
func deploymentFailedError(t *testing.T, detailCode string) error {
	t.Helper()
	type errorDetail struct {
		Code    string        `json:"code"`
		Message string        `json:"message,omitempty"`
		Details []errorDetail `json:"details,omitempty"`
	}
	type operationStatus struct {
		Status string      `json:"status"`
		Error  errorDetail `json:"error"`
	}
	// ARM serializes the resource-level failure as a JSON string inside the
	// outer detail's message, so marshal it separately before embedding.
	inner, err := json.Marshal(operationStatus{
		Status: "failed",
		Error: errorDetail{
			Code:    "ResourceDeploymentFailure",
			Details: []errorDetail{{Code: detailCode, Message: "The supporting resource did not provision."}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(operationStatus{
		Status: "Failed",
		Error: errorDetail{
			Code:    "DeploymentFailed",
			Message: "At least one resource deployment operation failed.",
			Details: []errorDetail{{Code: "Conflict", Message: string(inner)}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	reqURL, err := url.Parse("https://example.invalid/subscriptions/sub/resourcegroups/rg/providers/Microsoft.Resources/deployments/d/operationStatuses/1")
	if err != nil {
		t.Fatal(err)
	}
	return runtime.NewResponseErrorWithErrorCode(&http.Response{
		StatusCode: http.StatusOK,
		Status:     "200 OK",
		Header:     http.Header{},
		Body:       io.NopCloser(bytes.NewReader(body)),
		Request:    &http.Request{Method: http.MethodGet, URL: reqURL},
	}, "DeploymentFailed")
}

func TestIsDeploymentScriptACIProvisioningTimeout(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil", err: nil, want: false},
		{name: "non-Azure error mentioning the code", err: errors.New("DeploymentScriptACIProvisioningTimeout"), want: false},
		{name: "nested ACI provisioning timeout", err: deploymentFailedError(t, "DeploymentScriptACIProvisioningTimeout"), want: true},
		{name: "wrapped nested ACI provisioning timeout", err: fmt.Errorf("failed waiting for deployment: %w", deploymentFailedError(t, "DeploymentScriptACIProvisioningTimeout")), want: true},
		{name: "other deployment script failure", err: deploymentFailedError(t, "DeploymentScriptError"), want: false},
		{name: "unrelated Azure error", err: &azcore.ResponseError{StatusCode: http.StatusConflict, ErrorCode: "DeploymentActive"}, want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsDeploymentScriptACIProvisioningTimeout(tc.err); got != tc.want {
				t.Errorf("IsDeploymentScriptACIProvisioningTimeout() = %v, want %v", got, tc.want)
			}
		})
	}
}
