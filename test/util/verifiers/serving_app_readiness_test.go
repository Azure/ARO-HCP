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

package verifiers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"k8s.io/client-go/rest"

	configv1 "github.com/openshift/api/config/v1"
)

// jsonServer serves body as a JSON response for every request.
func jsonServer(t *testing.T, body string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	return server
}

// erroringServer fails every request with HTTP 500.
func erroringServer(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)
	return server
}

// assertCancellationReportedDistinctly calls gate with an already-cancelled context and verifies
// the failure is reported as the caller's cancellation, not as a gate timeout. The context is
// cancelled before the first request, so the client fails locally and no server is needed.
func assertCancellationReportedDistinctly(t *testing.T, gate func(ctx context.Context) error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := gate(ctx)
	if err == nil {
		t.Fatal("expected an error when the caller context is cancelled")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	if !strings.Contains(err.Error(), "cancelled") {
		t.Fatalf("error should report the cancellation distinctly, got: %v", err)
	}
	if strings.Contains(err.Error(), "timed out") {
		t.Fatalf("cancelled wait must not be reported as a timeout, got: %v", err)
	}
}

func TestReadyRESTConfigDisablesRateLimiting(t *testing.T) {
	// A token-bucket limiter refuses a token once the context deadline's timestamp
	// has passed, even with tokens to spare, and its error does not wrap the
	// context error — it would mask the last observation at the deadline race.
	config := readyRESTConfig(&rest.Config{Host: "https://api.example"})
	if config.Timeout != 10*time.Second {
		t.Fatalf("per-request timeout = %s, want 10s", config.Timeout)
	}
	ctx, cancel := context.WithTimeout(context.Background(), -time.Second) // already expired
	defer cancel()
	if err := config.RateLimiter.Wait(ctx); err != nil {
		t.Fatalf("gate client limiter must allow requests even past a deadline, got: %v", err)
	}
}

func TestWaitForClusterOperatorCondition_WaitsForAvailable(t *testing.T) {
	attempts := 0
	notReady := `{"kind":"ClusterOperator","apiVersion":"config.openshift.io/v1","status":{"conditions":[{"type":"Available","status":"False","reason":"Progressing","message":"waiting"}]}}`
	ready := `{"kind":"ClusterOperator","apiVersion":"config.openshift.io/v1","status":{"conditions":[{"type":"Available","status":"True","reason":"Progressing","message":"waiting"}]}}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Asserting the exact request path keeps a wrong URL from passing silently.
		if got, want := r.URL.Path, "/apis/config.openshift.io/v1/clusteroperators/ingress"; got != want {
			t.Errorf("ClusterOperator request path = %q, want %q", got, want)
		}
		w.Header().Set("Content-Type", "application/json")
		attempts++
		if attempts >= 2 {
			_, _ = w.Write([]byte(ready))
			return
		}
		_, _ = w.Write([]byte(notReady))
	}))
	t.Cleanup(server.Close)

	if err := waitForClusterOperatorCondition(context.Background(), &rest.Config{Host: server.URL}, "ingress", configv1.OperatorAvailable, 5*time.Second, 200*time.Millisecond); err != nil {
		t.Fatalf("expected the condition to become available, got: %v", err)
	}
}

func TestWaitForClusterOperatorCondition_ReportsUnavailableOperator(t *testing.T) {
	server := jsonServer(t, `{"kind":"ClusterOperator","apiVersion":"config.openshift.io/v1","status":{"conditions":[{"type":"Available","status":"False","reason":"Progressing","message":"rolling"}]}}`)

	err := waitForClusterOperatorCondition(context.Background(), &rest.Config{Host: server.URL}, "ingress", configv1.OperatorAvailable, 1*time.Second, 100*time.Millisecond)
	if err == nil {
		t.Fatal("expected the timeout to surface the unavailable condition")
	}
	if !strings.Contains(err.Error(), "ingress") || !strings.Contains(err.Error(), "Progressing") {
		t.Fatalf("error should name the operator and condition, got: %v", err)
	}
}

func TestWaitForClusterOperatorCondition_ReportsMissingCondition(t *testing.T) {
	// An operator that never reports the wanted condition must surface the
	// missing condition, not a bare wait timeout.
	server := jsonServer(t, `{"kind":"ClusterOperator","apiVersion":"config.openshift.io/v1","status":{"conditions":[{"type":"Progressing","status":"True"}]}}`)

	err := waitForClusterOperatorCondition(context.Background(), &rest.Config{Host: server.URL}, "ingress", configv1.OperatorAvailable, 1*time.Second, 100*time.Millisecond)
	if err == nil {
		t.Fatal("expected a timeout when the condition is missing")
	}
	for _, want := range []string{"timed out", "has no Available condition"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error should report the missing condition (want %q), got: %v", want, err)
		}
	}
}

func TestWaitForClusterOperatorCondition_ReportsPersistentAPIError(t *testing.T) {
	// When no condition is ever observed, the last API error must be preserved
	// instead of being displaced by the bare wait timeout.
	server := erroringServer(t)

	err := waitForClusterOperatorCondition(context.Background(), &rest.Config{Host: server.URL}, "ingress", configv1.OperatorAvailable, 1*time.Second, 100*time.Millisecond)
	if err == nil {
		t.Fatal("expected a timeout when the API keeps failing")
	}
	for _, want := range []string{"timed out", "failed to get ClusterOperator"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error should preserve the last API error (want %q), got: %v", want, err)
		}
	}
}

func TestWaitForClusterOperatorCondition_InterruptedRequestPreservesObservation(t *testing.T) {
	// A request that fails as the caller cancels is an interruption, not an
	// observation: its error must not displace the condition last seen.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	notReady := `{"kind":"ClusterOperator","apiVersion":"config.openshift.io/v1","status":{"conditions":[{"type":"Available","status":"False","reason":"Progressing","message":"rolling"}]}}`
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		attempts++
		if attempts >= 2 {
			cancel()
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte(notReady))
	}))
	t.Cleanup(server.Close)

	err := waitForClusterOperatorCondition(ctx, &rest.Config{Host: server.URL}, "ingress", configv1.OperatorAvailable, 5*time.Second, 50*time.Millisecond)
	if err == nil {
		t.Fatal("expected an error when the wait is interrupted")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	for _, want := range []string{"cancelled", "Progressing"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error should report the cancellation and keep the last observation (want %q), got: %v", want, err)
		}
	}
	if strings.Contains(err.Error(), "boom") {
		t.Fatalf("interrupted request error must not displace the observation, got: %v", err)
	}
}

func TestWaitForClusterOperatorCondition_ReportsCallerCancellation(t *testing.T) {
	assertCancellationReportedDistinctly(t, func(ctx context.Context) error {
		return waitForClusterOperatorCondition(ctx, &rest.Config{Host: "http://unused.invalid"}, "ingress", configv1.OperatorAvailable, 5*time.Second, 100*time.Millisecond)
	})
}

func TestWaitForClusterOperatorCondition_NilConfig(t *testing.T) {
	err := waitForClusterOperatorCondition(context.Background(), nil, "ingress", configv1.OperatorAvailable, time.Minute, time.Second)
	if err == nil {
		t.Fatal("expected an error for a nil admin REST config")
	}
	if !strings.Contains(err.Error(), "admin REST config is nil") {
		t.Fatalf("unexpected error text: %v", err)
	}
}

func TestWaitForClusterOperatorCondition_RejectsInvalidInterval(t *testing.T) {
	config := &rest.Config{Host: "http://unused.invalid"}
	err := waitForClusterOperatorCondition(context.Background(), config, "ingress", configv1.OperatorAvailable, time.Minute, 0)
	if err == nil || !strings.Contains(err.Error(), "interval must be > 0") {
		t.Fatalf("zero interval: got %v, want \"interval must be > 0\" error", err)
	}
}

func TestWaitForRouterDefaultExternalIP_WaitsUntilAssigned(t *testing.T) {
	attempts := 0
	noIP := `{"kind":"Service","apiVersion":"v1","status":{"loadBalancer":{"ingress":[]}}}`
	withIP := `{"kind":"Service","apiVersion":"v1","status":{"loadBalancer":{"ingress":[{"ip":"203.0.113.10"}]}}}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Asserting the exact request path keeps a wrong URL from passing silently.
		if got, want := r.URL.Path, "/api/v1/namespaces/openshift-ingress/services/router-default"; got != want {
			t.Errorf("Service request path = %q, want %q", got, want)
		}
		w.Header().Set("Content-Type", "application/json")
		attempts++
		if attempts >= 2 {
			_, _ = w.Write([]byte(withIP))
			return
		}
		_, _ = w.Write([]byte(noIP))
	}))
	t.Cleanup(server.Close)

	if err := waitForRouterDefaultExternalIP(context.Background(), &rest.Config{Host: server.URL}, 5*time.Second, 200*time.Millisecond); err != nil {
		t.Fatalf("expected the external IP to appear, got: %v", err)
	}
}

