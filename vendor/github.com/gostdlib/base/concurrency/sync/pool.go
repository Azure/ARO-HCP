package sync

import (
	"context"
	"fmt"
	"runtime"
	"sync"

	internalCtx "github.com/gostdlib/base/internal/context"
	"github.com/gostdlib/base/telemetry/log"
	"github.com/gostdlib/base/telemetry/otel/metrics"
	"go.opentelemetry.io/otel/metric"
)

// Resetter is an interface that can be implemented by a type to allow its values to be reset.
// You can do validations that your resetter is doing what you want in tests using the ./reset package.
type Resetter interface {
	Reset()
}

// Pool is an advanced generics based sync.Pool. The generics make for less verbose
// code and prevent accidental assignments of the wrong type which can cause a panic.
// This is NOT a drop in replacement for sync.Pool as we need to provide methods
// a context object.
//
// In addition it can provide:
//
// - provides OTEL tracing for the pool.
// - provides OTEL metrics for the pool if created with NewPool().
// - If the type T implements the Resetter interface, the Reset() method will be called on the value before it is returned to the pool.
//
// If you have a type implementing Resetter, use the ./reset package to validate your reset in tests.
type Pool[T any] struct {
	p sync.Pool

	buffer chan T

	getCalls, putCalls, newAllocated, bufferAllocated metric.Int64Counter

	_ sync.Mutex // Unused on purpose, for CopyLocks error
}

type opts struct {
	meterOpts   []metric.MeterOption
	prefixLevel int

	bufferSize int
}

// Option is an option for constructors in this package.
type Option func(opts) (opts, error)

// WithBuffer sets the buffer for the pool.
// This is a channel of available values that are not in the pool. If this is set to 10 a
// channel of capacity of 10 will be created. The Pool
// will always use the buffer before creating new values. It will always attempt to put
// values back into the buffer before putting them in the pool. If not set, the pool
// will only use the sync.Pool.
func WithBuffer(size int) Option {
	return func(o opts) (opts, error) {
		o.bufferSize = size
		return o, nil
	}
}

// WithMeterOptions sets the options for a meter.
func WithMeterOptions(meterOpts ...metric.MeterOption) Option {
	return func(o opts) (opts, error) {
		o.meterOpts = meterOpts
		return o, nil
	}
}

// WithMeterPrefixLevel sets the prefix for a meter. This is "[package path]/[package name]" of the caller
// of NewPool. However, occasionally you may want this to be a level higher in the call stack. 0
// will be the same as the default(the caller of NewPool), 1 would be the caller of the function that
// called NewPool, etc.
func WithMeterPrefixLevel(l int) Option {
	return func(o opts) (opts, error) {
		o.prefixLevel = l
		return o, nil
	}
}

// NewPool creates a new Pool for use. A "name" is used to create a new meter with the name:
//
//	"[package path]/[package name]:sync.Pool([type stored])/[name]".
//
// If you are providing a type that implementing Resetter, use the ./reset package to validate your reset in tests.
func NewPool[T any](ctx context.Context, name string, n func() T, options ...Option) *Pool[T] {
	var t T

	opts := opts{}
	var err error
	for _, o := range options {
		opts, err = o(opts)
		if err != nil {
			log.Default().Error(fmt.Sprintf("sync.NewPool(): bad option: %s", err))
		}
	}

	var c chan T
	if opts.bufferSize > 0 {
		c = make(chan T, opts.bufferSize)
	}

	mn := fmt.Sprintf("%s:sync.Pool(%T)/%s", metrics.MeterName(2+opts.prefixLevel), t, name)
	mp := internalCtx.MeterProvider(ctx)
	m := mp.Meter(mn, opts.meterOpts...)
	gc, err := m.Int64Counter("get_calls", metric.WithDescription("Number of times .Get() was called"))
	if err != nil {
		log.Default().Error(fmt.Sprintf("sync.NewPool(%s): %s", name, err))
	}
	pc, err := m.Int64Counter("put_calls", metric.WithDescription("Number of times .Put() was called"))
	if err != nil {
		log.Default().Error(fmt.Sprintf("sync.NewPool(%s): %s", name, err))
	}
	na, err := m.Int64Counter("new_allocated", metric.WithDescription("Number of times .New() was called"))
	if err != nil {
		log.Default().Error(fmt.Sprintf("sync.NewPool(%s): %s", name, err))
	}
	ba, err := m.Int64Counter("buffer_allocated", metric.WithDescription("Number of times a value was allocated from the buffer"))
	if err != nil {
		log.Default().Error(fmt.Sprintf("sync.NewPool(%s): %s", name, err))
	}

	p := Pool[T]{
		buffer:          c,
		getCalls:        gc,
		putCalls:        pc,
		newAllocated:    na,
		bufferAllocated: ba,
	}

	p.p = sync.Pool{
		New: func() any {
			if p.newAllocated != nil {
				p.newAllocated.Add(ctx, 1)
			}
			return n()
		},
	}
	return &p
}

// Get returns a value from the pool or creates a new one if the pool is empty.
func (p *Pool[T]) Get(ctx context.Context) T {
	if p.getCalls != nil {
		p.getCalls.Add(ctx, 1)
	}

	select {
	case v := <-p.buffer:
		if p.bufferAllocated != nil {
			p.bufferAllocated.Add(ctx, 1)
		}
		return v
	default:
	}

	return p.p.Get().(T)
}

// Put puts a value back into the pool.
func (p *Pool[T]) Put(ctx context.Context, v T) {
	if p.putCalls != nil {
		p.putCalls.Add(ctx, 1)
	}

	if r, ok := any(v).(Resetter); ok {
		r.Reset()
	}

	select {
	case p.buffer <- v:
		return
	default:
	}
	p.p.Put(v)
}

// Cleanup is a wrapper around a pointer to a value can be used with a Pool
// in order to automatically call Pool.Put() on the value.
// It is important to always pass the Cleanup and not the underlying Value. This is because
// the Cleanup is being tracked for going out of scope, not the underlying value.
// Otherwise when Cleanup goes out of scope, the underlying value will return to the Pool
// while is is still in use. Aka, that is bad.
type Cleanup[T any] struct {
	v *T
}

// V returns the value.
func (x Cleanup[T]) V() *T {
	return x.v
}

// NewCleanup creates a new Cleanup where *T will be put in *sync.Pool when Cleanup goes out of scope.
// This is not as efficient as using Pool.Put(), however if the value needs to span multiple functions, this
// is much safer. Read the usage on Cleanup for more information on safe usage.
// Note that due to the slight signature differences of Pool storing non-pointer values and
// Cleanup only allowing pointer values, a NewCleanup call looks like: NewCleanup[int](ctx, &pool)
func NewCleanup[T any](ctx context.Context, pool *Pool[*T]) *Cleanup[T] {
	// Design note: I really wanted to put Cleanup() as a method on Pool, but
	// that would require changing sync.Pool to use *T instead of T. This would diverge
	// from the stdlib, and I don't want to do that. So this can't be a method on Pool.

	x := &Cleanup[T]{v: pool.Get(ctx)}

	runtime.AddCleanup(
		x,
		func(y *T) {
			pool.Put(ctx, y)
		},
		x.v,
	)
	return x
}
