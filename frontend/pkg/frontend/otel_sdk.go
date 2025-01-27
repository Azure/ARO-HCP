package frontend

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

// TODO: can we move this into ocm-sdk-go/tracing?
import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"go.opentelemetry.io/contrib/exporters/autoexport"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/trace"
	tracesdk "go.opentelemetry.io/otel/sdk/trace"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/sdk/resource"
)

func InstallOpenTelemetryTracer(ctx context.Context, logger *slog.Logger, resourceAttrs ...attribute.KeyValue) (
	func(context.Context) error,
	error,
) {
	exp, err := autoexport.NewSpanExporter(ctx, autoexport.WithFallbackSpanExporter(newNoopFactory))
	if err != nil {
		return nil, fmt.Errorf("failed to create OTEL exporter: %w", err)
	}

	var isNoop bool
	if _, isNoop = exp.(*noopSpanExporter); !isNoop || autoexport.IsNoneSpanExporter(exp) {
		isNoop = true
	}
	logger.InfoContext(ctx, "initialising OpenTelemetry tracer", "isNoop", isNoop)

	opts := []resource.Option{resource.WithHost()}
	if len(resourceAttrs) > 0 {
		opts = append(opts, resource.WithAttributes(resourceAttrs...))
	}
	resources, err := resource.New(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to initialise trace resources: %w", err)
	}

	tp := tracesdk.NewTracerProvider(
		tracesdk.WithBatcher(exp),
		tracesdk.WithResource(resources),
	)
	otel.SetTracerProvider(tp)

	shutdown := func(ctx context.Context) error {
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		return tp.Shutdown(ctx)
	}

	propagator := propagation.NewCompositeTextMapPropagator(propagation.Baggage{}, propagation.TraceContext{})
	otel.SetTextMapPropagator(propagator)

	otel.SetErrorHandler(otelErrorHandlerFunc(func(err error) {
		logger.ErrorContext(ctx, fmt.Sprintf("OpenTelemetry.ErrorHandler: %v", err))
	}))

	return shutdown, nil
}

type otelErrorHandlerFunc func(error)

// Handle implements otel.ErrorHandler
func (f otelErrorHandlerFunc) Handle(err error) {
	f(err)
}

func newNoopFactory(_ context.Context) (trace.SpanExporter, error) {
	return &noopSpanExporter{}, nil
}

var _ trace.SpanExporter = noopSpanExporter{}

// noopSpanExporter is an implementation of trace.SpanExporter that performs no operations.
type noopSpanExporter struct{}

// ExportSpans is part of trace.SpanExporter interface.
func (e noopSpanExporter) ExportSpans(ctx context.Context, spans []trace.ReadOnlySpan) error {
	return nil
}

// Shutdown is part of trace.SpanExporter interface.
func (e noopSpanExporter) Shutdown(ctx context.Context) error {
	return nil
}
