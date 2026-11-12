// Package context is a drop in replacement for the standard library's context package.
// It provides additional functionality to the context package, such as attaching various clients
// to the context when calling Background().  This should be called after init.Service() to ensure
// that the clients are initialized. All clients that are attached to the context should return a
// a value that is safe to call methods on.
package context

import (
	"context"
	"log/slog"
	"runtime"
	"sync"
	"time"

	"github.com/gostdlib/base/concurrency/background"
	"github.com/gostdlib/base/concurrency/worker"
	internalCtx "github.com/gostdlib/base/internal/context"
	ierr "github.com/gostdlib/base/internal/errors"
	"github.com/gostdlib/base/telemetry/log"
	"github.com/gostdlib/base/telemetry/otel/metrics"
	"github.com/gostdlib/base/telemetry/otel/trace/span"

	"go.opentelemetry.io/otel/metric"
	metricsnoop "go.opentelemetry.io/otel/metric/noop"
)

// Types for keys used to attach clients to the context.
type (
	loggerKey  struct{}
	poolKey    struct{}
	tasksKey   struct{}
	attrsKey   struct{}
	metricsKey = internalCtx.MetricsKey
)

var contextOnce sync.Once
var backgroundContext Context

// Background returns a non-nil, empty [Context]. It is never canceled, and has no deadline.
// It is typically used by the main function, initialization, and tests, and as the top-level
// Context for incoming requests. This differs from the Background() function in the context package
// in that it attaches various clients to the context. This currently attaches:
//
// - log.Default(), a *slog.Logger.
// - metrics.Default(), a metric.MeterProvider.
// - worker.Default(), a *worker.Pool.
// - background.Default(), a *background.Tasks.
//
// These can be accessed using the Audit()/Log()/Metrics functions.
func Background() Context {
	contextOnce.Do(
		func() {
			ctx := context.Background()
			backgroundContext = Attach(ctx)
		},
	)
	return backgroundContext
}

// ResetBackground resets the cached background context, forcing it to be
// rebuilt with current defaults on the next call to Background().
// This is called by init.Service() after all defaults have been configured.
func ResetBackground() {
	contextOnce = sync.Once{}
	backgroundContext = nil
}

// Attach attaches the audit, logger, and metrics clients to the context.
// This is generally not called directly, but is used by Background() and
// things like RPC packages that need to attach these to an already existing context.
func Attach(ctx Context) Context {
	ctx = WithValue(ctx, loggerKey{}, log.Default())
	ctx = WithValue(ctx, metricsKey{}, metrics.Default())
	ctx = WithValue(ctx, poolKey{}, worker.Default())
	ctx = WithValue(ctx, tasksKey{}, background.Default())
	return ctx
}

// Logger is a wrapper around an *slog.Logger that prevents loss of Context logging attributes.
type Logger struct {
	ctx    context.Context
	logger *slog.Logger
}

// Debug logs at [LevelDebug].
func (l Logger) Debug(msg string, args ...any) {
	l.log(slog.LevelDebug, msg, args...)
}

// Enabled reports whether l emits log records at the given context and level.
func (l Logger) Enabled(level slog.Level) bool {
	return l.logger.Enabled(l.ctx, level)
}

// Error logs at [LevelError].
func (l Logger) Error(msg string, args ...any) {
	l.log(slog.LevelError, msg, args...)
}

// Handler returns l's Handler.
func (l Logger) Handler() slog.Handler {
	return l.logger.Handler()
}

// Logger returns the underlying *slog.Logger.
func (l Logger) Logger() *slog.Logger {
	return l.logger
}

// Info logs at [LevelInfo].
func (l Logger) Info(msg string, args ...any) {
	l.log(slog.LevelInfo, msg, args...)
}

// Log emits a log record with the current time and the given level and message.
// The Record's Attrs consist of the Logger's attributes followed by
// the Attrs specified by args.
//
// The attribute arguments are processed as follows:
//   - If an argument is an Attr, it is used as is.
//   - If an argument is a string and this is not the last argument,
//     the following argument is treated as the value and the two are combined
//     into an Attr.
//   - Otherwise, the argument is treated as a value with key "!BADKEY".
func (l Logger) Log(level slog.Level, msg string, args ...any) {
	l.log(level, msg, args...)
}

