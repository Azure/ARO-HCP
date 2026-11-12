/*
Package span provides methods to create new spans and get existing spans. This can be used to
provide tracing for a service. This package is a wrapper around the OpenTelemetry Go SDK.

The general use looks like:

	func someFunction(ctx context.Context) error {
		// This creates a new span inside a trace. If the trace is not recording, the span is a noop.
		// The span is created with a span kind of internal, unless another span kind is passed using
		// the WithSpanKind() option. The name of the span will be the name of the function. The span
		// includes attributes for the file name and line number. You can override the span name by
		// using the WithName() option. Timings for the span are automatically recorded by the OTEL
		// library.
		ctx, span := span.New(ctx)
		// This will automatically record the span status of Ok if span.Status() or errors.E() has not been
		// called.
		defer span.End()

		...

		// If you want to record an event within the span, you can simply do:
		span.Event("event name", attribute.String("key", "value"), attribute.Int("key", 1))

		...
	}

If you want to use an existing span, you can use span.Get(ctx) this usually looks like:

	func someFunction(ctx context.Context) error {
		span := span.Get(ctx)

		...

		span.Event("event name", attribute.String("key", "value"), attribute.Int("key", 1))

		...

	}
*/
package span

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"time"

	"github.com/gostdlib/base/telemetry/log"
	baseTrace "github.com/gostdlib/base/telemetry/otel/trace"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdkTrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

// Span represents an OTEL span for recording events to. This superclass handles noops
// for the trace.Span and events we record. You get a child Span object by using New() or
// an existing Span object by using Get(). We have standardized how we records events and
// other type of actions on the span. The original Span object is available as
// an attribute for other needs.
type Span struct {
	// Span is the OTEL trace.Span.
	Span trace.Span

	opts spanOpts
}

type spanOpts struct {
	name         string
	startOptions []trace.SpanStartOption
	endOptions   []trace.SpanEndOption
}

// Get returns the Span for recording events. This retrieves the OTEL span from the
// Context object. If the OTEL span is a noop, then all events will also be a noop.
// Only use this if you want to write to an existing span. Most times, you will want
// to use New() to create a new child span.
func Get(ctx context.Context) Span {
	return Span{Span: trace.SpanFromContext(ctx)}
}

// Option is an option to New().
type Option func(spanOpts) (spanOpts, error)

// WithSpanStartOption adds trace.SpanStartOption(s) to the span creation.
func WithSpanStartOption(options ...trace.SpanStartOption) Option {
	return func(s spanOpts) (spanOpts, error) {
		s.startOptions = options
		return s, nil
	}
}

// WithSpanEndOption adds trace.SpanEndOption(s) to the span end.
func WithSpanEndOption(options ...trace.SpanEndOption) Option {
	return func(s spanOpts) (spanOpts, error) {
		s.endOptions = options
		return s, nil
	}
}

// WithName overrides the default span name with the provided name. The name is limited to 255 characters.
// If the name is longer than 255 characters, it will be truncated.
func WithName(name string) Option {
	return func(s spanOpts) (spanOpts, error) {
		s.name = name
		return s, nil
	}
}

// New creates a new child span object from the span stored in Context. If that Span is
// a noOp, the child span will be a noop too. If you pass a nil Context, this will return
// the background Context with a noop span. If an option is passed that is not valid,
// it is ignored and an error is logged. The span is created with a span kind of internal,
// unless another span kind is passed using WithSpanStartOption(WithSpanKind([kind])).
// This starts the span.
func New(ctx context.Context, options ...Option) (context.Context, Span) {
	if ctx == nil {
		ctx = context.Background()
		return ctx, Span{Span: noop.Span{}}
	}

	span := trace.SpanFromContext(ctx)
	if !span.IsRecording() {
		return ctx, Span{Span: noop.Span{}}
	}

	opts := newOptions(options...)

	tracer := getTracer(ctx)

	var sp trace.Span
	ctx, sp = tracer.Start(ctx, opts.name, opts.startOptions...)

	return ctx, Span{Span: sp, opts: opts}
}

