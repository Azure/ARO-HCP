package frontend

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

import (
	"fmt"
	"net/http"
	"strings"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

func MiddlewareTracing(w http.ResponseWriter, r *http.Request, next http.HandlerFunc) {
	attrs := []attribute.KeyValue{}
	for k, v := range r.Header {
		attrs = append(attrs, attribute.String(fmt.Sprintf("http.request.header.%s", k), strings.Join(v, ",")))
	}
	otelhttp.NewHandler(
		http.Handler(next),
		"middleware",
		otelhttp.WithSpanOptions(
			trace.WithAttributes(attrs...),
		),
	).ServeHTTP(w, r)
}
