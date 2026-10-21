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
	"net/http/httptest"
	"testing"
)

func TestMiddlewareMux_PatternOnHandlerPanic(t *testing.T) {
	t.Parallel()

	var gotPattern string
	mux := NewMiddlewareMux(func(w http.ResponseWriter, r *http.Request, next http.HandlerFunc) {
		defer func() {
			if recover() != nil {
				if patt := PatternFromContext(r.Context()); patt != nil {
					gotPattern = *patt
				}
			}
		}()
		next(w, r)
	})
	mux.HandleFunc("GET /bar", func(http.ResponseWriter, *http.Request) {
		panic("test")
	})

	req := httptest.NewRequest(http.MethodGet, "/bar", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if gotPattern != "GET /bar" {
		t.Fatalf("got pattern %q, want %q", gotPattern, "GET /bar")
	}
}

func TestMiddlewareMux_PatternOnHandlerSuccess(t *testing.T) {
	t.Parallel()

	var gotPattern string
	mux := NewMiddlewareMux(func(w http.ResponseWriter, r *http.Request, next http.HandlerFunc) {
		next(w, r)
		if patt := PatternFromContext(r.Context()); patt != nil {
			gotPattern = *patt
		}
	})
	mux.HandleFunc("GET /bar", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/bar", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if gotPattern != "GET /bar" {
		t.Fatalf("got pattern %q, want %q", gotPattern, "GET /bar")
	}
}
