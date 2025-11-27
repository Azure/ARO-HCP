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

package server

import (
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/Azure/ARO-HCP/admin/server/interrupts"
)

// Health keeps a request multiplexer for health liveness and readiness endpoints
type Health struct {
	healthMux *http.ServeMux
	logger    *slog.Logger
}

// NewHealth creates a new health request multiplexer and starts serving the liveness endpoint
// on the given port
func NewHealthOnPort(logger *slog.Logger, port int) *Health {
	healthMux := http.NewServeMux()
	healthMux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if _, err := fmt.Fprint(w, "OK"); err != nil {
			logger.Error("failed to write health response", "error", err)
		}
	})
	server := &http.Server{Addr: ":" + strconv.Itoa(port), Handler: healthMux}
	interrupts.ListenAndServe(server, 5*time.Second)
	return &Health{
		healthMux: healthMux,
		logger:    logger,
	}
}

type ReadinessCheck func() bool

// ServeReady starts serving the readiness endpoint
func (h *Health) ServeReady(readinessChecks ...ReadinessCheck) {
	h.healthMux.HandleFunc("/healthz/ready", func(w http.ResponseWriter, r *http.Request) {
		for _, readinessCheck := range readinessChecks {
			if !readinessCheck() {
				w.WriteHeader(http.StatusServiceUnavailable)
				if _, err := fmt.Fprint(w, "ReadinessCheck failed"); err != nil {
					h.logger.Error("failed to write health response", "error", err)
				}
				return
			}
		}
		if _, err := fmt.Fprint(w, "OK"); err != nil {
			h.logger.Error("failed to write health response", "error", err)
		}
	})
}
