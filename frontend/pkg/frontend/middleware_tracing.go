package frontend

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

import (
	"net/http"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

func MiddlewareTracing(w http.ResponseWriter, r *http.Request, next http.HandlerFunc) {
	otelhttp.NewHandler(http.Handler(next), r.URL.Path).ServeHTTP(w, r)
	trace.SpanFromContext(r.Context()).SetAttributes(
		attribute.String("correlationID", r.Header.Get("x-ms-correlation-request-id")),
	)
}
