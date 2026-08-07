package controlplaneversion

import (
	"testing"

	configv1 "github.com/openshift/api/config/v1"
)

func TestPreferFeatureConnectivityOverPatchFixes(t *testing.T) {
	tests := []struct {
		name          string
		release       configv1.Release
		expectedRank  float32
		expectedError string
	}{
		{
			name:         "no channels gets rank 0",
			release:      configv1.Release{},
			expectedRank: 0,
		},
		{
			name:         "multiple channels ranks the highest major.minor in the target stability set",
			release:      configv1.Release{Channels: []string{"stable-4.22", "stable-5.0", "stable-4.23"}},
			expectedRank: 5,
		},
		{
			name:         "alternative stability sets are ignored",
			release:      configv1.Release{Channels: []string{"stable-4.22", "stable-5.0", "stable-4.23", "candidate-5.2"}},
			expectedRank: 5,
		},
		{
			name:         "minor versions up to 999 are ranked appropriately",
			release:      configv1.Release{Channels: []string{"stable-4.22", "stable-4.999"}},
			expectedRank: 4.999,
		},
		{
			name:         "minor versions up to 999 are ranked appropriately vs higher majors",
			release:      configv1.Release{Channels: []string{"stable-4.22", "stable-4.999", "stable-5.0"}},
			expectedRank: 5,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rank, err := preferFeatureConnectivityOverPatchFixes("stable")(tt.release)
			if len(tt.expectedError) == 0 && err != nil {
				t.Error(err)
			} else if len(tt.expectedError) > 0 && err == nil {
				t.Errorf("expected error %q, but got %v", tt.expectedError, err)
			} else if err != nil && err.Error() != tt.expectedError {
				t.Errorf("expected error %q, but got %q", tt.expectedError, err)
			}
			if rank != tt.expectedRank {
				t.Errorf("expected rank %g, but got %g", tt.expectedRank, rank)
			}
		})
	}
}

func TestParseChannel(t *testing.T) {
	tests := []struct {
		channel           string
		expectedStability string
		expectedMajor     uint
		expectedMinor     uint
		expectedError     string
	}{
		{
			channel:       "",
			expectedError: `no - delimiter found in channel ""`,
		},
		{
			channel:       "-",
			expectedError: `no channel stability found before the - delimiter in channel "-"`,
		},
		{
			channel:       "-4.20",
			expectedError: `no channel stability found before the - delimiter in channel "-4.20"`,
		},
		{
			channel:       "stable-",
			expectedError: `no target version found after the final - delimiter in channel "stable-"`,
		},
		{
			channel:           "stable-a",
			expectedStability: "stable",
			expectedError:     `expected a MAJOR.MINOR version in the "a" portion of channel "stable-a", but did not get two segments in [a]`,
		},
		{
			channel:           "stable-a.b.c",
			expectedStability: "stable",
			expectedError:     `expected a MAJOR.MINOR version in the "a.b.c" portion of channel "stable-a.b.c", but did not get two segments in [a b c]`,
		},
		{
			channel:           "stable-a.b",
			expectedStability: "stable",
			expectedError:     `expected a major version in the "a" portion of channel "stable-a.b": strconv.ParseUint: parsing "a": invalid syntax`,
		},
		{
			channel:           "stable-10000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000.a",
			expectedStability: "stable",
			expectedError:     `expected a major version in the "10000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000" portion of channel "stable-10000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000.a": strconv.ParseUint: parsing "10000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000": value out of range`,
		},
		{
			channel:           "stable-4.a",
			expectedStability: "stable",
			expectedMajor:     4,
			expectedError:     `expected a minor version in the "a" portion of channel "stable-4.a": strconv.ParseUint: parsing "a": invalid syntax`,
		},
		{
			channel:           "stable-4.20",
			expectedStability: "stable",
			expectedMajor:     4,
			expectedMinor:     20,
		},
	}
	for _, tt := range tests {
		t.Run(tt.channel, func(t *testing.T) {
			stability, major, minor, err := parseChannel(tt.channel)
			if len(tt.expectedError) == 0 && err != nil {
				t.Error(err)
			} else if len(tt.expectedError) > 0 && err == nil {
				t.Errorf("expected error %q, but got %v", tt.expectedError, err)
			} else if err != nil && err.Error() != tt.expectedError {
				t.Errorf("expected error %q, but got %q", tt.expectedError, err)
			}
			if stability != tt.expectedStability {
				t.Errorf("expected %q stability, got %q", tt.expectedStability, stability)
			}
			if major != tt.expectedMajor {
				t.Errorf("expected %d major, got %d", tt.expectedMajor, major)
			}
			if minor != tt.expectedMinor {
				t.Errorf("expected %d minor, got %d", tt.expectedMinor, minor)
			}
		})
	}
}
