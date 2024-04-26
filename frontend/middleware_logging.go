package main

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
	// Capture request and response data for logging
	r.Body = &LoggingReadCloser{ReadCloser: r.Body}
	w = &LoggingResponseWriter{ResponseWriter: w, statusCode: http.StatusOK}

	startTime := time.Now()

	logger, err := LoggerFromContext(r.Context())
	if err != nil {
		DefaultLogger().Error(err.Error())
		arm.WriteInternalServerError(w)
		return
	}

	logger = logger.With(
		"request_method", r.Method,
		"request_path", r.URL.Path,
		"request_proto", r.Proto,
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
	var pathValue string

	ctx := r.Context()

	correlationData := arm.NewCorrelationData(r)
	ctx = ContextWithCorrelationData(ctx, correlationData)

	w.Header().Set(arm.HeaderNameRequestID, correlationData.RequestID.String())

	if strings.EqualFold(r.Header.Get(arm.HeaderNameReturnClientRequestID), "true") {
		w.Header().Set(arm.HeaderNameClientRequestID, correlationData.ClientRequestID)
	}

	attrs := []slog.Attr{
		slog.String("request_id", correlationData.RequestID.String()),
		slog.String("client_request_id", correlationData.ClientRequestID),
		slog.String("correlation_request_id", correlationData.CorrelationRequestID),
	}

	if pathValue = r.PathValue(PathSegmentSubscriptionID); pathValue != "" {
		attrs = append(attrs, slog.String("subscription_id", pathValue))
	}

	if pathValue = r.PathValue(PathSegmentResourceGroupName); pathValue != "" {
		attrs = append(attrs, slog.String("resource_group", pathValue))
	}

	if pathValue = r.PathValue(PathSegmentResourceName); pathValue != "" {
		attrs = append(attrs, slog.String("resource_name", pathValue))
		resource_id := fmt.Sprintf("/subscriptions/%s/resourcegroups/%s/providers/%s/%s/%s",
			r.PathValue(PathSegmentSubscriptionID),
			r.PathValue(PathSegmentResourceGroupName),
			api.ProviderNamespace,
			api.ResourceType,
			pathValue)
		attrs = append(attrs, slog.String("resource_id", resource_id))
	}

	logger, err := LoggerFromContext(ctx)
	if err != nil {
		DefaultLogger().Error(err.Error())
		arm.WriteInternalServerError(w)
		return
	}

	handler := logger.Handler()
	ctx = ContextWithLogger(ctx, slog.New(handler.WithAttrs(attrs)))

	next(w, r.WithContext(ctx))
}
