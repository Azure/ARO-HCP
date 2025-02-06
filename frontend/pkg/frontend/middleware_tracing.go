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

// MiddlewareTracing starts an OpenTelemetry span wrapping all incoming HTTP
// requests. Other middlewares or actual request handlers can extend its
// metadata or create their own associated spans.
// The middleware expects that the trace provider is initialized and configured
// in advance.
func MiddlewareTracing(w http.ResponseWriter, r *http.Request, next http.HandlerFunc) {
	otelhttp.NewHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var (
			ctx    = r.Context()
			logger = LoggerFromContext(ctx)
		)

		data, err := CorrelationDataFromContext(ctx)
		if err != nil {
			span := trace.SpanFromContext(ctx)
			span.RecordError(err)
			logger.ErrorContext(ctx, "failed to find correlation data in context", "error", err)
			next(w, r)
			return
		}

		r = r.WithContext(
			addCorrelationDataToSpanContext(ctx, data),
		)

		next(w, r)
	}), fmt.Sprintf("HTTP %s", r.Method)).ServeHTTP(w, r)
}

// addCorrelationDataToSpanContext adds the correlation data as attributes to
// the propagated span. It also adds correlation data to the span's baggage
// which is propagated to the downstream services (e.g. Clusters Service). If
// the context does not maintain a span, the function has no effect.
func addCorrelationDataToSpanContext(ctx context.Context, data *arm.CorrelationData) context.Context {
	var (
		logger = LoggerFromContext(ctx)
		span   = trace.SpanFromContext(ctx)
	)

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
		if e.value == "" {
			continue
		}

		span.SetAttributes(attribute.String(e.name, e.value))

		m, err := baggage.NewMemberRaw(e.name, e.value)
		if err != nil {
			msg := fmt.Sprintf("unable to create baggage member %q", e.name)
			span.RecordError(fmt.Errorf("%s: %w", msg, err))
			logger.ErrorContext(ctx, msg, "error", err)

			continue
		}

		// SetMember will only return an error if m is uninitialized.
		bag, _ = bag.SetMember(m)
	}

	return baggage.ContextWithBaggage(ctx, bag)
}
