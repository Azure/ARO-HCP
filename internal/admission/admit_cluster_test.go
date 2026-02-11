// Copyright 2025 Microsoft Corporation
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

package admission

import (
	"context"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/utils/ptr"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
)

type expectedError struct {
	message   string // Expected error message (partial match)
	fieldPath string // Expected field path for the error
}

func verifyErrorsMatch(t *testing.T, expectedErrors []expectedError, errs field.ErrorList) {
	if len(expectedErrors) != len(errs) {
		t.Errorf("expected %d errors, got %d: %v", len(expectedErrors), len(errs), errs)
		return
	}

	// Check that each expected error message and field path is found
	for _, expectedErr := range expectedErrors {
		if len(strings.TrimSpace(expectedErr.fieldPath)) == 0 {
			t.Errorf("expected error with path %s to be non-empty", expectedErr.fieldPath)
		}
		if len(strings.TrimSpace(expectedErr.message)) == 0 {
			t.Errorf("expected error with msg %s to be non-empty", expectedErr.message)
		}

		found := false
		for _, err := range errs {
			messageMatch := strings.Contains(err.Detail, expectedErr.message) || strings.Contains(err.Error(), expectedErr.message)
			fieldMatch := strings.Contains(err.Field, expectedErr.fieldPath)
			if messageMatch && fieldMatch {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected error containing message '%s' at field '%s' but not found in: %v", expectedErr.message, expectedErr.fieldPath, errs)
		}
	}

	for _, err := range errs {
		found := false
		for _, expectedErr := range expectedErrors {
			messageMatch := strings.Contains(err.Detail, expectedErr.message) || strings.Contains(err.Error(), expectedErr.message)
			fieldMatch := strings.Contains(err.Field, expectedErr.fieldPath)
			if messageMatch && fieldMatch {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("actual error '%v' but not found in expected", err)
		}
	}
}

// Tests for AdmitClusterOnCreate without AllowDevNonStableChannels feature flag
func TestAdmitClusterOnCreate(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name         string
		cluster      *api.HCPOpenShiftCluster
		subscription *arm.Subscription
		expectErrors []expectedError
	}{
		{
			name:         "valid cluster with stable channel group",
			cluster:      api.MinimumValidClusterTestCase(),
			subscription: api.CreateTestSubscription(),
			expectErrors: []expectedError{},
		},
		{
			name:         "valid cluster with nil subscription",
			cluster:      api.MinimumValidClusterTestCase(),
			subscription: nil,
			expectErrors: []expectedError{},
		},
		{
			name: "invalid channel group without feature flag - candidate",
			cluster: func() *api.HCPOpenShiftCluster {
				c := api.MinimumValidClusterTestCase()
				c.CustomerProperties.Version.ChannelGroup = "candidate"
				c.CustomerProperties.Version.ID = "4.15"
				return c
			}(),
			subscription: api.CreateTestSubscription(),
			expectErrors: []expectedError{
				{message: "supported values: \"stable\"",
					fieldPath: "properties.version.channelGroup",
				},
			},
		},
		{
			name: "invalid channel group without feature flag - fast",
			cluster: func() *api.HCPOpenShiftCluster {
				c := api.MinimumValidClusterTestCase()
				c.CustomerProperties.Version.ChannelGroup = "fast"
				c.CustomerProperties.Version.ID = "4.15"
				return c
			}(),
			subscription: api.CreateTestSubscription(),
			expectErrors: []expectedError{
				{message: "supported values: \"stable\"",
					fieldPath: "properties.version.channelGroup"},
			},
		},
		{
			name: "invalid channel group without feature flag - nightly",
			cluster: func() *api.HCPOpenShiftCluster {
				c := api.MinimumValidClusterTestCase()
				c.CustomerProperties.Version.ChannelGroup = "nightly"
				c.CustomerProperties.Version.ID = "4.15"
				return c
			}(),
			subscription: api.CreateTestSubscription(),
			expectErrors: []expectedError{
				{message: "supported values: \"stable\"",
					fieldPath: "properties.version.channelGroup"},
			},
		},
		{
			name: "invalid version with malformed version",
			cluster: func() *api.HCPOpenShiftCluster {
				c := api.MinimumValidClusterTestCase()
				c.CustomerProperties.Version.ID = "invalid-version"
				return c
			}(),
			expectErrors: []expectedError{
				{message: "Malformed version", fieldPath: "properties.version.id"},
			},
		},
		{
			name: "invalid version format with patch version",
			cluster: func() *api.HCPOpenShiftCluster {
				c := api.MinimumValidClusterTestCase()
				c.CustomerProperties.Version.ID = "4.15.1"
				return c
			}(),
			subscription: api.CreateTestSubscription(),
			expectErrors: []expectedError{
				{message: "must be specified as MAJOR.MINOR", fieldPath: "properties.version.id"},
			},
		},
		{
			name: "invalid version format with prerelease",
			cluster: func() *api.HCPOpenShiftCluster {
				c := api.MinimumValidClusterTestCase()
				c.CustomerProperties.Version.ID = "4.15.0-rc.1"
				return c
			}(),
			subscription: api.CreateTestSubscription(),
			expectErrors: []expectedError{
				{message: "must be specified as MAJOR.MINOR", fieldPath: "properties.version.id"},
			},
		},

		{
			name: "valid cluster with fast channel group",
			cluster: func() *api.HCPOpenShiftCluster {
				c := api.MinimumValidClusterTestCase()
				c.CustomerProperties.Version.ChannelGroup = "fast"
				c.CustomerProperties.Version.ID = "4.17"
				return c
			}(),
			expectErrors: []expectedError{{message: "Unsupported value", fieldPath: "properties.version.channelGroup"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := AdmitClusterOnCreate(ctx, tt.cluster, tt.subscription)
			verifyErrorsMatch(t, tt.expectErrors, errs)
		})
	}
}

// Tests for AdmitClusterOnCreate with AllowDevNonStableChannels feature flag enabled
func TestAdmitClusterOnCreateWithNonStableChannels(t *testing.T) {
	ctx := context.Background()
	subscription := api.CreateTestSubscription(api.FeatureAllowDevNonStableChannels)

	tests := []struct {
		name         string
		cluster      *api.HCPOpenShiftCluster
		expectErrors []expectedError
	}{
		{
			name: "valid cluster with candidate channel group and MAJOR.MINOR version",
			cluster: func() *api.HCPOpenShiftCluster {
				c := api.MinimumValidClusterTestCase()
				c.CustomerProperties.Version.ChannelGroup = "candidate"
				c.CustomerProperties.Version.ID = "4.15"
				return c
			}(),
			expectErrors: []expectedError{},
		},
		{
			name: "valid cluster with fast channel group and MAJOR.MINOR version",
			cluster: func() *api.HCPOpenShiftCluster {
				c := api.MinimumValidClusterTestCase()
				c.CustomerProperties.Version.ChannelGroup = "fast"
				c.CustomerProperties.Version.ID = "4.16"
				return c
			}(),
			expectErrors: []expectedError{},
		},
		{
			name: "valid cluster with nightly channel group and full semver",
			cluster: func() *api.HCPOpenShiftCluster {
				c := api.MinimumValidClusterTestCase()
				c.CustomerProperties.Version.ChannelGroup = "nightly"
				c.CustomerProperties.Version.ID = "4.17.0-0.nightly-2024-01-15-123456"
				return c
			}(),
			expectErrors: []expectedError{},
		},
		{
			name: "valid cluster with candidate channel group and prerelease version",
			cluster: func() *api.HCPOpenShiftCluster {
				c := api.MinimumValidClusterTestCase()
				c.CustomerProperties.Version.ChannelGroup = "candidate"
				c.CustomerProperties.Version.ID = "4.15.0-rc.1"
				return c
			}(),
			expectErrors: []expectedError{},
		},
		{
			name: "valid cluster with custom channel group",
			cluster: func() *api.HCPOpenShiftCluster {
				c := api.MinimumValidClusterTestCase()
				c.CustomerProperties.Version.ChannelGroup = "custom-channel"
				c.CustomerProperties.Version.ID = "4.15.0-custom.1"
				return c
			}(),
			expectErrors: []expectedError{},
		},
		{
			name: "stable channel group still requires MAJOR.MINOR version",
			cluster: func() *api.HCPOpenShiftCluster {
				c := api.MinimumValidClusterTestCase()
				c.CustomerProperties.Version.ChannelGroup = "stable"
				c.CustomerProperties.Version.ID = "4.15"
				return c
			}(),
			expectErrors: []expectedError{},
		},
		{
			name: "stable channel group rejects full semver",
			cluster: func() *api.HCPOpenShiftCluster {
				c := api.MinimumValidClusterTestCase()
				c.CustomerProperties.Version.ChannelGroup = "stable"
				c.CustomerProperties.Version.ID = "4.15.1"
				return c
			}(),
			expectErrors: []expectedError{
				{message: "must be specified as MAJOR.MINOR", fieldPath: "properties.version.id"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := AdmitClusterOnCreate(ctx, tt.cluster, subscription)
			verifyErrorsMatch(t, tt.expectErrors, errs)
		})
	}
}

func TestMutateCluster(t *testing.T) {
	afecRegistered := &arm.Subscription{
		Properties: &arm.SubscriptionProperties{
			RegisteredFeatures: &[]arm.Feature{
				{
					Name:  ptr.To(api.FeatureExperimentalReleaseFeatures),
					State: ptr.To("Registered"),
				},
			},
		},
	}
	noAFEC := &arm.Subscription{
		Properties: &arm.SubscriptionProperties{},
	}

	tests := []struct {
		name                  string
		subscription          *arm.Subscription
		tags                  map[string]string
		expectNilFeatures     bool
		expectedSingleReplica bool
		expectedSizeOverride  bool
	}{
		{
			name:              "nil subscription ignores all tags",
			subscription:      nil,
			tags:              map[string]string{api.TagClusterSingleReplica: "true", api.TagClusterSizeOverride: "true"},
			expectNilFeatures: true,
		},
		{
			name:              "no AFEC registered ignores all tags",
			subscription:      noAFEC,
			tags:              map[string]string{api.TagClusterSingleReplica: "true", api.TagClusterSizeOverride: "true"},
			expectNilFeatures: true,
		},
		{
			name:                  "AFEC registered with single-replica tag only",
			subscription:          afecRegistered,
			tags:                  map[string]string{api.TagClusterSingleReplica: "true"},
			expectedSingleReplica: true,
			expectedSizeOverride:  false,
		},
		{
			name:                  "AFEC registered with size-override tag only",
			subscription:          afecRegistered,
			tags:                  map[string]string{api.TagClusterSizeOverride: "true"},
			expectedSingleReplica: false,
			expectedSizeOverride:  true,
		},
		{
			name:                  "AFEC registered with both tags",
			subscription:          afecRegistered,
			tags:                  map[string]string{api.TagClusterSingleReplica: "true", api.TagClusterSizeOverride: "true"},
			expectedSingleReplica: true,
			expectedSizeOverride:  true,
		},
		{
			name:              "AFEC registered but no tags",
			subscription:      afecRegistered,
			tags:              map[string]string{},
			expectNilFeatures: true,
		},
		{
			name:                  "AFEC registered with case insensitive tag values",
			subscription:          afecRegistered,
			tags:                  map[string]string{api.TagClusterSingleReplica: "True", api.TagClusterSizeOverride: "TRUE"},
			expectedSingleReplica: true,
			expectedSizeOverride:  true,
		},
		{
			name:                 "AFEC registered with case insensitive tag keys",
			subscription:         afecRegistered,
			tags:                 map[string]string{"ARO-HCP.Experimental.Cluster/Size-Override": "true"},
			expectedSizeOverride: true,
		},
		{
			name:              "AFEC registered but tag values are not true",
			subscription:      afecRegistered,
			tags:              map[string]string{api.TagClusterSingleReplica: "false", api.TagClusterSizeOverride: "false"},
			expectNilFeatures: true,
		},
		{
			name:              "nil tags",
			subscription:      afecRegistered,
			tags:              nil,
			expectNilFeatures: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cluster := &api.HCPOpenShiftCluster{
				TrackedResource: arm.TrackedResource{
					Tags: tt.tags,
				},
			}
			MutateCluster(cluster, tt.subscription)

			if tt.expectNilFeatures {
				if cluster.ServiceProviderProperties.ExperimentalFeatures != nil {
					t.Errorf("expected nil ExperimentalFeatures, got %+v", cluster.ServiceProviderProperties.ExperimentalFeatures)
				}
				return
			}
			if cluster.ServiceProviderProperties.ExperimentalFeatures == nil {
				t.Fatalf("expected non-nil ExperimentalFeatures")
			}
			if cluster.ServiceProviderProperties.ExperimentalFeatures.SingleReplica != tt.expectedSingleReplica {
				t.Errorf("expected SingleReplica %v, got %v",
					tt.expectedSingleReplica, cluster.ServiceProviderProperties.ExperimentalFeatures.SingleReplica)
			}
			if cluster.ServiceProviderProperties.ExperimentalFeatures.SizeOverride != tt.expectedSizeOverride {
				t.Errorf("expected SizeOverride %v, got %v",
					tt.expectedSizeOverride, cluster.ServiceProviderProperties.ExperimentalFeatures.SizeOverride)
			}
		})
	}
}
