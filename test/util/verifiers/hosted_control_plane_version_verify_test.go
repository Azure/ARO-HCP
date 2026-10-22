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
	"sync/atomic"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/version"
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

// newKubeAPIServerVersionTestServer serves the two endpoints the kube-apiserver upgrade verifier
// reads: /version for the kube-apiserver itself, and clusterversions/version for the diagnostics
// its failure path gathers. versionHandler serves /version; cv is served for the clusterversion.
func newKubeAPIServerVersionTestServer(t *testing.T, versionHandler http.HandlerFunc, cv *configv1.ClusterVersion) *rest.Config {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/version":
			versionHandler.ServeHTTP(w, r)
		case "/apis/config.openshift.io/v1/clusterversions/version":
			if err := json.NewEncoder(w).Encode(cv); err != nil {
				t.Errorf("encode the clusterversion response: %v", err)
			}
		default:
			t.Errorf("unexpected request to %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return &rest.Config{Host: server.URL}
}

// TestVerifyKubeAPIServerServerVersionUpgraded_SucceedsAfterVersionChange covers the retry path:
// the first reading still reports the pre-upgrade version and a later one reports the upgrade,
// which the verifier must observe rather than report a timeout. The poll interval outlasts the
// timeout, so the loop gets exactly one look and the changed version can only be seen by the
// post-deadline check -- the same shape as an apiserver that finishes rolling just inside the
// budget, which is the case the final check exists for.
func TestVerifyKubeAPIServerServerVersionUpgraded_SucceedsAfterVersionChange(t *testing.T) {
	preUpgrade := &version.Info{Major: "1", Minor: "31", GitVersion: "v1.31.0"}
	postUpgrade := version.Info{Major: "1", Minor: "32", GitVersion: "v1.32.6"}

	var versionReads atomic.Int32
	restConfig := newKubeAPIServerVersionTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if versionReads.Add(1) == 1 {
			_ = json.NewEncoder(w).Encode(*preUpgrade)
			return
		}
		_ = json.NewEncoder(w).Encode(postUpgrade)
	}, &configv1.ClusterVersion{})

	v := VerifyKubeAPIServerServerVersionUpgraded(preUpgrade, 150*time.Millisecond)
	if err := v.Verify(context.Background(), restConfig); err != nil {
		t.Fatalf("expected the verifier to succeed once /version reports the upgrade, got: %v", err)
	}
	if reads := versionReads.Load(); reads != 2 {
		t.Fatalf("expected 2 /version reads (pre-upgrade, then upgraded), got %d", reads)
	}
}

// TestVerifyKubeAPIServerServerVersionUpgraded_TimeoutAppendsClusterVersionDiagnostics covers
// the failure path: when /version never changes, the timeout error must carry the clusterversion
// state the verifier's diagnose function gathers -- the history and the Progressing message are
// what attribute a stalled rollout from the Prow log alone.
func TestVerifyKubeAPIServerServerVersionUpgraded_TimeoutAppendsClusterVersionDiagnostics(t *testing.T) {
	preUpgrade := &version.Info{Major: "1", Minor: "31", GitVersion: "v1.31.0"}
	cv := &configv1.ClusterVersion{
		Status: configv1.ClusterVersionStatus{
			Desired: configv1.Release{Version: "4.21.13"},
			History: []configv1.UpdateHistory{
				{Version: "4.21.13", State: configv1.PartialUpdate, Image: "registry.ci.openshift.io/ocp/release@sha256:9d0436ae6f2100000000000000000000000000000000000000000000000000"},
			},
			Conditions: []configv1.ClusterOperatorStatusCondition{
				{Type: configv1.OperatorProgressing, Status: configv1.ConditionTrue, Reason: "ClusterUpdating", Message: "Working towards 4.21.13"},
			},
		},
	}

	restConfig := newKubeAPIServerVersionTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(*preUpgrade)
	}, cv)

	v := VerifyKubeAPIServerServerVersionUpgraded(preUpgrade, 150*time.Millisecond)
	err := v.Verify(context.Background(), restConfig)
	if err == nil {
		t.Fatal("expected a timeout error when /version never changes, got nil")
	}

	message := err.Error()
	if !strings.Contains(message, "timed out") {
		t.Fatalf("expected 'timed out' in the error, got: %s", message)
	}
	if !strings.Contains(message, `still observing "v1.31.0"`) {
		t.Fatalf("expected the failure to name the unchanged version, got: %s", message)
	}
	if !strings.Contains(message, "4.21.13=Partial") {
		t.Fatalf("expected the clusterversion history in the timeout diagnostics, got: %s", message)
	}
	if !strings.Contains(message, "Working towards 4.21.13") {
		t.Fatalf("expected the Progressing message in the timeout diagnostics, got: %s", message)
	}
}
