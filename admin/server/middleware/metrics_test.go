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
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestMuxPatternRoute(t *testing.T) {
	for _, tc := range []struct {
		name    string
		pattern string
		want    string
	}{
		{name: "method and route", pattern: "GET /admin/v1/stamps/{id}", want: "/admin/v1/stamps/{id}"},
		{name: "no method", pattern: "/admin/helloworld", want: "/admin/helloworld"},
		{name: "empty", pattern: "", want: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := muxPatternRoute(tc.pattern); got != tc.want {
				t.Errorf("muxPatternRoute(%q) = %q, want %q", tc.pattern, got, tc.want)
			}
		})
	}
}

func TestMetricsMiddleware(t *testing.T) {
	for _, tc := range []struct {
		name string
		// handler is registered at "GET /admin/thing"; nil means don't register
		// a handler at all (exercises the unmatched-route path).
		handler http.HandlerFunc
		// preMux, when set, runs before the mux dispatch and may short-circuit
		// (e.g. an auth middleware returning 401 before a route is matched).
		preMux MiddlewareFunc
		method string
		path   string

		wantMethod string
		wantCode   string
		wantRoute  string
	}{
		{
			name:       "registered route returns 200",
			handler:    func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) },
			method:     http.MethodGet,
			path:       "/admin/thing",
			wantMethod: http.MethodGet,
			wantCode:   "200",
			wantRoute:  "/admin/thing",
		},
		{
			name:       "explicit non-200 is captured",
			handler:    func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusTeapot) },
			method:     http.MethodGet,
			path:       "/admin/thing",
			wantMethod: http.MethodGet,
			wantCode:   "418",
			wantRoute:  "/admin/thing",
		},
		{
			name:       "handler that only writes is recorded as 200",
			handler:    func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("hi")) },
			method:     http.MethodGet,
			path:       "/admin/thing",
			wantMethod: http.MethodGet,
			wantCode:   "200",
			wantRoute:  "/admin/thing",
		},
		{
			name:       "unregistered path falls back to no-match route",
			handler:    nil,
			method:     http.MethodGet,
			path:       "/admin/does-not-exist",
			wantMethod: http.MethodGet,
			wantCode:   "404",
			wantRoute:  noMatchRouteLabel,
		},
		{
			name:    "middleware short-circuits before routing, so route is unmatched",
			handler: func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) },
			preMux: func(w http.ResponseWriter, r *http.Request, next http.HandlerFunc) {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
			},
			method:     http.MethodGet,
			path:       "/admin/thing",
			wantMethod: http.MethodGet,
			wantCode:   "401",
			wantRoute:  noMatchRouteLabel,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			registry := prometheus.NewRegistry()
			mm := NewMiddlewareMetrics(registry)

			pre := []MiddlewareFunc{mm.HandleRequest}
			if tc.preMux != nil {
				pre = append(pre, tc.preMux)
			}
			mux := NewMiddlewareMux(pre...)
			if tc.handler != nil {
				mux.Handle("GET /admin/thing", tc.handler)
			}

			rec := httptest.NewRecorder()
			req := httptest.NewRequest(tc.method, tc.path, nil)
			mux.ServeHTTP(rec, req)

			labels := prometheus.Labels{
				"method": tc.wantMethod,
				"code":   tc.wantCode,
				"route":  tc.wantRoute,
			}

			if got := testutil.ToFloat64(mm.requestCounter.With(labels)); got != 1 {
				t.Errorf("requestCounter%v = %v, want 1", labels, got)
			}
			if got := testutil.CollectAndCount(mm.requestDuration); got != 1 {
				t.Errorf("requestDuration series count = %d, want 1", got)
			}
		})
	}
}
