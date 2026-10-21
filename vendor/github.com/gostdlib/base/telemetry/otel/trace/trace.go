/*
Package span provides access to OpenTelemetry Span objects for the purpose of logging events.

OTEL spans are not well defined for scope. So the standard for us will generally be function wide spans.
This means we will create a child span for each function call. This will allow us to track at minimum the timing
of each function call.

Generally you should use this package where you would use the log package to record events or information not
related to security. Security information such as AAA should use the audit package (or be automatically configured
at the RPC service) and errors are automatically logged by the errors package.

In addition, this package is tied with the errors and log packages. An errors.Error will automatically be recorded
for the span with no additional work. The log package will write any usage errors from ths package, as to not
stall or require error checking in the main code.

This code is taken and expand from github.com/gostdlib/internal/trace .

This package defaults to using the OTEL stdout exporter. If the environment variable "TracingEndpoint" is set,
this will be used to send traces to an OTEL collector. Tracing collectors seem to be setup as insecure by default,
so this uses insecure connections.

If TracingEndpoint is not set and if it is not a production environment, the stdout exporter will be used to
send to stderr. If this is not needed, --localTraceDisable can be set to true to disable the stdout exporter.

The default production sampler is a filter based sampler that can be updated to capture certain traces based
on metadata. It has a secondary sampler that is set to --traceSampleRate, which defaults to 0.01 or 1%.

The default production sampler can be overridden by calling Set(tp *sdkTrace.TracerProvider) before
init.Service() is called. The new trace provider can have a different sampler or other settings.

If you simply want to adjust the sampling rate, you can use the flag --traceSampleRate.
*/
package trace

import (
	"context"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/gostdlib/base/env/detect"
	"github.com/gostdlib/base/internal/envvar"
	"github.com/gostdlib/base/telemetry/log"
	"github.com/gostdlib/base/telemetry/otel/trace/sampler"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdkTrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials/insecure"
)

// tracerKeyType is the key type for the tracer in the context.
type tracerKeyType int

var TracerKey = tracerKeyType(0)
var TracerNameKey = tracerKeyType(1)

var defaultTP *sdkTrace.TracerProvider
var once sync.Once

// Default returns the default trace provider. Normally not required by an end user.
// TraceProviders are not normally used, instead spans are done via the .../otel/trace/span package
// and taken from a context. If your program does not take input from a base/rpc package,
// you may use this to supply an initial span to a context.
//
// Example:
//
//	tp := trace.Default()
//	tracer := otel.Tracer("example-tracer")
//	var sp trace.Span
//	ctx, sp := tracer.Start(ctx, opts.name, opts.startOptions...)
func Default() *sdkTrace.TracerProvider {
	return defaultTP
}

// Set will set the default trace provider. This can be used to override the
// TraceProvider before init.Service() is called. The other use is for testing.
func Set(tp *sdkTrace.TracerProvider) {
	defaultTP = tp
}

// Init initializes the trace package. This should be called once at the beginning of the program.
// Normally this is done by init.Service(). Can only be called once.
func Init(disable bool, sampleRate float64) error {
	// Our check if defaultTP != nil is in Init() so that Once() will fire.

	// Default sample rate is 1%.
	if sampleRate == 0 {
		sampleRate = 0.01
	}

	i := newIniter(os.Getenv(envvar.TracingEndpoint), disable, sampleRate)

	var err error
	once.Do(
		func() {
			err = i.Init()
		},
	)
	return err
}

// initer is a helper struct to initialize the trace package.
// It is used to allow for testing.
type initer struct {
	env               detect.RunEnv
	localTraceDisable bool
	sampleRate        float64
	endpoint          string

	prodProvider     func(context.Context, string, float64) (*sdkTrace.TracerProvider, error)
	localProvider    func(context.Context, io.Writer) (*sdkTrace.TracerProvider, error)
	setTraceProvider func(tp trace.TracerProvider)
}

// newIniter creates a new initer.
func newIniter(endpoint string, localTraceDisable bool, sampleRate float64) initer {
	return initer{
		env:               detect.Env(),
		localTraceDisable: localTraceDisable,
		endpoint:          endpoint,
		sampleRate:        sampleRate,
		prodProvider:      prodProvider,
		localProvider:     localProvider,
		setTraceProvider:  otel.SetTracerProvider,
	}
}

