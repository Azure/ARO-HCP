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

package hcpversionselection

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/blang/semver/v4"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	utilsclock "k8s.io/utils/clock"

	configv1 "github.com/openshift/api/config/v1"
	cvocincinnati "github.com/openshift/cluster-version-operator/pkg/cincinnati"
	"github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/Azure/ARO-HCP/internal/cincinnati"
)

func liveHostedCluster(version string) *v1beta1.HostedCluster {
	return &v1beta1.HostedCluster{
		Status: v1beta1.HostedClusterStatus{
			ControlPlaneVersion: v1beta1.ControlPlaneVersionStatus{
				History: []v1beta1.ControlPlaneUpdateHistory{
					{Version: version, State: configv1.CompletedUpdate},
				},
			},
		},
	}
}

func TestLiveSelectControlPlaneVersion(t *testing.T) {
	t.Parallel()

	cvoClient := cincinnati.NewCachingClient(
		cvocincinnati.NewClient(
			uuid.Nil,
			http.DefaultTransport.(*http.Transport).Clone(),
			"ARO-HCP-test",
			cincinnati.NewAcceptAllConditionRegistry(),
		),
		utilsclock.RealClock{},
		1*time.Hour,
	)
	graphClient := cincinnati.NewGraphClient()

	tests := []struct {
		name             string
		channelStability string
		desiredYVersion  semver.Version
		hostedCluster    *v1beta1.HostedCluster
		wantUpgrade      bool
		wantNoGateway    bool
	}{
		{
			name:             "candidate 4.22.2 cluster finds upgrade target",
			channelStability: "candidate",
			desiredYVersion:  semver.MustParse("4.22.0"),
			hostedCluster:    liveHostedCluster("4.22.2"),
			wantUpgrade:      true,
		},
		{
			name:             "candidate 4.22.0 cluster finds upgrade target",
			channelStability: "candidate",
			desiredYVersion:  semver.MustParse("4.22.0"),
			hostedCluster:    liveHostedCluster("4.22.0"),
			wantUpgrade:      true,
		},
		{
			name:             "stable 4.19.0 cluster finds upgrade target",
			channelStability: "stable",
			desiredYVersion:  semver.MustParse("4.19.0"),
			hostedCluster:    liveHostedCluster("4.19.0"),
			wantUpgrade:      true,
		},
		{
			name:             "stable 4.18.1 cluster finds upgrade target",
			channelStability: "stable",
			desiredYVersion:  semver.MustParse("4.18.0"),
			hostedCluster:    liveHostedCluster("4.18.1"),
			wantUpgrade:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()

			cincinnatiURI, err := cincinnati.GetCincinnatiURI(tt.channelStability)
			require.NoError(t, err)

			activeVersion := semver.MustParse(tt.hostedCluster.Status.ControlPlaneVersion.History[0].Version)
			result, err := SelectControlPlaneVersion(ctx, tt.channelStability, tt.desiredYVersion, cincinnatiURI, cvoClient, graphClient, tt.hostedCluster)

			if tt.wantNoGateway {
				require.Error(t, err)
				var noGateway *NoGatewayError
				require.True(t, errors.As(err, &noGateway), "expected *NoGatewayError, got %T: %v", err, err)
				t.Logf("active: %s  error: %s", activeVersion, noGateway.Error())
				t.Logf("  channel check URL:  %s", noGateway.ChannelCheckURL)
				t.Logf("  gateway probe URL:  %s", noGateway.GatewayProbeURL)
				return
			}

			require.NoError(t, err)
			if tt.wantUpgrade {
				require.NotNil(t, result, "expected an upgrade target but got nil")
				assert.True(t, result.GT(activeVersion),
					"selected version %s should be greater than active version %s", result, activeVersion)
				t.Logf("active: %s  selected: %s  channel: %s-%d.%d",
					activeVersion, result, tt.channelStability, tt.desiredYVersion.Major, tt.desiredYVersion.Minor)
			}
		})
	}
}
