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
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/baggage"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

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

		expectedAttrs   map[string]string
		expectedBaggage map[string]string
	}{
		{
			name: "empty correlation data",
			data: &arm.CorrelationData{},
			expectedAttrs: map[string]string{
				"aro.request_id": "00000000-0000-0000-0000-000000000000",
			},
			expectedBaggage: map[string]string{
				"aro.request_id": "00000000-0000-0000-0000-000000000000",
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
				"aro.request_id":        testRequestID.String(),
				"aro.client.request_id": testClientRequestID,
				"aro.correlation_id":    testCorrelationRequestID,
			},
			expectedBaggage: map[string]string{
				"aro.request_id":        testRequestID.String(),
				"aro.client.request_id": testClientRequestID,
				"aro.correlation_id":    testCorrelationRequestID,
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Setup the testing tracer.
			sr := tracetest.NewSpanRecorder()
			tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))

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
			middlewareTracing(writer, req, next, otelhttp.WithTracerProvider(tp))

			ss := sr.Ended()

			assert.Len(t, ss, 1)
			span := ss[0]
			containSpanAttributes(t, span, tc.expectedAttrs)

			// Check that the baggage has been extended.
			assert.Len(t, b.Members(), len(tc.expectedBaggage))
			for k, v := range tc.expectedBaggage {
				assert.Equal(t, v, b.Member(k).Value())
			}
		})
	}
}

// equalSpanAttributes ensures that the span's attributes are strictly equal to
// the expected map.
func equalSpanAttributes(t *testing.T, span sdktrace.ReadOnlySpan, expected map[string]string) {
	t.Helper()
	assert.Len(t, span.Attributes(), len(expected))
	containSpanAttributes(t, span, expected)
}

// containSpanAttributes ensures that all the key/value pairs of the map are
// found in the span's attributes.
// Compared to equalSpanAttributes(), it won't fail if there are more
// attributes than expected in the span.
func containSpanAttributes(t *testing.T, span sdktrace.ReadOnlySpan, expected map[string]string) {
	t.Helper()

	for k, v := range expected {
		var found bool
		for _, attr := range span.Attributes() {
			if string(attr.Key) == k {
				assert.Equal(t, v, attr.Value.AsString(), "span attribute %q", k)
				found = true
				continue
			}
		}

		if !found {
			t.Errorf("expected span attribute %q but found none", k)
		}
	}
}

// initSpanRecorder returns a child context containing a new root span and a
// span recorder to introspect the final span and children.
func initSpanRecorder(ctx context.Context) (context.Context, *spanRecorder) {
	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))
	ctx, span := tp.Tracer("test").Start(ctx, "root", trace.WithNewRoot())

	return ctx, &spanRecorder{sr: sr, span: span}
}

type spanRecorder struct {
	sr   *tracetest.SpanRecorder
	span trace.Span
}

func (sr *spanRecorder) collect() []sdktrace.ReadOnlySpan {
	sr.span.End()
	return sr.sr.Ended()
}
