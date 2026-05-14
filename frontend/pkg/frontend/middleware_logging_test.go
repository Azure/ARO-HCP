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

	resourcesapi "github.com/Azure/ARO-HCP/internal/apis/resources"
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
			wantLogAttrs:  []slog.Attr{slog.String("subscription_id", resourcesapi.TestSubscriptionID)},
			wantSpanAttrs: map[string]string{"aro.subscription.id": resourcesapi.TestSubscriptionID},
			requestURL:    "/subscriptions/" + resourcesapi.TestSubscriptionID,
			setReqPathValue: func(req *http.Request) {
				req.SetPathValue(PathSegmentSubscriptionID, resourcesapi.TestSubscriptionID)
			},
		},
		{
			name:          "handles the common attributes and the attributes for the resourcegroupname path",
			wantLogAttrs:  []slog.Attr{slog.String("resource_group", strings.ToLower(resourcesapi.TestResourceGroupName))},
			wantSpanAttrs: map[string]string{"aro.resource_group.name": resourcesapi.TestResourceGroupName},
			requestURL:    "/subscriptions/" + resourcesapi.TestSubscriptionID + "/resourceGroups/" + resourcesapi.TestResourceGroupName,
			setReqPathValue: func(req *http.Request) {
				req.SetPathValue(PathSegmentResourceGroupName, resourcesapi.TestResourceGroupName)
			},
		},
		{
			name: "handles the common attributes and the attributes for the resourcename path, and produces the correct resourceID attribute",
			wantLogAttrs: []slog.Attr{
				slog.String("subscription_id", resourcesapi.TestSubscriptionID),
				slog.String("resource_group", strings.ToLower(resourcesapi.TestResourceGroupName)),
				slog.String("resource_name", strings.ToLower(resourcesapi.TestClusterName)),
				slog.String("resource_id", strings.ToLower(resourcesapi.TestClusterResourceID)),
			},
			wantSpanAttrs: map[string]string{
				"aro.subscription.id":     resourcesapi.TestSubscriptionID,
				"aro.resource_group.name": resourcesapi.TestResourceGroupName,
				"aro.resource.name":       resourcesapi.TestClusterName,
			},
			requestURL: "/subscriptions/" + resourcesapi.TestSubscriptionID + "/resourceGroups/" + resourcesapi.TestResourceGroupName + "providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + resourcesapi.TestClusterName,
			setReqPathValue: func(req *http.Request) {
				// assuming the PathSegmentResourceName is present in the Path
				req.SetPathValue(PathSegmentResourceName, resourcesapi.TestClusterName)

				// assuming the PathSegmentSubscriptionID is present in the Path
				req.SetPathValue(PathSegmentSubscriptionID, resourcesapi.TestSubscriptionID)

				// assuming the PathSegmentResourceGroupName is present in the Path
				req.SetPathValue(PathSegmentResourceGroupName, resourcesapi.TestResourceGroupName)
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
	assert.Equal(t, "$.log.kubernetes.container_hash", columnToPath["container_status_image_id"])
}

type frontendKustoMapping struct {
	Column     string `json:"column"`
	Properties struct {
		Path string `json:"path"`
	} `json:"Properties"`
}

func parseFrontendKustoMappings(t *testing.T, kqlContent string) []frontendKustoMapping {
	t.Helper()

	// Find the ingestion json mapping section to avoid matching brackets in the schema
	mappingIdx := strings.Index(kqlContent, "ingestion json mapping")
	require.NotEqual(t, -1, mappingIdx, "ingestion json mapping section not found")

	// Find the opening backtick block after the mapping declaration
	rest := kqlContent[mappingIdx:]
	openTick := strings.Index(rest, "```")
	require.NotEqual(t, -1, openTick, "opening backtick block not found")

	// Skip past the opening backticks to find the JSON content
	afterOpenTick := rest[openTick+3:]

	// Find the closing backtick block
	closeTick := strings.Index(afterOpenTick, "```")
	require.NotEqual(t, -1, closeTick, "closing backtick block not found")

	var mappings []frontendKustoMapping
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(afterOpenTick[:closeTick])), &mappings))
	return mappings
}

func repoRootFromCurrentTest(t *testing.T) string {
	t.Helper()

	_, currentTestFile, _, ok := runtime.Caller(0)
	require.True(t, ok)

	return filepath.Clean(filepath.Join(filepath.Dir(currentTestFile), "..", "..", ".."))
}
