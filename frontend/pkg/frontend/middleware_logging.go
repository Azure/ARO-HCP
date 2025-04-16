package frontend

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/tracing"
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
		"request_referer", r.Referer(),
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

	attrs := &attributes{
		subscriptionID: r.PathValue(PathSegmentSubscriptionID),
		resourceGroup:  r.PathValue(PathSegmentResourceGroupName),
		resourceName:   r.PathValue(PathSegmentResourceName),
	}
	attrs.addToCurrentSpan(ctx)
	ctx = ContextWithLogger(ctx, attrs.extendLogger(logger))
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
		"/subscriptions/%s/resourcegroups/%s/providers/%s/%s",
		a.subscriptionID,
		a.resourceGroup,
		api.ClusterResourceType,
		a.resourceName,
	)
}

// extendLogger returns a new logger with additional Logging attributes based
// on the wildcards from the matched pattern.
func (a *attributes) extendLogger(logger *slog.Logger) *slog.Logger {
	var attrs []slog.Attr

	if a.subscriptionID != "" {
		attrs = append(attrs, slog.String("subscription_id", a.subscriptionID))
	}

	if a.resourceGroup != "" {
		attrs = append(attrs, slog.String("resource_group", a.resourceGroup))
	}

	if a.resourceName != "" {
		attrs = append(attrs, slog.String("resource_name", a.resourceName))
	}

	if resourceID := a.resourceID(); resourceID != "" {
		attrs = append(attrs, slog.String("resource_id", resourceID))
	}

	return slog.New(logger.Handler().WithAttrs(attrs))
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
