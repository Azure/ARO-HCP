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

package controlplaneversion

import (
	"context"
	"testing"

	"github.com/blang/semver/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controlplaneversion/testserver"
)

func v(s string) semver.Version { return semver.MustParse(s) }

func hostedClusterWithHistory(versions ...string) *v1beta1.HostedCluster {
	hc := &v1beta1.HostedCluster{}
	for _, ver := range versions {
		hc.Status.ControlPlaneVersion.History = append(
			hc.Status.ControlPlaneVersion.History,
			v1beta1.ControlPlaneUpdateHistory{
				Version: ver,
				State:   configv1.CompletedUpdate,
			},
		)
	}
	return hc
}

func TestSelectControlPlaneVersion_Install(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		channelStability string
		desiredYVersion  string
		channels         map[string]*testserver.Graph
		wantVersion      string
		wantNil          bool
		wantErr          bool
	}{
		{
			name:             "selects latest gateway when next minor exists",
			channelStability: "stable",
			desiredYVersion:  "4.19.0",
			channels: map[string]*testserver.Graph{
				"stable-4.19": testserver.NewGraph().
					Edges("4.19.0", "4.19.10", "4.19.15", "4.19.22").
					Edges("4.19.10", "4.19.15", "4.19.22").
					Edges("4.19.15", "4.19.22"),
				"stable-4.20": testserver.NewGraph().
					Versions("4.19.22", "4.20.0", "4.20.5").
					Edges("4.19.22", "4.20.5").
					Edges("4.20.0", "4.20.5"),
			},
			wantVersion: "4.19.22",
		},
		{
			name:             "selects latest when next minor does not exist",
			channelStability: "stable",
			desiredYVersion:  "4.19.0",
			channels: map[string]*testserver.Graph{
				"stable-4.19": testserver.NewGraph().
					Edges("4.19.0", "4.19.10", "4.19.15", "4.19.22").
					Edges("4.19.10", "4.19.15", "4.19.22").
					Edges("4.19.15", "4.19.22"),
			},
			wantVersion: "4.19.22",
		},
		{
			name:             "selects older gateway over newer non-gateway",
			channelStability: "stable",
			desiredYVersion:  "4.19.0",
			channels: map[string]*testserver.Graph{
				"stable-4.19": testserver.NewGraph().
					Edges("4.19.0", "4.19.10", "4.19.15", "4.19.22").
					Edges("4.19.10", "4.19.15", "4.19.22").
					Edges("4.19.15", "4.19.22"),
				"stable-4.20": testserver.NewGraph().
					Versions("4.19.15", "4.20.0", "4.20.5").
					Edges("4.19.15", "4.20.5").
					Edges("4.20.0", "4.20.5"),
			},
			wantVersion: "4.19.15",
		},
		{
			name:             "returns .0 when it is the only version",
			channelStability: "stable",
			desiredYVersion:  "4.19.0",
			channels: map[string]*testserver.Graph{
				"stable-4.19": testserver.NewGraph().
					Versions("4.19.0"),
			},
			wantVersion: "4.19.0",
		},
		{
			name:             "candidate channel works the same as stable",
			channelStability: "candidate",
			desiredYVersion:  "4.20.0",
			channels: map[string]*testserver.Graph{
				"candidate-4.20": testserver.NewGraph().
					Edges("4.20.0", "4.20.5", "4.20.10"),
			},
			wantVersion: "4.20.10",
		},
		{
			name:             "4.22 to 5.0 transition",
			channelStability: "stable",
			desiredYVersion:  "4.22.0",
			channels: map[string]*testserver.Graph{
				"stable-4.22": testserver.NewGraph().
					Edges("4.22.0", "4.22.5", "4.22.10"),
				"stable-5.0": testserver.NewGraph().
					Versions("4.22.10", "5.0.0", "5.0.3").
					Edges("4.22.10", "5.0.3").
					Edges("5.0.0", "5.0.3"),
			},
			wantVersion: "4.22.10",
		},
		{
			name:             "transitive — skips latest when its y+1 target cannot reach y+2",
			channelStability: "stable",
			desiredYVersion:  "4.19.0",
			channels: map[string]*testserver.Graph{
				"stable-4.19": testserver.NewGraph().
					Edges("4.19.0", "4.19.10", "4.19.15", "4.19.22").
					Edges("4.19.10", "4.19.15", "4.19.22").
					Edges("4.19.15", "4.19.22"),
				"stable-4.20": testserver.NewGraph().
					Versions("4.19.15", "4.19.22", "4.20.0", "4.20.3", "4.20.8").
					Edges("4.19.15", "4.20.3").
					Edges("4.19.22", "4.20.8").
					Edges("4.20.0", "4.20.3", "4.20.8").
					Edges("4.20.3", "4.20.8"),
				"stable-4.21": testserver.NewGraph().
					Versions("4.20.3", "4.21.0", "4.21.2").
					Edges("4.20.3", "4.21.2").
					Edges("4.21.0", "4.21.2"),
			},
			wantVersion: "4.19.15",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()

			server := testserver.NewServer(t, tt.channels)

			result, err := SelectControlPlaneVersion(ctx, tt.channelStability, v(tt.desiredYVersion), nil, server.URI(), nil)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			if tt.wantNil {
				assert.Nil(t, result)
				return
			}
			require.NotNil(t, result, "expected version %s but got nil", tt.wantVersion)
			assert.Equal(t, tt.wantVersion, result.String())
		})
	}
}

