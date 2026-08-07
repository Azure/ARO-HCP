package controlplaneversion

import (
	"context"
	"testing"

	"github.com/blang/semver/v4"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	configv1 "github.com/openshift/api/config/v1"
	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
)

func TestSelectControlPlaneVersionUpdate(t *testing.T) {
	tests := []struct {
		name            string
		cluster         *hypershiftv1beta1.HostedCluster
		expectedVersion string
		expectedError   string
	}{
		{
			name: "nil status.version returns error",
			cluster: &hypershiftv1beta1.HostedCluster{
				Spec: hypershiftv1beta1.HostedClusterSpec{
					Channel: "stable-0.0",
				},
				Status: hypershiftv1beta1.HostedClusterStatus{
					Version: nil,
				},
			},
			expectedError: "HostedCluster status.version is not set, so neither the current version nor available update advice are available.",
		},
		{
			name: "empty available updates returns error",
			cluster: &hypershiftv1beta1.HostedCluster{
				Spec: hypershiftv1beta1.HostedClusterSpec{
					Channel: "stable-0.0",
				},
				Status: hypershiftv1beta1.HostedClusterStatus{
					Version: &hypershiftv1beta1.ClusterVersionStatus{
						Desired: configv1.Release{Version: "4.20.3"},
					},
				},
			},
			expectedError: "HostedCluster status.version.availableUpdates and conditionalUpdates are both empty, so no updates are currently recommended for this cluster.",
		},
		{
			name: "empty available updates with conditional updates counts the conditional entries",
			cluster: &hypershiftv1beta1.HostedCluster{
				Spec: hypershiftv1beta1.HostedClusterSpec{
					Channel: "stable-0.0",
				},
				Status: hypershiftv1beta1.HostedClusterStatus{
					Version: &hypershiftv1beta1.ClusterVersionStatus{
						Desired:            configv1.Release{Version: "4.20.3"},
						ConditionalUpdates: []configv1.ConditionalUpdate{{Release: configv1.Release{}}},
					},
				},
			},
			expectedError: "HostedCluster status.version.availableUpdates is empty, so no updates are currently recommended for this cluster.  There are 1 conditional updates, which are supported, but not recommended for this cluster without administrator approval.",
		},
		{
			name: "available updates with Upgradeable=False processing",
			cluster: &hypershiftv1beta1.HostedCluster{
				Spec: hypershiftv1beta1.HostedClusterSpec{
					Channel: "stable-0.0",
				},
				Status: hypershiftv1beta1.HostedClusterStatus{
					Conditions: []metav1.Condition{
						{
							Type:   string(hypershiftv1beta1.ClusterVersionUpgradeable),
							Status: metav1.ConditionFalse,
						},
					},
					Version: &hypershiftv1beta1.ClusterVersionStatus{
						Desired:          configv1.Release{Version: "4.20.3"},
						AvailableUpdates: []configv1.Release{{Version: "4.21.0"}},
					},
				},
			},
			expectedError: "HostedCluster status.version.availableUpdates is empty, so no updates are currently recommended for this cluster.  There are 1 conditional updates, which are supported, but not recommended for this cluster without administrator approval.",
		},
		{
			name: "returns highest version when channel membership ties",
			cluster: &hypershiftv1beta1.HostedCluster{
				Spec: hypershiftv1beta1.HostedClusterSpec{
					Channel: "stable-0.0",
				},
				Status: hypershiftv1beta1.HostedClusterStatus{
					Version: &hypershiftv1beta1.ClusterVersionStatus{
						AvailableUpdates: []configv1.Release{
							{Version: "4.20.1", Channels: []string{"stable-4.20", "stable-4.21"}},
							{Version: "4.20.2", Channels: []string{"stable-4.20", "stable-4.21"}},
							{Version: "4.20.3", Channels: []string{"stable-4.20", "stable-4.21"}},
							{Version: "4.21.1", Channels: []string{"stable-4.20", "stable-4.21"}},
							{Version: "4.21.2", Channels: []string{"stable-4.20", "stable-4.21"}},
						},
					},
				},
			},
			expectedVersion: "4.21.2",
		},
		{
			name: "filter rejects highest, returns an older release that improves channel connectivity",
			cluster: &hypershiftv1beta1.HostedCluster{
				Spec: hypershiftv1beta1.HostedClusterSpec{
					Channel: "stable-0.0",
				},
				Status: hypershiftv1beta1.HostedClusterStatus{
					Version: &hypershiftv1beta1.ClusterVersionStatus{
						AvailableUpdates: []configv1.Release{
							{Version: "4.20.1", Channels: []string{"stable-4.20", "stable-4.22"}},
							{Version: "4.20.2", Channels: []string{"stable-4.20", "stable-4.22"}},
							{Version: "4.20.3", Channels: []string{"stable-4.20", "stable-4.22"}},
							{Version: "4.21.1", Channels: []string{"stable-4.21", "stable-4.22", "stable-4.23"}},
							{Version: "4.21.2", Channels: []string{"stable-4.21"}},
						},
					},
				},
			},
			expectedVersion: "4.21.1", // leaves 4.20.3 and 4.21.2 bug fixes on the table
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			version, err := SelectControlPlaneVersion(context.Background(), "stable", semver.Version{}, nil, nil, tt.cluster)
			if len(tt.expectedError) == 0 && err != nil {
				t.Error(err)
			} else if len(tt.expectedError) > 0 && err == nil {
				t.Errorf("expected error %q, but got %v", tt.expectedError, err)
			} else if err != nil && err.Error() != tt.expectedError {
				t.Errorf("expected error %q, but got %q", tt.expectedError, err)
			}
			if len(tt.expectedVersion) == 0 && version != nil {
				t.Errorf("expected nil version, got %q", version)
			} else if len(tt.expectedVersion) > 0 && version == nil {
				t.Errorf("expected version %q, but got %v", tt.expectedVersion, version)
			} else if version != nil && version.String() != tt.expectedVersion {
				t.Errorf("expected version %q, but got %q", tt.expectedVersion, version)
			}
		})
	}
}
