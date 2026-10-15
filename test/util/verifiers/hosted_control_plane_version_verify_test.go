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
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"k8s.io/client-go/rest"

	configv1 "github.com/openshift/api/config/v1"
)

func TestVerifyHostedControlPlaneZStreamUpgradeOnly_FailureIncludesHistoryAndProgressing(t *testing.T) {
	cv := configv1.ClusterVersion{
		Status: configv1.ClusterVersionStatus{
			History: []configv1.UpdateHistory{
				{Version: "4.20.20", State: configv1.CompletedUpdate},
			},
			Conditions: []configv1.ClusterOperatorStatusCondition{
				{Type: "Progressing", Status: configv1.ConditionTrue, Reason: "ClusterUpdating", Message: "rolling content"},
			},
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(cv)
	}))
	t.Cleanup(server.Close)

	restConfig := &rest.Config{Host: server.URL}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	v := VerifyHostedControlPlaneZStreamUpgradeOnly("4.20.20", time.Second)
	err := v.Verify(ctx, restConfig)
	if err == nil {
		t.Fatal("expected failure, got nil")
	}

	message := err.Error()
	if !strings.Contains(message, "4.20.20=Completed") {
		t.Fatalf("expected failure error to include history, got: %s", message)
	}
	if !strings.Contains(message, "Progressing=True") {
		t.Fatalf("expected failure error to include Progressing details, got: %s", message)
	}
}

func TestVerifyHostedControlPlaneYStreamUpgrade_FailureIncludesHistoryAndProgressing(t *testing.T) {
	cv := configv1.ClusterVersion{
		Status: configv1.ClusterVersionStatus{
			History: []configv1.UpdateHistory{
				{Version: "4.20.20", State: configv1.CompletedUpdate},
			},
			Conditions: []configv1.ClusterOperatorStatusCondition{
				{Type: "Progressing", Status: configv1.ConditionTrue, Reason: "ClusterUpdating", Message: "rolling content"},
			},
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(cv)
	}))
	t.Cleanup(server.Close)

	restConfig := &rest.Config{Host: server.URL}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	v := VerifyHostedControlPlaneYStreamUpgrade("4.20", "4.21", time.Second)
	err := v.Verify(ctx, restConfig)
	if err == nil {
		t.Fatal("expected failure, got nil")
	}

	message := err.Error()
	if !strings.Contains(message, "4.20.20=Completed") {
		t.Fatalf("expected failure error to include history, got: %s", message)
	}
	if !strings.Contains(message, "Progressing=True") {
		t.Fatalf("expected failure error to include Progressing details, got: %s", message)
	}
}
