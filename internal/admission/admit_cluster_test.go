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
	"strings"
	"testing"

	"k8s.io/utils/ptr"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
)

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
		name                             string
		subscription                     *arm.Subscription
		tags                             map[string]string
		expectError                      string
		expectZeroFeatures               bool
		expectedControlPlaneAvailability api.ControlPlaneAvailability
		expectedControlPlanePodSizing    api.ControlPlanePodSizing
	}{
		{
			name:               "nil subscription ignores all tags",
			subscription:       nil,
			tags:               map[string]string{api.TagClusterSingleReplica: string(api.SingleReplicaControlPlane), api.TagClusterSizeOverride: string(api.MinimalControlPlanePodSizing)},
			expectZeroFeatures: true,
		},
		{
			name:               "no AFEC registered ignores all tags",
			subscription:       noAFEC,
			tags:               map[string]string{api.TagClusterSingleReplica: string(api.SingleReplicaControlPlane), api.TagClusterSizeOverride: string(api.MinimalControlPlanePodSizing)},
			expectZeroFeatures: true,
		},
		{
			name:                             "AFEC registered with single-replica tag only",
			subscription:                     afecRegistered,
			tags:                             map[string]string{api.TagClusterSingleReplica: string(api.SingleReplicaControlPlane)},
			expectedControlPlaneAvailability: api.SingleReplicaControlPlane,
		},
		{
			name:                          "AFEC registered with size-override tag only",
			subscription:                  afecRegistered,
			tags:                          map[string]string{api.TagClusterSizeOverride: string(api.MinimalControlPlanePodSizing)},
			expectedControlPlanePodSizing: api.MinimalControlPlanePodSizing,
		},
		{
			name:                             "AFEC registered with both tags",
			subscription:                     afecRegistered,
			tags:                             map[string]string{api.TagClusterSingleReplica: string(api.SingleReplicaControlPlane), api.TagClusterSizeOverride: string(api.MinimalControlPlanePodSizing)},
			expectedControlPlaneAvailability: api.SingleReplicaControlPlane,
			expectedControlPlanePodSizing:    api.MinimalControlPlanePodSizing,
		},
		{
			name:               "AFEC registered but no tags",
			subscription:       afecRegistered,
			tags:               map[string]string{},
			expectZeroFeatures: true,
		},
		{
			name:                          "AFEC registered with case insensitive tag keys - size-override",
			subscription:                  afecRegistered,
			tags:                          map[string]string{"ARO-HCP.Experimental.Cluster.Size-Override": string(api.MinimalControlPlanePodSizing)},
			expectedControlPlanePodSizing: api.MinimalControlPlanePodSizing,
		},
		{
			name:                             "AFEC registered with case insensitive tag keys - single-replica",
			subscription:                     afecRegistered,
			tags:                             map[string]string{"ARO-HCP.Experimental.Cluster.Single-Replica": string(api.SingleReplicaControlPlane)},
			expectedControlPlaneAvailability: api.SingleReplicaControlPlane,
		},
		{
			name:               "AFEC registered but tag values are empty strings",
			subscription:       afecRegistered,
			tags:               map[string]string{api.TagClusterSingleReplica: "", api.TagClusterSizeOverride: ""},
			expectZeroFeatures: true,
		},
		{
			name:         "AFEC registered but single-replica tag has invalid value",
			subscription: afecRegistered,
			tags:         map[string]string{api.TagClusterSingleReplica: "yes"},
			expectError:  "Invalid value",
		},
		{
			name:         "AFEC registered but single-replica tag rejects true",
			subscription: afecRegistered,
			tags:         map[string]string{api.TagClusterSingleReplica: "true"},
			expectError:  "Invalid value",
		},
		{
			name:         "AFEC registered but size-override tag has invalid value",
			subscription: afecRegistered,
			tags:         map[string]string{api.TagClusterSizeOverride: "1"},
			expectError:  "Invalid value",
		},
		{
			name:         "AFEC registered with unrecognized experimental tag",
			subscription: afecRegistered,
			tags:         map[string]string{"aro-hcp.experimental.cluster.unknown-feature": string(api.SingleReplicaControlPlane)},
			expectError:  "unrecognized experimental tag",
		},
		{
			name:                          "AFEC registered with only size-override after removing single-replica",
			subscription:                  afecRegistered,
			tags:                          map[string]string{api.TagClusterSizeOverride: string(api.MinimalControlPlanePodSizing)},
			expectedControlPlanePodSizing: api.MinimalControlPlanePodSizing,
		},
		{
			name:         "AFEC registered with unrecognized experimental tag in mixed case",
			subscription: afecRegistered,
			tags:         map[string]string{"ARO-HCP.Experimental.Cluster.Unknown-Feature": string(api.SingleReplicaControlPlane)},
			expectError:  "unrecognized experimental tag",
		},
		{
			name:               "non-experimental tags are ignored",
			subscription:       afecRegistered,
			tags:               map[string]string{"environment": "dev", "team": "platform"},
			expectZeroFeatures: true,
		},
		{
			name:         "valid tag alongside unrecognized experimental tag fails",
			subscription: afecRegistered,
			tags:         map[string]string{api.TagClusterSingleReplica: string(api.SingleReplicaControlPlane), "aro-hcp.experimental.cluster.unknown": "value"},
			expectError:  "unrecognized experimental tag",
		},
		{
			name:               "nil tags",
			subscription:       afecRegistered,
			tags:               nil,
			expectZeroFeatures: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cluster := &api.HCPOpenShiftCluster{
				TrackedResource: arm.TrackedResource{
					Tags: tt.tags,
				},
			}
			errs := MutateCluster(cluster, tt.subscription)

			if len(tt.expectError) != 0 {
				if len(errs) == 0 {
					t.Fatalf("expected error containing %q, got none", tt.expectError)
				}
				found := false
				for _, e := range errs {
					if strings.Contains(e.Error(), tt.expectError) {
						found = true
						break
					}
				}
				if !found {
					t.Fatalf("expected error containing %q, got %v", tt.expectError, errs)
				}
				return
			}
			if len(errs) != 0 {
				t.Fatalf("unexpected errors: %v", errs)
			}

			if tt.expectZeroFeatures {
				if cluster.ServiceProviderProperties.ExperimentalFeatures != (api.ExperimentalFeatures{}) {
					t.Errorf("expected zero ExperimentalFeatures, got %+v", cluster.ServiceProviderProperties.ExperimentalFeatures)
				}
				return
			}
			if cluster.ServiceProviderProperties.ExperimentalFeatures.ControlPlaneAvailability != tt.expectedControlPlaneAvailability {
				t.Errorf("expected ControlPlaneAvailability %q, got %q",
					tt.expectedControlPlaneAvailability, cluster.ServiceProviderProperties.ExperimentalFeatures.ControlPlaneAvailability)
			}
			if cluster.ServiceProviderProperties.ExperimentalFeatures.ControlPlanePodSizing != tt.expectedControlPlanePodSizing {
				t.Errorf("expected ControlPlanePodSizing %q, got %q",
					tt.expectedControlPlanePodSizing, cluster.ServiceProviderProperties.ExperimentalFeatures.ControlPlanePodSizing)
			}
		})
	}
}
