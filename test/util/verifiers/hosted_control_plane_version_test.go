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
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/version"
	"k8s.io/client-go/rest"
	"k8s.io/utils/ptr"

	configv1 "github.com/openshift/api/config/v1"
)

func mustTime(t *testing.T, value string) metav1.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, value)
	require.NoError(t, err)
	return metav1.NewTime(parsed)
}

func TestRenderClusterVersionHistory(t *testing.T) {
	started := mustTime(t, "2026-05-06T00:36:22Z")
	completed := mustTime(t, "2026-05-06T01:24:16Z")

	for _, testCase := range []struct {
		name     string
		history  []configv1.UpdateHistory
		expected string
	}{
		{
			name:     "empty history",
			history:  nil,
			expected: "<empty>",
		},
		{
			name: "in-progress entry has no completion stamp",
			history: []configv1.UpdateHistory{
				{Version: "4.21.13", State: configv1.PartialUpdate, StartedTime: completed},
			},
			expected: "4.21.13=Partial started=2026-05-06T01:24:16Z",
		},
		{
			name: "the ARO-26775 history, newest entry first",
			history: []configv1.UpdateHistory{
				{
					Version: "4.21.13", State: configv1.PartialUpdate, StartedTime: completed,
					Image: "example.io/openshift-release-dev/ocp-release@sha256:9d0436ae6f2106e055d7f735d9416d2842d39f2f988be5f3a8ec4f1d0e064ff9",
				},
				{
					Version: "4.20.20", State: configv1.CompletedUpdate, StartedTime: started, CompletionTime: &completed,
					Image: "example.io/openshift-release-dev/ocp-release@sha256:1a59c7136e704fb5e0b4824b55757dac29b04c606ee2d05757a142cb90504b5d",
				},
			},
			expected: "4.21.13=Partial started=2026-05-06T01:24:16Z image=sha256:9d0436ae6f21; " +
				"4.20.20=Completed started=2026-05-06T00:36:22Z completed=2026-05-06T01:24:16Z image=sha256:1a59c7136e70",
		},
		{
			name: "entry with no timestamps or image renders version and state only",
			history: []configv1.UpdateHistory{
				{Version: "4.20.20", State: configv1.CompletedUpdate},
			},
			expected: "4.20.20=Completed",
		},
		{
			name: "same version, different payloads, stays distinguishable",
			history: []configv1.UpdateHistory{
				{Version: "4.21.13", State: configv1.PartialUpdate, Image: "example.io/ocp-release@sha256:aaaaaaaaaaaabbbb"},
				{Version: "4.21.13", State: configv1.PartialUpdate, Image: "example.io/ocp-release@sha256:ccccccccccccdddd"},
			},
			expected: "4.21.13=Partial image=sha256:aaaaaaaaaaaa; 4.21.13=Partial image=sha256:cccccccccccc",
		},
		{
			name: "an entry spanning UTC midnight still reads in order",
			history: []configv1.UpdateHistory{
				{
					Version:        "4.20.20",
					State:          configv1.CompletedUpdate,
					StartedTime:    mustTime(t, "2026-05-05T23:55:00Z"),
					CompletionTime: ptr.To(mustTime(t, "2026-05-06T00:20:00Z")),
				},
			},
			expected: "4.20.20=Completed started=2026-05-05T23:55:00Z completed=2026-05-06T00:20:00Z",
		},
		{
			name: "a non-UTC stamp is normalised to UTC",
			history: []configv1.UpdateHistory{
				{
					Version:     "4.20.20",
					State:       configv1.CompletedUpdate,
					StartedTime: metav1.NewTime(started.In(time.FixedZone("UTC+5", 5*60*60))),
				},
			},
			expected: "4.20.20=Completed started=2026-05-06T00:36:22Z",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			require.Equal(t, testCase.expected, renderClusterVersionHistory(testCase.history))
		})
	}
}

