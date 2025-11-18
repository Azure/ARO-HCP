/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// SessionInfo represents the context information returned by the session endpoint
type SessionInfo struct {
	SessionID          string     `json:"sessionId"`
	ExpiresAt          *time.Time `json:"expiresAt,omitempty"`
	ManagementCluster  string     `json:"managementCluster"`
	HostedControlPlane string     `json:"hostedControlPlane"`
	AccessGroup        string     `json:"accessGroup"`
	Namespaces         []string   `json:"namespaces,omitempty"`
}

// SessionOptions contains the configuration for registering a session
type SessionOptions struct {
	SessionID          string
	ExpiresAt          *metav1.Time
	ManagementCluster  string
	HostedControlPlane string
	AccessGroup        string
	RESTConfig         *rest.Config
}

// Server manages a shared HTTP server with dynamic session path handlers
type Server struct {
	addr     string
	server   *http.Server
	mux      *http.ServeMux
	sessions map[string]*SessionInfo
	clients  map[string]*kubernetes.Clientset // keyed by sessionID
	mu       sync.RWMutex
	started  bool
}

// NewServer creates a new shared webserver instance
func NewServer(addr string) *Server {
	mux := http.NewServeMux()
	return &Server{
		addr:     addr,
		mux:      mux,
		sessions: make(map[string]*SessionInfo),
		clients:  make(map[string]*kubernetes.Clientset),
		server: &http.Server{
			Addr:         addr,
			Handler:      mux,
			ReadTimeout:  10 * time.Second,
			WriteTimeout: 10 * time.Second,
			IdleTimeout:  60 * time.Second,
		},
	}
}

// Start starts the shared HTTP server
func (s *Server) Start(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.started {
		return nil
	}

	// Start server in background
	go func() {
		if err := s.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			fmt.Printf("Server error: %v\n", err)
		}
	}()

	s.started = true
	return nil
}

// Stop gracefully stops the HTTP server
func (s *Server) Stop(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.started {
		return nil
	}

	if err := s.server.Shutdown(ctx); err != nil {
		return fmt.Errorf("failed to shutdown server: %w", err)
	}

	s.started = false
	return nil
}

// RegisterSession registers a new session handler or updates an existing one
func (s *Server) RegisterSession(opts SessionOptions) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	info := &SessionInfo{
		SessionID:          opts.SessionID,
		ManagementCluster:  opts.ManagementCluster,
		HostedControlPlane: opts.HostedControlPlane,
		AccessGroup:        opts.AccessGroup,
	}
	if opts.ExpiresAt != nil {
		t := opts.ExpiresAt.Time
		info.ExpiresAt = &t
	}

	// Create Kubernetes client from REST config
	clientset, err := kubernetes.NewForConfig(opts.RESTConfig)
	if err != nil {
		return "", fmt.Errorf("failed to create Kubernetes client: %w", err)
	}

	sessionPath := fmt.Sprintf("/session/%s", opts.SessionID)

	// Check if this is a new registration
	if _, exists := s.sessions[opts.SessionID]; !exists {
		// Register the session info handler
		s.mux.HandleFunc(sessionPath, s.makeSessionInfoHandler(opts.SessionID))

		handler, err := newKASProxyHandler(opts.RESTConfig, opts.SessionID, sessionPath+"/kas")
		if err != nil {
			return "", fmt.Errorf("failed to create Kubernetes API reverse proxy handler: %w", err)
		}
		s.mux.Handle(sessionPath+"/kas/", handler)
	}

	// Store/update session info and client
	s.sessions[opts.SessionID] = info
	s.clients[opts.SessionID] = clientset

	return fmt.Sprintf("http://%s%s", s.addr, sessionPath), nil
}

// UnregisterSession removes a session handler
func (s *Server) UnregisterSession(sessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.sessions, sessionID)
	delete(s.clients, sessionID)
	// Note: Go's http.ServeMux doesn't support unregistering handlers,
	// so the handler will remain but return 404 when session info is deleted
}

// makeSessionHandler creates a handler function for a specific session
func (s *Server) makeSessionInfoHandler(sessionID string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		s.mu.RLock()
		info, exists := s.sessions[sessionID]
		s.mu.RUnlock()

		if !exists {
			http.Error(w, "Session not found", http.StatusNotFound)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(info); err != nil {
			http.Error(w, "Failed to encode response", http.StatusInternalServerError)
			return
		}
	}
}

// GetEndpoint returns the full endpoint URL for a session
func (s *Server) GetEndpoint(sessionID string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if _, exists := s.sessions[sessionID]; exists {
		return fmt.Sprintf("http://%s/session/%s", s.addr, sessionID)
	}
	return ""
}
