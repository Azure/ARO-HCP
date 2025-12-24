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
	"context"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"k8s.io/klog/v2"

	"github.com/Azure/ARO-HCP/sessiongate/pkg/registry"
	"github.com/Azure/ARO-HCP/sessiongate/pkg/server/middleware"
)

const (
	sessionGatePathPrefix = "/sessiongate"
)

type sessionRegistration struct {
	session *kasProxySession
}

// Server manages a shared HTTP server with dynamic session path handlers
type Server struct {
	bindAddress    string
	ingressBaseURL string
	server         *http.Server
	mux            *http.ServeMux
	sessions       map[string]*sessionRegistration
	mu             sync.RWMutex
	reg            prometheus.Registerer
}

// NewServer creates a new shared webserver instance
// bindAddress is the local bind address (e.g., "localhost:8080" or ":8080")
// ingressBaseURL is the externally-accessible base URL for session URLs (e.g., "https://sessiongate.example.com")
func NewServer(bindAddress, ingressBaseURL string, reg prometheus.Registerer) *Server {
	mux := http.NewServeMux()
	s := &Server{
		bindAddress:    bindAddress,
		ingressBaseURL: ingressBaseURL,
		mux:            mux,
		sessions:       make(map[string]*sessionRegistration),
		reg:            reg,
		server: &http.Server{
			Addr:         bindAddress,
			Handler:      mux,
			ReadTimeout:  10 * time.Second,
			WriteTimeout: 10 * time.Second,
			IdleTimeout:  60 * time.Second,
		},
	}

	promauto.With(reg).NewGaugeFunc(
		prometheus.GaugeOpts{
			Name: "sessiongate_active_sessions",
			Help: "Number of currently active sessions",
		},
		func() float64 {
			s.mu.RLock()
			defer s.mu.RUnlock()
			return float64(len(s.sessions))
		},
	)

	return s
}

func (s *Server) BindAddress() string {
	return s.bindAddress
}

// Run starts the HTTP server and blocks until the context is cancelled or an error occurs.
// It fails fast if the port is unavailable, then serves requests until shutdown.
func (s *Server) Run(ctx context.Context) error {
	// Bind the port immediately - fails fast if port unavailable
	listener, err := net.Listen("tcp", s.bindAddress)
	if err != nil {
		return fmt.Errorf("failed to bind to %s: %w", s.bindAddress, err)
	}
	defer listener.Close()

	// Register handlers using Go 1.22+ path patterns with logging middleware
	s.mux.Handle(
		fmt.Sprintf("%s/{path...}", BuildSessionKASUrlPath("{sessionID}")),
		middleware.WithMetrics(
			"sessiongate_kas_proxy_requests_total",
			"sessiongate_kas_proxy_requests_duration_seconds",
			s.reg,
			http.HandlerFunc(s.kasProxyHandler),
		),
	)
	s.mux.Handle("/healthz", http.HandlerFunc(s.healthzHandler))
	s.mux.Handle("/readyz", http.HandlerFunc(s.readyzHandler))
	s.mux.Handle("/metrics", promhttp.Handler())

	// Start server in goroutine
	serverErr := make(chan error, 1)
	go func() {
		if err := s.server.Serve(listener); err != nil && err != http.ErrServerClosed {
			serverErr <- err
		}
	}()

	// Block until context cancels or server errors
	select {
	case <-ctx.Done():
		klog.Info("Context cancelled - performing graceful webserver shutdown")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.server.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("failed to shutdown server: %w", err)
		}
		return nil
	case err := <-serverErr:
		klog.Error(err, "Server encountered an error")
		return err
	}
}

// RegisterSession registers session information and REST configuration.
// If the session already exists, this is a no-op (returns existing endpoint).
// Note: Sessions are immutable - we don't support updating REST config for existing sessions.
// To update credentials, the session must be unregistered and re-registered.
func (s *Server) RegisterSession(opts registry.SessionOptions) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, exists := s.sessions[opts.SessionID]
	if !exists {
		klog.V(2).Info("Registering new session", "sessionID", opts.SessionID, "resourceID", opts.ResourceID)
		session, err := newKASProxyHandler(context.Background(), opts.RESTConfig, opts.SessionID, BuildSessionKASUrlPath(opts.SessionID))
		if err != nil {
			klog.Error(err, "Failed to create KAS proxy handler", "sessionID", opts.SessionID)
			return "", fmt.Errorf("failed to create KAS proxy handler: %w", err)
		}
		s.sessions[opts.SessionID] = &sessionRegistration{
			session: session,
		}
	} else {
		klog.V(4).Info("Session already registered, returning existing endpoint", "sessionID", opts.SessionID)
	}

	endpoint := s.GetSessionEndpoint(opts.SessionID)
	return endpoint, nil
}

// GetSessionEndpoint computes the public endpoint URL for a given session ID.
func (s *Server) GetSessionEndpoint(sessionID string) string {
	return fmt.Sprintf("%s%s", s.ingressBaseURL, BuildSessionKASUrlPath(sessionID))
}

// UnregisterSession removes session data and forcibly stops all backend interactions.
// This is a hard stop - all in-flight requests will be cancelled, WebSocket/SPDY upgrades
// will fail, and idle connections will be closed.
func (s *Server) UnregisterSession(sessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if reg, exists := s.sessions[sessionID]; exists {
		klog.V(2).InfoS("Unregister session", "sessionID", sessionID)
		reg.session.Close()

		delete(s.sessions, sessionID)
	}
}

// kasProxyHandler handles /session/{sessionID}/kas/* requests
func (s *Server) kasProxyHandler(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("sessionID")

	s.mu.RLock()
	info, exists := s.sessions[sessionID]
	s.mu.RUnlock()

	if !exists {
		http.Error(w, "Session not found", http.StatusNotFound)
		return
	}
	info.session.ServeHTTP(w, r)
}

// healthzHandler returns 200 OK if the server is running
func (s *Server) healthzHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
}

// readyzHandler returns 200 OK if the server is ready to accept requests
func (s *Server) readyzHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
}

func BuildSessionKASUrlPath(sessionName string) string {
	return fmt.Sprintf("%s/%s/kas", sessionGatePathPrefix, sessionName)
}