func TestWaitForRouterDefaultExternalIP_IgnoresIngressEntriesWithoutIP(t *testing.T) {
	// A nonempty ingress list passes the readiness gate only once an entry reports an IP: an
	// entry with every field unset, or only a hostname, still has no IP for the DNS record.
	attempts := 0
	noIP := `{"kind":"Service","apiVersion":"v1","status":{"loadBalancer":{"ingress":[{}]}}}`
	withIP := `{"kind":"Service","apiVersion":"v1","status":{"loadBalancer":{"ingress":[{"hostname":"ingress-ip.example.io"},{"ip":"203.0.113.10"}]}}}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		attempts++
		if attempts >= 3 {
			_, _ = w.Write([]byte(withIP))
			return
		}
		_, _ = w.Write([]byte(noIP))
	}))
	t.Cleanup(server.Close)

	if err := waitForRouterDefaultExternalIP(context.Background(), &rest.Config{Host: server.URL}, 5*time.Second, 200*time.Millisecond); err != nil {
		t.Fatalf("expected the gate to wait past IP-less ingress entries and pass once an IP appears, got: %v", err)
	}
}

func TestWaitForRouterDefaultExternalIP_TimesOutWhenIngressNeverGainsAnIP(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		status string
		want   string
	}{
		{name: "no ingress entries", status: ``, want: "no external load balancer ingress IPs"},
		{name: "entry with no fields set", status: `"ingress":[{}]`, want: "none has an assigned external IP"},
		{name: "hostname-only entry", status: `"ingress":[{"hostname":"ingress-ip.example.io"}]`, want: "none has an assigned external IP"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			service := fmt.Sprintf(`{"kind":"Service","apiVersion":"v1","status":{"loadBalancer":{%s}}}`, testCase.status)
			server := jsonServer(t, service)

			err := waitForRouterDefaultExternalIP(context.Background(), &rest.Config{Host: server.URL}, 1*time.Second, 100*time.Millisecond)
			if err == nil {
				t.Fatalf("expected a timeout for %q, got success", testCase.status)
			}
			if !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("unexpected error text: %v", err)
			}
		})
	}
}

func TestWaitForRouterDefaultExternalIP_TimeoutCollectsDiagnostics(t *testing.T) {
	// A gate timeout must dump the same router-default state the DNS failure path
	// collects, so it names which component was holding out.
	var eventsRequests int
	service := `{"kind":"Service","apiVersion":"v1","status":{"loadBalancer":{}}}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Path, "/events") {
			eventsRequests++
			_, _ = w.Write([]byte(`{"kind":"EventList","apiVersion":"v1","items":[]}`))
			return
		}
		_, _ = w.Write([]byte(service))
	}))
	t.Cleanup(server.Close)

	err := waitForRouterDefaultExternalIP(context.Background(), &rest.Config{Host: server.URL}, 1*time.Second, 100*time.Millisecond)
	if err == nil {
		t.Fatal("expected a timeout when no external IP is assigned")
	}
	if eventsRequests == 0 {
		t.Fatal("gate timeout should collect router-default diagnostics, but no events request was made")
	}
}

