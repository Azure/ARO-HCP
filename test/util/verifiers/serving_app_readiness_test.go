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
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"k8s.io/client-go/rest"

	configv1 "github.com/openshift/api/config/v1"
)

func TestWaitForClusterOperatorCondition_WaitsForAvailable(t *testing.T) {
	attempts := 0
	start := time.Now()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		status := configv1.ConditionFalse
		attempts++
		if attempts >= 2 {
			status = configv1.ConditionTrue
		}
		t.Logf("handler attempt=%d status=%v elapsed=%s", attempts, status, time.Since(start))
		operator := configv1.ClusterOperator{
			Status: configv1.ClusterOperatorStatus{
				Conditions: []configv1.ClusterOperatorStatusCondition{
					{Type: configv1.OperatorAvailable, Status: status, Reason: "Progressing", Message: "waiting"},
				},
			},
		}
		_ = json.NewEncoder(w).Encode(operator)
	}))
	t.Cleanup(server.Close)

	if err := waitForClusterOperatorCondition(context.Background(), &rest.Config{Host: server.URL}, "ingress", configv1.OperatorAvailable, 5*time.Second, 200*time.Millisecond); err != nil {
		t.Fatalf("expected the condition to become available, got: %v", err)
	}
}

func TestWaitForClusterOperatorCondition_ReportsUnavailableOperator(t *testing.T) {
	operator := configv1.ClusterOperator{
		Status: configv1.ClusterOperatorStatus{
			Conditions: []configv1.ClusterOperatorStatusCondition{
				{Type: configv1.OperatorAvailable, Status: configv1.ConditionFalse, Reason: "Progressing", Message: "rolling"},
			},
		},
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(operator)
	}))
	t.Cleanup(server.Close)

	if err := waitForClusterOperatorCondition(context.Background(), &rest.Config{Host: server.URL}, "ingress", configv1.OperatorAvailable, 1*time.Second, 100*time.Millisecond); err == nil {
		t.Fatal("expected the timeout to surface the unavailable condition")
	} else if !strings.Contains(err.Error(), "ingress") || !strings.Contains(err.Error(), "Progressing") {
		t.Fatalf("error should name the operator and condition, got: %v", err)
	}
}

func TestWaitForRouterDefaultExternalIP_WaitsUntilAssigned(t *testing.T) {
	attempts := 0
	service := `{"kind":"Service","apiVersion":"v1","status":{"loadBalancer":{"ingress":[]}}}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		attempts++
		if attempts >= 2 {
			service = `{"kind":"Service","apiVersion":"v1","status":{"loadBalancer":{"ingress":[{"ip":"203.0.113.10"}]}}}`
		}
		_, _ = w.Write([]byte(service))
	}))
	t.Cleanup(server.Close)

	if err := waitForRouterDefaultExternalIP(context.Background(), &rest.Config{Host: server.URL}, 5*time.Second, 200*time.Millisecond); err != nil {
		t.Fatalf("expected the external IP to appear, got: %v", err)
	}
}

func TestWaitForRouterDefaultExternalIP_ReportsMissingExternalIP(t *testing.T) {
	service := `{"kind":"Service","apiVersion":"v1","status":{"loadBalancer":{}}}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(service))
	}))
	t.Cleanup(server.Close)

	if err := waitForRouterDefaultExternalIP(context.Background(), &rest.Config{Host: server.URL}, 1*time.Second, 100*time.Millisecond); err == nil {
		t.Fatal("expected a timeout when no external IP is assigned")
	} else if !strings.Contains(err.Error(), "no external load balancer ingress IPs") {
		t.Fatalf("unexpected error text: %v", err)
	}
}

func TestWaitForRouterDefaultExternalIP_IgnoresIngressEntriesWithoutIP(t *testing.T) {
	// A nonempty ingress list passes the readiness gate only once an entry reports an IP: an
	// entry with every field unset, or only a hostname, still has no IP for the DNS record.
	attempts := 0
	service := `{"kind":"Service","apiVersion":"v1","status":{"loadBalancer":{"ingress":[{}]}}}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		attempts++
		if attempts >= 3 {
			service = `{"kind":"Service","apiVersion":"v1","status":{"loadBalancer":{"ingress":[{"hostname":"ingress-ip.example.io"},{"ip":"203.0.113.10"}]}}}`
		}
		_, _ = w.Write([]byte(service))
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
		{name: "entry with no fields set", status: `"ingress":[{}]`, want: "none has an assigned external IP"},
		{name: "hostname-only entry", status: `"ingress":[{"hostname":"ingress-ip.example.io"}]`, want: "none has an assigned external IP"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			service := fmt.Sprintf(`{"kind":"Service","apiVersion":"v1","status":{"loadBalancer":{%s}}}`, testCase.status)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(service))
			}))
			t.Cleanup(server.Close)

			if err := waitForRouterDefaultExternalIP(context.Background(), &rest.Config{Host: server.URL}, 1*time.Second, 100*time.Millisecond); err == nil {
				t.Fatalf("expected a timeout for %q, got success", testCase.status)
			} else if !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("unexpected error text: %v", err)
			}
		})
	}
}