// LogAttrs is a more efficient version of [Logger.Log] that accepts only Attrs. If the Logger was created with a
// Context with attributes, both those and the Context passed will be set. If those are in conflict, the one passed
// here will take precedence.
func (l Logger) LogAttrs(ctx context.Context, level slog.Level, msg string, attrs ...slog.Attr) {
	l.logAttrs(ctx, level, msg, attrs...)
}

// Warn logs at [LevelWarn].
func (l Logger) Warn(msg string, args ...any) {
	l.log(slog.LevelWarn, msg, args...)
}

// With returns a Logger that includes the given attributes
// in each output operation. Arguments are converted to
// attributes as if by [Logger.Log].
func (l Logger) With(args ...any) Logger {
	if len(args) == 0 {
		return l
	}
	return Logger{ctx: l.ctx, logger: l.logger.With(args...)}
}

// WithGroup returns a Logger that starts a group, if name is non-empty.
// The keys of all attributes added to the Logger will be qualified by the given
// name. (How that qualification happens depends on the [Handler.WithGroup]
// method of the Logger's Handler.)
//
// If name is empty, WithGroup returns the receiver.
func (l Logger) WithGroup(name string) Logger {
	if name == "" {
		return l
	}
	return Logger{ctx: l.ctx, logger: l.logger.WithGroup(name)}
}

// log is the low-level logging method for methods that take ...any.
// It must always be called directly by an exported logging method
// or function, because it uses a fixed call depth to obtain the pc.
func (l Logger) log(level slog.Level, msg string, args ...any) {
	if l.ctx == nil {
		l.ctx = context.Background()
	}
	if !l.Enabled(level) {
		return
	}

	var pc uintptr
	var pcs [1]uintptr
	// skip [runtime.Callers, this function, this function's caller]
	runtime.Callers(3, pcs[:])
	pc = pcs[0]

	r := slog.NewRecord(time.Now(), level, msg, pc)
	r.AddAttrs(Attrs(l.ctx)...)
	r.Add(args...)
	_ = l.Handler().Handle(l.ctx, r)
}

// logAttrs is like [Logger.log], but for methods that take ...Attr.
func (l Logger) logAttrs(ctx context.Context, level slog.Level, msg string, attrs ...slog.Attr) {
	if ctx == nil {
		ctx = context.Background()
	}
	if !l.Enabled(level) {
		return
	}

	var pc uintptr
	var pcs [1]uintptr
	// skip [runtime.Callers, this function, this function's caller]
	runtime.Callers(3, pcs[:])
	pc = pcs[0]

	r := slog.NewRecord(time.Now(), level, msg, pc)
	r.AddAttrs(Attrs(l.ctx)...) // Must be first
	r.AddAttrs(Attrs(ctx)...)
	r.AddAttrs(attrs...)
	_ = l.Handler().Handle(ctx, r)
}

// Log returns a Logger with the *slog.Logger that is attached to the context. If no logger is attached,
// it returns Logger wrapping log.Default().
func Log(ctx Context) Logger {
	a := ctx.Value(loggerKey{})
	if a == nil {
		return Logger{ctx: ctx, logger: log.Default()}
	}
	l, ok := a.(*slog.Logger)
	if !ok {
		return Logger{ctx: ctx, logger: log.Default()}
	}
	return Logger{ctx: ctx, logger: l}
}

// Meter returns a metric.Meter scoped to the package that calls context.Meter(). If you need to have a
// sub-namespace for a specific package, you should use the MeterProvider() function to get the meter provider.
// If no meter is attached to the context it returns a meter from metrics.Default(). This may be a noop Meter.
func Meter(ctx Context, opts ...metric.MeterOption) metric.Meter {
	const stackFrame = 3

	return MeterWithStackFrame(ctx, stackFrame, opts...)
}

