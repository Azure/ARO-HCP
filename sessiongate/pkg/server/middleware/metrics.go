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

package middleware

import (
	"bufio"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

type logResponseWriter struct {
	http.ResponseWriter
	statusCode int
}

// WriteHeader captures the status code sent to the client.
func (lrw *logResponseWriter) WriteHeader(code int) {
	lrw.statusCode = code
	lrw.ResponseWriter.WriteHeader(code)
}

// Hijack implements http.Hijacker to support WebSocket upgrades.
func (lrw *logResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := lrw.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, fmt.Errorf("response writer does not support hijacking")
	}
	return hijacker.Hijack()
}

func WithMetrics(requestCounterName, requestDurationName string, registry prometheus.Registerer, next http.HandlerFunc) http.HandlerFunc {
	requestCounter := promauto.With(registry).NewCounterVec(
		prometheus.CounterOpts{
			Name: requestCounterName,
			Help: "Counter for HTTP requests by method, status, route",
		},
		[]string{"method", "status", "route"},
	)
	requestDuration := promauto.With(registry).NewHistogramVec(
		prometheus.HistogramOpts{
			Name:                            requestDurationName,
			Help:                            "Histogram of latencies for HTTP requests by method, status, route",
			NativeHistogramBucketFactor:     1.1,
			NativeHistogramMaxBucketNumber:  100,
			NativeHistogramMinResetDuration: 1 * time.Hour,
		},
		[]string{"method", "status", "route"},
	)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		startTime := time.Now()

		lrw := &logResponseWriter{ResponseWriter: w, statusCode: 200}

		next(lrw, r)

		duration := time.Since(startTime).Seconds()
		requestCounter.WithLabelValues(r.Method, strconv.Itoa(lrw.statusCode), r.Pattern).Inc()
		requestDuration.WithLabelValues(r.Method, strconv.Itoa(lrw.statusCode), r.Pattern).Observe(duration)
	})
}