func TestShortImageDigest(t *testing.T) {
	for _, testCase := range []struct {
		name     string
		image    string
		expected string
	}{
		{name: "empty", image: "", expected: ""},
		{
			name:     "digest is truncated to 12 characters",
			image:    "example.io/ocp-release@sha256:9d0436ae6f2106e055d7f735d9416d2842d39f2f988be5f3a8ec4f1d0e064ff9",
			expected: "sha256:9d0436ae6f21",
		},
		{name: "short digest is preserved", image: "example.io/ocp-release@sha256:abc", expected: "sha256:abc"},
		{
			name:     "tag reference falls back to the last path segment",
			image:    "example.io/ocp-release:4.21.13",
			expected: "ocp-release:4.21.13",
		},
		{name: "reference with no path separator", image: "ocp-release:4.21.13", expected: "ocp-release:4.21.13"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			require.Equal(t, testCase.expected, shortImageDigest(testCase.image))
		})
	}
}

func TestClusterVersionSummaryIncludesProgress(t *testing.T) {
	status := configv1.ClusterVersionStatus{
		History: []configv1.UpdateHistory{
			{Version: "4.21.13", State: configv1.PartialUpdate, StartedTime: mustTime(t, "2026-05-06T01:24:16Z")},
		},
		Conditions: []configv1.ClusterOperatorStatusCondition{
			{
				Type:    configv1.OperatorProgressing,
				Status:  configv1.ConditionTrue,
				Message: "Working towards 4.21.13: 150 of 900 done",
			},
		},
	}

	require.Equal(t,
		"history is 4.21.13=Partial started=2026-05-06T01:24:16Z; Progressing=True: Working towards 4.21.13: 150 of 900 done",
		clusterVersionSummary(status))
}

func TestClusterVersionSummaryWithoutProgress(t *testing.T) {
	status := configv1.ClusterVersionStatus{
		History: []configv1.UpdateHistory{
			{Version: "4.20.20", State: configv1.CompletedUpdate},
		},
	}

	require.Equal(t, "history is 4.20.20=Completed", clusterVersionSummary(status))
}

func newClusterVersionServer(t *testing.T, clusterVersion configv1.ClusterVersion) *rest.Config {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(writer).Encode(clusterVersion))
	}))
	t.Cleanup(server.Close)
	return &rest.Config{Host: server.URL}
}

func TestVerifyHostedControlPlaneZStreamUpgradeOnly_FailureIncludesHistoryAndProgressing(t *testing.T) {
	clusterVersion := configv1.ClusterVersion{
		Status: configv1.ClusterVersionStatus{
			History: []configv1.UpdateHistory{
				{Version: "4.20.20", State: configv1.CompletedUpdate},
			},
			Conditions: []configv1.ClusterOperatorStatusCondition{
				{
					Type:    configv1.OperatorProgressing,
					Status:  configv1.ConditionTrue,
					Message: "rolling content",
				},
			},
		},
	}

	err := VerifyHostedControlPlaneZStreamUpgradeOnly("4.20.20").
		Verify(context.Background(), newClusterVersionServer(t, clusterVersion))
	require.Error(t, err)
	require.Contains(t, err.Error(), "4.20.20=Completed")
	require.Contains(t, err.Error(), "Progressing=True")
}

func TestVerifyHostedControlPlaneYStreamUpgrade_FailureIncludesHistoryAndProgressing(t *testing.T) {
	clusterVersion := configv1.ClusterVersion{
		Status: configv1.ClusterVersionStatus{
			History: []configv1.UpdateHistory{
				{Version: "4.20.20", State: configv1.CompletedUpdate},
			},
			Conditions: []configv1.ClusterOperatorStatusCondition{
				{
					Type:    configv1.OperatorProgressing,
					Status:  configv1.ConditionTrue,
					Message: "rolling content",
				},
			},
		},
	}

	err := VerifyHostedControlPlaneYStreamUpgrade("4.20", "4.21").
		Verify(context.Background(), newClusterVersionServer(t, clusterVersion))
	require.Error(t, err)
	require.Contains(t, err.Error(), "4.20.20=Completed")
	require.Contains(t, err.Error(), "Progressing=True")
}

func TestVerifyKubeAPIServerServerVersionUpgraded_FailureNamesBothVersions(t *testing.T) {
	preUpgrade := &version.Info{GitVersion: "4.20.20"}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(writer).Encode(preUpgrade))
	}))
	t.Cleanup(server.Close)

	err := VerifyKubeAPIServerServerVersionUpgraded(preUpgrade).
		Verify(context.Background(), &rest.Config{Host: server.URL})
	require.Error(t, err)
	require.Contains(t, err.Error(), "pre-upgrade \"4.20.20\"")
	require.Contains(t, err.Error(), "still observing \"4.20.20\"")
}
