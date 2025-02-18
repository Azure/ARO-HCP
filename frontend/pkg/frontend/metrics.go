package frontend

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

import (
	"net/http"
	"regexp"
	"strconv"
	"time"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// patternRe is used to strip the METHOD string from the [ServerMux] pattern string.
var patternRe = regexp.MustCompile(`^[^\s]*\s+`)

type SubscriptionStateGetter interface {
	GetSubscriptionState(string) string
}

type MetricsMiddleware struct {
	ssg             SubscriptionStateGetter
	requestCounter  *prometheus.CounterVec
	requestDuration *prometheus.HistogramVec
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

func NewMetricsMiddleware(r prometheus.Registerer, ssg SubscriptionStateGetter) *MetricsMiddleware {
	mm := &MetricsMiddleware{
		ssg: ssg,
		requestCounter: promauto.With(r).NewCounterVec(
			prometheus.CounterOpts{
				Name: requestCounterName,
				Help: "Counter for HTTP requests by method, code and route.",
			},
			[]string{"api_version", "method", "code", "route", "state"},
		),
		requestDuration: promauto.With(r).NewHistogramVec(
			prometheus.HistogramOpts{
				Name: requestDurationName,
				Help: "Histogram of latencies for HTTP requests by method, code and route.",
				// The bucket values are chosen to match the general
				// recommendation in terms of latency for resource providers
				// (e.g. latency less than or equal to 1 second).
				Buckets: []float64{.25, .5, 1, 2.5, 5, 10},
				// Enable native histogram (sparse buckets). The settings have
				// been chosen to offer a balance between accuracy and memory
				// usage.
				// Note that it requires support from the scraper (e.g. Prometheus).
				NativeHistogramBucketFactor:     1.1,
				NativeHistogramMaxBucketNumber:  100,
				NativeHistogramMinResetDuration: 1 * time.Hour,
			},
			[]string{"api_version", "method", "code", "route"},
		),
	}

	return mm
}

// Metrics middleware to capture response time and status code
func (mm MetricsMiddleware) Metrics() MiddlewareFunc {
	return func(w http.ResponseWriter, r *http.Request, next http.HandlerFunc) {
		startTime := time.Now()

		lrw := &logResponseWriter{ResponseWriter: w}

		next(lrw, r) // Process the request.

		// Get the route pattern that matched.
		// Note that the value can be empty if one of the middlewares executing
		// before the ServeMux handler returned early.
		var (
			routePattern = PatternFromContext(r.Context())
			route        string
		)
		if routePattern != nil {
			route = patternRe.ReplaceAllString(*routePattern, "")
		}
		if route == "" {
			route = noMatchRouteLabel
		}

		apiVersion := r.URL.Query().Get(APIVersionKey)
		if apiVersion == "" {
			apiVersion = unknownVersionLabel
		}

		var subscriptionID string
		if resource, _ := azcorearm.ParseResourceID(r.URL.Path); resource != nil {
			subscriptionID = resource.SubscriptionID
		}

		mm.requestCounter.With(prometheus.Labels{
			"method":      r.Method,
			"api_version": apiVersion,
			"code":        strconv.Itoa(lrw.statusCode),
			"route":       route,
			"state":       mm.ssg.GetSubscriptionState(subscriptionID),
		}).Inc()

		mm.requestDuration.With(prometheus.Labels{
			"method":      r.Method,
			"api_version": apiVersion,
			"code":        strconv.Itoa(lrw.statusCode),
			"route":       route,
		}).Observe(time.Since(startTime).Seconds())
	}
}
