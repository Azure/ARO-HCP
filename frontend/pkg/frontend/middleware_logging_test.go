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

package frontend

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// ReqPathModifier is an alias to a function that receives a request
// and it should modify its Path value as needed, for testing purposes.
type ReqPathModifier func(req *http.Request)

// noModifyReqfunc is a function that receives a request and does not modify it.
func noModifyReqfunc(req *http.Request) {
	// empty on purpose
}

func TestMiddlewareLoggingPostMux(t *testing.T) {
	type testCase struct {
		name            string
		wantSpanAttrs   map[string]string
		requestURL      string
		setReqPathValue ReqPathModifier
	}

	tests := []testCase{
		{
			name:            "handles the common logging attributes",
			setReqPathValue: noModifyReqfunc,
		},
		{
			name:          "handles the common attributes and the attributes for the subscription_id segment path",
			wantSpanAttrs: map[string]string{"aro.subscription.id": api.TestSubscriptionID},
			requestURL:    "/subscriptions/" + api.TestSubscriptionID,
			setReqPathValue: func(req *http.Request) {
				req.SetPathValue(PathSegmentSubscriptionID, api.TestSubscriptionID)
			},
		},
		{
			name:          "handles the common attributes and the attributes for the resourcegroupname path",
			wantSpanAttrs: map[string]string{"aro.resource_group.name": api.TestResourceGroupName},
			requestURL:    "/subscriptions/" + api.TestSubscriptionID + "/resourceGroups/" + api.TestResourceGroupName,
			setReqPathValue: func(req *http.Request) {
				req.SetPathValue(PathSegmentResourceGroupName, api.TestResourceGroupName)
			},
		},
		{
			name: "handles the common attributes and the attributes for the resourcename path, and produces the correct resourceID attribute",
			wantSpanAttrs: map[string]string{
				"aro.subscription.id":     api.TestSubscriptionID,
				"aro.resource_group.name": api.TestResourceGroupName,
				"aro.resource.name":       api.TestClusterName,
			},
			requestURL: "/subscriptions/" + api.TestSubscriptionID + "/resourceGroups/" + api.TestResourceGroupName + "providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + api.TestClusterName,
			setReqPathValue: func(req *http.Request) {
				// assuming the PathSegmentResourceName is present in the Path
				req.SetPathValue(PathSegmentResourceName, api.TestClusterName)

				// assuming the PathSegmentSubscriptionID is present in the Path
				req.SetPathValue(PathSegmentSubscriptionID, api.TestSubscriptionID)

				// assuming the PathSegmentResourceGroupName is present in the Path
				req.SetPathValue(PathSegmentResourceGroupName, api.TestResourceGroupName)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			writer := httptest.NewRecorder()
			logger := testr.New(t)

			ctx := utils.ContextWithLogger(context.Background(), logger)
			ctx, sr := initSpanRecorder(ctx)
			req, err := http.NewRequestWithContext(ctx, "GET", "http://example.com"+tt.requestURL, nil)
			assert.NoError(t, err)
			if tt.setReqPathValue != nil {
				tt.setReqPathValue(req)
			}

			next := func(w http.ResponseWriter, r *http.Request) {
				contextLogger := utils.LoggerFromContext(r.Context())
				// Emit a log message to verify the logger is available.
				contextLogger.Info("test")
				w.WriteHeader(http.StatusOK)
			}

			MiddlewareLogging(writer, req, func(w http.ResponseWriter, r *http.Request) {
				MiddlewareLoggingPostMux(w, r, next)
			})

			// Check that the attributes have been added to the span.
			ss := sr.collect()
			require.Len(t, ss, 1)
			span := ss[0]
			equalSpanAttributes(t, span, tt.wantSpanAttrs)
		})
	}
}
