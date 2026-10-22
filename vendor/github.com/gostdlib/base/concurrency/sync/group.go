package sync

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Azure/retry/exponential"
	"github.com/gostdlib/base/telemetry/otel/trace/span"
	"go.opentelemetry.io/otel/attribute"
	otelTrace "go.opentelemetry.io/otel/trace"
)

// WorkerPool is a interface that represents a pool of workers that can be used
// to submit work to.
type WorkerPool interface {
	// Submit submits a function to the pool for execution. The context is for the
	// pool use, not the function itself.
	Submit(ctx context.Context, f func())
}

// IndexErr is an error that includes the index of the error. This will
// correlate with the order in which Group.Go() was called.
type IndexErr struct {
	// Index is the index of the error.
	Index int
	// Err is the error that was returned.
	Err error
}

// Error implements the error interface.
func (i IndexErr) Error() string {
	return i.Err.Error()
}

// Unwrap implements the errors.Wrapper interface.
func (i IndexErr) Unwrap() error {
	return i.Err
}

// Errors implements the error interface and stores a collection of errors.
// Use this as a pointer to Errors and not as a value. See .Joined() and .Is()
// for special use information.
type Errors struct {
	mu   sync.Mutex
	errs []error
}

// Add adds an error to the Errors. This is thread-safe.
func (e *Errors) Add(i int, err error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.errs = append(e.errs, IndexErr{Index: i, Err: err})
}

// Errors returns all the errors added to the Errors. This is thread-safe.
func (e *Errors) Errors() []error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.errs
}

// Error returns all the errors joined together with errors.Join()
// and then converted to a string. This is thread-safe.
func (e *Errors) Error() string {
	e.mu.Lock()
	defer e.mu.Unlock()

	if len(e.errs) == 0 {
		return ""
	}

	return errors.Join(e.errs...).Error()
}

// Joined returns all the errors joined together
// with errors.Join(). Use this if you need to unwrap this error
// to get to the underlying errors with errors.As() or errors.Is().
// This is thread-safe.
func (e *Errors) Joined() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	return errors.Join(e.errs...)
}

// Is implements the error.Is() interface. However, this wil only match *Errors and not look
// at the underlying errors. Use Errors.Joined() for those use cases. This is thread-safe.
func (e *Errors) Is(target error) bool {
	e.mu.Lock()
	defer e.mu.Unlock()

	if _, ok := target.(*Errors); ok {
		return true
	}
	return false
}

// Group provides a Group implementation that allows launching
// goroutines in safer way by handling the .Add() and .Done() methods in a standard
// sync.WaitGroup. This prevents problems where you forget to increment or
// decrement the sync.WaitGroup. In addition you can use a goroutines.Pool object
// to allow concurrency control and goroutine reuse (if you don't, it just uses
// a goroutine per call). It provides a Running() method that keeps track of
// how many goroutines are running. This can be used with the goroutines.Pool stats
// to understand what goroutines are in use. It has a CancelOnErr() method to
// allow mimicing of the golang.org/x/sync/errgroup package.
// Finally we provide OTEL support in the Group that can
// be named via the Group.Name string. This will provide span messages on the
// current span when Wait() is called and record any errors in the span.
type Group struct {
	count  atomic.Int64
	total  atomic.Int64
	errors Errors
	wg     sync.WaitGroup

	start    time.Time
	otelOnce sync.Once
	span     span.Span

	noCopy noCopy // Flag govet to prevent copying

	// Pool is an optional goroutines.Pool for concurrency control and reuse.
	Pool WorkerPool
	// CancelOnErr holds a CancelFunc that will be called if any goroutine
	// returns an error. This will automatically be called when Wait() is
	// finished and then reset to nil to allow reuse.
	CancelOnErr context.CancelFunc
	// Backoff is an optional backoff to use for retries.
	// If nil, no retries will be done.
	Backoff *exponential.Backoff
}

// reset resets various internal states of the Group to allow reuse.
func (w *Group) reset() {
	w.start = time.Time{}
	w.otelOnce = sync.Once{}
	w.count.Store(0)
	w.total.Store(0)
	w.CancelOnErr = nil
}

type goOpts struct {
	index   int
	backoff *exponential.Backoff
}

// GoOption is an option for the .Go method.
type GoOption func(goOpts) goOpts

// WithIndex sets the index of the error in the Errors. This allows you to track an
// error back to data that was entered in some type of order, like from a slice.  So
// if you take data from []string, you can tell which ones failed (like index 2) so that
// you can reprocess or do whatever is required.
func WithIndex(i int) GoOption {
	return func(o goOpts) goOpts {
		o.index = i
		return o
	}
}

// WithBackoff sets a backoff to use for retries. This overrides any backoff set
// on the Group for this call to Go().
func WithBackoff(b *exponential.Backoff) GoOption {
	return func(o goOpts) goOpts {
		o.backoff = b
		return o
	}
}

