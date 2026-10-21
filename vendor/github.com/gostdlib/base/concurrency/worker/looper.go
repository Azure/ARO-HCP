package worker

import (
	"context"
	"iter"
	synclib "sync"

	"github.com/gostdlib/base/concurrency/sync"
)

type opts struct {
	goOptions   []sync.GoOption
	cancelOnErr bool
}

// LoopOption is an option for one of our loopers.
type LoopOption func(o opts) opts

// WithGroupOptions sets the options for the underlying Group.Go calls. This has no effect if used
// with Seq, only with Wait.
func WithGroupOptions(goOpts ...sync.GoOption) func(o opts) opts {
	return func(o opts) opts {
		o.goOptions = append(o.goOptions, goOpts...)
		return o
	}
}

// WithCancelOnError configures the looper to cancel the context if any function returns an error. This will
// also stop any further functions from being started.
func WithCancelOnError() func(o opts) opts {
	return func(o opts) opts {
		o.cancelOnErr = true
		return o
	}
}

// Func is a function that is called for each key/value pair in an iter.Seq2.
type Func[K comparable, V any] func(ctx context.Context, k K, v V) error

// Seq loops over an iter.Seq2 and calls the provided function for each key/value pair. It does not wait
// for the functions to complete before returning. It uses the provided Pool to manage concurrency.
func Seq[K comparable, V any](ctx context.Context, pool *Pool, seq iter.Seq2[K, V], f Func[K, V], options ...LoopOption) {
	opts := opts{}
	for _, o := range options {
		opts = o(opts)
	}

	var cancel context.CancelFunc
	if opts.cancelOnErr {
		ctx, cancel = context.WithCancel(ctx)
		defer cancel()
	}

	wg := synclib.WaitGroup{}
	for k, v := range seq {
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		pool.Submit(
			ctx,
			func() {
				defer wg.Done()
				if err := f(ctx, k, v); err != nil && opts.cancelOnErr {
					cancel()
				}
			},
		)
	}
	if cancel != nil {
		pool.Submit(
			ctx,
			func() {
				wg.Wait()
				cancel()
			},
		)
	}
}

// Wait loops over an iter.Seq2 and calls the provided function for each key/value pair. It will automatically
// wait for all functions to complete before returning. It automatically breaks if the Context is cancelled.
// It uses the provided Pool to manage concurrency.
func Wait[K comparable, V any, A any](ctx context.Context, pool *Pool, seq iter.Seq2[K, V], f Func[K, V], options ...LoopOption) error {
	opts := opts{}
	for _, o := range options {
		opts = o(opts)
	}

	var cancel context.CancelFunc
	if opts.cancelOnErr {
		ctx, cancel = context.WithCancel(ctx)
		defer cancel()
	}

	g := pool.Group()
	for k, v := range seq {
		if ctx.Err() != nil {
			break
		}
		g.Go(
			ctx,
			func(ctx context.Context) error {
				return f(ctx, k, v)
			},
			opts.goOptions...,
		)
	}
	return g.Wait(ctx)
}

// ChanSeq2 converts a channel into a iter.Seq2.
func ChanSeq2[V any, C chan V | <-chan V](c C) iter.Seq2[int, V] {
	return func(yield func(int, V) bool) {
		i := 0
		for v := range c {
			if !yield(i, v) {
				return
			}
			i++
		}
	}
}

// MapSeq converts a map into a iter.Seq2.
func MapSeq2[K comparable, V any](m map[K]V) iter.Seq2[K, V] {
	return func(yield func(K, V) bool) {
		for k, v := range m {
			if !yield(k, v) {
				return
			}
		}
	}
}

// SliceSeq converts a slice into a iter.Seq2.
func SliceSeq2[V any](s []V) iter.Seq2[int, V] {
	return func(yield func(int, V) bool) {
		for i, v := range s {
			if !yield(i, v) {
				return
			}
		}
	}
}
