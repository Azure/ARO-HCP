// Copyright 2026 Microsoft Corporation
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package middleware

import (
	"bufio"
	"errors"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

const (
	requestCounterName  = "admin_http_requests_total"
	requestDurationName = "admin_http_requests_duration_seconds"
	// noMatchRouteLabel is the route label used when no ServeMux pattern matched
	// (e.g. a 404, or a middleware that short-circuited before mux dispatch).
	noMatchRouteLabel = "<no match>"
)

// patternRe strips the METHOD prefix from an http.ServeMux pattern string,
// leaving only the route template. A pattern looks like "GET /admin/v1/stamps/{id}".
var patternRe = regexp.MustCompile(`^[^\s]*\s+`)

// muxPatternRoute returns the route-template portion of a ServeMux pattern,
// dropping the leading HTTP method (e.g. "GET /admin/v1/stamps/{id}" ->
// "/admin/v1/stamps/{id}"). Using the template rather than the raw URL keeps
// the "route" label cardinality bounded to the set of registered routes.
func muxPatternRoute(pattern string) string {
	return patternRe.ReplaceAllString(pattern, "")
}

// MetricsMiddleware records per-request Prometheus metrics for admin HTTP routes.
type MetricsMiddleware struct {
	requestCounter  *prometheus.CounterVec
	requestDuration *prometheus.HistogramVec
}

// NewMiddlewareMetrics registers the HTTP request metrics against r and returns
// the middleware that records them.
func NewMiddlewareMetrics(r prometheus.Registerer) *MetricsMiddleware {
	labels := []string{"method", "code", "route"}
	return &MetricsMiddleware{
		requestCounter: promauto.With(r).NewCounterVec(
			prometheus.CounterOpts{
				Name: requestCounterName,
				Help: "Counter for HTTP requests by method, code, and route.",
			},
			labels,
		),
		requestDuration: promauto.With(r).NewHistogramVec(
			prometheus.HistogramOpts{
				Name: requestDurationName,
				Help: "Histogram of latencies for HTTP requests by method, code, and route.",
				// Buckets are modeled after k8s.io/apiserver request latency
				// histograms, with dense resolution around the 1s mark to enable
				// accurate P99 calculation.
				Buckets: []float64{0.005, 0.025, 0.05, 0.1, 0.2, 0.4, 0.6, 0.8, 1.0, 1.25, 1.5, 2, 3, 4, 5, 6, 8, 10, 15},
			},
			labels,
		),
	}
}

// metricsResponseWriter captures the status code written to the client. It
// forwards Hijack and Flush so streaming/hijacking handlers (e.g. the serial
// console) keep working when their ResponseWriter is wrapped.
//
// statusCode is left at its zero value until WriteHeader is observed. Note this
// wrapper overrides WriteHeader but not Write: when a handler calls Write
// without an explicit WriteHeader, net/http's implicit WriteHeader(200) runs on
// the underlying writer and bypasses this override, so statusCode stays 0. A
// statusCode of 0 therefore means "no explicit status" and is normalized to 200
// (what the client actually receives) at recording time by statusOrDefault.
type metricsResponseWriter struct {
	http.ResponseWriter
	statusCode int
}

// WriteHeader captures the status code sent to the client.
func (w *metricsResponseWriter) WriteHeader(code int) {
	w.statusCode = code
	w.ResponseWriter.WriteHeader(code)
}

// statusOrDefault returns the captured status code, or 200 when none was
// explicitly written (the default net/http sends to the client).
func (w *metricsResponseWriter) statusOrDefault() int {
	if w.statusCode == 0 {
		return http.StatusOK
	}
	return w.statusCode
}

// Hijack forwards to the underlying ResponseWriter when it supports hijacking.
func (w *metricsResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hj, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("underlying ResponseWriter does not support hijacking")
	}
	return hj.Hijack()
}

// Flush forwards to the underlying ResponseWriter when it supports flushing.
func (w *metricsResponseWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// HandleRequest records request count and latency labeled by method, status
// code, and matched route template. It is intended to run first in the pre-mux
// chain so it also captures responses from middlewares that short-circuit
// before mux dispatch (e.g. an unauthenticated 401). The route label is read
// from the pattern captured in the request context by MiddlewareMux after the
// mux resolves the request.
func (m *MetricsMiddleware) HandleRequest(w http.ResponseWriter, r *http.Request, next http.HandlerFunc) {
	startTime := time.Now()
	mw := &metricsResponseWriter{ResponseWriter: w}

	// Record in a defer so a panicking handler is still counted.
	defer func() {
		route := noMatchRouteLabel
		if pattern := PatternFromContext(r.Context()); pattern != nil && *pattern != "" {
			route = muxPatternRoute(*pattern)
		}
		labels := prometheus.Labels{
			"method": r.Method,
			"code":   strconv.Itoa(mw.statusOrDefault()),
			"route":  route,
		}
		m.requestCounter.With(labels).Inc()
		m.requestDuration.With(labels).Observe(time.Since(startTime).Seconds())
	}()

	next(mw, r)
}
