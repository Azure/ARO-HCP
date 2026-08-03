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

package upgradegraph

import (
	"context"
	"testing"

	"github.com/blang/semver/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Azure/ARO-HCP/internal/cincinnati/testserver"
)

func v(s string) semver.Version {
	return semver.MustParse(s)
}

func TestReachable(t *testing.T) {
	g := New()
	g.AddEdge(v("4.19.0"), v("4.19.5"))
	g.AddEdge(v("4.19.5"), v("4.19.10"))
	g.AddVersion(v("4.19.3"))

	assert.True(t, g.Reachable(v("4.19.0"), v("4.19.5")), "direct edge")
	assert.True(t, g.Reachable(v("4.19.0"), v("4.19.10")), "transitive")
	assert.True(t, g.Reachable(v("4.19.5"), v("4.19.10")), "direct edge")
	assert.False(t, g.Reachable(v("4.19.10"), v("4.19.0")), "wrong direction")
	assert.False(t, g.Reachable(v("4.19.3"), v("4.19.10")), "isolated node")
	assert.True(t, g.Reachable(v("4.19.0"), v("4.19.0")), "self")
	assert.False(t, g.Reachable(v("4.18.0"), v("4.19.0")), "not in graph")
}

func TestPathBetween(t *testing.T) {
	g := New()
	g.AddEdge(v("4.19.0"), v("4.19.5"))
	g.AddEdge(v("4.19.5"), v("4.19.10"))
	g.AddEdge(v("4.19.0"), v("4.19.10"))

	path := g.PathBetween(v("4.19.0"), v("4.19.10"))
	require.NotNil(t, path, "path should exist")
	assert.Equal(t, v("4.19.0"), path[0], "starts at src")
	assert.Equal(t, v("4.19.10"), path[len(path)-1], "ends at dst")

	path = g.PathBetween(v("4.19.0"), v("4.19.5"))
	assert.Equal(t, []semver.Version{v("4.19.0"), v("4.19.5")}, path, "direct edge path")

	assert.Nil(t, g.PathBetween(v("4.19.10"), v("4.19.0")), "no reverse path")
	assert.Nil(t, g.PathBetween(v("4.18.0"), v("4.19.0")), "version not in graph")

	path = g.PathBetween(v("4.19.5"), v("4.19.5"))
	assert.Equal(t, []semver.Version{v("4.19.5")}, path, "self path")
}

func TestStreams(t *testing.T) {
	g := New()
	g.AddEdge(v("4.19.0"), v("4.19.5"))
	g.AddEdge(v("4.19.5"), v("4.19.10"))
	g.AddEdge(v("4.20.0"), v("4.20.3"))

	streams := g.Streams()
	require.Len(t, streams, 2, "two minor streams")

	assert.Equal(t, uint64(4), streams[0].Major)
	assert.Equal(t, uint64(19), streams[0].Minor)
	assert.Equal(t, v("4.19.10"), streams[0].LatestZ, "latest-z for 4.19")
	assert.Len(t, streams[0].Versions, 3, "three versions in 4.19")

	assert.Equal(t, uint64(4), streams[1].Major)
	assert.Equal(t, uint64(20), streams[1].Minor)
	assert.Equal(t, v("4.20.3"), streams[1].LatestZ, "latest-z for 4.20")
	assert.Len(t, streams[1].Versions, 2, "two versions in 4.20")
}

func TestStreams_CrossMajor(t *testing.T) {
	g := New()
	g.AddEdge(v("4.22.0"), v("4.22.5"))
	g.AddEdge(v("4.22.5"), v("5.0.0"))
	g.AddEdge(v("5.0.0"), v("5.0.3"))

	streams := g.Streams()
	require.Len(t, streams, 2, "4.22 and 5.0")
	assert.Equal(t, "4.22", streams[0].String())
	assert.Equal(t, "5.0", streams[1].String())
}

func TestValidate_ValidChain(t *testing.T) {
	g := New()
	g.AddEdge(v("4.19.0"), v("4.19.5"))
	g.AddEdge(v("4.19.5"), v("4.19.10"))
	g.AddEdge(v("4.19.0"), v("4.19.10"))
	g.AddEdge(v("4.19.10"), v("4.20.0"))
	g.AddEdge(v("4.20.0"), v("4.20.3"))

	result := g.Validate()
	assert.True(t, result.OK(), "expected valid graph, got: %s", result.String())
}

