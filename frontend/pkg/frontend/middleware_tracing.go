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
	span.SetAttributes(attribute.String("correlation.id", data.CorrelationRequestID))
	span.SetAttributes(attribute.String("client.request.id", data.ClientRequestID))
	span.SetAttributes(attribute.String("request.id", data.RequestID.String()))

	cID, err := baggage.NewMemberRaw("correlation.id", data.CorrelationRequestID)
	if err != nil {
		fmtStr := `unable to create baggage member "correlation.id": %w`
		span.RecordError(fmt.Errorf(fmtStr, err))
		logger.ErrorContext(ctx, fmtStr, "error", err)
	}
	rID, err := baggage.NewMemberRaw("request.id", data.RequestID.String())
	if err != nil {
		fmtStr := `unable to create baggage member "request.id": %w`
		span.RecordError(fmt.Errorf(fmtStr, err))
		logger.ErrorContext(ctx, fmtStr, "error", err)
	}
	crID, err := baggage.NewMemberRaw("client.request.id", data.ClientRequestID)
	if err != nil {
		fmtStr := `unable to create baggage member "client.request.id": %w`
		span.RecordError(fmt.Errorf(fmtStr, err))
		logger.ErrorContext(ctx, fmtStr, "error", err)
	}

	bag, err := baggage.New(cID, rID, crID)
	if err != nil {
		fmtStr := "unable to generate new baggage: %w"
		span.RecordError(fmt.Errorf(fmtStr, err))
		logger.ErrorContext(ctx, fmtStr, "error", err)
	}

	return baggage.ContextWithBaggage(ctx, bag)
}
