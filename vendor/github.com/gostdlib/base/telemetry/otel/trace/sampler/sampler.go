// Package sampler provides a sampler that can be used to sample calls based on a filter.
// This allows for more fine grained control over sampling than the standard sampler.
// The sampler can be updated at runtime to change the filter. The sampler is thread safe.
// In addition, the sampler can have a secondary sampler that is used if the filter does not
// match. This allows for a default sampling rate to be set.
package sampler

import (
	"context"
	"sync/atomic"

	internalCtx "github.com/gostdlib/base/internal/context"

	sdkTrace "go.opentelemetry.io/otel/sdk/trace"
)

var defaultFilters = &atomic.Pointer[[]Filter]{}

// DefaultFilters returns the default filters for the sampler. This is the set of filters
// used by the sampler returned by DefaultSampler. If you are not using the default sampler,
// these will do nothing for you.
func DefaultFilters() []Filter {
	f := defaultFilters.Load()
	if f == nil {
		return nil
	}
	return *f
}

// SetDefaultFilters sets the default filters for the DefaultSampler. This is the set of filters
// using the defaults. If you are not using the default sampler, these will do nothing for you.
func SetDefaultFilters(filters []Filter) {
	defaultFilters.Store(&filters)
}

// DefaultSampler returns a sampler that will sample calls based on the default filters and
// a secondary sampler which will sample at the rate provided.
func DefaultSampler(sampleRate float64) sdkTrace.Sampler {
	return &Filtered{
		filters:   defaultFilters,
		secondary: sdkTrace.TraceIDRatioBased(sampleRate),
	}
}

// Filter is used to match a call based on attributes and is used in decision making on
// if a call should be sampled outside of the normal sampling process. While fields can
type Filter interface {
	// Match returns true if the filter matches the given context. This will cause the call to be
	// sampled.
	Match(ctx context.Context) bool
}

// Filtered is a custom sampler that will sample certain operations based on a filter, otherwise it will use a
// secondary sampler to determine if the operation should be sampled. This allows for fine-grained control
// over sampling. The sampler is thread safe.
type Filtered struct {
	secondary sdkTrace.Sampler

	filters *atomic.Pointer[[]Filter]
}

// New creates a new Filtered sampler with the given secondary sampler. If the secondary sampler is nil,
// then only filter matches will be used to determine if an operation should be sampled.
func New(secondary sdkTrace.Sampler) (*Filtered, error) {
	return &Filtered{
		secondary: secondary,
		filters:   &atomic.Pointer[[]Filter]{},
	}, nil
}

// ReplaceFilters replaces all filters with the given filters.
func (c *Filtered) ReplaceFilters(filters ...Filter) error {
	(*c.filters).Store(&filters)
	return nil
}

// ShouldSample returns a sampling decision based on the sampling parameters.
func (c *Filtered) ShouldSample(params sdkTrace.SamplingParameters) sdkTrace.SamplingResult {
	if internalCtx.ShouldTrace(params.ParentContext) {
		return sdkTrace.SamplingResult{Decision: sdkTrace.RecordAndSample}
	}

	filters := *c.filters.Load()
	if len(filters) > 0 {
		for _, f := range filters {
			if f.Match(params.ParentContext) {
				return sdkTrace.SamplingResult{Decision: sdkTrace.RecordAndSample}
			}
		}
	}
	if c.secondary != nil {
		return c.secondary.ShouldSample(params)
	}
	return sdkTrace.SamplingResult{Decision: sdkTrace.Drop}
}

// Description returns the description of the sampler.
func (c *Filtered) Description() string {
	return "go.goms.io/base/telemetry/otel/trace/sampler"
}
