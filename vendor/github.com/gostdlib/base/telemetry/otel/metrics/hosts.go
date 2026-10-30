package metrics

import (
	"fmt"
	"time"

	"github.com/gostdlib/base/telemetry/log"
	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/contrib/instrumentation/host"
	otelruntime "go.opentelemetry.io/contrib/instrumentation/runtime"
	"go.opentelemetry.io/otel"
	otelprometheus "go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.4.0"
)

func initer(meta *resource.Resource, port uint16) error {
	if fmt.Sprintf("%T", defaultProvider) == fmt.Sprintf("%T", noop.NewMeterProvider()) {
		otel.SetMeterProvider(defaultProvider)
		return nil
	}

	if meta == nil {
		return fmt.Errorf("resource must not be nil")
	}

	if len(meta.Attributes()) == 0 {
		return fmt.Errorf("resource must have at least one attribute")
	}

	found := false
	for _, attr := range meta.Attributes() {
		if attr.Key != semconv.ServiceNameKey {
			continue
		}
		if attr.Value.AsString() == "" {
			return fmt.Errorf("cannot have a metrics service key with an empty value")
		}
		found = true
		break
	}

	if !found {
		return fmt.Errorf("metrics.Init()resource must have a service name attribute")
	}

	var meterProvider metric.MeterProvider

	if defaultProvider == nil {
		metricExporter, err := otelprometheus.New(otelprometheus.WithRegisterer(prometheus.DefaultRegisterer))
		if err != nil {
			return fmt.Errorf("failed to create metrics exporter: %w", err)
		}
		meterProvider = sdkmetric.NewMeterProvider(
			sdkmetric.WithReader(metricExporter),
			sdkmetric.WithResource(meta),
		)
	} else {
		meterProvider = defaultProvider
	}

	// use global meter provider
	otel.SetMeterProvider(meterProvider)
	defaultProvider = meterProvider

	// Setup runtime metrics.
	if err := otelruntime.Start(otelruntime.WithMinimumReadMemStatsInterval(10 * time.Second)); err != nil {
		log.Default().Error(fmt.Sprintf("failed to start runtime metrics: %v", err))
	}

	// Setup host metrics.
	if err := host.Start(); err != nil {
		log.Default().Error(fmt.Sprintf("failed to start host metrics: %v", err))
	}

	go func() {
		if err := serveMetrics(uint16(port)); err != nil {
			log.Default().Error(fmt.Sprintf("metric http server stop: %v", err))
		}
	}()

	return nil
}
