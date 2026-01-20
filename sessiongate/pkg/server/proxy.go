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
	"net/url"
	"strings"

	"k8s.io/apimachinery/pkg/util/proxy"
	"k8s.io/client-go/rest"
	"k8s.io/klog/v2"
)

// kasProxySession wraps a KAS proxy handler with lifecycle management
type kasProxySession struct {
	handler http.Handler
	cleanup func()
}

// ServeHTTP implements http.Handler
func (s *kasProxySession) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.handler.ServeHTTP(w, r)
}

// Close performs cleanup
func (s *kasProxySession) Close() {
	s.cleanup()
}

func newKASProxyHandler(
	ctx context.Context,
	restCfg *rest.Config,
	sessionID string,
	stripPathPrefix string,
) (*kasProxySession, error) {
	klog.V(4).InfoS("Creating KAS proxy handler", "sessionID", sessionID, "host", restCfg.Host, "stripPathPrefix", stripPathPrefix)

	backendBase, err := url.Parse(restCfg.Host)
	if err != nil {
		return nil, err
	}

	tracker := NewConnTracker()

	// Set up connection tracking on the dialer
	originalDial := restCfg.Dial
	if originalDial == nil {
		dialer := &net.Dialer{}
		originalDial = dialer.DialContext
	}
	restCfg.Dial = func(ctx context.Context, network, addr string) (net.Conn, error) {
		conn, err := originalDial(ctx, network, addr)
		if err != nil {
			return nil, err
		}
		return tracker.wrap(conn), nil
	}

	transport, err := rest.TransportFor(restCfg)
	if err != nil {
		return nil, err
	}

	// Create a cancellable context for this session
	sessionCtx, cancel := context.WithCancel(ctx)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		logger := klog.FromContext(r.Context()).WithValues("sessionID", sessionID, "host", restCfg.Host)
		// Use the session context so requests can be cancelled when session is unregistered
		r = r.Clone(klog.NewContext(sessionCtx, logger))
		logger.V(6).Info("Proxying request", "method", r.Method, "path", r.URL.Path)

		if !strings.HasPrefix(r.URL.Path, stripPathPrefix) {
			http.NotFound(w, r)
			return
		}

		restPath := strings.TrimPrefix(r.URL.Path, stripPathPrefix)

		backendURL := *backendBase
		backendURL.Path = backendURL.Path + restPath
		backendURL.RawQuery = r.URL.RawQuery

		klog.V(6).Info("Backend URL constructed", "backendURL", backendURL.String())

		proxyHandler := proxy.NewUpgradeAwareHandler(&backendURL, transport, true, false, &sessionErrorResponder{sessionID: sessionID})
		proxyHandler.ServeHTTP(w, r)
	})

	return &kasProxySession{
		handler: handler,
		cleanup: func() {
			cancel()
			// Kill all active connections immediately when the session expires.
			// This ensures long-running connections (watches, websockets) are
			// terminated promptly rather than waiting for idle timeouts.
			err := tracker.CloseAll()
			if err != nil {
				klog.Error(err, "Failed to close connections", "sessionID", sessionID)
			}
			klog.V(2).InfoS("Session closed", "sessionID", sessionID)
		},
	}, nil
}

// sessionErrorResponder implements proxy.ErrorResponder with session-specific context
type sessionErrorResponder struct {
	sessionID string
}

func (r *sessionErrorResponder) Error(w http.ResponseWriter, req *http.Request, err error) {
	http.Error(w, fmt.Sprintf("Proxy request failed for session %s: %v", r.sessionID, err), http.StatusBadGateway)
}
