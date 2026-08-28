// Copyright 2026 Microsoft Corporation
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package version

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/blang/semver/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGetZStreamOffset verifies that the stable channel group selects the penultimate release
// (offset 1) while every other channel group uses the tip (offset 0).
func TestGetZStreamOffset(t *testing.T) {
	tests := []struct {
		channel string
		want    uint
	}{
		{channel: "stable", want: 1},
		{channel: "candidate", want: 0},
		{channel: "fast", want: 0},
		{channel: "nightly", want: 0},
		{channel: "", want: 0},
	}
	for _, tt := range tests {
		t.Run(tt.channel, func(t *testing.T) {
			assert.Equalf(t, tt.want, GetZStreamOffset(tt.channel), "unexpected z-stream offset for channel %q", tt.channel)
		})
	}
}

// graphResponseRoundTripper returns a RoundTrip that always responds with the given Cincinnati
// graph JSON body and HTTP 200. It records whether it was invoked so a test can assert that the
// graph path was (or was not) taken.
func graphResponseRoundTripper(body string, called *bool) func(*http.Request) (*http.Response, error) {
	return func(*http.Request) (*http.Response, error) {
		if called != nil {
			*called = true
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(body)),
		}, nil
	}
}

// TestSelectControlPlaneVersion_ReturnsChannelTipAtOffset verifies that selectControlPlaneVersion
// queries the OpenShift update service (graph API) for the minor's channel and returns the release at
// the requested offset from the tip, independent of any active versions.
func TestSelectControlPlaneVersion_ReturnsChannelTipAtOffset(t *testing.T) {
	// Nodes intentionally out of order to prove selection sorts descending.
	graphJSON := `{"nodes":[` +
		`{"version":"4.20.1","payload":"quay.io/openshift-release-dev/ocp-release:4.20.1-multi"},` +
		`{"version":"4.20.5","payload":"quay.io/openshift-release-dev/ocp-release:4.20.5-multi"},` +
		`{"version":"4.20.3","payload":"quay.io/openshift-release-dev/ocp-release:4.20.3-multi"}` +
		`]}`

	tests := []struct {
		name   string
		offset uint
		want   string
	}{
		{name: "tip", offset: 0, want: "4.20.5"},
		{name: "penultimate", offset: 1, want: "4.20.3"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var graphCalled bool
			result, err := selectControlPlaneVersion(context.Background(), graphResponseRoundTripper(graphJSON, &graphCalled), "candidate", semver.MustParse("4.20.0"), tt.offset)
			require.NoError(t, err, "expected selectControlPlaneVersion to succeed")
			require.NotNil(t, result, "expected a resolved version")
			assert.Equal(t, tt.want, result.String(), "unexpected version for offset %d", tt.offset)
			assert.True(t, graphCalled, "expected the graph API round tripper to be invoked")
		})
	}
}
