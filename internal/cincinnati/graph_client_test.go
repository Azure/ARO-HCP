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
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestChannelExists(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/upgrades_info/v1/graph":
			switch r.URL.Query().Get("channel") {
			case "stable-4.20", "candidate-4.20", "fast-4.20":
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"nodes":[{"version":"4.20.0"}]}`))
			case "stable-4.99":
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"nodes":[]}`))
			case "stable-4.98":
				w.WriteHeader(http.StatusNotFound)
			case "stable-4.97":
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte("boom"))
			case "stable-4.96":
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{not-json`))
			default:
				http.Error(w, "unexpected channel: "+r.URL.Query().Get("channel"), http.StatusBadRequest)
				return
			}
		case strings.HasPrefix(r.URL.Path, "/api/v1/releasestream/") && strings.HasSuffix(r.URL.Path, "/tags"):
			if r.URL.Query().Get("phase") != "Accepted" {
				http.Error(w, "unexpected phase: "+r.URL.Query().Get("phase"), http.StatusBadRequest)
				return
			}
			switch r.URL.Path {
			case "/api/v1/releasestream/4.20.0-0.nightly-multi/tags":
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"tags":[{"name":"4.20.0-0.nightly-multi-2026-01-01"}]}`))
			case "/api/v1/releasestream/4.99.0-0.nightly-multi/tags":
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"tags":[]}`))
			case "/api/v1/releasestream/4.98.0-0.nightly-multi/tags":
				w.WriteHeader(http.StatusNotFound)
			case "/api/v1/releasestream/4.97.0-0.nightly-multi/tags":
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte("boom"))
			case "/api/v1/releasestream/4.96.0-0.nightly-multi/tags":
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{not-json`))
			default:
				http.Error(w, "unexpected path: "+r.URL.Path, http.StatusBadRequest)
				return
			}
		default:
			http.Error(w, "unexpected path: "+r.URL.Path, http.StatusBadRequest)
			return
		}
	}))
	t.Cleanup(server.Close)

	var client GraphClient = graphClient{
		graphAPIBase:   server.URL,
		nightlyAPIBase: server.URL,
	}
	ctx := context.Background()

	tests := []struct {
		name         string
		channelGroup string
		minor        string
		wantExists   bool
		wantErr      string
	}{
		{
			name:         "stable existing channel",
			channelGroup: "stable",
			minor:        "4.20",
			wantExists:   true,
		},
		{
			name:         "candidate existing channel",
			channelGroup: "candidate",
			minor:        "4.20",
			wantExists:   true,
		},
		{
			name:         "fast existing channel",
			channelGroup: "fast",
			minor:        "4.20",
			wantExists:   true,
		},
		{
			name:         "stable missing channel empty nodes",
			channelGroup: "stable",
			minor:        "4.99",
			wantExists:   false,
		},
		{
			name:         "stable missing channel 404",
			channelGroup: "stable",
			minor:        "4.98",
			wantExists:   false,
		},
		{
			name:         "stable non-OK status",
			channelGroup: "stable",
			minor:        "4.97",
			wantErr:      "query graph for stable-4.97 returned 500 Internal Server Error: boom",
		},
		{
			name:         "stable invalid JSON body",
			channelGroup: "stable",
			minor:        "4.96",
			wantErr:      "decode graph response for stable-4.96:",
		},
		{
			name:         "nightly existing stream",
			channelGroup: "nightly",
			minor:        "4.20",
			wantExists:   true,
		},
		{
			name:         "nightly missing stream empty tags",
			channelGroup: "nightly",
			minor:        "4.99",
			wantExists:   false,
		},
		{
			name:         "nightly missing stream 404",
			channelGroup: "nightly",
			minor:        "4.98",
			wantExists:   false,
		},
		{
			name:         "nightly non-OK status",
			channelGroup: "nightly",
			minor:        "4.97",
			wantErr:      "query nightly tags for 4.97.0-0.nightly-multi returned 500 Internal Server Error: boom",
		},
		{
			name:         "nightly invalid JSON body",
			channelGroup: "nightly",
			minor:        "4.96",
			wantErr:      "decode nightly tags response for 4.96.0-0.nightly-multi:",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			exists, err := client.ChannelExists(ctx, tt.channelGroup, tt.minor)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				assert.False(t, exists)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantExists, exists)
		})
	}
}
