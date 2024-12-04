package frontend

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
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

func MiddlewareLogging(w http.ResponseWriter, r *http.Request, next http.HandlerFunc) {
	ctx := r.Context()
	logger := LoggerFromContext(ctx)

	// Capture request and response data for logging
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

func MiddlewareLoggingPostMux(w http.ResponseWriter, r *http.Request, next http.HandlerFunc) {
	ctx := r.Context()
	logger := LoggerFromContext(ctx)

	correlationData := arm.NewCorrelationData(r)
	ctx = ContextWithCorrelationData(ctx, correlationData)

	setHeaders(w, r, correlationData)

	attrs := getLogAttrs(correlationData, r)
	logger = slog.New(logger.Handler().WithAttrs(attrs))
	ctx = ContextWithLogger(ctx, logger)
	r = r.WithContext(ctx)

	next(w, r)
}

// setHeaders writes the appropriate headers in the response writer
// based on the request and the correlation data.
func setHeaders(w http.ResponseWriter, r *http.Request, correlationData *arm.CorrelationData) {
	if correlationData == nil {
		return
	}

	w.Header().Set(arm.HeaderNameRequestID, correlationData.RequestID.String())

	returnClientRequestId := r.Header.Get(arm.HeaderNameReturnClientRequestID)
	if strings.EqualFold(returnClientRequestId, "true") {
		w.Header().Set(arm.HeaderNameClientRequestID, correlationData.ClientRequestID)
	}
}

// getLogAttrs returns the appropiate Logging Attributes based on correlationData and a request.
func getLogAttrs(correlationData *arm.CorrelationData, r *http.Request) []slog.Attr {
	attrs := []slog.Attr{
		slog.String("request_id", correlationData.RequestID.String()),
		slog.String("client_request_id", correlationData.ClientRequestID),
		slog.String("correlation_request_id", correlationData.CorrelationRequestID),
	}

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