func TestValidate_BrokenWithinMinor(t *testing.T) {
	g := New()
	g.AddVersion(v("4.19.0"))
	g.AddEdge(v("4.19.5"), v("4.19.10"))

	result := g.Validate()
	require.False(t, result.OK(), "expected failures")

	var withinMinor bool
	for _, f := range result.Failures {
		if f.Version.EQ(v("4.19.0")) && f.NextLatestZ == nil {
			withinMinor = true
			assert.Contains(t, f.Reason, "no path to latest-z within minor")
		}
	}
	assert.True(t, withinMinor, "expected within-minor failure for 4.19.0")
}

func TestValidate_BrokenCrossMinor(t *testing.T) {
	g := New()
	g.AddEdge(v("4.19.0"), v("4.19.5"))
	g.AddEdge(v("4.19.5"), v("4.19.10"))
	g.AddEdge(v("4.20.0"), v("4.20.3"))

	result := g.Validate()
	require.False(t, result.OK(), "expected cross-minor failure")

	var crossMinor bool
	for _, f := range result.Failures {
		if f.NextLatestZ != nil && f.Version.EQ(v("4.19.10")) {
			crossMinor = true
			assert.Equal(t, v("4.20.3"), *f.NextLatestZ)
		}
	}
	assert.True(t, crossMinor, "expected cross-minor failure from 4.19.10 to 4.20.3")
}

func TestValidate_CrossMajorChain(t *testing.T) {
	g := New()
	g.AddEdge(v("4.22.0"), v("4.22.5"))
	g.AddEdge(v("4.22.5"), v("5.0.0"))
	g.AddEdge(v("5.0.0"), v("5.0.3"))

	result := g.Validate()
	assert.True(t, result.OK(), "4.22 to 5.0 chain should be valid: %s", result.String())
}

func TestValidate_SingleStream(t *testing.T) {
	g := New()
	g.AddEdge(v("4.19.0"), v("4.19.5"))
	g.AddEdge(v("4.19.5"), v("4.19.10"))

	result := g.Validate()
	assert.True(t, result.OK(), "single stream with complete paths: %s", result.String())
}

func TestFetchAndBuild(t *testing.T) {
	server := testserver.NewServer(t, map[string]*testserver.Graph{
		"stable-4.19": testserver.NewGraph().
			Edges("4.19.0", "4.19.5", "4.19.10").
			Edges("4.19.5", "4.19.10"),
		"stable-4.20": testserver.NewGraph().
			Edges("4.19.10", "4.20.0", "4.20.3").
			Edges("4.20.0", "4.20.3"),
	})

	ctx := context.Background()
	g, err := FetchAndBuild(ctx, server.URI().String(), "stable", v("4.19.0"))
	require.NoError(t, err)

	assert.Equal(t, 5, g.NodeCount(), "5 unique versions across both channels")
	assert.True(t, g.Reachable(v("4.19.0"), v("4.20.3")), "full chain reachable")
	assert.True(t, g.Reachable(v("4.19.5"), v("4.20.3")), "mid-chain reachable")

	result := g.Validate()
	assert.True(t, result.OK(), "fetched graph should validate: %s", result.String())
}

func TestFetchAndBuild_StopsOnEmptyChannel(t *testing.T) {
	server := testserver.NewServer(t, map[string]*testserver.Graph{
		"stable-4.19": testserver.NewGraph().
			Edges("4.19.0", "4.19.5"),
	})

	ctx := context.Background()
	g, err := FetchAndBuild(ctx, server.URI().String(), "stable", v("4.19.0"))
	require.NoError(t, err)

	assert.Equal(t, 2, g.NodeCount(), "only 4.19 versions")
	streams := g.Streams()
	require.Len(t, streams, 1, "single stream")
	assert.Equal(t, "4.19", streams[0].String())
}

func TestFetchAndBuild_MergesNodeAcrossChannels(t *testing.T) {
	server := testserver.NewServer(t, map[string]*testserver.Graph{
		"stable-4.19": testserver.NewGraph().
			Edges("4.19.0", "4.19.10"),
		"stable-4.20": testserver.NewGraph().
			Edges("4.19.10", "4.20.5").
			Edges("4.20.0", "4.20.5"),
	})

	ctx := context.Background()
	g, err := FetchAndBuild(ctx, server.URI().String(), "stable", v("4.19.0"))
	require.NoError(t, err)

	assert.Equal(t, 4, g.NodeCount(), "4.19.10 shared across channels")
	assert.True(t, g.Reachable(v("4.19.0"), v("4.20.5")), "chain through shared node")
}