func TestWaitForRouterDefaultExternalIP_ReportsPersistentAPIError(t *testing.T) {
	// When no Service state is ever observed, the last API error must be
	// preserved instead of being displaced by the bare wait timeout.
	server := erroringServer(t)

	err := waitForRouterDefaultExternalIP(context.Background(), &rest.Config{Host: server.URL}, 1*time.Second, 100*time.Millisecond)
	if err == nil {
		t.Fatal("expected a timeout when the API keeps failing")
	}
	for _, want := range []string{"timed out", "failed to get openshift-ingress/router-default Service"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error should preserve the last API error (want %q), got: %v", want, err)
		}
	}
}

func TestWaitForRouterDefaultExternalIP_ReportsCallerCancellation(t *testing.T) {
	assertCancellationReportedDistinctly(t, func(ctx context.Context) error {
		return waitForRouterDefaultExternalIP(ctx, &rest.Config{Host: "http://unused.invalid"}, 5*time.Second, 100*time.Millisecond)
	})
}

func TestWaitForRouterDefaultExternalIP_NilConfig(t *testing.T) {
	err := waitForRouterDefaultExternalIP(context.Background(), nil, time.Minute, time.Second)
	if err == nil {
		t.Fatal("expected an error for a nil admin REST config")
	}
	if !strings.Contains(err.Error(), "admin REST config is nil") {
		t.Fatalf("unexpected error text: %v", err)
	}
}

func TestWaitForRouterDefaultExternalIP_RejectsInvalidInterval(t *testing.T) {
	config := &rest.Config{Host: "http://unused.invalid"}
	err := waitForRouterDefaultExternalIP(context.Background(), config, time.Minute, 0)
	if err == nil || !strings.Contains(err.Error(), "interval must be > 0") {
		t.Fatalf("zero interval: got %v, want \"interval must be > 0\" error", err)
	}
}
