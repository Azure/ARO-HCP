package worker

import (
	"go.opentelemetry.io/otel/metric"
)

type poolMetrics struct {
	meter metric.Meter
	// StaticExists is the number of goroutines that always exist. This can go to 0
	// if the pool is closed.
	StaticExists metric.Int64UpDownCounter
	// StaticRunning is the number of static goroutines that are currently
	// being used.
	StaticRunning metric.Int64UpDownCounter
	// DynamicExist is the number of dynamic goroutines that exist.
	DynamicExists metric.Int64UpDownCounter
	// DynamicRunning is the number of no-reusable goroutines that are
	// currently being used.
	DynamicRunning metric.Int64UpDownCounter
	// DynamicTotal is the total number of dynamic goroutines that have been created.
	DynamicTotal metric.Int64Counter
}

func newPoolMetrics(m metric.Meter) *poolMetrics {
	se, err := m.Int64UpDownCounter("static_exists", metric.WithDescription("The number of static goroutines that always exist."))
	if err != nil {
		panic(err)
	}
	sr, err := m.Int64UpDownCounter("static_running", metric.WithDescription("The number of static goroutines that are currently in use."))
	if err != nil {
		panic(err)
	}
	de, err := m.Int64UpDownCounter("dynamic_exists", metric.WithDescription("The number of dynamic goroutines that exist."))
	if err != nil {
		panic(err)
	}
	dr, err := m.Int64UpDownCounter("dynamic_running", metric.WithDescription("The number of dynamic goroutines that are currently in use."))
	if err != nil {
		panic(err)
	}
	dt, err := m.Int64Counter("dynamic_total", metric.WithDescription("The total number of dynamic goroutines that have been created."))
	if err != nil {
		panic(err)
	}

	return &poolMetrics{
		meter:          m,
		StaticExists:   se,
		StaticRunning:  sr,
		DynamicExists:  de,
		DynamicRunning: dr,
		DynamicTotal:   dt,
	}
}
