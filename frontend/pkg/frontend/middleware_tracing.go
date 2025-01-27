package frontend

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

import (
	"fmt"
	"net/http"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

// MiddlewareTracing starts a span wrapping all incoming HTTP requests.
// Other middlwares or actual request handlers can extend its metadata or create
// their own associated spans.
func MiddlewareTracing(w http.ResponseWriter, r *http.Request, next http.HandlerFunc) {
	otelhttp.NewHandler(http.Handler(next), fmt.Sprintf("HTTP %s", r.Method)).ServeHTTP(w, r)
}
