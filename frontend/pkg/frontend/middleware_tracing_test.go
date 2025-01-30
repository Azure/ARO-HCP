package frontend

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/otel/baggage"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/Azure/ARO-HCP/internal/api/arm"
)

func TestContextWithTraceCorrelationData(t *testing.T) {
	requestID := uuid.New()

	expected := map[string]string{
		"correlation.id":    "12345",
		"client.request.id": "67890",
		"request.id":        requestID.String(),
	}

	data := &arm.CorrelationData{
		CorrelationRequestID: expected["correlation.id"],
		ClientRequestID:      expected["client.request.id"],
		RequestID:            requestID,
	}

	// NOTE: no span, no effect
	assert.NotNil(t, ContextWithCorrelationData(context.Background(), data))

	// NOTE: empty correlation data, no effect
	assert.NotNil(t, ContextWithCorrelationData(context.Background(), &arm.CorrelationData{}))

	// NOTE: check attributes and baggage
	inMemoryExporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSpanProcessor(sdktrace.NewSimpleSpanProcessor(inMemoryExporter)),
	)

	ctx := context.Background()
	ctx, span := tp.Tracer("unittest").Start(ctx, "test")
	// call..
	ctx = ContextWithTraceCorrelationData(ctx, data)
	span.End() // NOTE: Stop span!

	stubs := inMemoryExporter.GetSpans()
	ss := stubs.Snapshots()
	assert.Len(t, ss, 1)
	assert.Len(t, ss[0].Attributes(), len(expected))

	b := baggage.FromContext(ctx)
	assert.Equal(t, data.CorrelationRequestID, b.Member("correlation.id").Value())
	assert.Equal(t, data.RequestID.String(), b.Member("request.id").Value())
	assert.Equal(t, data.ClientRequestID, b.Member("client.request.id").Value())
}
