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
	"bufio"
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/util/proxy"
	"k8s.io/client-go/rest"
	"k8s.io/klog/v2"

	sessiongatev1alpha1 "github.com/Azure/ARO-HCP/sessiongate/pkg/apis/sessiongate/v1alpha1"
	"github.com/Azure/ARO-HCP/sessiongate/pkg/server/middleware"
)

// responseCapture wraps http.ResponseWriter to capture the status code for logging.
type responseCapture struct {
	http.ResponseWriter
	statusCode int
	hijacked   bool
}

func (rc *responseCapture) WriteHeader(code int) {
	rc.statusCode = code
	rc.ResponseWriter.WriteHeader(code)
}

func (rc *responseCapture) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := rc.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, fmt.Errorf("response writer does not support hijacking")
	}
	rc.hijacked = true
	return hijacker.Hijack()
}

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
	sessionName string,
	owner sessiongatev1alpha1.Principal,
	stripPathPrefix string,
) (*kasProxySession, error) {
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
		logger := klog.FromContext(r.Context()).WithValues("session", sessionName, "identity", owner.Name)
		// Use the session context so requests can be cancelled when session is unregistered
		r = r.Clone(klog.NewContext(sessionCtx, logger))

		if !strings.HasPrefix(r.URL.Path, stripPathPrefix) {
			http.NotFound(w, r)
			return
		}

		restPath := strings.TrimPrefix(r.URL.Path, stripPathPrefix)

		backendURL := *backendBase
		backendURL.Path = backendURL.Path + restPath
		backendURL.RawQuery = r.URL.RawQuery

		rc := &responseCapture{ResponseWriter: w, statusCode: http.StatusOK}
		start := time.Now()

		proxyHandler := proxy.NewUpgradeAwareHandler(&backendURL, transport, true, false, &sessionErrorResponder{sessionName: sessionName})
		proxyHandler.ServeHTTP(rc, r)

		if rc.hijacked {
			logger.V(2).Info("proxied request (upgraded)",
				"method", r.Method,
				"path", restPath,
				"userAgent", r.UserAgent(),
			)
		} else {
			logger.Info("proxied request",
				"method", r.Method,
				"path", restPath,
				"status", rc.statusCode,
				"duration", time.Since(start).String(),
				"userAgent", r.UserAgent(),
			)
		}
	})

	return &kasProxySession{
		handler: middleware.WithSessionProxyClaimHeaderAuthorization(owner, handler),
		cleanup: func() {
			cancel()
			// Kill all active connections immediately when the session expires.
			// This ensures long-running connections (watches, websockets) are
			// terminated promptly rather than waiting for idle timeouts.
			err := tracker.CloseAll()
			if err != nil {
				klog.Error(err, "Failed to close connections", "session", sessionName)
			}
			klog.V(2).InfoS("Session closed", "session", sessionName)
		},
	}, nil
}

// sessionErrorResponder implements proxy.ErrorResponder with session-specific context
type sessionErrorResponder struct {
	sessionName string
}

func (r *sessionErrorResponder) Error(w http.ResponseWriter, req *http.Request, err error) {
	http.Error(w, fmt.Sprintf("Proxy request failed for session %s: %v", r.sessionName, err), http.StatusBadGateway)
}
