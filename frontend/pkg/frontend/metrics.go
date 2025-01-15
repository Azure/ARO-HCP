package frontend

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

import (
	"net/http"
	"regexp"
	"strconv"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"golang.org/x/exp/maps"

	"github.com/Azure/ARO-HCP/internal/database"
)

// Emitter emits different types of metrics
type Emitter interface {
	AddCounter(metricName string, value float64, labels map[string]string)
	EmitGauge(metricName string, value float64, labels map[string]string)
}

type PrometheusEmitter struct {
	mutex    sync.Mutex
	gauges   map[string]*prometheus.GaugeVec
	counters map[string]*prometheus.CounterVec
	registry prometheus.Registerer
}

func NewPrometheusEmitter(r prometheus.Registerer) *PrometheusEmitter {
	return &PrometheusEmitter{
		gauges:   make(map[string]*prometheus.GaugeVec),
		counters: make(map[string]*prometheus.CounterVec),
		registry: r,
	}
}

func (pe *PrometheusEmitter) EmitGauge(name string, value float64, labels map[string]string) {
	pe.mutex.Lock()
	defer pe.mutex.Unlock()
	vec, exists := pe.gauges[name]
	if !exists {
		labelKeys := maps.Keys(labels)
		vec = prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: name}, labelKeys)
		pe.registry.MustRegister(vec)
		pe.gauges[name] = vec
	}
	vec.With(labels).Set(value)
}

func (pe *PrometheusEmitter) AddCounter(name string, value float64, labels map[string]string) {
	pe.mutex.Lock()
	defer pe.mutex.Unlock()
	vec, exists := pe.counters[name]
	if !exists {
		labelKeys := maps.Keys(labels)
		vec = prometheus.NewCounterVec(prometheus.CounterOpts{Name: name}, labelKeys)
		pe.registry.MustRegister(vec)
		pe.counters[name] = vec
	}
	vec.With(labels).Add(value)
}

// patternRe is used to strip the METHOD string from the [ServerMux] pattern string.
var patternRe = regexp.MustCompile(`^[^\s]*\s+`)

type MetricsMiddleware struct {
	Emitter
	dbClient database.DBClient
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
		ctx := r.Context()
		logger := LoggerFromContext(ctx)

		startTime := time.Now()

		lrw := &logResponseWriter{ResponseWriter: w}

		next(lrw, r) // Process the request

		duration := time.Since(startTime).Seconds()

		// Get the route pattern that matched
		routePattern := r.Pattern
		routePattern = patternRe.ReplaceAllString(routePattern, "")

		subscriptionState := "Unknown"
		subscriptionId := r.PathValue(PathSegmentSubscriptionID)
		if subscriptionId != "" {
			sub, err := mm.dbClient.GetSubscriptionDoc(r.Context(), subscriptionId)
			if err != nil {
				// If we can't determine the subscription state, we can still expose a metric for subscriptionState "Unknown"
				logger.Info("unable to retrieve subscription document for the `frontend_requests_total` metric", "subscriptionId", subscriptionId, "error", err)
			} else {
				subscriptionState = string(sub.Subscription.State)
			}
		}

		mm.Emitter.AddCounter("frontend_requests_total", 1.0, map[string]string{
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
