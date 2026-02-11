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

	"k8s.io/client-go/rest"
	"k8s.io/klog/v2"

	sessiongatev1alpha1 "github.com/Azure/ARO-HCP/sessiongate/pkg/apis/sessiongate/v1alpha1"
	"github.com/Azure/ARO-HCP/sessiongate/pkg/registry"
	"github.com/Azure/ARO-HCP/sessiongate/pkg/server/middleware"
)

const (
	sessionGatePathPrefix = "/sessiongate"

	// gracefulShutdownTimeout is the maximum time to wait for in-flight requests
	// to complete during server shutdown.
	gracefulShutdownTimeout = 5 * time.Second
)

// Server manages a shared HTTP server with dynamic session path handlers
type Server struct {
	bindAddress    string
	ingressBaseURL string
	server         *http.Server
	mux            *http.ServeMux
	sessions       map[string]*kasProxySession
	mu             sync.RWMutex
	reg            prometheus.Registerer
}

// make sure Server implements the Registry interface
var _ registry.SessionRegistry = &Server{}

// NewServer creates a new shared webserver instance
// bindAddress is the local bind address (e.g., "localhost:8080" or ":8080")
// ingressBaseURL is the externally-accessible base URL for session URLs
func NewServer(bindAddress, ingressBaseURL string, reg prometheus.Registerer) *Server {
	mux := http.NewServeMux()
	s := &Server{
		bindAddress:    bindAddress,
		ingressBaseURL: ingressBaseURL,
		mux:            mux,
		sessions:       make(map[string]*kasProxySession),
		reg:            reg,
		server: &http.Server{
			Addr:    bindAddress,
			Handler: mux,
			// ReadTimeout and WriteTimeout must be 0 (disabled) because this server
			// proxies Kubernetes API connections that use SPDY upgrades (exec/attach).
			// Go's http.Server sets deadlines based on these timeouts, and those
			// deadlines persist on the net.Conn after hijacking. The Kubernetes
			// UpgradeAwareHandler does not clear them, so non-zero values would
			// cause hijacked connections to fail with "i/o deadline reached" errors.
			ReadTimeout:       0,
			WriteTimeout:      0,
			IdleTimeout:       120 * time.Second,
			ReadHeaderTimeout: 10 * time.Second,
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
func (s *Server) Run(ctx context.Context) error {
	listener, err := net.Listen("tcp", s.bindAddress)
	if err != nil {
		return fmt.Errorf("failed to bind to %s: %w", s.bindAddress, err)
	}
	defer listener.Close()

	s.mux.Handle(
		fmt.Sprintf("%s/{path...}", BuildSessionKASUrlPath("{session}")),
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
		shutdownCtx, cancel := context.WithTimeout(context.Background(), gracefulShutdownTimeout)
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
func (s *Server) RegisterSession(sessionName, resourceID string, owner sessiongatev1alpha1.Principal, restConfig *rest.Config) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	logger := klog.FromContext(context.Background()).WithValues("sessionName", sessionName, "resourceID", resourceID, "identity", owner.Name)

	_, exists := s.sessions[sessionName]
	if !exists {
		logger.V(2).Info("Registering new session")
		session, err := newKASProxyHandler(context.Background(), restConfig, sessionName, owner, BuildSessionKASUrlPath(sessionName))
		if err != nil {
			logger.Error(err, "Failed to create KAS proxy handler")
			return "", fmt.Errorf("failed to create KAS proxy handler: %w", err)
		}
		s.sessions[sessionName] = session
	} else {
		logger.V(4).Info("Session already registered, returning existing endpoint")
	}

	endpoint := s.GetSessionEndpoint(sessionName)
	return endpoint, nil
}

// GetSessionEndpoint computes the public endpoint URL for a given session ID.
func (s *Server) GetSessionEndpoint(sessionName string) string {
	return fmt.Sprintf("%s%s", s.ingressBaseURL, BuildSessionKASUrlPath(sessionName))
}

// UnregisterSession removes session data and forcibly stops all backend interactions.
// This is a hard stop - all in-flight requests will be cancelled, WebSocket/SPDY upgrades
// will fail, and idle connections will be closed.
func (s *Server) UnregisterSession(sessionName string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if session, exists := s.sessions[sessionName]; exists {
		klog.V(2).InfoS("Unregister session", "session", sessionName)
		session.Close()

		delete(s.sessions, sessionName)
	}
}

// kasProxyHandler handles /session/{session}/kas/* requests
func (s *Server) kasProxyHandler(w http.ResponseWriter, r *http.Request) {
	sessionName := r.PathValue("session")

	s.mu.RLock()
	session, exists := s.sessions[sessionName]
	s.mu.RUnlock()

	if !exists {
		http.Error(w, "Session not found", http.StatusNotFound)
		return
	}
	session.ServeHTTP(w, r)
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
