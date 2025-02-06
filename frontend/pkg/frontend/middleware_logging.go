package frontend

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/Azure/ARO-HCP/internal/api"
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
	logger := LoggerFromContext(ctx)

	// Capture the request and response data for logging.
	r.Body = &LoggingReadCloser{ReadCloser: r.Body}
	w = &LoggingResponseWriter{ResponseWriter: w, statusCode: http.StatusOK}

	startTime := time.Now()

	logger = logger.With(
		"request_method", r.Method,
		"request_path", r.URL.Path,
		"request_proto", r.Proto,
		"request_query", r.URL.RawQuery,
		"request_remote_addr", r.RemoteAddr,
		"request_user_agent", r.UserAgent())

	logger.Info("read request")

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
	logger := LoggerFromContext(ctx)

	attrs := getLogAttrs(r)
	logger = slog.New(logger.Handler().WithAttrs(attrs))
	r = r.WithContext(ContextWithLogger(ctx, logger))

	next(w, r)
}

// getLogAttrs returns the additional Logging attributes based on the wildcards
// from the matched pattern.
func getLogAttrs(r *http.Request) []slog.Attr {
	var attrs []slog.Attr

	subscriptionID := r.PathValue(PathSegmentSubscriptionID)
	if subscriptionID != "" {
		attrs = append(attrs, slog.String("subscription_id", subscriptionID))
	}

	resourceGroup := r.PathValue(PathSegmentResourceGroupName)
	if resourceGroup != "" {
		attrs = append(attrs, slog.String("resource_group", resourceGroup))
	}

	resourceName := r.PathValue(PathSegmentResourceName)
	if resourceName != "" {
		attrs = append(attrs, slog.String("resource_name", resourceName))
	}

	wholePath := subscriptionID != "" && resourceGroup != "" && resourceName != ""
	if wholePath {
		format := "/subscriptions/%s/resourcegroups/%s/providers/%s/%s"
		resource_id := fmt.Sprintf(format, subscriptionID, resourceGroup, api.ClusterResourceType, resourceName)
		attrs = append(attrs, slog.String("resource_id", resource_id))
	}

	return attrs
}
