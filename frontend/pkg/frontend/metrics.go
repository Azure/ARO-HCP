package frontend

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

import (
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"golang.org/x/exp/maps"

	"github.com/Azure/ARO-HCP/internal/api/arm"
)

// Emitter emits different types of metrics
type Emitter interface {
	EmitCounter(metricName string, value float64, labels map[string]string)
	EmitGauge(metricName string, value float64, labels map[string]string)
}

type PrometheusEmitter struct {
	gauges   map[string]*prometheus.GaugeVec
	counters map[string]*prometheus.CounterVec
}

func NewPrometheusEmitter() *PrometheusEmitter {
	return &PrometheusEmitter{
		gauges:   make(map[string]*prometheus.GaugeVec),
		counters: make(map[string]*prometheus.CounterVec),
	}
}

func (pe *PrometheusEmitter) EmitGauge(name string, value float64, labels map[string]string) {
	vec, exists := pe.gauges[name]
	if !exists {
		labelKeys := maps.Keys(labels)
		vec = prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: name}, labelKeys)
		prometheus.MustRegister(vec)
		pe.gauges[name] = vec
	}
	vec.With(labels).Set(value)
}

func (pe *PrometheusEmitter) EmitCounter(name string, value float64, labels map[string]string) {
	vec, exists := pe.counters[name]
	if !exists {
		labelKeys := maps.Keys(labels)
		vec = prometheus.NewCounterVec(prometheus.CounterOpts{Name: name}, labelKeys)
		prometheus.MustRegister(vec)
		pe.counters[name] = vec
	}
	vec.With(labels).Add(value)
}

type MetricsMiddleware struct {
	Emitter
	cache *Cache
}

type logResponseWriter struct {
	http.ResponseWriter
	statusCode int
}

// WriteHeader captures the status code sent to the client.
func (lrw *logResponseWriter) WriteHeader(code int) {
	lrw.statusCode = code
	lrw.ResponseWriter.WriteHeader(code)
}

// Metrics middleware to capture response time and status code
func (mm MetricsMiddleware) Metrics() MiddlewareFunc {
	return func(w http.ResponseWriter, r *http.Request, next http.HandlerFunc) {
		startTime := time.Now()

		lrw := &logResponseWriter{ResponseWriter: w}

		next(lrw, r) // Process the request

		// Get the route pattern that matched
		routePattern := r.URL.Path
		duration := time.Since(startTime).Milliseconds()

		subscriptionState := "Unknown"
		subscriptionId := r.PathValue(PathSegmentSubscriptionID)
		if subscriptionId != "" {
			sub, exists := mm.cache.GetSubscription(subscriptionId)
			if !exists {
				arm.WriteError(
					w, http.StatusBadRequest,
					arm.CloudErrorInvalidSubscriptionState, "",
					UnregisteredSubscriptionStateMessage,
					subscriptionId)
				return
			}

			subscriptionState = string(sub.State)
		}

		mm.Emitter.EmitCounter("frontend_count", 1.0, map[string]string{
			"verb":        r.Method,
			"api_version": r.URL.Query().Get(APIVersionKey),
			"code":        strconv.Itoa(lrw.statusCode),
			"route":       routePattern,
			"state":       subscriptionState,
		})

		mm.Emitter.EmitGauge("frontend_duration", float64(duration), map[string]string{
			"verb":        r.Method,
			"api_version": r.URL.Query().Get(APIVersionKey),
			"code":        strconv.Itoa(lrw.statusCode),
			"route":       routePattern,
		})
	}
}