// Init initializes the trace package.
func (i *initer) Init() error {
	ctx := context.Background()

	if defaultTP != nil {
		return nil
	}
	defer func() {
		if defaultTP != nil {
			// sdkTrace.Sampler
			i.setTraceProvider(defaultTP)

			// set global propagator to tracecontext (the default is no-op).
			otel.SetTextMapPropagator(propagation.TraceContext{})
		}
	}()

	var err error
	switch i.env.Prod() {
	case true:
		if i.endpoint != "" {
			defaultTP, err = i.prodProvider(ctx, i.endpoint, i.sampleRate)
			if err != nil {
				return err
			}
			return nil
		}
		log.Default().Error("prod environment detected, but no TracingEndpoint set, tracing disabled")
		return nil
	default:
		if i.localTraceDisable {
			return nil
		}
		defaultTP, err = i.localProvider(ctx, os.Stderr)
		if err != nil {
			return fmt.Errorf("could not create a new stdout trace provider: %v", err)
		}
	}

	return nil
}

var connTimeout = 5 * time.Second

// If the OpenTelemetry Collector is running on a kubernetes cluster (AKS, GKE, EKS, etc.),
// it should be accessible through "opentelemetry-collector.<namespace>.svc.cluster.local:<port>".
func prodProvider(ctx context.Context, endpoint string, sampleRate float64) (*sdkTrace.TracerProvider, error) {
	// If the OpenTelemetry Collector is running on a kubernetes cluster (AKS, GKE, EKS, etc.),
	// it should be accessible through "opentelemetry-collector.<namespace>.svc.cluster.local:<port>".
	if endpoint == "" {
		return nil, fmt.Errorf("trace.prodProvider: endpoint is empty")
	}

	res, err := resources(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create opentelemetry resource: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	conn, err := grpc.NewClient(endpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("failed to create gRPC connection to OTEL collector: %w", err)
	}

	// grpc's new API sucks. This is the only way to check if the connection is ready that isn't deprecated.
	// Because, hey, maybe I want to do something else if the endpoint isn't there.
	// Now, this simply checks that grpc answers, but not that it is the otel collector.
	conn.Connect()
	start := time.Now()
	for conn.GetState() != connectivity.Ready {
		if time.Since(start) > connTimeout {
			break
		}
	}
	if conn.GetState() != connectivity.Ready {
		return nil, fmt.Errorf("could not connect to the otel grpc endpoint(%s)", endpoint)
	}

	// Set up a trace exporter
	exp, err := otlptracegrpc.New(ctx, otlptracegrpc.WithGRPCConn(conn))
	if err != nil {
		return nil, fmt.Errorf("failed to create otel trace exporter: %w", err)
	}

	s := sampler.DefaultSampler(sampleRate)

	bsp := sdkTrace.NewBatchSpanProcessor(exp, sdkTrace.WithBatchTimeout(5*time.Second))
	tp := sdkTrace.NewTracerProvider(
		sdkTrace.WithSampler(s),
		sdkTrace.WithResource(res),
		sdkTrace.WithSpanProcessor(bsp),
	)

	return tp, nil
}

// localProvider creates a new trace provider that writes to stderr. This always samples.
func localProvider(ctx context.Context, w io.Writer) (*sdkTrace.TracerProvider, error) {
	res, err := resources(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create opentelemetry resource: %w", err)
	}

	exp, err := stdouttrace.New(
		stdouttrace.WithWriter(w),
		stdouttrace.WithPrettyPrint(),
	)
	if err != nil {
		return nil, err
	}

	bsp := sdkTrace.NewBatchSpanProcessor(exp, sdkTrace.WithBatchTimeout(1*time.Second))
	tp := sdkTrace.NewTracerProvider(
		sdkTrace.WithSampler(sdkTrace.AlwaysSample()),
		sdkTrace.WithResource(res),
		sdkTrace.WithSpanProcessor(bsp),
	)

	return tp, nil
}

// resources creates a new resource with global information.
func resources(ctx context.Context) (*resource.Resource, error) {
	// set global information with resource
	// https://opentelemetry.io/docs/languages/go/resources/
	return resource.New(
		ctx,
		resource.WithTelemetrySDK(),
		resource.WithOS(),
		resource.WithContainer(),
		resource.WithHost(),
		resource.WithProcess(),
	)
}

// Close shuts down the trace provider. This should be called at the end of the program.
// Normally this is done by init.Close().
func Close() {
	if defaultTP != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		defaultTP.Shutdown(ctx)
	}
}


