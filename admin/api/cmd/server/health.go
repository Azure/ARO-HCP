package server

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/go-logr/logr"

	"github.com/Azure/ARO-HCP/admin/api/interrupts"
)

// Health keeps a request multiplexer for health liveness and readiness endpoints
type Health struct {
	healthMux *http.ServeMux
	logger    logr.Logger
}

// NewHealth creates a new health request multiplexer and starts serving the liveness endpoint
// on the given port
func NewHealthOnPort(logger logr.Logger, port int) *Health {
	healthMux := http.NewServeMux()
	healthMux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if _, err := fmt.Fprint(w, "OK"); err != nil {
			logger.Error(err, "failed to write health response")
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
					h.logger.Error(err, "failed to write health response")
				}
				return
			}
		}
		if _, err := fmt.Fprint(w, "OK"); err != nil {
			h.logger.Error(err, "failed to write health response")
		}
	})
}
