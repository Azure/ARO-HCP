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

package framework

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/version"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/rest"
)

// newVersionTestDiscoveryClient serves /version with the given handler and returns a discovery
// client pointed at it.
func newVersionTestDiscoveryClient(t *testing.T, handler http.HandlerFunc) discovery.DiscoveryInterface {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	discoveryClient, err := discovery.NewDiscoveryClientForConfig(&rest.Config{Host: server.URL})
	if err != nil {
		t.Fatalf("build a discovery client for %s: %v", server.URL, err)
	}
	return discoveryClient
}

// TestGetKubeAPIServerVersionIsCancelledByContext is the reason this helper exists: the
// discovery.ServerVersion() it replaces issues its request with context.TODO(), so a stalled
// /version request outlives the caller's deadline. Serve a request that never responds and assert
// the call comes back at the caller's deadline rather than hanging, so a regression to
// context.TODO() fails here instead of burning a verifier's whole budget in CI.
func TestGetKubeAPIServerVersionIsCancelledByContext(t *testing.T) {
	const callerTimeout = 250 * time.Millisecond

	discoveryClient := newVersionTestDiscoveryClient(t, func(w http.ResponseWriter, r *http.Request) {
		// Respond only once the client has gone away, which is what a stalled kube-apiserver
		// looks like from here.
		<-r.Context().Done()
	})

	ctx, cancel := context.WithTimeout(context.Background(), callerTimeout)
	defer cancel()

	startTime := time.Now()
	info, err := GetKubeAPIServerVersion(ctx, discoveryClient)
	elapsed := time.Since(startTime)

	if err == nil {
		t.Fatalf("expected the stalled request to fail, got version %+v", info)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("expected the caller's context error to reach the HTTP call, got: %v", err)
	}
	// Generous upper bound: the point is that the request is bounded by the caller's context at
	// all, not that the scheduler is precise.
	if maximum := callerTimeout + 5*time.Second; elapsed > maximum {
		t.Errorf("the stalled request was not cancelled promptly: returned after %s, expected within %s", elapsed, maximum)
	}
}

// TestGetKubeAPIServerVersionParsesResponse checks the helper reads the same payload
// discovery.ServerVersion() does, so values from the two remain comparable.
func TestGetKubeAPIServerVersionParsesResponse(t *testing.T) {
	expected := version.Info{Major: "1", Minor: "32", GitVersion: "v1.32.6"}

	discoveryClient := newVersionTestDiscoveryClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/version" {
			t.Errorf("expected a request to /version, got %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(expected); err != nil {
			t.Errorf("encode the /version response: %v", err)
		}
	})

	info, err := GetKubeAPIServerVersion(context.Background(), discoveryClient)
	if err != nil {
		t.Fatalf("expected the /version read to succeed, got: %v", err)
	}
	if info.GitVersion != expected.GitVersion || info.Major != expected.Major || info.Minor != expected.Minor {
		t.Errorf("unexpected version: got %+v, want %+v", *info, expected)
	}
}

// discoveryWithoutRESTClient stands in for a discovery client that cannot issue requests, which
// is what the generated fakes are: they return a nil REST client. Only RESTClient is ever called.
type discoveryWithoutRESTClient struct {
	discovery.DiscoveryInterface
}

func (discoveryWithoutRESTClient) RESTClient() rest.Interface { return nil }

// TestGetKubeAPIServerVersionWithoutRESTClient covers the guard: a discovery client with no REST
// client (the fakes return nil) must name the problem rather than panic.
func TestGetKubeAPIServerVersionWithoutRESTClient(t *testing.T) {
	_, err := GetKubeAPIServerVersion(context.Background(), discoveryWithoutRESTClient{})
	if err == nil {
		t.Fatal("expected an error when the discovery client has no REST client, got nil")
	}
}
