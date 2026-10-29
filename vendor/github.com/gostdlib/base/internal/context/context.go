// Package context exists to avoid some import cycles.
package context

import (
	"context"

	ierr "github.com/gostdlib/base/internal/errors"
	"github.com/gostdlib/base/telemetry/otel/metrics"

	"go.opentelemetry.io/otel/metric"
	metricsnoop "go.opentelemetry.io/otel/metric/noop"
)

// EOptionKey is a key for the context that stores an errors.EOption.
type EOptionKey struct{}

// MetricsKey is a key for the context that stores a metrics.MeterProvider.
type MetricsKey struct{}

// ShouldTraceKey is a key for the context that stores a bool.
type ShouldTraceKey struct{}

// MeterProvider returns a metric.MeterProvider attached to the context. If no meter provider is attached,
// it returns metrics.Default(). This may be a noop provider.
func MeterProvider(ctx context.Context) metric.MeterProvider {
	a := ctx.Value(MetricsKey{})
	if a == nil {
		return metrics.Default()
	}
	l, ok := a.(metric.MeterProvider)
	if !ok {
		return metricsnoop.NewMeterProvider()
	}
	return l
}

// ShouldTrace returns true if the request has had SetShouldTrace called on it.
func ShouldTrace(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	if ctx.Value(ShouldTraceKey{}) == nil {
		return false
	}
	v, ok := ctx.Value(ShouldTraceKey{}).(bool)
	if !ok {
		return false
	}
	return v
}

// SetShouldTrace attaches a boolean value to the context to indicate if the request should be traced.
// This is not usually used by a service, but by the middleware to determine if the request should
// be traced. This only works if done before the trace is started.
func SetShouldTrace(ctx context.Context, b bool) context.Context {
	return context.WithValue(ctx, ShouldTraceKey{}, b)
}

// EOptions returns the error options attached to the context. If no options are attached, it returns nil.
// This allows for setting per call error options. These will override local options if the same options are set.
// An example of this is writing a traceback to errors on a specific call or all calls.
func EOptions(ctx context.Context) []ierr.EOption {
	opts, ok := ctx.Value(EOptionKey{}).([]ierr.EOption)
	if ok {
		return opts
	}
	return nil
}

// SetEOptions attaches error options to the context. This allows for setting per call error options.
// These will override local options if the same options are set. An example of this is writing a traceback
// to errors on a specific call or all calls.
func SetEOptions(ctx context.Context, options ...ierr.EOption) context.Context {
	return context.WithValue(ctx, EOptionKey{}, options)
}
