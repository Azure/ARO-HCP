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

package database

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"

	"github.com/Azure/ARO-HCP/internal/utils"
)

func TestIsResponseError(t *testing.T) {
	t.Parallel()

	responseErrorCheckers := []struct {
		statusCode int
		check      func(error) bool
	}{
		{http.StatusBadRequest, IsBadRequestError},
		{http.StatusNotFound, IsNotFoundError},
		{http.StatusConflict, IsConflictError},
		{http.StatusPreconditionFailed, IsPreconditionFailedError},
	}

	tests := []struct {
		name           string
		err            error
		wantMessage    string
		wantHTTPStatus int // 0 means the error should not match any checker
	}{
		{
			name: "transaction step error",
			err: NewTransactionStepError(1, 2, http.StatusPreconditionFailed, CosmosDBTransactionStepDetails{
				ActionType: "Replace",
				CosmosID:   "cosmos-uid-1",
				ResourceID: "/subscriptions/sub1/resourceGroups/rg1/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster1",
				GoType:     "HCPOpenShiftCluster",
				Etag:       azcore.ETag("etag-1"),
			}),
			wantMessage:    `transaction step 1 of 2 (Replace HCPOpenShiftCluster on /subscriptions/sub1/resourceGroups/rg1/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster1, etag "etag-1") failed with 412 Precondition Failed`,
			wantHTTPStatus: http.StatusPreconditionFailed,
		},
		{
			name:           "transaction step error through TrackError",
			err:            utils.TrackError(NewTransactionStepError(1, 1, http.StatusPreconditionFailed, CosmosDBTransactionStepDetails{})),
			wantHTTPStatus: http.StatusPreconditionFailed,
		},
		{
			name:           "azcore ResponseError conflict",
			err:            &azcore.ResponseError{StatusCode: http.StatusConflict, ErrorCode: "409 Conflict"},
			wantHTTPStatus: http.StatusConflict,
		},
		{
			name: "wrapped azcore ResponseError precondition failed",
			err: fmt.Errorf("replace failed: %w", &azcore.ResponseError{
				StatusCode: http.StatusPreconditionFailed,
				ErrorCode:  "412 Precondition Failed",
			}),
			wantHTTPStatus: http.StatusPreconditionFailed,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if tt.wantMessage != "" {
				require.Equal(t, tt.wantMessage, tt.err.Error())
			}
			for _, checker := range responseErrorCheckers {
				require.Equal(t, tt.wantHTTPStatus == checker.statusCode, checker.check(tt.err),
					"unexpected result from checker for status %d", checker.statusCode)
			}
		})
	}
}
