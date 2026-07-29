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

package cincinnati

import (
	"context"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/blang/semver/v4"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	cvocincinnati "github.com/openshift/cluster-version-operator/pkg/cincinnati"
)

func skipUnlessCincinnatiReachable(t *testing.T) {
	t.Helper()
	if os.Getenv("LIVE_CINCINNATI_TESTS") == "" {
		t.Skip("set LIVE_CINCINNATI_TESTS=1 to run tests against real Cincinnati")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.openshift.com/api/upgrades_info/v1/graph?channel=stable-4.18", nil)
	if err != nil {
		t.Skipf("cannot create probe request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Skipf("Cincinnati unreachable: %v", err)
	}
	resp.Body.Close()
}

func TestLiveGraphClient_ChannelExists(t *testing.T) {
	skipUnlessCincinnatiReachable(t)
	t.Parallel()

	client := NewGraphClient()
	ctx := context.Background()

	tests := []struct {
		name         string
		channelGroup string
		minor        string
		wantExists   bool
	}{
		// --- stable ---
		{
			name:         "stable-4.18 exists",
			channelGroup: "stable",
			minor:        "4.18",
			wantExists:   true,
		},
		{
			name:         "stable-4.19 exists",
			channelGroup: "stable",
			minor:        "4.19",
			wantExists:   true,
		},
		{
			name:         "stable-5.0 does not exist yet",
			channelGroup: "stable",
			minor:        "5.0",
			wantExists:   false,
		},
		{
			name:         "stable-99.99 does not exist",
			channelGroup: "stable",
			minor:        "99.99",
			wantExists:   false,
		},

		// --- candidate ---
		{
			name:         "candidate-4.20 exists",
			channelGroup: "candidate",
			minor:        "4.20",
			wantExists:   true,
		},
		{
			name:         "candidate-4.22 exists",
			channelGroup: "candidate",
			minor:        "4.22",
			wantExists:   true,
		},
		{
			name:         "candidate-5.0 exists",
			channelGroup: "candidate",
			minor:        "5.0",
			wantExists:   true,
		},
		{
			name:         "candidate-99.99 does not exist",
			channelGroup: "candidate",
			minor:        "99.99",
			wantExists:   false,
		},

		// --- fast ---
		{
			name:         "fast-4.19 exists",
			channelGroup: "fast",
			minor:        "4.19",
			wantExists:   true,
		},
		{
			name:         "fast-99.99 does not exist",
			channelGroup: "fast",
			minor:        "99.99",
			wantExists:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			exists, err := client.ChannelExists(ctx, tt.channelGroup, tt.minor)
			require.NoError(t, err)
			assert.Equal(t, tt.wantExists, exists, "ChannelExists(%s, %s)", tt.channelGroup, tt.minor)
			t.Logf("url: %s  exists: %v", client.ChannelExistsURL(tt.channelGroup, tt.minor), exists)
		})
	}
}

func TestLiveGraphClient_NightlyChannelExists(t *testing.T) {
	skipUnlessCincinnatiReachable(t)
	t.Parallel()

	client := NewGraphClient()
	ctx := context.Background()

	tests := []struct {
		name       string
		minor      string
		wantExists bool
	}{
		{
			name:       "nightly-4.19 exists",
			minor:      "4.19",
			wantExists: true,
		},
		{
			name:       "nightly-4.20 exists",
			minor:      "4.20",
			wantExists: true,
		},
		{
			name:       "nightly-5.0 exists",
			minor:      "5.0",
			wantExists: true,
		},
		{
			name:       "nightly-99.99 does not exist",
			minor:      "99.99",
			wantExists: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			exists, err := client.ChannelExists(ctx, "nightly", tt.minor)
			require.NoError(t, err)
			assert.Equal(t, tt.wantExists, exists, "ChannelExists(nightly, %s)", tt.minor)
			t.Logf("url: %s  exists: %v", client.ChannelExistsURL("nightly", tt.minor), exists)
		})
	}
}

func TestLiveGetUpdates_Graph(t *testing.T) {
	//skipUnlessCincinnatiReachable(t)
	t.Parallel()

	cvoClient := cvocincinnati.NewClient(
		uuid.Nil,
		http.DefaultTransport.(*http.Transport).Clone(),
		"ARO-HCP-test",
		NewAcceptAllConditionRegistry(),
	)

	tests := []struct {
		name         string
		channelGroup string
		channel      string
		version      semver.Version
		wantUpdates  bool
		wantNotFound bool
	}{
		// z-stream upgrades within stable-4.18 (4.18.0 is not in stable, starts at 4.18.1)
		{
			name:        "stable-4.18 from 4.18.1 has upgrades",
			channel:     "stable-4.18",
			version:     semver.MustParse("4.18.1"),
			wantUpdates: true,
		},
		// z-stream upgrades within stable-4.19
		{
			name:        "stable-4.19 from 4.19.0 has upgrades",
			channel:     "stable-4.19",
			version:     semver.MustParse("4.19.0"),
			wantUpdates: true,
		},
		// y-stream gateway: 4.19 versions present in candidate-4.20
		{
			name:        "candidate-4.20 from 4.19.9 has upgrades (gateway check)",
			channel:     "candidate-4.20",
			version:     semver.MustParse("4.19.9"),
			wantUpdates: true,
		},
		// y-stream gateway: 4.22 versions present in candidate-5.0
		{
			name:        "candidate-5.0 from 4.22.2 has upgrades (4.22 to 5.0 gateway)",
			channel:     "candidate-5.0",
			version:     semver.MustParse("4.22.2"),
			wantUpdates: true,
		},
		// version not in channel
		{
			name:         "stable-4.18 does not contain 3.0.0",
			channel:      "stable-4.18",
			version:      semver.MustParse("3.0.0"),
			wantNotFound: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()

			uri, err := GetCincinnatiURI("stable")
			require.NoError(t, err)

			_, updates, _, err := cvoClient.GetUpdates(ctx, uri, "multi", "multi", tt.channel, tt.version)

			if tt.wantNotFound {
				require.Error(t, err)
				assert.True(t, IsCincinnatiVersionNotFoundError(err), "expected VersionNotFound, got: %v", err)
				t.Logf("version %s not found in %s (expected)", tt.version, tt.channel)
				return
			}

			require.NoError(t, err)
			t.Logf("channel %s from %s: %d updates available", tt.channel, tt.version, len(updates))
			if tt.wantUpdates {
				assert.NotEmpty(t, updates, "expected updates from %s in %s", tt.version, tt.channel)
				for _, u := range updates {
					t.Logf("  -> %s", u.Version)
				}
			}
		})
	}
}