// Go spins off a goroutine that executes f(). This will use the underlying
// Pool if provided. The context is used to allow cancellation of work
// before it is submitted to the underlying pool for execution. If the context passed on the
// first call to Go() has a span attached to it, the Group will create a new span
// to record all execution in. That span will be attached to the context passed Go() for each subsequent call.
// This is done once per Group until the Group is reset. The passed context is then passed to the function f, which
// must deal with individual context cancellation and recording any span information. The returned error
// only occurrs the function "f" is not run. This happens when the Context is cancelled.
func (w *Group) Go(ctx context.Context, f func(ctx context.Context) error, options ...GoOption) {
	opts := goOpts{index: -1}
	for _, o := range options {
		opts = o(opts)
	}

	var didOnce bool
	// This sets up a new child span where all execution will be recorded.
	// This is done once per Group until the Group is reset.s
	w.otelOnce.Do(func() {
		spanner := span.Get(ctx)
		if spanner.IsRecording() {
			w.start = time.Now().UTC()
			ctx, w.span = span.New(ctx, span.WithName("github.com/gostdlib/base/concurrency/sync.Group"))
		}
		didOnce = true
	})
	// Since this wasn't the initial setup, we need to attach the span to the context.
	if !didOnce {
		if span.Get(ctx).IsRecording() {
			otelTrace.ContextWithSpan(ctx, w.span.Span)
		}
	}

	w.execute(ctx, f, opts)
}

// execute is a helper function that executes the function f and handles the
// incrementing of the WaitGroup, count and total counters. Decrementing is handled in executeFn.
// This determines if the Group has a backoff set and calls the appropriate function
// that handles execution.
func (w *Group) execute(ctx context.Context, f func(ctx context.Context) error, opts goOpts) {
	w.wg.Add(1)
	w.count.Add(1)
	w.total.Add(1)

	if opts.backoff == nil {
		opts.backoff = w.Backoff
	}

	if opts.backoff == nil {
		w.noBackoff(ctx, f, opts)
		return
	}
	w.withBackoff(ctx, f, opts)
}

// noBackoff is a helper function that executes the function f and handles the
// case where we don't have a backoff set. This will determine if we are using a pool or not
// and either spins off a goroutine or submits the work to the pool.
func (w *Group) noBackoff(ctx context.Context, f func(ctx context.Context) error, opts goOpts) {
	if w.Pool == nil {
		go w.executeFn(ctx, f, opts)
		return
	}
	w.Pool.Submit(ctx, func() { w.executeFn(ctx, f, opts) })
}

// withBackoff is a helper function that executes the function f and handles the
// case where we have a backoff set. This will determine if we are using a pool or not
// and either spins off a goroutine or submits the work to the pool.
func (w *Group) withBackoff(ctx context.Context, f func(ctx context.Context) error, opts goOpts) {
	if w.Pool == nil {
		go func() {
			opts.backoff.Retry(
				ctx,
				func(ctx context.Context, record exponential.Record) error {
					return w.executeFn(ctx, f, opts)
				},
			)
		}()
		return
	}

	w.Pool.Submit(
		ctx,
		func() {
			opts.backoff.Retry(
				ctx,
				func(ctx context.Context, record exponential.Record) error {
					return w.executeFn(ctx, f, opts)
				},
			)
		},
	)
}

// executeFn is a helper function that executes the function f and handles the
// decrementing of the count and wg.Done() calls. It also handles the application
// of errors to the Group.
func (w *Group) executeFn(ctx context.Context, f func(context.Context) error, opts goOpts) error {
	defer w.count.Add(-1)
	defer w.wg.Done()

	if err := context.Cause(ctx); err != nil {
		w.recErr(opts.index, err)
		return err
	}

	if err := f(ctx); err != nil {
		w.recErr(opts.index, err)
		if w.CancelOnErr != nil {
			w.CancelOnErr()
		}
		return err
	}
	return nil
}

// Running returns the number of goroutines that are currently running.
func (w *Group) Running() int {
	return int(w.count.Load())
}

// Wait blocks until all goroutines are finshed. The passed Context cannot be cancelled.
func (w *Group) Wait(ctx context.Context) error {
	defer w.reset()

	now := time.Now().UTC()
	w.waitOTELStart()
	defer w.waitOTELEnd(now)

	// Now do the actual waiting.
	w.wg.Wait()

	if w.CancelOnErr != nil {
		w.CancelOnErr()
		w.CancelOnErr = nil
	}

	if w.errors.Errors() == nil {
		return nil
	}
	return &w.errors
}

// waitOTELStart is called when Wait() is called and will log information to the span.
func (w *Group) waitOTELStart() {
	if !w.span.IsRecording() {
		return
	}

	w.span.Event(
		"WaitGroup.Wait() called",
		attribute.Int64("total goroutines", w.total.Load()),
		attribute.Bool("cancelOnErr", w.CancelOnErr != nil),
		attribute.Bool("using pool", w.Pool != nil),
	)
}

// waitOTELEnd is called when Wait() is finished and will log information to the span.
func (w *Group) waitOTELEnd(t time.Time) {
	if w.span.IsRecording() {
		w.span.Event(
			"wait.Group.Wait() done",
			attribute.Int64("elapsed_ns", int64(time.Now().UTC().Sub(t))),
		)
		w.span.End()
	}
}

func (w *Group) recErr(index int, err error) {
	if w.span.IsRecording() {
		w.span.Span.RecordError(err)
	}
	w.errors.Add(index, err)
}

type noCopy struct{}

func (*noCopy) Lock() {}
