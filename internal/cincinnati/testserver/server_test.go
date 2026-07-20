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

package testserver

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/blang/semver/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	cvocincinnati "github.com/openshift/cluster-version-operator/pkg/cincinnati"
)

func TestServer_GetUpdates(t *testing.T) {
	t.Parallel()

	graph419 := NewGraph().
		Edges("4.19.10", "4.19.15", "4.19.18", "4.19.22").
		Edges("4.19.15", "4.19.18", "4.19.22").
		Edges("4.19.18", "4.19.22")

	graph420 := NewGraph().
		Versions("4.19.22").
		Edges("4.19.22", "4.20.5").
		Edges("4.20.0", "4.20.5")

	server := NewServer(t, map[string]*Graph{
		"stable-4.19": graph419,
		"stable-4.20": graph420,
	})
	client := server.NewClient()
	ctx := context.Background()

	tests := []struct {
		name               string
		channel            string
		version            string
		wantCurrent        string
		wantUpdateCount    int
		wantUpdateVersions []string
		wantErr            bool
		wantErrReason      string
	}{
		{
			name:               "upgrades from 4.19.10",
			channel:            "stable-4.19",
			version:            "4.19.10",
			wantCurrent:        "4.19.10",
			wantUpdateCount:    3,
			wantUpdateVersions: []string{"4.19.15", "4.19.18", "4.19.22"},
		},
		{
			name:               "upgrades from 4.19.15",
			channel:            "stable-4.19",
			version:            "4.19.15",
			wantCurrent:        "4.19.15",
			wantUpdateCount:    2,
			wantUpdateVersions: []string{"4.19.18", "4.19.22"},
		},
		{
			name:            "no upgrades from latest",
			channel:         "stable-4.19",
			version:         "4.19.22",
			wantCurrent:     "4.19.22",
			wantUpdateCount: 0,
		},
		{
			name:               "cross-minor gateway",
			channel:            "stable-4.20",
			version:            "4.19.22",
			wantCurrent:        "4.19.22",
			wantUpdateCount:    1,
			wantUpdateVersions: []string{"4.20.5"},
		},
		{
			name:          "version not in channel",
			channel:       "stable-4.19",
			version:       "4.18.0",
			wantErr:       true,
			wantErrReason: "VersionNotFound",
		},
		{
			name:          "unknown channel returns error",
			channel:       "stable-4.21",
			version:       "4.21.0",
			wantErr:       true,
			wantErrReason: "ResponseFailed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ver := semver.MustParse(tt.version)
			current, updates, _, err := client.GetUpdates(ctx, server.URI(), "multi", "multi", tt.channel, ver)
			if tt.wantErr {
				require.Error(t, err)
				if tt.wantErrReason != "" {
					var cErr *cvocincinnati.Error
					require.True(t, errors.As(err, &cErr), "expected *cincinnati.Error, got %T", err)
					assert.Equal(t, tt.wantErrReason, cErr.Reason)
				}
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantCurrent, current.Version)
			assert.Len(t, updates, tt.wantUpdateCount)
			for _, wantVersion := range tt.wantUpdateVersions {
				found := false
				for _, u := range updates {
					if u.Version == wantVersion {
						found = true
						break
					}
				}
				assert.True(t, found, "expected update to %s not found in %v", wantVersion, updates)
			}
		})
	}
}

func TestServer_MultipleClients(t *testing.T) {
	t.Parallel()

	server := NewServer(t, map[string]*Graph{
		"stable-4.19": NewGraph().
			Edges("4.19.0", "4.19.5"),
	})

	ctx := context.Background()
	client1 := server.NewClient()
	client2 := server.NewClient()

	_, updates1, _, err := client1.GetUpdates(ctx, server.URI(), "multi", "multi", "stable-4.19", semver.MustParse("4.19.0"))
	require.NoError(t, err)
	assert.Len(t, updates1, 1)

	_, updates2, _, err := client2.GetUpdates(ctx, server.URI(), "multi", "multi", "stable-4.19", semver.MustParse("4.19.0"))
	require.NoError(t, err)
	assert.Len(t, updates2, 1)
}

func TestGraph_EdgesAutoAddNodes(t *testing.T) {
	t.Parallel()

	g := NewGraph().
		Edges("4.19.0", "4.19.1")

	data, err := g.marshal()
	require.NoError(t, err)

	assert.Contains(t, string(data), `"4.19.0"`)
	assert.Contains(t, string(data), `"4.19.1"`)
}

func TestGraph_DuplicateVersions(t *testing.T) {
	t.Parallel()

	g := NewGraph().
		Versions("4.19.0", "4.19.1").
		Versions("4.19.0").
		Edges("4.19.0", "4.19.1")

	data, err := g.marshal()
	require.NoError(t, err)

	var graph graphJSON
	require.NoError(t, json.Unmarshal(data, &graph))
	assert.Len(t, graph.Nodes, 2)
	assert.Len(t, graph.Edges, 1)
}

func TestGraph_InvalidSemverPanics(t *testing.T) {
	t.Parallel()

	assert.Panics(t, func() {
		NewGraph().Versions("not-a-version")
	})
}
