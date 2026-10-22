// Package metrics sets up the OpenTelemetry metrics provider and Prometheus exporter. It also provides a
// Fiber middleware to expose the Prometheus metrics endpoint. The default port is 2223.
// The package also provides a default meter provider that can be used to create meters.
package metrics

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gostdlib/base/telemetry/log"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/adaptor"
)

const defaultPort uint16 = 2223

var defaultProvider metric.MeterProvider

// Default returns the default meter provider. If the default provider is currently nil,
// this will return a noop provider. That should only happen if you call Default() before
// calling init.Service(). If you want to disable metrics, you can use SetDefault()
// with a noop.NewMeterProvider().
func Default() metric.MeterProvider {
	if defaultProvider == nil {
		return noop.NewMeterProvider()
	}
	return defaultProvider
}

// Set sets the default meter provider. This is only used if trying to use a custom meter provider
// before calling init.Service(). You can use noop.NewMeterProvider() to disable metrics.
// "go.opentelemetry.io/otel/metric/noop".
func Set(p metric.MeterProvider) {
	defaultProvider = p
	otel.SetMeterProvider(p)
}

// MeterName returns the import path of the package containing the function calling MeterName().
// If this can't be determined "unknown" will be returned. This is used to create meters with a name
// that is unique to the package. Level is the number of stack frames to go back. If you are calling
// this function from a function that is not the direct caller of the meter, you will need to adjust
// the level. For example, if you are calling this function from a function that is called by the
// function that creates the meter, you will need to set level to 2. Generally this is set to 1.
//
// The returned name will be:
//
// [package path]/[package name]
//
// For example:
// "github.com/user/project/pkgName"
func MeterName(stackFrame int) string {
	pc, _, _, ok := runtime.Caller(stackFrame)
	if !ok {
		return "unknown"
	}

	// Get the details of the caller function
	fn := runtime.FuncForPC(pc)
	if fn == nil {
		return "unknown"
	}

	// Extract the package path from the fully qualified function name
	fullName := fn.Name() // e.g., "github.com/user/project/pkg.subpkg.(*MyStruct).MyMethod"
	lastSlash := strings.LastIndex(fullName, "/")

	// This only happens when I'm running in the playground where we don't really have a path.
	if lastSlash == -1 {
		sp := strings.Split(fullName, ".")
		if len(sp) > 1 {
			return sp[0]
		}
		return fullName
	}

	// Extract the part before the function name, up to the package path
	return fullName[:strings.Index(fullName[lastSlash:], ".")+lastSlash]
}

var mu sync.Mutex
var called bool

// Init initializes the metric provider. This is usually called by init.Service().
// If the default provider is already set, this function will return nil. This will automatically
// provide the runtime and host instrumentation from the contrib set of package. The default
// provider at this time is a Prometheus exporter. Metrics are only enabled if running
// in a K8 environment. If called more than once, this will panic.
func Init(meta *resource.Resource, port uint16) error {
	mu.Lock()
	defer mu.Unlock()

	if called {
		panic("metrics.Init() can only be called once")
	}

	return initer(meta, port)
}

var app atomic.Pointer[fiber.App]

// Close closes the metric provider and the http server.
func Close() {
	p := Default()

	wg := sync.WaitGroup{}

	if p != nil {
		if v, ok := p.(*sdkmetric.MeterProvider); ok {
			wg.Add(1)
			go func() {
				defer wg.Done()
				v.Shutdown(context.Background())
			}()
		}
	}

	a := app.Load()
	if a != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			a.Shutdown()
			app.CompareAndSwap(a, nil)
		}()
	}

	wg.Wait()
}

// serveMetrics creates a new http server that serves Prometheus metrics
// at 0:<port>/metrics. This a blocking function.
func serveMetrics(port uint16) error {
	if port == 0 {
		port = defaultPort
	}

	config := fiber.Config{
		Concurrency:           10,
		DisableStartupMessage: true,
		IdleTimeout:           5 * time.Minute,
		ReadTimeout:           10 * time.Second,
		WriteTimeout:          20 * time.Second,
	}

	// Initialize a new Fiber app
	a := fiber.New(config)
	if !app.CompareAndSwap(nil, a) {
		if !testing.Testing() {
			panic("metrics http server already started")
		}
	}

	// Define a route for the GET method on the root path '/'
	a.Get(
		"/metrics",
		adaptor.HTTPHandler(
			promhttp.HandlerFor(
				prometheus.DefaultGatherer,
				promhttp.HandlerOpts{
					MaxRequestsInFlight: 10,
				},
			),
		),
	)
	addr := fmt.Sprintf(":%d", port)
	log.Default().Info(fmt.Sprintf("serving metrics at %s/metrics", addr))
	return a.Listen(addr)
}