func newOptions(options ...Option) spanOpts {
	opts := spanOpts{
		startOptions: []trace.SpanStartOption{
			// This option will be overridden if another WithSpanKind() is passed.
			// You can pass this with WithSpanStartOption().
			trace.WithSpanKind(trace.SpanKindInternal),
		},
	}
	var err error
	for _, o := range options {
		opts, err = o(opts)
		if err != nil {
			log.Default().Error(fmt.Sprintf("trace.New: error applying option: %v", err))
		}
	}

	pc, filename, line, _ := runtime.Caller(1)

	if opts.name == "" {
		opts.name = runtime.FuncForPC(pc).Name()
	}

	// Tracing has a limit of 255 characters for the span name.
	if len(opts.name) > 255 {
		// len == 300
		// startShouldBe at: 255 - 303 = -48
		opts.name = "..." + resizeName(opts.name)
	}
	if len(filename) > 255 {
		filename = "..." + resizeName(filename)
	}

	opts.startOptions = append(
		opts.startOptions,
		trace.WithAttributes(
			attribute.String("filename", filename),
			attribute.Int("line", line),
		),
	)

	return opts
}

// 255 -300 = -45 -3 = -48 * -1 =
func resizeName(s string) string {
	return s[(255-len(s)-3)*-1:]
}

var now = time.Now

// Event records an event with name and KeyValue. You can create KeyValue(s) using the attribute package, like
// attribute.String("key", "value"). If the span is not recording, this is a noop.
func (s Span) Event(name string, attrs ...attribute.KeyValue) {
	if !s.IsRecording() {
		return
	}

	name = strings.TrimSpace(name)

	if name == "" {
		log.Default().Error(fmt.Sprintf("trace.Span.Event: must provide an event name"))
		return
	}

	opts := []trace.EventOption{
		trace.WithTimestamp(now()),
	}

	if len(attrs) > 0 {
		opts = append(opts, trace.WithAttributes(attrs...))
	}
	s.Span.AddEvent(
		name,
		opts...,
	)
}

// IsRecording returns true if the span is recording events. This
// is safe to call even if the span is nil.
func (s Span) IsRecording() bool {
	if s.Span == nil || !s.Span.IsRecording() {
		return false
	}
	return true
}

// Status records a status for the span. description is only used if the code is an Error.
// If using the errors.Error type, E() automatically sets the status to Error for the span.
func (s Span) Status(code codes.Code, description string) {
	if !s.IsRecording() {
		return
	}
	s.Span.SetStatus(code, description)
}

// End ends the span. If the status hasn't been set by either calling .Status() or using errors.E(),
// then the status is set to OK. If the span is not recording, this is a no-op.
func (s Span) End() {
	if !s.IsRecording() {
		return
	}

	if sdkSpan, ok := s.Span.(sdkTrace.ReadOnlySpan); ok {
		if sdkSpan.Status().Code == codes.Unset {
			s.Status(codes.Ok, "")
		}
	}

	s.Span.End(s.opts.endOptions...)
}

// getTracer gets a trace.Tracer from the Context or the span. If this cannot find a
// tracer on the Context, it will return a noop tracer.
func getTracer(ctx context.Context) trace.Tracer {
	if ctx == nil {
		log.Default().Error(fmt.Sprintf("bug: getTracer() is being called with a nil Context."))
		return noop.NewTracerProvider().Tracer("noop")
	}
	if t := ctx.Value(baseTrace.TracerKey); t != nil {
		tracer, ok := t.(trace.Tracer)
		if !ok {
			log.Default().Error(fmt.Sprintf("bug: getTracer() found a non-Tracer type(%T) at the TracerKey", t))
			return noop.NewTracerProvider().Tracer("noop")
		}
		return tracer
	}
	return noop.NewTracerProvider().Tracer("noop")
}

// Attributes is a helper collect attributes for a span. If an error is encountered
// while adding attributes, the error will be stored and no more attributes will be added.
// The error can be retrieved by calling Err().
type Attributes struct {
	// Attrs is the list of attributes to be added to the span.
	Attrs []attribute.KeyValue
	err   error
}

// Add adds a key value pair to the Attributes. If the key is already present, the value wil
// added to the attribute list and OTEL package will determine how to handle it.
func (a *Attributes) Add(kv attribute.KeyValue) {
	if kv.Key == "" {
		a.err = errors.Join(a.err, errors.New("key cannot be empty"))
		return
	}

	a.Attrs = append(a.Attrs, kv)
}

// Err returns the error that was set on the Attributes during the Add() method.
func (a *Attributes) Err() error {
	return a.err
}

// Reset clears the Attributes and error.
func (a *Attributes) Reset() {
	a.Attrs = nil
	a.err = nil
}
