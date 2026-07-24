// Copyright 2025 Microsoft Corporation
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

package frontend

import (
	"net/http"
	"regexp"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
)

// patternRe is used to strip the METHOD string from the [ServerMux] pattern string.
var patternRe = regexp.MustCompile(`^[^\s]*\s+`)

// muxPatternRoute returns the route pattern portion of a ServeMux pattern.
// A ServeMux pattern is a http.Request.Pattern, which consists
// of the HTTP method and the route pattern.
// This function removes the method prefix of a ServeMux pattern, leaving
// only the route pattern.
func muxPatternRoute(pattern string) string {
	return patternRe.ReplaceAllString(pattern, "")
}

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
				Help: "Counter for HTTP requests by method, code, route and user agent.",
			},
			[]string{"api_version", "method", "code", "route", "state", "user_agent"},
		),
		requestDuration: promauto.With(r).NewHistogramVec(
			prometheus.HistogramOpts{
				Name: requestDurationName,
				Help: "Histogram of latencies for HTTP requests by method, code, route and user agent.",
				// Buckets are modeled after k8s.io/apiserver request latency
				// histograms, with dense resolution around the 1s mark to
				// enable accurate P99 calculation for the S360 Control Plane
				// Latency KPI (synchronous responses within 1 second).
				Buckets: []float64{0.005, 0.025, 0.05, 0.1, 0.2, 0.4, 0.6, 0.8, 1.0, 1.25, 1.5, 2, 3, 4, 5, 6, 8, 10, 15},
			},
			[]string{"api_version", "method", "code", "route", "user_agent"},
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
			route = muxPatternRoute(*routePattern)
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

		userAgent := userAgentMetricLabel(r.UserAgent())

		mm.requestCounter.With(prometheus.Labels{
			"method":      r.Method,
			"api_version": apiVersion,
			"code":        strconv.Itoa(lrw.statusCode),
			"route":       route,
			"state":       mm.ssg.GetSubscriptionState(subscriptionID),
			"user_agent":  userAgent,
		}).Inc()

		mm.requestDuration.With(prometheus.Labels{
			"method":      r.Method,
			"api_version": apiVersion,
			"code":        strconv.Itoa(lrw.statusCode),
			"route":       route,
			"user_agent":  userAgent,
		}).Observe(time.Since(startTime).Seconds())
	}
}
