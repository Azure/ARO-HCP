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
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/go-logr/logr"
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
		wantLogAttrs    []slog.Attr
		wantSpanAttrs   map[string]string
		requestURL      string
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
			requestURL:    "/subscriptions/" + api.TestSubscriptionID,
			setReqPathValue: func(req *http.Request) {
				req.SetPathValue(PathSegmentSubscriptionID, api.TestSubscriptionID)
			},
		},
		{
			name:          "handles the common attributes and the attributes for the resourcegroupname path",
			wantLogAttrs:  []slog.Attr{slog.String("resource_group", strings.ToLower(api.TestResourceGroupName))},
			wantSpanAttrs: map[string]string{"aro.resource_group.name": api.TestResourceGroupName},
			requestURL:    "/subscriptions/" + api.TestSubscriptionID + "/resourceGroups/" + api.TestResourceGroupName,
			setReqPathValue: func(req *http.Request) {
				req.SetPathValue(PathSegmentResourceGroupName, api.TestResourceGroupName)
			},
		},
		{
			name: "handles the common attributes and the attributes for the resourcename path, and produces the correct resourceID attribute",
			wantLogAttrs: []slog.Attr{
				slog.String("subscription_id", api.TestSubscriptionID),
				slog.String("resource_group", strings.ToLower(api.TestResourceGroupName)),
				slog.String("resource_name", strings.ToLower(api.TestClusterName)),
				slog.String("resource_id", strings.ToLower(api.TestClusterResourceID)),
			},
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
			var (
				writer = httptest.NewRecorder()
				buf    bytes.Buffer
				logger = logr.FromSlogHandler(slog.NewTextHandler(&buf, nil))
			)

			ctx := utils.ContextWithLogger(context.Background(), logger)
			ctx, sr := initSpanRecorder(ctx)
			req, err := http.NewRequestWithContext(ctx, "GET", "http://example.com"+tt.requestURL, nil)
			assert.NoError(t, err)
			if tt.setReqPathValue != nil {
				tt.setReqPathValue(req)
			}

			next := func(w http.ResponseWriter, r *http.Request) {
				logger := utils.LoggerFromContext(r.Context())
				// Emit a log message to check that it includes the expected attributes.
				logger.Info("test")
				w.WriteHeader(http.StatusOK)
			}

			MiddlewareLogging(writer, req, func(w http.ResponseWriter, r *http.Request) {
				MiddlewareLoggingPostMux(w, r, next)
			})

			// Check that the contextual logger has the expected attributes.
			lines := strings.Split(strings.TrimSuffix(buf.String(), "\n"), "\n")
			require.Equal(t, 3, len(lines))

			line := string(lines[1])
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

func TestMiddlewareLoggingLogsMethodAndPathKeys(t *testing.T) {
	var (
		writer = httptest.NewRecorder()
		buf    bytes.Buffer
		logger = logr.FromSlogHandler(slog.NewJSONHandler(&buf, nil))
	)

	ctx := utils.ContextWithLogger(context.Background(), logger)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://example.com/My/Path", nil)
	require.NoError(t, err)

	MiddlewareLogging(writer, req, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	lines := strings.Split(strings.TrimSuffix(buf.String(), "\n"), "\n")
	require.GreaterOrEqual(t, len(lines), 1)

	requestLog := map[string]any{}
	require.NoError(t, json.Unmarshal([]byte(lines[0]), &requestLog))

	assert.Equal(t, "post", requestLog["method"])
	assert.Equal(t, "/my/path", requestLog["path"])
	_, hasRequestMethod := requestLog["request_method"]
	_, hasRequestPath := requestLog["request_path"]
	assert.False(t, hasRequestMethod)
	assert.False(t, hasRequestPath)
}

func TestFrontendLogsKustoMappingContract(t *testing.T) {
	kqlFilePath := filepath.Join(
		repoRootFromCurrentTest(t),
		"dev-infrastructure",
		"modules",
		"logs",
		"kusto",
		"tables",
		"frontendLogs.kql",
	)

	kqlContent, err := os.ReadFile(kqlFilePath)
	require.NoError(t, err)

	mappings := parseFrontendKustoMappings(t, string(kqlContent))
	columnToPath := map[string]string{}
	for _, mapping := range mappings {
		columnToPath[mapping.Column] = mapping.Properties.Path
	}

	assert.Equal(t, "$.log.method", columnToPath["request_method"])
	assert.Equal(t, "$.log.path", columnToPath["request_path"])

	assert.Equal(t, "$.log.request_id", columnToPath["request_id"])
	assert.Equal(t, "$.log.client_request_id", columnToPath["client_request_id"])
	assert.Equal(t, "$.log.correlation_request_id", columnToPath["correlation_request_id"])
	assert.Equal(t, "$.log.resource_id", columnToPath["resource_id"])
	assert.Equal(t, "$.log.resource_name", columnToPath["resource_name"])
}

type frontendKustoMapping struct {
	Column     string `json:"column"`
	Properties struct {
		Path string `json:"path"`
	} `json:"Properties"`
}

func parseFrontendKustoMappings(t *testing.T, kqlContent string) []frontendKustoMapping {
	t.Helper()

	start := strings.Index(kqlContent, "[")
	end := strings.LastIndex(kqlContent, "]")
	require.NotEqual(t, -1, start)
	require.NotEqual(t, -1, end)
	require.Greater(t, end, start)

	var mappings []frontendKustoMapping
	require.NoError(t, json.Unmarshal([]byte(kqlContent[start:end+1]), &mappings))
	return mappings
}

func repoRootFromCurrentTest(t *testing.T) string {
	t.Helper()

	_, currentTestFile, _, ok := runtime.Caller(0)
	require.True(t, ok)

	return filepath.Clean(filepath.Join(filepath.Dir(currentTestFile), "..", "..", ".."))
}
