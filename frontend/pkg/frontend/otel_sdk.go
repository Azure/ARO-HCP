// Copyright 2025 Microsoft Corporation
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package frontend

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
	semconv "go.opentelemetry.io/otel/semconv/v1.27.0"

	"github.com/Azure/ARO-HCP/internal/version"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/sdk/resource"
)

// ConfigureOpenTelemetryTracer configures the global OpenTelemetry trace
// provider.
//
// The function uses the following environment variables for the tracer
// configuration:
//   - `OTEL_TRACES_EXPORTER`, either `otlp` to send traces to an OTLP endpoint or `console`.
//   - `OTEL_EXPORTER_OTLP_TRACES_PROTOCOL`, either `grpc` or `http`.
//   - `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT`, endpoint where to send the OTLP
//     traces (e.g. `https://localhost:4318/v1/traces`).
//
// See
// https://pkg.go.dev/go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp
// for the list of all supported variables.
//
// An error is returned if an environment value is set to an unhandled value.
//
// If no environment variable are set, a no-op tracer is setup.
func ConfigureOpenTelemetryTracer(ctx context.Context, logger *slog.Logger, resourceAttrs ...attribute.KeyValue) (
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
	opts = append(opts, resource.WithAttributes(
		semconv.ServiceNameKey.String(ProgramName),
		semconv.ServiceVersionKey.String(version.CommitSHA),
		semconv.CloudProviderAzure,
	))
	resources, err := resource.New(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to initialise trace resources: %w", err)
	}

	tp := trace.NewTracerProvider(
		trace.WithBatcher(exp),
		trace.WithResource(resources),
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
