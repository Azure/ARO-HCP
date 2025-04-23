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
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Azure/ARO-HCP/internal/api"
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
		wantLogAttrs    []slog.Attr
		wantSpanAttrs   map[string]string
		setReqPathValue ReqPathModifier
	}

	tests := []testCase{
		{
			name:            "handles the common logging attributes",
			wantLogAttrs:    []slog.Attr{},
			setReqPathValue: noModifyReqfunc,
		},
		{
			name:          "handles the common attributes and the attributes for the subscription_id segment path",
			wantLogAttrs:  []slog.Attr{slog.String("subscription_id", api.TestSubscriptionID)},
			wantSpanAttrs: map[string]string{"aro.subscription.id": api.TestSubscriptionID},
			setReqPathValue: func(req *http.Request) {
				req.SetPathValue(PathSegmentSubscriptionID, api.TestSubscriptionID)
			},
		},
		{
			name:          "handles the common attributes and the attributes for the resourcegroupname path",
			wantLogAttrs:  []slog.Attr{slog.String("resource_group", api.TestResourceGroupName)},
			wantSpanAttrs: map[string]string{"aro.resource_group.name": api.TestResourceGroupName},
			setReqPathValue: func(req *http.Request) {
				req.SetPathValue(PathSegmentResourceGroupName, api.TestResourceGroupName)
			},
		},
		{
			name: "handles the common attributes and the attributes for the resourcename path, and produces the correct resourceID attribute",
			wantLogAttrs: []slog.Attr{
				slog.String("subscription_id", api.TestSubscriptionID),
				slog.String("resource_group", api.TestResourceGroupName),
				slog.String("resource_name", api.TestClusterName),
				slog.String("resource_id", api.TestClusterResourceID),
			},
			wantSpanAttrs: map[string]string{
				"aro.subscription.id":     api.TestSubscriptionID,
				"aro.resource_group.name": api.TestResourceGroupName,
				"aro.resource.name":       api.TestClusterName,
			},
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
			var (
				writer = httptest.NewRecorder()
				buf    bytes.Buffer
				logger = slog.New(slog.NewTextHandler(&buf, nil))
			)

			ctx := ContextWithLogger(context.Background(), logger)
			ctx, sr := initSpanRecorder(ctx)
			req, err := http.NewRequestWithContext(ctx, "GET", "http://example.com", nil)
			assert.NoError(t, err)
			tt.setReqPathValue(req)

			next := func(w http.ResponseWriter, r *http.Request) {
				logger := LoggerFromContext(r.Context())
				// Emit a log message to check that it includes the expected attributes.
				logger.Info("test")
				w.WriteHeader(http.StatusOK)
			}

			MiddlewareLoggingPostMux(writer, req, next)

			// Check that the contextual logger has the expected attributes.
			lines := strings.Split(strings.TrimSuffix(buf.String(), "\n"), "\n")
			require.Equal(t, 1, len(lines))

			line := string(lines[0])
			for _, attr := range tt.wantLogAttrs {
				assert.Contains(t, line, attr.String())
			}

			// Check that the attributes have been added to the span too.
			ss := sr.collect()
			require.Len(t, ss, 1)
			span := ss[0]
			equalSpanAttributes(t, span, tt.wantSpanAttrs)
		})
	}
}
