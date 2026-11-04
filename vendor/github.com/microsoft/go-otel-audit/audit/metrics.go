package audit

import (
	"fmt"
	"sync/atomic"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"
)

type metrics struct {
	meter             metric.Meter
	msgsSent          metric.Int64Counter
	msgsRequeued      metric.Int64Counter
	msgsDropped       metric.Int64Counter
	msgErrs           metric.Int64Counter
	heartbeatDropped  metric.Int64Counter
	diagnosticDropped metric.Int64Counter

	// requeuedCounter exists to track the number of requeued messages in a test.
	// You cannot extract the value from an otel counter.
	requeuedCounter atomic.Uint64
}

func newMetrics() (*metrics, error) {
	m := &metrics{
		meter: otel.GetMeterProvider().Meter("github.com/microsoft/go-otel-audit/audit"),
	}
	var err error
	m.msgsSent, err = m.meter.Int64Counter("messages_sent")
	if err != nil {
		return nil, fmt.Errorf("failed to create messages_sent counter: %w", err)
	}
	m.msgsRequeued, err = m.meter.Int64Counter("messages_requeued")
	if err != nil {
		return nil, fmt.Errorf("failed to create messages_requeued counter: %w", err)
	}
	m.msgsDropped, err = m.meter.Int64Counter("messages_dropped")
	if err != nil {
		return nil, fmt.Errorf("failed to create messages_dropped counter: %w", err)
	}
	m.msgErrs, err = m.meter.Int64Counter("messages_errors")
	if err != nil {
		return nil, fmt.Errorf("failed to create messages_errors counter: %w", err)
	}
	m.heartbeatDropped, err = m.meter.Int64Counter("heartbeat_dropped")
	if err != nil {
		return nil, fmt.Errorf("failed to create heartbeat_dropped counter: %w", err)
	}
	return m, nil
}
