// Package log is a replacement for the standard log package based on slog. This should not be used unless
// writing debug statements. Most logging happens at the service level packages. Choose tracing over logging
// for production debugging.
package log

import (
	"fmt"
	"log"
	"log/slog"
	"os"
	"sync/atomic"

	"github.com/gostdlib/base/env/detect"
)

var defaultLog = atomic.Pointer[slog.Logger]{}

func init() {
	l := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{AddSource: true, Level: LogLevel}))
	defaultLog.Store(l)
}

// LogLevel is the log level for the program. This is used to set the log level for the program.
// This is automatically set for the default logger unless .Set() is used to switch out that logger.
// If the new logger is created from the adapter package, it uses this LogLevel.
// If not, you must pass this to your logger manually.
var LogLevel = new(slog.LevelVar) // Info by default

// Default returns the default logger. This logger should only be used outside main() and the
// gRPC service level if there is code running in a goroutine outside the request handling
// that needs to log messages or you need to pass the logger to a function outside of medbay.
// Anything other use of this logger (or any logger) is verboten.
func Default() *slog.Logger {
	l := defaultLog.Load()
	if l == nil {
		return slog.Default()
	}
	return l
}

// Set sets the logger returned by Default().
// This must be done in main() before any logging is done to avoid a
// concurrency issue.
func Set(l *slog.Logger) {
	defaultLog.Store(l)
	slog.SetDefault(l)
}

// Everything below this is from the standard log package. If it detects we are running in production
// it sends log messages to slog as a Debug message. Otherwise it acts normally. Fatals call panic
// in production to ensure we capture the output.

// Fatal is equivalent to [Print] followed by a call to os.Exit(1).
// If called in production, it will panic instead of exiting.
func Fatal(v ...any) {
	if detect.Env().Prod() {
		panic(fmt.Sprint(v...))
	}
	log.Fatal(v...)
}

// Fatalf is equivalent to [Printf] followed by a call to os.Exit(1).
// If called in production, it will panic instead of exiting.
func Fatalf(format string, v ...any) {
	if detect.Env().Prod() {
		panic(fmt.Sprintf(format, v...))
	}
	log.Fatalf(format, v...)
}

// Fatalln is equivalent to [Println] followed by a call to os.Exit(1).
// If called in production, it will panic instead of exiting.
func Fatalln(v ...any) {
	if detect.Env().Prod() {
		panic(fmt.Sprint(v...) + "\n")
	}
	log.Fatalln(v...)
}

// Flags returns the output flags for the standard logger. The flag bits are [Ldate], [Ltime], and so on.
func Flags() int {
	return log.Flags()
}

// Panic is equivalent to [Print] followed by a call to panic().
func Panic(v ...any) {
	if detect.Env().Prod() {
		panic(fmt.Sprint(v...))
	}
	log.Panic(v...)
}

// Panicf is equivalent to [Printf] followed by a call to panic().
func Panicf(format string, v ...any) {
	if detect.Env().Prod() {
		panic(fmt.Sprintf(format, v...))
	}
	log.Panicf(format, v...)
}

// Panicln is equivalent to [Println] followed by a call to panic().
func Panicln(v ...any) {
	if detect.Env().Prod() {
		defaultLog.Load().Debug(fmt.Sprint(v...))
	}
	log.Panicln(v...)
}

// Prefix returns the output prefix for the standard logger.
func Prefix() string {
	return log.Prefix()
}

// Print calls Output to print to the standard logger. Arguments are handled in the manner of fmt.Print.
func Print(v ...any) {
	if detect.Env().Prod() {
		defaultLog.Load().Debug(fmt.Sprint(v...))
	}
	log.Print(v...)
}

// Printf calls Output to print to the standard logger. Arguments are handled in the manner of fmt.Printf.
func Printf(format string, v ...any) {
	if detect.Env().Prod() {
		defaultLog.Load().Debug(fmt.Sprintf(format, v...))
	}
	log.Printf(format, v...)
}

// Println calls Output to print to the standard logger. Arguments are handled in the manner of fmt.Println.
func Println(v ...any) {
	if detect.Env().Prod() {
		defaultLog.Load().Debug(fmt.Sprintf("%v\n", v...))
	}
	log.Println(v...)
}

// SetFlags sets the output flags for the standard logger. The flag bits are [Ldate], [Ltime], and so on.
func SetFlags(flag int) {
	log.SetFlags(flag)
}
