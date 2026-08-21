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
	"fmt"
	"net/http"
	"sync/atomic"
	"time"
)

type controllerStatus struct {
	lastAttempt      atomic.Int64
	lastSuccess      atomic.Int64
	targetNamespaces atomic.Int64
	bindingsCreated  atomic.Uint64
	bindingsUpdated  atomic.Uint64
	bindingsDeleted  atomic.Uint64
	reconcileErrors  atomic.Uint64
}

func (s *controllerStatus) recordReconcile(success bool) {
	now := time.Now().UnixNano()
	s.lastAttempt.Store(now)
	if success {
		s.lastSuccess.Store(now)
		return
	}
	s.reconcileErrors.Add(1)
}

func (s *controllerStatus) registerHandlers(mux *http.ServeMux, staleAfter time.Duration) {
	mux.HandleFunc("/healthz", func(writer http.ResponseWriter, _ *http.Request) {
		writeFreshness(writer, s.lastAttempt.Load(), staleAfter)
	})
	mux.HandleFunc("/readyz", func(writer http.ResponseWriter, _ *http.Request) {
		writeFreshness(writer, s.lastSuccess.Load(), staleAfter)
	})
	mux.HandleFunc("/metrics", s.serveMetrics)
}

func writeFreshness(writer http.ResponseWriter, timestamp int64, staleAfter time.Duration) {
	if timestamp == 0 || time.Since(time.Unix(0, timestamp)) > staleAfter {
		http.Error(writer, "reconciliation is stale", http.StatusServiceUnavailable)
		return
	}
	writer.WriteHeader(http.StatusOK)
}

func (s *controllerStatus) serveMetrics(writer http.ResponseWriter, _ *http.Request) {
	writer.Header().Set("Content-Type", "text/plain; version=0.0.4")
	_, _ = fmt.Fprintf(writer, "# TYPE cert_exporter_target_namespaces gauge\ncert_exporter_target_namespaces %d\n", s.targetNamespaces.Load())
	_, _ = fmt.Fprintf(writer, "# TYPE cert_exporter_rolebindings_created_total counter\ncert_exporter_rolebindings_created_total %d\n", s.bindingsCreated.Load())
	_, _ = fmt.Fprintf(writer, "# TYPE cert_exporter_rolebindings_updated_total counter\ncert_exporter_rolebindings_updated_total %d\n", s.bindingsUpdated.Load())
	_, _ = fmt.Fprintf(writer, "# TYPE cert_exporter_rolebindings_deleted_total counter\ncert_exporter_rolebindings_deleted_total %d\n", s.bindingsDeleted.Load())
	_, _ = fmt.Fprintf(writer, "# TYPE cert_exporter_reconcile_errors_total counter\ncert_exporter_reconcile_errors_total %d\n", s.reconcileErrors.Load())
}
