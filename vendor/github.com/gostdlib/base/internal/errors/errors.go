// Package errors exists to avoid some import cycles.
package errors

import "log/slog"

// EOpts is the options for E(). Defaults are set by errors.E().
type EOpts struct {
	// SuppressTraceErr is an option to suppress the trace error.
	// This is useful on retries where you don't want to see the same error.
	SuppressTraceErr bool
	// CallNum is the number of calls to skip in the stack trace.
	CallNum int
	// StackTrace is an option to include the stack trace.
	StackTrace bool
	// Attrs are additional attributes to include in the error.
	Attrs []slog.Attr
}

// EOption is an optional argument for E().
type EOption func(EOpts) EOpts
