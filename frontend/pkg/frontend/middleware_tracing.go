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

import (
	"context"
	"fmt"
	"net/http"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/baggage"
	"go.opentelemetry.io/otel/trace"

	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/tracing"
)

// MiddlewareTracing starts an OpenTelemetry span wrapping all incoming HTTP
// requests. Other middlewares or actual request handlers can extend its
// metadata or create their own associated spans.
// The middleware expects that the trace provider is initialized and configured
// in advance.
func MiddlewareTracing(w http.ResponseWriter, r *http.Request, next http.HandlerFunc) {
	middlewareTracing(w, r, next)
}

// middlewareTracing allows to modify the default otelhttp handler for the tests.
func middlewareTracing(w http.ResponseWriter, r *http.Request, next http.HandlerFunc, opts ...otelhttp.Option) {
	otelhttp.NewHandler(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

			ctx = addCorrelationDataToSpanContext(ctx, data)

			r = r.WithContext(ctx)

			next(w, r)
		}),
		fmt.Sprintf("HTTP %s", r.Method),
		opts...,
	).ServeHTTP(w, r)
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
		attr  attribute.Key
		value string
	}{
		{
			attr:  tracing.CorrelationIDKey,
			value: data.CorrelationRequestID,
		},
		{
			attr:  tracing.ClientRequestIDKey,
			value: data.ClientRequestID,
		},
		{
			attr:  tracing.RequestIDKey,
			value: data.RequestID.String(),
		},
	} {
		if e.value == "" {
			continue
		}

		span.SetAttributes(e.attr.String(e.value))

		m, err := baggage.NewMemberRaw(string(e.attr), e.value)
		if err != nil {
			msg := fmt.Sprintf("unable to create baggage member %q", e.attr)
			span.RecordError(fmt.Errorf("%s: %w", msg, err))
			logger.ErrorContext(ctx, msg, "error", err)

			continue
		}

		// SetMember will only return an error if m is uninitialized.
		bag, _ = bag.SetMember(m)
	}

	return baggage.ContextWithBaggage(ctx, bag)
}