// MeterWithStackFrame returns a metric.Meter scoped to the stack frame number provided by "sf".
// This is for uses by packages that use this underneath so they can get the write stack frame.
// Generally, you should be using Meter().
func MeterWithStackFrame(ctx Context, sf uint, opts ...metric.MeterOption) metric.Meter {
	a := ctx.Value(metricsKey{})
	if a == nil {
		return metrics.Default().Meter(metrics.MeterName(int(sf)), opts...)
	}
	l, ok := a.(metric.MeterProvider)
	if !ok {
		return metricsnoop.NewMeterProvider().Meter("")
	}
	return l.Meter(metrics.MeterName(int(sf)), opts...)
}

// MeterProvider returns a metric.MeterProvider attached to the context. If no meter provider is attached,
// it returns metrics.Default(). This may be a noop provider.
func MeterProvider(ctx Context) metric.MeterProvider {
	return internalCtx.MeterProvider(ctx)
}

// NewSpan creates a new child span object from the span stored in Context. If that Span is
// a noOp, the child span will be a noop too. If you pass a nil Context, this will return
// the background Context with a noop span. If an option is passed that is not valid,
// it is ignored and an error is logged. The span is created with a span kind of internal,
// unless another span kind is passed using WithSpanStartOption(WithSpanKind([kind])).
// This starts the span.
func NewSpan(ctx Context, options ...span.Option) (Context, span.Span) {
	return span.New(ctx, options...)
}

// Span returns the current span from the context. If no span is attached, it returns a noop span.
func Span(ctx Context) span.Span {
	return span.Get(ctx)
}

// Pool returns the worker pool attached to the context. If no pool is attached, it returns worker.Default().
func Pool(ctx Context) *worker.Pool {
	a := ctx.Value(poolKey{})
	if a == nil {
		return worker.Default()
	}
	p, ok := a.(*worker.Pool)
	if !ok {
		return worker.Default()
	}
	return p
}

// Tasks returns a background.Tasks attached to the context. If not tasks are attached,
// it returns background.Default().
func Tasks(ctx Context) *background.Tasks {
	a := ctx.Value(tasksKey{})
	if a == nil {
		return background.Default()
	}
	t, ok := a.(*background.Tasks)
	if !ok {
		return background.Default()
	}
	return t
}

// AddAttrs adds slog.Attr attributes to the context. These attributes can be used by logging,
// tracing or errors packages to add additional context to logs, traces or errors. Duplicate attr
// keys are allowed, but upper layer packages will apply the last value for a given key.
// If ctx is nil, it will be set to context.Background().
func AddAttrs(ctx context.Context, attrs ...slog.Attr) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	a := ctx.Value(attrsKey{})
	if a == nil {
		ctx = context.WithValue(ctx, attrsKey{}, attrs)
		return ctx
	}
	s := make([]slog.Attr, 0, len(a.([]slog.Attr))+len(attrs))
	s = append(s, a.([]slog.Attr)...)
	s = append(s, attrs...)
	return context.WithValue(ctx, attrsKey{}, s)
}

// Attrs returns the slog.Attr attributes attached to the context. If no attributes are attached, it returns nil.
func Attrs(ctx context.Context) []slog.Attr {
	if ctx == nil {
		return nil
	}
	a := ctx.Value(attrsKey{})
	if a == nil {
		return nil
	}
	return a.([]slog.Attr)
}

// SetShouldTrace attaches a boolean value to the context to indicate if the request should be traced.
// This is not usually used by a service, but by the middleware to determine if the request should
// be traced. This only works if done before the trace is started.
func SetShouldTrace(ctx context.Context, b bool) context.Context {
	return internalCtx.SetShouldTrace(ctx, b)
}

// ShouldTrace returns true if the request has had SetShouldTrace called on it.
func ShouldTrace(ctx context.Context) bool {
	return internalCtx.ShouldTrace(ctx)
}

// EOptions returns the error options attached to the context. If no options are attached, it returns nil.
// This allows for setting per call error options. These will override local options if the same options are set.
// An example of this is writing a traceback to errors on a specific call or all calls.
func EOptions(ctx context.Context) []ierr.EOption {
	return internalCtx.EOptions(ctx)
}

// SetEOptions attaches error options to the context. This allows for setting per call error options.
// These will override local options if the same options are set. An example of this is writing a traceback
// to errors on a specific call or all calls.
func SetEOptions(ctx context.Context, options ...ierr.EOption) context.Context {
	return internalCtx.SetEOptions(ctx, options...)
}
