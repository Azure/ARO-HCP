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
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/tracing"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type LoggingReadCloser struct {
	io.ReadCloser
	bytesRead int
}

func (rc *LoggingReadCloser) Read(b []byte) (int, error) {
	n, err := rc.ReadCloser.Read(b)
	rc.bytesRead += n
	return n, err
}

type LoggingResponseWriter struct {
	http.ResponseWriter
	statusCode   int
	bytesWritten int
}

func (w *LoggingResponseWriter) Write(b []byte) (int, error) {
	n, err := w.ResponseWriter.Write(b)
	w.bytesWritten += n
	return n, err
}

func (w *LoggingResponseWriter) WriteHeader(statusCode int) {
	w.ResponseWriter.WriteHeader(statusCode)
	w.statusCode = statusCode
}

// MiddlewareLogging logs the HTTP request and response.
func MiddlewareLogging(w http.ResponseWriter, r *http.Request, next http.HandlerFunc) {
	ctx := r.Context()
	logger := utils.LoggerFromContext(ctx)

	// Capture the request and response data for logging.
	r.Body = &LoggingReadCloser{ReadCloser: r.Body}
	w = &LoggingResponseWriter{ResponseWriter: w, statusCode: http.StatusOK}

	startTime := time.Now()

	logger = logger.WithValues(
		"request_method", r.Method,
		"request_path", r.URL.Path,
	)

	// make a best attempt at parsing the resourceID. This will often fail because we have non-resource requests.
	// we do this so that we can add subscription, resourceGroup, and hcpCluster to the logger context for future searching
	// if possible.
	// It's important to do before the second panic handler so that panics can be correlated easily.
	// TODO are the value we find case sensitive or case insensitive.  They used to be case sensitive, so I have left that
	if resourceID, err := azcorearm.ParseResourceID(r.URL.Path); err == nil {
		logger = logger.WithValues(
			"subscription_id", resourceID.SubscriptionID,
			"resource_group", resourceID.ResourceGroupName,
		)

		currID := resourceID
		for currID != nil {
			// TODO we have the option on recording each type.  I have no real preference
			if currID.ResourceType.String() == strings.ToLower(api.ClusterResourceType.String()) {
				logger = logger.WithValues(
					"hcp_cluster_name", currID.Name,
				)
				break
			}
			currID = currID.Parent
		}
	}

	// include the context values (logger.With) with every line so we can grep for them.
	ctx = utils.ContextWithLogger(ctx, logger)
	r = r.WithContext(ctx)

	logger.Info("read request",
		"request_proto", r.Proto,
		"request_query", r.URL.RawQuery,
		// TODO referrer is under the client's control.  Printing it out could be huge.
		"request_referer", r.Referer(),
		"request_remote_addr", r.RemoteAddr,
		// TODO user agent is under the client's control.  Printing it out could be huge.
		"request_user_agent", r.UserAgent(),
	)

	next(w, r)

	logger.Info("send response",
		"body_read_bytes", r.Body.(*LoggingReadCloser).bytesRead,
		"body_written_bytes", w.(*LoggingResponseWriter).bytesWritten,
		"response_status_code", w.(*LoggingResponseWriter).statusCode,
		"duration", time.Since(startTime).Seconds())
}

// MiddlewareLoggingPostMux extends the contextual logger with additional
// attributes after the request has been matched by the ServeMux.
func MiddlewareLoggingPostMux(w http.ResponseWriter, r *http.Request, next http.HandlerFunc) {
	ctx := r.Context()
	logger := utils.LoggerFromContext(ctx)

	attrs := &attributes{
		subscriptionID: r.PathValue(PathSegmentSubscriptionID),
		resourceGroup:  r.PathValue(PathSegmentResourceGroupName),
		resourceName:   r.PathValue(PathSegmentResourceName),
	}
	attrs.addToCurrentSpan(ctx)
	ctx = utils.ContextWithLogger(ctx, attrs.extendLogr(logger))
	r = r.WithContext(ctx)

	next(w, r)
}

type attributes struct {
	subscriptionID string
	resourceGroup  string
	resourceName   string
}

func (a *attributes) resourceID() string {
	if a.subscriptionID == "" || a.resourceGroup == "" || a.resourceName == "" {
		return ""
	}

	return fmt.Sprintf(
		"/subscriptions/%s/resourceGroups/%s/providers/%s/%s",
		a.subscriptionID,
		a.resourceGroup,
		api.ClusterResourceType,
		a.resourceName,
	)
}

// extendLogr returns a new logger with additional Logging attributes based
// on the wildcards from the matched pattern.
func (a *attributes) extendLogr(logger logr.Logger) logr.Logger {
	if a.resourceName != "" {
		logger = logger.WithValues("resource_name", a.resourceName)
	}

	if resourceID := a.resourceID(); resourceID != "" {
		logger = logger.WithValues("resource_id", resourceID)
	}

	return logger
}

func (a *attributes) addToCurrentSpan(ctx context.Context) {
	span := trace.SpanFromContext(ctx)

	var attrs []attribute.KeyValue
	if a.subscriptionID != "" {
		attrs = append(attrs, tracing.SubscriptionIDKey.String(a.subscriptionID))
	}

	if a.resourceGroup != "" {
		attrs = append(attrs, tracing.ResourceGroupNameKey.String(a.resourceGroup))
	}

	if a.resourceName != "" {
		attrs = append(attrs, tracing.ResourceNameKey.String(a.resourceName))
	}

	span.SetAttributes(attrs...)
}
