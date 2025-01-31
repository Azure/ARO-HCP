package frontend

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

import (
	"context"
	"fmt"
	"net/http"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/baggage"
	"go.opentelemetry.io/otel/trace"

	"github.com/Azure/ARO-HCP/internal/api/arm"
)

// MiddlewareTracing starts a span wrapping all incoming HTTP requests.
// Other middlwares or actual request handlers can extend its metadata or create
// their own associated spans.
func MiddlewareTracing(w http.ResponseWriter, r *http.Request, next http.HandlerFunc) {
	otelhttp.NewHandler(http.Handler(next), fmt.Sprintf("HTTP %s", r.Method)).ServeHTTP(w, r)
}

// ContextWithTraceCorrelationData adds correlationData as attributes to a propagated span.
// It also registers a baggage with correlation data to the context.
// If the context does not maintain a span, it has no effect.
func ContextWithTraceCorrelationData(ctx context.Context, data *arm.CorrelationData) context.Context {
	logger := LoggerFromContext(ctx)
	// NOTE: Here the middleware span is extended by further attributes.
	// If the tracingMiddleware is not registered, this lines will have no effect.
	span := trace.SpanFromContext(ctx)
	// Calling New() without any member never returns an error.
	bag, _ := baggage.New()

	for _, e := range []struct {
		name  string
		value string
	}{
		{
			name:  "correlation.id",
			value: data.CorrelationRequestID,
		},
		{
			name:  "client.request.id",
			value: data.ClientRequestID,
		},
		{
			name:  "request.id",
			value: data.RequestID.String(),
		},
	} {
		span.SetAttributes(attribute.String(e.name, e.value))

		m, err := baggage.NewMemberRaw(e.name, e.value)
		if err != nil {
			fmtStr := "unable to create baggage member %q"
			span.RecordError(fmt.Errorf(fmtStr+": %w", e.name, err))
			logger.ErrorContext(ctx, fmt.Sprintf(fmtStr, e.name), "error", err)
			continue
		}

		bag, _ = bag.SetMember(m)
	}

	return baggage.ContextWithBaggage(ctx, bag)
}
