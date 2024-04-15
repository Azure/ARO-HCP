package metrics

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

// Emitter emits different types of metrics
type Emitter interface {
	EmitCounter(metricName string, value float64, labels map[string]string)
	EmitGauge(metricName string, value float64, labels map[string]string)
}
