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
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"k8s.io/apimachinery/pkg/util/sets"

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
	statusCode    int
	bytesWritten  int
	observedBytes *bytes.Buffer
	logger        logr.Logger
}

func (w *LoggingResponseWriter) Write(b []byte) (int, error) {
	n, err := w.ResponseWriter.Write(b)
	w.bytesWritten += n

	if w.observedBytes != nil {
		// best effort to capture the body for debugging. Very expensive memory-wise, but we're having trouble with an invisible problem at the moment
		if m, err := w.observedBytes.Write(b[:n]); err != nil || m != n {
			w.logger.Error(err, "failed to write to observed bytes buffer", "n", n, "m", m)
		}
	}

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
	logger = logger.WithValues(
		utils.LogValues{}.
			AddMethod(r.Method).
			AddPath(r.URL.Path)...,
	)

	startTime := time.Now()

	// Capture the request and response data for logging.
	r.Body = &LoggingReadCloser{ReadCloser: r.Body}
	w = &LoggingResponseWriter{
		ResponseWriter: w,
		statusCode:     http.StatusOK,
		observedBytes:  &bytes.Buffer{}, // set this to nil to stop the expensive collection
		logger:         logger,          // the responsewriter interface doesn't take a context, so we have to track the logger like this.
	}

	// make a best attempt at parsing the resourceID. This will often fail because we have non-resource requests.
	// we do this so that we can add subscription, resourceGroup, and hcpCluster to the logger context for future searching
	// if possible.
	// It's important to do before the second panic handler so that panics can be correlated easily.
	// TODO are the value we find case sensitive or case insensitive.  They used to be case sensitive, so I have left that
	if resourceID, err := azcorearm.ParseResourceID(r.URL.Path); err == nil {
		logger = logger.WithValues(utils.LogValues{}.AddLogValuesForResourceID(resourceID)...)
	}

	// include the context values (logger.With) with every line so we can grep for them.
	ctx = utils.ContextWithLogger(ctx, logger)
	r = r.WithContext(ctx)

	// list out headers for future debugging.  limit to 100 headers
	headers := sets.Set[string]{}
	for _, header := range sets.KeySet(r.Header).UnsortedList() {
		headers.Insert(strings.ToLower(header))
		if len(headers) >= 100 {
			break
		}
	}
	requestContextValues := []any{
		"request_proto", r.Proto,
		"request_query", r.URL.RawQuery,
		// TODO referrer is under the client's control.  Printing it out could be huge.
		"request_referer", r.Referer(),
		"request_remote_addr", r.RemoteAddr,
		// TODO user agent is under the client's control.  Printing it out could be huge.
		"request_user_agent", r.UserAgent(),
		"header_keys", sets.List(headers),
	}
	logger.Info("request received", requestContextValues...)

	next(w, r)

	responseContextValues := []any{
		"body_read_bytes", r.Body.(*LoggingReadCloser).bytesRead,
		"body_written_bytes", w.(*LoggingResponseWriter).bytesWritten,
		"response_status_code", w.(*LoggingResponseWriter).statusCode,
		"duration", time.Since(startTime).Seconds(),
	}
	if w.(*LoggingResponseWriter).observedBytes != nil {
		// super expensive, but much easier to read. hopefully this is turned off at some point.
		ret := map[string]any{}
		if err := json.Unmarshal(w.(*LoggingResponseWriter).observedBytes.Bytes(), &ret); err == nil {
			responseContextValues = append(responseContextValues, "body_json", ret)
		} else {
			responseContextValues = append(responseContextValues, "body", w.(*LoggingResponseWriter).observedBytes.String())
		}
	}

	for _, header := range []string{
		"Azure-AsyncOperation", // used by poller async.Applicable
		"Fake-Poller-Status",   // used by poller fake.Applicable
		"Operation-Location",   // used by op.Applicable
		"Location",             // used by loc.Applicable
		"Retry-After",
		"Retry-After-Ms",
		"x-ms-error-code",
	} {
		responseContextValues = append(responseContextValues, "Header---"+header, w.Header().Get(header))
	}

	logger.Info("response complete", responseContextValues...)
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
		logger = logger.WithValues(utils.LogValues{}.AddResourceName(a.resourceName)...)
	}

	if resourceID := a.resourceID(); resourceID != "" {
		logger = logger.WithValues(utils.LogValues{}.AddLogValuesForResourceIDString(resourceID)...)
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
