package frontend

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

import (
	"net/http"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

func MiddlewareTracing(w http.ResponseWriter, r *http.Request, next http.HandlerFunc) {
	otelhttp.NewHandler(http.Handler(next), r.URL.Path).ServeHTTP(w, r)
}
