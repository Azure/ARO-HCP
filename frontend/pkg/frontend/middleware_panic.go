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
	"fmt"
	"net/http"
	"runtime/debug"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"k8s.io/component-base/metrics/legacyregistry"

	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

var (
	// frontendHTTPRequestPanicsTotalCounterVec counts the total number of panics that occurred
	// during request handling.
	// To get the total panics use sum() in PromQL for the aggregate.
	// The label values must never be empty.
	frontendHTTPRequestPanicsTotalCounterVec = promauto.With(legacyregistry.Registerer()).NewCounterVec(
		prometheus.CounterOpts{
			Name: "frontend_http_request_panics_total",
			Help: "Total number of panics during HTTP request handling. Labels: route (request pattern without method prefix, 'unmatched' if no route matched), method (HTTP method, 'unknown' if empty).",
		},
		[]string{"route", "method"},
	)
)

func MiddlewarePanic(w http.ResponseWriter, r *http.Request, next http.HandlerFunc) {
	// Do not catch panics when running "go test".
	middlewarePanicRecover(w, r, next, !testing.Testing())
}

// middlewarePanicRecover runs the next http.HandlerFunc. Additionally, when
// recoverPanics is true it recovers panics, increments the
// frontend_http_request_panics_total metric, and writes an HTTP 500 response.
func middlewarePanicRecover(w http.ResponseWriter, r *http.Request, next http.HandlerFunc, recoverPanics bool) {
	if recoverPanics {
		defer func() {
			if e := recover(); e != nil {
				logger := utils.LoggerFromContext(r.Context())
				panicErr := fmt.Errorf("panic: %#v", e)
				logger.Error(panicErr, "panic recovered", "stack", string(debug.Stack()))

				// We retrieve the pattern from the context instead of using r.Pattern.
				// This is because from here we can't rely on the value stored in
				// r.Pattern because the original request can be copied and mutated
				// by following middlewares before the route pattern is captured,
				// and here we could still have access to the original request which
				// still does not contain the final matched route pattern.
				// To solve this we retrieve the pattern from the context which is
				// set by MiddlewareMux when ServeMux matches a route.
				requestRoutePattern := "unmatched"
				patternFromContext := PatternFromContext(r.Context())
				if patternFromContext != nil && *patternFromContext != "" {
					requestRoutePattern = *patternFromContext
				}
				// Strip the method prefix from the mux pattern. This is because
				// for the prometheus counter we store the request route pattern and the
				// request method separately so we can filter them independently.
				requestRoutePattern = muxPatternRoute(requestRoutePattern)
				requestMethod := r.Method
				if requestMethod == "" {
					requestMethod = "unknown"
				}
				frontendHTTPRequestPanicsTotalCounterVec.WithLabelValues(requestRoutePattern, requestMethod).Inc()

				arm.WriteInternalServerError(w)
			}
		}()
	}

	next(w, r)
}
