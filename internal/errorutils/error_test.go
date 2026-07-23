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

package errorutils

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-logr/logr/testr"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"

	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func TestWriteError_TransactionPreconditionFailedBecomes429(t *testing.T) {
	innerErr := database.NewTransactionStepError(6, 6, http.StatusPreconditionFailed, database.CosmosDBTransactionStepDetails{
		ActionType: "Replace",
		CosmosID:   "cosmos-uid-abc123",
		ResourceID: "/subscriptions/sub1/resourceGroups/rg1/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster1",
		GoType:     "HCPOpenShiftCluster",
		Etag:       azcore.ETag("etag-old-value"),
	})
	wrappedErr := utils.TrackError(innerErr)

	t.Logf("error message: %s", wrappedErr)
	t.Logf("IsPreconditionFailedError: %v", database.IsPreconditionFailedError(wrappedErr))

	var stepError *database.TransactionStepError
	if !errors.As(wrappedErr, &stepError) {
		t.Fatal("expected errors.As to find TransactionStepError through LineTrackingError wrapper")
	}
	t.Logf("cast succeeded: step=%d, totalSteps=%d, httpStatusCode=%d, action=%s, resourceID=%s",
		stepError.Step, stepError.TotalSteps, stepError.HTTPStatusCode,
		stepError.StepDetails.ActionType, stepError.StepDetails.ResourceID)

	ctx := utils.ContextWithLogger(t.Context(), testr.New(t))
	recorder := httptest.NewRecorder()

	handler := ReportError(func(w http.ResponseWriter, r *http.Request) error {
		return wrappedErr
	})

	req := httptest.NewRequest(http.MethodDelete, "/subscriptions/sub1/resourceGroups/rg1/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster1", nil)
	req = req.WithContext(ctx)
	handler.ServeHTTP(recorder, req)

	resp := recorder.Result()
	t.Logf("response status code: %d", resp.StatusCode)

	var cloudErr arm.CloudError
	if err := json.NewDecoder(resp.Body).Decode(&cloudErr); err != nil {
		t.Fatalf("failed to decode response body: %v", err)
	}
	t.Logf("response body code: %s, message: %s", cloudErr.Code, cloudErr.Message)

	if resp.StatusCode != http.StatusTooManyRequests {
		t.Errorf("expected status %d (429 Too Many Requests), got %d", http.StatusTooManyRequests, resp.StatusCode)
	}

	retryAfter := resp.Header.Get("Retry-After")
	if retryAfter != "1" {
		t.Errorf("expected Retry-After header to be %q, got %q", "1", retryAfter)
	}
}
