package frontend

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/baggage"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/Azure/ARO-HCP/internal/api/arm"
)

func TestMiddlewareTracing(t *testing.T) {
	var (
		testRequestID            = uuid.MustParse("11111111-1111-1111-1111-111111111111")
		testClientRequestID      = "22222222-2222-2222-2222-222222222222"
		testCorrelationRequestID = "33333333-3333-3333-3333-333333333333"
	)
	for _, tc := range []struct {
		name string
		data *arm.CorrelationData
		//withoutSpanContext bool

		expectedAttrs   map[string]string
		expectedBaggage map[string]string
	}{
		//{
		//	// Verify that the function doesn't panic if there's no span in the
		//	// context.
		//	name:               "no span context",
		//	data:               &arm.CorrelationData{},
		//	withoutSpanContext: true,
		//},

		{
			name: "empty correlation data",
			data: &arm.CorrelationData{},
			expectedAttrs: map[string]string{
				"aro.request.id": "00000000-0000-0000-0000-000000000000",
			},
			expectedBaggage: map[string]string{
				"aro.request.id": "00000000-0000-0000-0000-000000000000",
			},
		},
		{
			name: "with correlation data",
			data: &arm.CorrelationData{
				RequestID:            testRequestID,
				ClientRequestID:      testClientRequestID,
				CorrelationRequestID: testCorrelationRequestID,
			},
			expectedAttrs: map[string]string{
				"aro.request.id":        testRequestID.String(),
				"aro.client.request.id": testClientRequestID,
				"aro.correlation.id":    testCorrelationRequestID,
			},
			expectedBaggage: map[string]string{
				"aro.request.id":        testRequestID.String(),
				"aro.client.request.id": testClientRequestID,
				"aro.correlation.id":    testCorrelationRequestID,
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Setup the testing tracer.
			inMemoryExporter := tracetest.NewInMemoryExporter()
			tp := sdktrace.NewTracerProvider(
				sdktrace.WithSpanProcessor(
					sdktrace.NewSimpleSpanProcessor(inMemoryExporter),
				),
			)
			otel.SetTracerProvider(tp)

			var (
				ctx = context.Background()
				b   baggage.Baggage
			)
			ctx = ContextWithCorrelationData(ctx, tc.data)
			req, err := http.NewRequestWithContext(ctx, "GET", "http://example.com", nil)
			assert.NoError(t, err)

			next := func(w http.ResponseWriter, r *http.Request) {
				// Capture the baggage to check it later.
				b = baggage.FromContext(r.Context())
				w.WriteHeader(http.StatusOK)
			}

			writer := httptest.NewRecorder()
			MiddlewareTracing(writer, req, next)

			stubs := inMemoryExporter.GetSpans()
			ss := stubs.Snapshots()

			// Check the span attributes.
			assert.Len(t, ss, 1)
			span := ss[0]
			for k, v := range tc.expectedAttrs {
				var found bool
				for _, attr := range span.Attributes() {
					if string(attr.Key) == k {
						assert.Equal(t, v, attr.Value.AsString())
						found = true
						continue
					}
				}

				assert.True(t, found)
			}

			// Check that the baggage has been extended.
			assert.Len(t, b.Members(), len(tc.expectedBaggage))
			for k, v := range tc.expectedBaggage {
				assert.Equal(t, v, b.Member(k).Value())
			}
		})
	}
}
