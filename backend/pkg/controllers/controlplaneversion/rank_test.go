package main

import (
	"errors"
	"testing"

	configv1 "github.com/openshift/api/config/v1"
)

func TestRankedSelection(t *testing.T) {
	tests := []struct {
		name            string
		targets         []configv1.Release
		rankRelease     rankRelease
		expectedVersion string
		expectedError   string
	}{
		{
			name: "every target erroring is fatal",
			targets: []configv1.Release{
				{Version: "4.20.1"},
				{Version: "4.20.2"},
				{Version: "4.20.3"},
			},
			rankRelease:   func(_ configv1.Release) (float32, error) { return 0, errors.New("example error") },
			expectedError: "failed to rank all 3 releases, including 4.20.3 (rank 0, error example error)",
		},
		{
			name: "prefers non-error releases, even if they have low rank or lower SemVer",
			targets: []configv1.Release{
				{Version: "4.20.1"},
				{Version: "4.20.2"},
				{Version: "4.20.3"},
			},
			rankRelease: func(release configv1.Release) (float32, error) {
				switch release.Version {
				case "4.20.2":
					return 1, errors.New("example error")
				case "4.20.3":
					return 0, errors.New("example error")
				default:
					return 0, nil
				}
			},
			expectedVersion: "4.20.1",
		},
		{
			name: "prefers high rank among non-error releases",
			targets: []configv1.Release{
				{Version: "4.20.1"},
				{Version: "4.20.2"},
				{Version: "4.20.3"},
			},
			rankRelease: func(release configv1.Release) (float32, error) {
				switch release.Version {
				case "4.20.2":
					return 0, nil
				case "4.20.3":
					return 0, errors.New("example error")
				default:
					return 1, nil
				}
			},
			expectedVersion: "4.20.1",
		},
		{
			name: "returns highest version when rank ties",
			targets: []configv1.Release{
				{Version: "4.20.1"},
				{Version: "4.20.2"},
				{Version: "4.20.3"},
			},
			rankRelease:     func(_ configv1.Release) (float32, error) { return 0, nil },
			expectedVersion: "4.20.3",
		},
		{
			name: "versions strings that are not SemVer are never returned",
			targets: []configv1.Release{
				{Version: "0"},
				{Version: "1"},
			},
			expectedError: `rankedSelection requires a non-empty target set, and while this call had 2 targets, none of them had valid SemVer versions: failed to parse SemVer "0": No Major.Minor.Patch elements found`,
		},
		{
			name: "versions strings that are not SemVer are never prioritized",
			targets: []configv1.Release{
				{Version: "0"},
				{Version: "4.20.1"},
			},
			rankRelease:     func(_ configv1.Release) (float32, error) { return 0, nil },
			expectedVersion: "4.20.1",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			version, err := rankedSelection(tt.targets, tt.rankRelease)
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
