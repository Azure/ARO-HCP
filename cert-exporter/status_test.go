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

package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestStatusEndpointsTrackReconciliation(t *testing.T) {
	status := &controllerStatus{}
	mux := http.NewServeMux()
	status.registerHandlers(mux, time.Minute)

	assertStatusCode(t, mux, "/healthz", http.StatusOK)                // no reconciliation yet - healthy at startup
	assertStatusCode(t, mux, "/readyz", http.StatusServiceUnavailable) // no reconciliation yet - not ready

	status.recordReconcile(false)
	assertStatusCode(t, mux, "/healthz", http.StatusOK)
	assertStatusCode(t, mux, "/readyz", http.StatusServiceUnavailable) // last reconciliation was a failure

	status.recordReconcile(true)
	assertStatusCode(t, mux, "/healthz", http.StatusOK)
	assertStatusCode(t, mux, "/readyz", http.StatusOK)
}

func TestMetricsExposeControllerState(t *testing.T) {
	status := &controllerStatus{}
	status.targetNamespaces.Store(2)
	status.bindingsCreated.Store(3)
	status.bindingsUpdated.Store(4)
	status.bindingsDeleted.Store(5)
	status.reconcileErrors.Store(6)

	recorder := httptest.NewRecorder()
	status.serveMetrics(recorder, httptest.NewRequest(http.MethodGet, "/metrics", nil))

	for _, expected := range []string{
		"cert_exporter_target_namespaces 2",
		"cert_exporter_rolebindings_created_total 3",
		"cert_exporter_rolebindings_updated_total 4",
		"cert_exporter_rolebindings_deleted_total 5",
		"cert_exporter_reconcile_errors_total 6",
	} {
		if !strings.Contains(recorder.Body.String(), expected) {
			t.Errorf("metrics output does not contain %q", expected)
		}
	}
}

func assertStatusCode(t *testing.T, handler http.Handler, path string, expected int) {
	t.Helper()
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
	if recorder.Code != expected {
		t.Errorf("GET %s returned %d, want %d", path, recorder.Code, expected)
	}
}