func TestSelectControlPlaneVersion_Upgrade(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		channelStability string
		desiredYVersion  string
		channels         map[string]*testserver.Graph
		hostedCluster    *v1beta1.HostedCluster
		wantVersion      string
		wantNil          bool
		wantErr          bool
	}{
		{
			name:             "z-stream upgrade to latest gateway",
			channelStability: "stable",
			desiredYVersion:  "4.19.0",
			channels: map[string]*testserver.Graph{
				"stable-4.19": testserver.NewGraph().
					Edges("4.19.10", "4.19.15", "4.19.18", "4.19.22").
					Edges("4.19.15", "4.19.18", "4.19.22").
					Edges("4.19.18", "4.19.22"),
				"stable-4.20": testserver.NewGraph().
					Versions("4.19.22", "4.20.0", "4.20.5").
					Edges("4.19.22", "4.20.5").
					Edges("4.20.0", "4.20.5"),
			},
			hostedCluster: hostedClusterWithHistory("4.19.10"),
			wantVersion:   "4.19.22",
		},
		{
			name:             "already at latest — no upgrade needed",
			channelStability: "stable",
			desiredYVersion:  "4.19.0",
			channels: map[string]*testserver.Graph{
				"stable-4.19": testserver.NewGraph().
					Versions("4.19.22"),
			},
			hostedCluster: hostedClusterWithHistory("4.19.22"),
			wantNil:       true,
		},
		{
			name:             "multiple active versions — only common candidates considered",
			channelStability: "stable",
			desiredYVersion:  "4.19.0",
			channels: map[string]*testserver.Graph{
				"stable-4.19": testserver.NewGraph().
					Edges("4.19.10", "4.19.15", "4.19.18").
					Edges("4.19.12", "4.19.15"),
			},
			hostedCluster: hostedClusterWithHistory("4.19.12", "4.19.10"),
			wantVersion:   "4.19.15",
		},
		{
			name:             "no next minor — selects latest z-stream",
			channelStability: "stable",
			desiredYVersion:  "4.19.0",
			channels: map[string]*testserver.Graph{
				"stable-4.19": testserver.NewGraph().
					Edges("4.19.10", "4.19.15", "4.19.22"),
			},
			hostedCluster: hostedClusterWithHistory("4.19.10"),
			wantVersion:   "4.19.22",
		},
		{
			name:             "next minor exists with gateway from other version — returns error",
			channelStability: "stable",
			desiredYVersion:  "4.19.0",
			channels: map[string]*testserver.Graph{
				"stable-4.19": testserver.NewGraph().
					Edges("4.19.0", "4.19.10", "4.19.15", "4.19.20").
					Edges("4.19.10", "4.19.15"),
				"stable-4.20": testserver.NewGraph().
					Versions("4.19.20", "4.20.0", "4.20.5").
					Edges("4.19.20", "4.20.5").
					Edges("4.20.0", "4.20.5"),
			},
			hostedCluster: hostedClusterWithHistory("4.19.10"),
			wantErr:       true,
		},
		{
			name:             "skips older gateway — prefers latest gateway",
			channelStability: "stable",
			desiredYVersion:  "4.19.0",
			channels: map[string]*testserver.Graph{
				"stable-4.19": testserver.NewGraph().
					Edges("4.19.5", "4.19.10", "4.19.15", "4.19.22").
					Edges("4.19.10", "4.19.15", "4.19.22").
					Edges("4.19.15", "4.19.22"),
				"stable-4.20": testserver.NewGraph().
					Versions("4.19.15", "4.19.22", "4.20.0", "4.20.5").
					Edges("4.19.15", "4.20.5").
					Edges("4.19.22", "4.20.5").
					Edges("4.20.0", "4.20.5"),
			},
			hostedCluster: hostedClusterWithHistory("4.19.5"),
			wantVersion:   "4.19.22",
		},
		{
			name:             "partial entry older than completed is excluded from active versions",
			channelStability: "stable",
			desiredYVersion:  "4.19.0",
			channels: map[string]*testserver.Graph{
				"stable-4.19": testserver.NewGraph().
					Edges("4.19.10", "4.19.15").
					Edges("4.19.18", "4.19.22"),
			},
			hostedCluster: func() *v1beta1.HostedCluster {
				hc := &v1beta1.HostedCluster{}
				hc.Status.ControlPlaneVersion.History = []v1beta1.ControlPlaneUpdateHistory{
					{Version: "4.19.18", State: configv1.CompletedUpdate},
					{Version: "4.19.10", State: configv1.PartialUpdate},
				}
				return hc
			}(),
			wantVersion: "4.19.22",
		},
		{
			name:             "partial entries more recent than completed are included as active",
			channelStability: "stable",
			desiredYVersion:  "4.19.0",
			channels: map[string]*testserver.Graph{
				"stable-4.19": testserver.NewGraph().
					Edges("4.19.10", "4.19.18", "4.19.20").
					Edges("4.19.15", "4.19.18", "4.19.22"),
			},
			hostedCluster: func() *v1beta1.HostedCluster {
				hc := &v1beta1.HostedCluster{}
				hc.Status.ControlPlaneVersion.History = []v1beta1.ControlPlaneUpdateHistory{
					{Version: "4.19.15", State: configv1.PartialUpdate},
					{Version: "4.19.10", State: configv1.CompletedUpdate},
				}
				return hc
			}(),
			wantVersion: "4.19.18",
		},
		{
			name:             "all-partial history includes every entry as active",
			channelStability: "stable",
			desiredYVersion:  "4.19.0",
			channels: map[string]*testserver.Graph{
				"stable-4.19": testserver.NewGraph().
					Edges("4.19.0", "4.19.10", "4.19.15", "4.19.18", "4.19.22").
					Edges("4.19.10", "4.19.18", "4.19.22").
					Edges("4.19.15", "4.19.18"),
			},
			hostedCluster: func() *v1beta1.HostedCluster {
				hc := &v1beta1.HostedCluster{}
				hc.Status.ControlPlaneVersion.History = []v1beta1.ControlPlaneUpdateHistory{
					{Version: "4.19.15", State: configv1.PartialUpdate},
					{Version: "4.19.10", State: configv1.PartialUpdate},
				}
				return hc
			}(),
			wantVersion: "4.19.18",
		},
		{
			name:             "4.22 to 5.0 transition with upgrade",
			channelStability: "stable",
			desiredYVersion:  "4.22.0",
			channels: map[string]*testserver.Graph{
				"stable-4.22": testserver.NewGraph().
					Edges("4.22.5", "4.22.8", "4.22.10"),
				"stable-5.0": testserver.NewGraph().
					Versions("4.22.10", "5.0.0", "5.0.3").
					Edges("4.22.10", "5.0.3").
					Edges("5.0.0", "5.0.3"),
			},
			hostedCluster: hostedClusterWithHistory("4.22.5"),
			wantVersion:   "4.22.10",
		},
		{
			name:             "edge removed — upgrade to stepping stone that leads to gateway",
			channelStability: "stable",
			desiredYVersion:  "4.20.0",
			channels: map[string]*testserver.Graph{
				"stable-4.20": testserver.NewGraph().
					Edges("4.20.0", "4.20.12", "4.20.15", "4.20.18").
					Edges("4.20.12", "4.20.15").
					Edges("4.20.15", "4.20.18"),
				"stable-4.21": testserver.NewGraph().
					Versions("4.20.18", "4.21.0", "4.21.5").
					Edges("4.20.18", "4.21.5").
					Edges("4.21.0", "4.21.5"),
			},
			hostedCluster: hostedClusterWithHistory("4.20.12"),
			wantVersion:   "4.20.15",
		},
		{
			name:             "at 4.20.22 with only 4.20.23 reachable — older 4.20.19 is the only gateway to 4.21 — must wait",
			channelStability: "stable",
			desiredYVersion:  "4.20.0",
			channels: map[string]*testserver.Graph{
				"stable-4.20": testserver.NewGraph().
					Edges("4.20.0", "4.20.19", "4.20.22", "4.20.23").
					Edges("4.20.22", "4.20.23"),
				"stable-4.21": testserver.NewGraph().
					Versions("4.20.19", "4.21.0", "4.21.8").
					Edges("4.20.19", "4.21.8").
					Edges("4.21.0", "4.21.8"),
			},
			hostedCluster: hostedClusterWithHistory("4.20.22"),
			wantErr:       true,
		},
		{
			name:             "4.21 channel exists with only intra-minor edges — no 4.20 gateway — returns latest",
			channelStability: "stable",
			desiredYVersion:  "4.20.0",
			channels: map[string]*testserver.Graph{
				"stable-4.20": testserver.NewGraph().
					Edges("4.20.0", "4.20.22", "4.20.23").
					Edges("4.20.22", "4.20.23"),
				"stable-4.21": testserver.NewGraph().
					Edges("4.21.0", "4.21.8"),
			},
			hostedCluster: hostedClusterWithHistory("4.20.22"),
			wantVersion:   "4.20.23",
		},
		{
			name:             "edge removed — no version in y is a gateway to y+1 — returns latest",
			channelStability: "stable",
			desiredYVersion:  "4.20.0",
			channels: map[string]*testserver.Graph{
				"stable-4.20": testserver.NewGraph().
					Edges("4.20.10", "4.20.15", "4.20.18"),
			},
			hostedCluster: hostedClusterWithHistory("4.20.10"),
			wantVersion:   "4.20.18",
		},
		{
			name:             "transitive — skips candidate whose y+1 target cannot reach y+2",
			channelStability: "stable",
			desiredYVersion:  "4.19.0",
			channels: map[string]*testserver.Graph{
				"stable-4.19": testserver.NewGraph().
					Edges("4.19.0", "4.19.10", "4.19.15", "4.19.22").
					Edges("4.19.10", "4.19.15", "4.19.22"),
				"stable-4.20": testserver.NewGraph().
					Versions("4.19.15", "4.19.22", "4.20.0", "4.20.3", "4.20.8").
					Edges("4.19.15", "4.20.3").
					Edges("4.19.22", "4.20.8").
					Edges("4.20.0", "4.20.3", "4.20.8").
					Edges("4.20.3", "4.20.8"),
				"stable-4.21": testserver.NewGraph().
					Versions("4.20.3", "4.21.0", "4.21.2").
					Edges("4.20.3", "4.21.2").
					Edges("4.21.0", "4.21.2"),
			},
			hostedCluster: hostedClusterWithHistory("4.19.10"),
			wantVersion:   "4.19.15",
		},
		{
			name:             "transitive — both y+1 targets are gateways but only one has a chain through y+2",
			channelStability: "stable",
			desiredYVersion:  "4.19.0",
			channels: map[string]*testserver.Graph{
				"stable-4.19": testserver.NewGraph().
					Edges("4.19.0", "4.19.10", "4.19.22").
					Edges("4.19.10", "4.19.22"),
				"stable-4.20": testserver.NewGraph().
					Versions("4.19.22", "4.20.0", "4.20.3", "4.20.8").
					Edges("4.19.22", "4.20.3", "4.20.8").
					Edges("4.20.0", "4.20.3", "4.20.8").
					Edges("4.20.3", "4.20.8"),
				"stable-4.21": testserver.NewGraph().
					Versions("4.20.3", "4.21.0", "4.21.2").
					Edges("4.20.3", "4.21.2").
					Edges("4.21.0", "4.21.2"),
			},
			hostedCluster: hostedClusterWithHistory("4.19.10"),
			wantVersion:   "4.19.22",
		},
		{
			name:             "transitive — chain through 4.20 → 4.21 → 4.22 → 5.0",
			channelStability: "stable",
			desiredYVersion:  "4.20.0",
			channels: map[string]*testserver.Graph{
				"stable-4.20": testserver.NewGraph().
					Edges("4.20.0", "4.20.10", "4.20.15", "4.20.20").
					Edges("4.20.10", "4.20.15", "4.20.20"),
				"stable-4.21": testserver.NewGraph().
					Versions("4.20.15", "4.20.20", "4.21.0", "4.21.5", "4.21.12").
					Edges("4.20.15", "4.21.5").
					Edges("4.20.20", "4.21.12").
					Edges("4.21.0", "4.21.5", "4.21.12").
					Edges("4.21.5", "4.21.12"),
				"stable-4.22": testserver.NewGraph().
					Versions("4.21.5", "4.22.0", "4.22.3", "4.22.7").
					Edges("4.21.5", "4.22.3").
					Edges("4.22.0", "4.22.3", "4.22.7").
					Edges("4.22.3", "4.22.7"),
				"stable-5.0": testserver.NewGraph().
					Versions("4.22.3", "5.0.0", "5.0.2").
					Edges("4.22.3", "5.0.2").
					Edges("5.0.0", "5.0.2"),
			},
			hostedCluster: hostedClusterWithHistory("4.20.10"),
			wantVersion:   "4.20.15",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()

			server := testserver.NewServer(t, tt.channels)

			result, err := SelectControlPlaneVersion(ctx, tt.channelStability, v(tt.desiredYVersion), nil, server.URI(), nil)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			if tt.wantNil {
				assert.Nil(t, result)
				return
			}
			require.NotNil(t, result, "expected version %s but got nil", tt.wantVersion)
			assert.Equal(t, tt.wantVersion, result.String())
		})
	}
}
