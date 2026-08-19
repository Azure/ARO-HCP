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
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/blang/semver/v4"

	"k8s.io/apimachinery/pkg/api/operation"
	"k8s.io/apimachinery/pkg/util/validation/field"
	utilsclock "k8s.io/utils/clock"
	clocktesting "k8s.io/utils/clock/testing"
	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func TestMutateCluster(t *testing.T) {
	afecRegistered := &coreapi.Subscription{
		Properties: &coreapi.SubscriptionProperties{
			RegisteredFeatures: &[]coreapi.Feature{
				{
					Name:  ptr.To(metadataapi.FeatureExperimentalReleaseFeatures),
					State: ptr.To("Registered"),
				},
			},
		},
	}
	noAFEC := &coreapi.Subscription{
		Properties: &coreapi.SubscriptionProperties{},
	}

	tests := []struct {
		name                              string
		subscription                      *coreapi.Subscription
		tags                              map[string]string
		expectErrors                      []utils.ExpectedError
		expectZeroFeatures                bool
		expectedControlPlaneAvailability  coreapi.ControlPlaneAvailability
		expectedControlPlanePodSizing     coreapi.ControlPlanePodSizing
		expectedControlPlaneOperatorImage string
	}{
		{
			name:               "nil subscription ignores all tags",
			subscription:       nil,
			tags:               map[string]string{metadataapi.TagClusterSingleReplica: string(coreapi.SingleReplicaControlPlane), metadataapi.TagClusterSizeOverride: string(coreapi.MinimalControlPlanePodSizing)},
			expectErrors:       []utils.ExpectedError{},
			expectZeroFeatures: true,
		},
		{
			name:               "no AFEC registered ignores all tags",
			subscription:       noAFEC,
			tags:               map[string]string{metadataapi.TagClusterSingleReplica: string(coreapi.SingleReplicaControlPlane), metadataapi.TagClusterSizeOverride: string(coreapi.MinimalControlPlanePodSizing)},
			expectErrors:       []utils.ExpectedError{},
			expectZeroFeatures: true,
		},
		{
			name:                             "AFEC registered with single-replica tag only",
			subscription:                     afecRegistered,
			tags:                             map[string]string{metadataapi.TagClusterSingleReplica: string(coreapi.SingleReplicaControlPlane)},
			expectErrors:                     []utils.ExpectedError{},
			expectedControlPlaneAvailability: coreapi.SingleReplicaControlPlane,
		},
		{
			name:                          "AFEC registered with size-override tag only",
			subscription:                  afecRegistered,
			tags:                          map[string]string{metadataapi.TagClusterSizeOverride: string(coreapi.MinimalControlPlanePodSizing)},
			expectErrors:                  []utils.ExpectedError{},
			expectedControlPlanePodSizing: coreapi.MinimalControlPlanePodSizing,
		},
		{
			name:                             "AFEC registered with both tags",
			subscription:                     afecRegistered,
			tags:                             map[string]string{metadataapi.TagClusterSingleReplica: string(coreapi.SingleReplicaControlPlane), metadataapi.TagClusterSizeOverride: string(coreapi.MinimalControlPlanePodSizing)},
			expectErrors:                     []utils.ExpectedError{},
			expectedControlPlaneAvailability: coreapi.SingleReplicaControlPlane,
			expectedControlPlanePodSizing:    coreapi.MinimalControlPlanePodSizing,
		},
		{
			name:               "AFEC registered but no tags",
			subscription:       afecRegistered,
			tags:               map[string]string{},
			expectErrors:       []utils.ExpectedError{},
			expectZeroFeatures: true,
		},
		{
			name:                          "AFEC registered with case insensitive tag keys - size-override",
			subscription:                  afecRegistered,
			tags:                          map[string]string{"ARO-HCP.Experimental.Cluster.Size-Override": string(coreapi.MinimalControlPlanePodSizing)},
			expectErrors:                  []utils.ExpectedError{},
			expectedControlPlanePodSizing: coreapi.MinimalControlPlanePodSizing,
		},
		{
			name:                             "AFEC registered with case insensitive tag keys - single-replica",
			subscription:                     afecRegistered,
			tags:                             map[string]string{"ARO-HCP.Experimental.Cluster.Single-Replica": string(coreapi.SingleReplicaControlPlane)},
			expectErrors:                     []utils.ExpectedError{},
			expectedControlPlaneAvailability: coreapi.SingleReplicaControlPlane,
		},
		{
			name:               "AFEC registered but tag values are empty strings",
			subscription:       afecRegistered,
			tags:               map[string]string{metadataapi.TagClusterSingleReplica: "", metadataapi.TagClusterSizeOverride: ""},
			expectErrors:       []utils.ExpectedError{},
			expectZeroFeatures: true,
		},
		{
			name:         "AFEC registered but single-replica tag has invalid value",
			subscription: afecRegistered,
			tags:         map[string]string{metadataapi.TagClusterSingleReplica: "yes"},
			expectErrors: []utils.ExpectedError{
				{FieldPath: "tags", Message: "Invalid value"},
			},
		},
		{
			name:         "AFEC registered but single-replica tag rejects true",
			subscription: afecRegistered,
			tags:         map[string]string{metadataapi.TagClusterSingleReplica: "true"},
			expectErrors: []utils.ExpectedError{
				{FieldPath: "tags", Message: "Invalid value"},
			},
		},
		{
			name:         "AFEC registered but size-override tag has invalid value",
			subscription: afecRegistered,
			tags:         map[string]string{metadataapi.TagClusterSizeOverride: "1"},
			expectErrors: []utils.ExpectedError{
				{FieldPath: "tags", Message: "Invalid value"},
			},
		},
		{
			name:         "AFEC registered with unrecognized experimental tag",
			subscription: afecRegistered,
			tags:         map[string]string{"aro-hcp.experimental.cluster.unknown-feature": string(coreapi.SingleReplicaControlPlane)},
			expectErrors: []utils.ExpectedError{
				{FieldPath: "tags", Message: "unrecognized experimental tag"},
			},
		},
		{
			name:                          "AFEC registered with only size-override after removing single-replica",
			subscription:                  afecRegistered,
			tags:                          map[string]string{metadataapi.TagClusterSizeOverride: string(coreapi.MinimalControlPlanePodSizing)},
			expectErrors:                  []utils.ExpectedError{},
			expectedControlPlanePodSizing: coreapi.MinimalControlPlanePodSizing,
		},
		{
			name:         "AFEC registered with unrecognized experimental tag in mixed case",
			subscription: afecRegistered,
			tags:         map[string]string{"ARO-HCP.Experimental.Cluster.Unknown-Feature": string(coreapi.SingleReplicaControlPlane)},
			expectErrors: []utils.ExpectedError{
				{FieldPath: "tags", Message: "unrecognized experimental tag"},
			},
		},
		{
			name:               "non-experimental tags are ignored",
			subscription:       afecRegistered,
			tags:               map[string]string{"environment": "dev", "team": "platform"},
			expectErrors:       []utils.ExpectedError{},
			expectZeroFeatures: true,
		},
		{
			name:         "valid tag alongside unrecognized experimental tag fails",
			subscription: afecRegistered,
			tags:         map[string]string{metadataapi.TagClusterSingleReplica: string(coreapi.SingleReplicaControlPlane), "aro-hcp.experimental.cluster.unknown": "value"},
			expectErrors: []utils.ExpectedError{
				{FieldPath: "tags", Message: "unrecognized experimental tag"},
			},
		},
		{
			name:               "nil tags",
			subscription:       afecRegistered,
			tags:               nil,
			expectErrors:       []utils.ExpectedError{},
			expectZeroFeatures: true,
		},
		{
			name:                              "AFEC registered with CPO image override tag",
			subscription:                      afecRegistered,
			tags:                              map[string]string{metadataapi.TagClusterCPOImageOverride: "quay.io/openshift/cpo:latest"},
			expectErrors:                      []utils.ExpectedError{},
			expectedControlPlaneOperatorImage: "quay.io/openshift/cpo:latest",
		},
		{
			name:                              "AFEC registered with CPO image override tag with digest",
			subscription:                      afecRegistered,
			tags:                              map[string]string{metadataapi.TagClusterCPOImageOverride: "quay.io/openshift/cpo@sha256:abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890"},
			expectErrors:                      []utils.ExpectedError{},
			expectedControlPlaneOperatorImage: "quay.io/openshift/cpo@sha256:abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890",
		},
		{
			name:               "AFEC registered with empty CPO image override tag",
			subscription:       afecRegistered,
			tags:               map[string]string{metadataapi.TagClusterCPOImageOverride: ""},
			expectErrors:       []utils.ExpectedError{},
			expectZeroFeatures: true,
		},
		{
			name:         "AFEC registered with whitespace-only CPO image override tag",
			subscription: afecRegistered,
			tags:         map[string]string{metadataapi.TagClusterCPOImageOverride: "  "},
			expectErrors: []utils.ExpectedError{
				{FieldPath: "tags", Message: "Invalid value"},
			},
		},
		{
			name:               "no AFEC registered ignores CPO image override tag",
			subscription:       noAFEC,
			tags:               map[string]string{metadataapi.TagClusterCPOImageOverride: "quay.io/openshift/cpo:latest"},
			expectErrors:       []utils.ExpectedError{},
			expectZeroFeatures: true,
		},
		{
			name:                              "AFEC registered with case insensitive CPO image override tag",
			subscription:                      afecRegistered,
			tags:                              map[string]string{"ARO-HCP.Experimental.Cluster.Control-Plane-Operator-Image-Override": "quay.io/openshift/cpo:v1.0"},
			expectErrors:                      []utils.ExpectedError{},
			expectedControlPlaneOperatorImage: "quay.io/openshift/cpo:v1.0",
		},
		{
			name:               "AFEC registered with max-creation-duration tag is recognized",
			subscription:       afecRegistered,
			tags:               map[string]string{metadataapi.TagClusterMaxCreationDuration: "19m"},
			expectErrors:       []utils.ExpectedError{},
			expectZeroFeatures: true,
		},
		{
			name:               "no AFEC registered ignores max-creation-duration tag",
			subscription:       noAFEC,
			tags:               map[string]string{metadataapi.TagClusterMaxCreationDuration: "19m"},
			expectErrors:       []utils.ExpectedError{},
			expectZeroFeatures: true,
		},
		{
			name:               "AFEC registered with case insensitive max-creation-duration tag key",
			subscription:       afecRegistered,
			tags:               map[string]string{"ARO-HCP.Experimental.Cluster.Max-Creation-Duration": "19m"},
			expectErrors:       []utils.ExpectedError{},
			expectZeroFeatures: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cluster := &coreapi.HCPOpenShiftCluster{
				TrackedResource: coreapi.TrackedResource{
					Tags: tt.tags,
				},
			}
			admissionContext := &ClusterAdmissionContext{
				Clock:           utilsclock.RealClock{},
				Subscription:    tt.subscription,
				OriginalCluster: cluster.DeepCopy(),
			}
			errs := MutateCluster(context.Background(), admissionContext, operation.Operation{Type: operation.Create}, cluster, nil)

			utils.VerifyErrorsMatch(t, tt.expectErrors, errs)

			if tt.expectZeroFeatures {
				if cluster.ServiceProviderProperties.ExperimentalFeatures != (coreapi.ExperimentalFeatures{}) {
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
			if cluster.ServiceProviderProperties.ExperimentalFeatures.ControlPlaneOperatorImage != tt.expectedControlPlaneOperatorImage {
				t.Errorf("expected ControlPlaneOperatorImage %q, got %q",
					tt.expectedControlPlaneOperatorImage, cluster.ServiceProviderProperties.ExperimentalFeatures.ControlPlaneOperatorImage)
			}
		})
	}
}

func TestMutateClusterControlPlaneExactVersion(t *testing.T) {
	afecRegistered := &coreapi.Subscription{
		Properties: &coreapi.SubscriptionProperties{
			RegisteredFeatures: &[]coreapi.Feature{
				{
					Name:  ptr.To(metadataapi.FeatureExperimentalReleaseFeatures),
					State: ptr.To("Registered"),
				},
			},
		},
	}
	noAFEC := &coreapi.Subscription{
		Properties: &coreapi.SubscriptionProperties{},
	}

	const exactTag = metadataapi.TagClusterControlPlaneExactVersion

	tests := []struct {
		name              string
		subscription      *coreapi.Subscription
		tags              map[string]string
		versionID         string
		expectErrors      []utils.ExpectedError
		expectExactNil    bool
		expectExactString string
		expectVersionID   string
	}{
		{
			name:            "no AFEC ignores exact-version tag value",
			subscription:    noAFEC,
			tags:            map[string]string{exactTag: "4.17.3"},
			versionID:       "4.17",
			expectErrors:    []utils.ExpectedError{},
			expectExactNil:  true,
			expectVersionID: "4.17",
		},
		{
			name:            "no AFEC ignores exact-version tag present with patch version.id",
			subscription:    noAFEC,
			tags:            map[string]string{exactTag: ""},
			versionID:       "4.17.3",
			expectErrors:    []utils.ExpectedError{},
			expectExactNil:  true,
			expectVersionID: "4.17.3",
		},
		{
			name:              "AFEC with full-semver tag value pins exact version",
			subscription:      afecRegistered,
			tags:              map[string]string{exactTag: "4.17.3"},
			versionID:         "4.17",
			expectErrors:      []utils.ExpectedError{},
			expectExactString: "4.17.3",
			expectVersionID:   "4.17",
		},
		{
			name:         "AFEC with invalid tag value is rejected",
			subscription: afecRegistered,
			tags:         map[string]string{exactTag: "not-a-version"},
			versionID:    "4.17",
			expectErrors: []utils.ExpectedError{
				{FieldPath: "tags", Message: "Invalid value"},
			},
		},
		{
			name:         "AFEC with minor-only tag value is rejected (must be exact)",
			subscription: afecRegistered,
			tags:         map[string]string{exactTag: "4.17"},
			versionID:    "4.17",
			expectErrors: []utils.ExpectedError{
				{FieldPath: "tags", Message: "Invalid value"},
			},
		},
		{
			name:              "AFEC with empty tag relocates patch version.id",
			subscription:      afecRegistered,
			tags:              map[string]string{exactTag: ""},
			versionID:         "4.17.3",
			expectErrors:      []utils.ExpectedError{},
			expectExactString: "4.17.3",
			expectVersionID:   "4.17",
		},
		{
			name:            "AFEC with empty tag and minor-only version.id does nothing",
			subscription:    afecRegistered,
			tags:            map[string]string{exactTag: ""},
			versionID:       "4.17",
			expectErrors:    []utils.ExpectedError{},
			expectExactNil:  true,
			expectVersionID: "4.17",
		},
		{
			name:            "AFEC without exact-version tag does not relocate patch version.id",
			subscription:    afecRegistered,
			tags:            map[string]string{},
			versionID:       "4.17.3",
			expectErrors:    []utils.ExpectedError{},
			expectExactNil:  true,
			expectVersionID: "4.17.3",
		},
		{
			name:              "patch version.id takes precedence over tag value",
			subscription:      afecRegistered,
			tags:              map[string]string{exactTag: "4.17.3"},
			versionID:         "4.18.9",
			expectErrors:      []utils.ExpectedError{},
			expectExactString: "4.18.9",
			expectVersionID:   "4.18",
		},
		{
			name:              "case-insensitive tag key relocates patch version.id",
			subscription:      afecRegistered,
			tags:              map[string]string{"ARO-HCP.Experimental.Cluster.Control-Plane-Exact-Version": ""},
			versionID:         "4.20.5",
			expectErrors:      []utils.ExpectedError{},
			expectExactString: "4.20.5",
			expectVersionID:   "4.20",
		},
		{
			name:              "pre-release patch version.id is relocated and stripped to major.minor",
			subscription:      afecRegistered,
			tags:              map[string]string{exactTag: ""},
			versionID:         "4.20.0-rc.1",
			expectErrors:      []utils.ExpectedError{},
			expectExactString: "4.20.0-rc.1",
			expectVersionID:   "4.20",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cluster := &coreapi.HCPOpenShiftCluster{
				TrackedResource: coreapi.TrackedResource{
					Tags: tt.tags,
				},
			}
			cluster.CustomerProperties.Version.ID = tt.versionID
			admissionContext := &ClusterAdmissionContext{
				Clock:           utilsclock.RealClock{},
				Subscription:    tt.subscription,
				OriginalCluster: cluster.DeepCopy(),
			}

			errs := MutateCluster(context.Background(), admissionContext, operation.Operation{Type: operation.Create}, cluster, nil)

			utils.VerifyErrorsMatch(t, tt.expectErrors, errs)

			gotExact := cluster.ServiceProviderProperties.ExperimentalFeatures.ControlPlaneExactVersion
			if tt.expectExactNil {
				if gotExact != nil {
					t.Errorf("expected ControlPlaneExactVersion to be nil, got %q", gotExact.String())
				}
			} else if len(tt.expectExactString) > 0 {
				if gotExact == nil {
					t.Errorf("expected ControlPlaneExactVersion %q, got nil", tt.expectExactString)
				} else if gotExact.String() != tt.expectExactString {
					t.Errorf("expected ControlPlaneExactVersion %q, got %q", tt.expectExactString, gotExact.String())
				}
			}

			if len(tt.expectVersionID) > 0 && cluster.CustomerProperties.Version.ID != tt.expectVersionID {
				t.Errorf("expected version.id %q, got %q", tt.expectVersionID, cluster.CustomerProperties.Version.ID)
			}
		})
	}
}

func TestMutateCreateOperationCompletionDeadline(t *testing.T) {
	afecRegistered := &coreapi.Subscription{
		Properties: &coreapi.SubscriptionProperties{
			RegisteredFeatures: &[]coreapi.Feature{
				{
					Name:  ptr.To(metadataapi.FeatureExperimentalReleaseFeatures),
					State: ptr.To("Registered"),
				},
			},
		},
	}
	noAFEC := &coreapi.Subscription{
		Properties: &coreapi.SubscriptionProperties{},
	}

	tests := []struct {
		name             string
		subscription     *coreapi.Subscription
		tags             map[string]string
		op               operation.Operation
		expectErrors     []utils.ExpectedError
		expectDeadline   bool
		expectedDuration time.Duration
	}{
		{
			name:             "CREATE defaults to 60 minutes",
			subscription:     noAFEC,
			tags:             nil,
			op:               operation.Operation{Type: operation.Create},
			expectDeadline:   true,
			expectedDuration: 60 * time.Minute,
		},
		{
			name:         "UPDATE does not set deadline",
			subscription: noAFEC,
			tags:         nil,
			op:           operation.Operation{Type: operation.Update},
		},
		{
			name:             "AFEC registered with max-creation-duration tag overrides default",
			subscription:     afecRegistered,
			tags:             map[string]string{metadataapi.TagClusterMaxCreationDuration: "19m"},
			op:               operation.Operation{Type: operation.Create},
			expectDeadline:   true,
			expectedDuration: 19 * time.Minute,
		},
		{
			name:             "AFEC registered without tag uses default",
			subscription:     afecRegistered,
			tags:             nil,
			op:               operation.Operation{Type: operation.Create},
			expectDeadline:   true,
			expectedDuration: 60 * time.Minute,
		},
		{
			name:             "no AFEC ignores max-creation-duration tag, uses default",
			subscription:     noAFEC,
			tags:             map[string]string{metadataapi.TagClusterMaxCreationDuration: "19m"},
			op:               operation.Operation{Type: operation.Create},
			expectDeadline:   true,
			expectedDuration: 60 * time.Minute,
		},
		{
			name:         "AFEC registered with invalid duration value",
			subscription: afecRegistered,
			tags:         map[string]string{metadataapi.TagClusterMaxCreationDuration: "not-a-duration"},
			op:           operation.Operation{Type: operation.Create},
			expectErrors: []utils.ExpectedError{
				{FieldPath: "tags", Message: "must be a valid Go duration string"},
			},
		},
		{
			name:             "nil subscription still sets default deadline",
			subscription:     nil,
			op:               operation.Operation{Type: operation.Create},
			expectDeadline:   true,
			expectedDuration: 60 * time.Minute,
		},
		{
			name:             "AFEC registered with empty string tag uses default",
			subscription:     afecRegistered,
			tags:             map[string]string{metadataapi.TagClusterMaxCreationDuration: ""},
			op:               operation.Operation{Type: operation.Create},
			expectDeadline:   true,
			expectedDuration: 60 * time.Minute,
		},
		{
			name:             "AFEC registered with case insensitive tag key",
			subscription:     afecRegistered,
			tags:             map[string]string{"ARO-HCP.Experimental.Cluster.Max-Creation-Duration": "25m"},
			op:               operation.Operation{Type: operation.Create},
			expectDeadline:   true,
			expectedDuration: 25 * time.Minute,
		},
		{
			name:             "AFEC registered with compound duration",
			subscription:     afecRegistered,
			tags:             map[string]string{metadataapi.TagClusterMaxCreationDuration: "1h30m"},
			op:               operation.Operation{Type: operation.Create},
			expectDeadline:   true,
			expectedDuration: 90 * time.Minute,
		},
		{
			name:         "AFEC registered with duration less than one minute is rejected",
			subscription: afecRegistered,
			tags:         map[string]string{metadataapi.TagClusterMaxCreationDuration: "30s"},
			op:           operation.Operation{Type: operation.Create},
			expectErrors: []utils.ExpectedError{
				{FieldPath: "tags", Message: "must be at least 1m0s"},
			},
		},
		{
			name:         "AFEC registered with zero duration is rejected",
			subscription: afecRegistered,
			tags:         map[string]string{metadataapi.TagClusterMaxCreationDuration: "0s"},
			op:           operation.Operation{Type: operation.Create},
			expectErrors: []utils.ExpectedError{
				{FieldPath: "tags", Message: "must be at least 1m0s"},
			},
		},
		{
			name:         "AFEC registered with negative duration is rejected",
			subscription: afecRegistered,
			tags:         map[string]string{metadataapi.TagClusterMaxCreationDuration: "-5m"},
			op:           operation.Operation{Type: operation.Create},
			expectErrors: []utils.ExpectedError{
				{FieldPath: "tags", Message: "must be at least 1m0s"},
			},
		},
		{
			name:             "AFEC registered with exactly one minute is accepted",
			subscription:     afecRegistered,
			tags:             map[string]string{metadataapi.TagClusterMaxCreationDuration: "1m"},
			op:               operation.Operation{Type: operation.Create},
			expectDeadline:   true,
			expectedDuration: time.Minute,
		},
	}

	fixedNow, _ := time.Parse(time.RFC3339, "2025-01-15T10:00:00Z")
	fakeClock := clocktesting.NewFakePassiveClock(fixedNow)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cluster := &coreapi.HCPOpenShiftCluster{
				TrackedResource: coreapi.TrackedResource{
					Tags: tt.tags,
				},
			}
			admissionContext := &ClusterAdmissionContext{
				Clock:           fakeClock,
				Subscription:    tt.subscription,
				OriginalCluster: cluster.DeepCopy(),
			}
			errs := MutateCluster(context.Background(), admissionContext, tt.op, cluster, nil)

			utils.VerifyErrorsMatch(t, tt.expectErrors, errs)

			if !tt.expectDeadline {
				if cluster.ServiceProviderProperties.CreateOperationCompletionDeadline != nil {
					t.Errorf("expected no deadline, got %v", cluster.ServiceProviderProperties.CreateOperationCompletionDeadline)
				}
				return
			}

			deadline := cluster.ServiceProviderProperties.CreateOperationCompletionDeadline
			if deadline == nil {
				t.Fatal("expected deadline to be set, got nil")
			}

			expected := fixedNow.Add(tt.expectedDuration)
			if !deadline.Time.Equal(expected) {
				t.Errorf("expected deadline %v, got %v", expected, deadline.Time)
			}
		})
	}
}

func TestAdmitCluster_Update(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	const (
		subscriptionID    = "6b690bec-0c16-4ecb-8f67-781caf40bba7"
		resourceGroupName = "test-rg"
		clusterName       = "test-cluster"
	)

	clusterResourceID := metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + subscriptionID +
			"/resourceGroups/" + resourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + clusterName))

	serviceProviderResourceID := metadataapi.Must(azcorearm.ParseResourceID(
		clusterResourceID.String() + "/serviceProviderClusters/default"))

	serviceProviderClusterStatusWithActiveControlPlaneVersion := func(fullVersion string) coreapi.ServiceProviderClusterStatus {
		return coreapi.ServiceProviderClusterStatus{
			ControlPlaneVersion: coreapi.ServiceProviderClusterStatusVersion{
				ActiveVersions: []coreapi.HCPClusterActiveVersion{{Version: ptr.To(metadataapi.Must(semver.ParseTolerant(fullVersion)))}},
			},
		}
	}

	serviceProviderClusterStatusWithActiveControlPlaneVersions := func(fullVersions ...string) coreapi.ServiceProviderClusterStatus {
		active := make([]coreapi.HCPClusterActiveVersion, 0, len(fullVersions))
		for _, v := range fullVersions {
			active = append(active, coreapi.HCPClusterActiveVersion{Version: ptr.To(metadataapi.Must(semver.ParseTolerant(v)))})
		}
		return coreapi.ServiceProviderClusterStatus{
			ControlPlaneVersion: coreapi.ServiceProviderClusterStatusVersion{ActiveVersions: active},
		}
	}

	makeTestNodePool := func(name, versionID string) *coreapi.HCPOpenShiftClusterNodePool {
		nodePoolResourceID := metadataapi.Must(azcorearm.ParseResourceID(
			clusterResourceID.String() + "/nodePools/" + name))
		return &coreapi.HCPOpenShiftClusterNodePool{
			CosmosMetadata: coreapi.CosmosMetadata{
				ResourceID:   nodePoolResourceID,
				PartitionKey: strings.ToLower(nodePoolResourceID.SubscriptionID),
			},
			TrackedResource: coreapi.NewTrackedResource(nodePoolResourceID, "eastus"),
			Properties: coreapi.HCPOpenShiftClusterNodePoolProperties{
				Version: coreapi.NodePoolVersionProfile{ID: versionID},
			},
		}
	}

	makeServiceProviderNodePool := func(nodePoolName string, activeFullVersions ...string) *coreapi.ServiceProviderNodePool {
		npResourceID := metadataapi.Must(azcorearm.ParseResourceID(clusterResourceID.String() + "/nodePools/" + nodePoolName))
		spResourceID := metadataapi.Must(azcorearm.ParseResourceID(fmt.Sprintf("%s/%s/%s",
			npResourceID.String(), coreapi.ServiceProviderNodePoolResourceTypeName, coreapi.ServiceProviderNodePoolResourceName)))
		active := make([]coreapi.HCPNodePoolActiveVersion, 0, len(activeFullVersions))
		for _, v := range activeFullVersions {
			active = append(active, coreapi.HCPNodePoolActiveVersion{Version: ptr.To(metadataapi.Must(semver.ParseTolerant(v)))})
		}
		return &coreapi.ServiceProviderNodePool{
			CosmosMetadata: coreapi.CosmosMetadata{ResourceID: spResourceID, PartitionKey: strings.ToLower(spResourceID.SubscriptionID)},
			Status: coreapi.ServiceProviderNodePoolStatus{
				NodePoolVersion: coreapi.ServiceProviderNodePoolStatusVersion{ActiveVersions: active},
			},
		}
	}

	kmsEtcdProfile := func(keyVersion string) coreapi.EtcdProfile {
		return coreapi.EtcdProfile{
			DataEncryption: coreapi.EtcdDataEncryptionProfile{
				KeyManagementMode: metadataapi.EtcdDataEncryptionKeyManagementModeTypeCustomerManaged,
				CustomerManaged: &coreapi.CustomerManagedEncryptionProfile{
					Kms: &coreapi.KmsEncryptionProfile{
						ActiveKey: coreapi.KmsKey{
							Name:      "test-key",
							VaultName: "test-vault",
							Version:   keyVersion,
						},
					},
				},
			},
		}
	}

	tests := []struct {
		name                         string
		oldClusterVersionID          string
		channelGroup                 string
		etcd                         coreapi.EtcdProfile
		options                      []string
		serviceProviderClusterStatus coreapi.ServiceProviderClusterStatus
		nodePools                    []*coreapi.HCPOpenShiftClusterNodePool
		serviceProviderNodePools     []*coreapi.ServiceProviderNodePool
		newClusterFromOld            func(*coreapi.HCPOpenShiftCluster) //This method uses a copy of the oldCluster, changes are applied to that copy.
		expectErrors                 []utils.ExpectedError
	}{
		{
			name:                         "empty desired version skips admission",
			oldClusterVersionID:          "4.10",
			serviceProviderClusterStatus: serviceProviderClusterStatusWithActiveControlPlaneVersion("4.22"),
			nodePools:                    []*coreapi.HCPOpenShiftClusterNodePool{makeTestNodePool("np1", "4.10.0")},
			newClusterFromOld: func(oldCopy *coreapi.HCPOpenShiftCluster) {
				oldCopy.CustomerProperties.Version.ID = ""
			},
			expectErrors: []utils.ExpectedError{},
		},
		{
			name:                         "unchanged version skips admission",
			oldClusterVersionID:          "5.0",
			serviceProviderClusterStatus: serviceProviderClusterStatusWithActiveControlPlaneVersion("4.22"),
			nodePools:                    []*coreapi.HCPOpenShiftClusterNodePool{makeTestNodePool("workers", "4.20.0")},
			expectErrors:                 []utils.ExpectedError{},
		},
		{
			name:                         "unparsable old version id",
			oldClusterVersionID:          "4.x",
			serviceProviderClusterStatus: serviceProviderClusterStatusWithActiveControlPlaneVersion("4.22"),
			newClusterFromOld: func(c *coreapi.HCPOpenShiftCluster) {
				c.CustomerProperties.Version.ID = "4.22"
			},
			expectErrors: []utils.ExpectedError{
				{FieldPath: "properties.version.id", Message: "Invalid character(s) found in minor number"},
			},
		},
		{
			name:                         "skips skew vs lowest when old minor matches lowest active cluster version",
			oldClusterVersionID:          "4.21",
			serviceProviderClusterStatus: serviceProviderClusterStatusWithActiveControlPlaneVersion("4.21"),
			newClusterFromOld: func(c *coreapi.HCPOpenShiftCluster) {
				c.CustomerProperties.Version.ID = "4.23"
			},
			expectErrors: []utils.ExpectedError{},
		},
		{
			name:                         "allows 4.22 to 5.0 with active cluster version 4.22",
			oldClusterVersionID:          "4.22",
			serviceProviderClusterStatus: serviceProviderClusterStatusWithActiveControlPlaneVersion("4.22"),
			newClusterFromOld: func(c *coreapi.HCPOpenShiftCluster) {
				c.CustomerProperties.Version.ID = "5.0"
			},
			expectErrors: []utils.ExpectedError{},
		},
		{
			name:                         "rejects 5.1 when old minor below lowest active cluster version",
			oldClusterVersionID:          "4.21",
			serviceProviderClusterStatus: serviceProviderClusterStatusWithActiveControlPlaneVersion("4.22"),
			newClusterFromOld: func(c *coreapi.HCPOpenShiftCluster) {
				c.CustomerProperties.Version.ID = "5.1"
			},
			expectErrors: []utils.ExpectedError{
				{FieldPath: "properties.version.id", Message: "invalid upgrade path"},
			},
		},
		{
			name:                         "rejects 4.24 when old minor below lowest active cluster version",
			oldClusterVersionID:          "4.21",
			serviceProviderClusterStatus: serviceProviderClusterStatusWithActiveControlPlaneVersion("4.22"),
			newClusterFromOld: func(c *coreapi.HCPOpenShiftCluster) {
				c.CustomerProperties.Version.ID = "4.24"
			},
			expectErrors: []utils.ExpectedError{
				{FieldPath: "properties.version.id", Message: "only upgrade to the next minor is allowed"},
			},
		},
		{
			name:                         "rejects version below highest active cluster version",
			oldClusterVersionID:          "4.22",
			serviceProviderClusterStatus: serviceProviderClusterStatusWithActiveControlPlaneVersion("4.22"),
			newClusterFromOld: func(c *coreapi.HCPOpenShiftCluster) {
				c.CustomerProperties.Version.ID = "4.21"
			},
			expectErrors: []utils.ExpectedError{
				{FieldPath: "properties.version.id", Message: "must be at least"},
			},
		},
		{
			name:                         "allows upgrade across adjacent active cluster minors",
			oldClusterVersionID:          "4.21",
			serviceProviderClusterStatus: serviceProviderClusterStatusWithActiveControlPlaneVersions("4.22", "4.21"),
			newClusterFromOld: func(c *coreapi.HCPOpenShiftCluster) {
				c.CustomerProperties.Version.ID = "4.22"
			},
			expectErrors: []utils.ExpectedError{},
		},
		{
			name:                         "rejects skip minor vs lowest when fleet spans minors",
			oldClusterVersionID:          "4.21",
			serviceProviderClusterStatus: serviceProviderClusterStatusWithActiveControlPlaneVersions("4.20", "4.22"),
			newClusterFromOld: func(c *coreapi.HCPOpenShiftCluster) {
				c.CustomerProperties.Version.ID = "4.22"
			},
			expectErrors: []utils.ExpectedError{
				{FieldPath: "properties.version.id", Message: "only upgrade to the next minor is allowed"},
			},
		},
		{
			name:                         "rejects when node pool over two minors behind",
			oldClusterVersionID:          "4.20",
			serviceProviderClusterStatus: serviceProviderClusterStatusWithActiveControlPlaneVersion("4.20"),
			nodePools:                    []*coreapi.HCPOpenShiftClusterNodePool{makeTestNodePool("workers", "4.17.0")},
			newClusterFromOld: func(c *coreapi.HCPOpenShiftCluster) {
				c.CustomerProperties.Version.ID = "4.21"
			},
			expectErrors: []utils.ExpectedError{
				{FieldPath: "properties.version.id", Message: "must not be more than two minor versions ahead"},
			},
		},
		{
			name:                         "allows no-op version with node pools in skew",
			oldClusterVersionID:          "4.20",
			serviceProviderClusterStatus: serviceProviderClusterStatusWithActiveControlPlaneVersion("4.20"),
			nodePools: []*coreapi.HCPOpenShiftClusterNodePool{
				makeTestNodePool("workers", "4.18.0"),
				makeTestNodePool("infra", "4.20.3"),
				makeTestNodePool("spot", "4.20.1"),
			},
			expectErrors: []utils.ExpectedError{},
		},
		{
			name:                         "allows 4.22 to 5.0 node pool 4.22",
			oldClusterVersionID:          "4.22",
			serviceProviderClusterStatus: serviceProviderClusterStatusWithActiveControlPlaneVersion("4.22"),
			nodePools:                    []*coreapi.HCPOpenShiftClusterNodePool{makeTestNodePool("workers", "4.22.0")},
			newClusterFromOld: func(c *coreapi.HCPOpenShiftCluster) {
				c.CustomerProperties.Version.ID = "5.0"
			},
			expectErrors: []utils.ExpectedError{},
		},
		{
			name:                         "allows 4.22 to 5.0 node pool 4.21",
			oldClusterVersionID:          "4.22",
			serviceProviderClusterStatus: serviceProviderClusterStatusWithActiveControlPlaneVersion("4.22"),
			nodePools:                    []*coreapi.HCPOpenShiftClusterNodePool{makeTestNodePool("workers", "4.21.0")},
			newClusterFromOld: func(c *coreapi.HCPOpenShiftCluster) {
				c.CustomerProperties.Version.ID = "5.0"
			},
			expectErrors: []utils.ExpectedError{},
		},
		{
			name:                         "allows 4.23 to 5.1 node pool 4.22",
			oldClusterVersionID:          "4.23",
			serviceProviderClusterStatus: serviceProviderClusterStatusWithActiveControlPlaneVersion("4.23"),
			nodePools:                    []*coreapi.HCPOpenShiftClusterNodePool{makeTestNodePool("workers", "4.22.0")},
			newClusterFromOld: func(c *coreapi.HCPOpenShiftCluster) {
				c.CustomerProperties.Version.ID = "5.1"
			},
			expectErrors: []utils.ExpectedError{},
		},
		{
			name:                         "allows 4.23 to 5.1 node pool 4.23",
			oldClusterVersionID:          "4.23",
			serviceProviderClusterStatus: serviceProviderClusterStatusWithActiveControlPlaneVersion("4.23"),
			nodePools:                    []*coreapi.HCPOpenShiftClusterNodePool{makeTestNodePool("workers", "4.23.0")},
			newClusterFromOld: func(c *coreapi.HCPOpenShiftCluster) {
				c.CustomerProperties.Version.ID = "5.1"
			},
			expectErrors: []utils.ExpectedError{},
		},
		{
			name:                         "allows 5.1 to 5.2 node pool 4.23",
			oldClusterVersionID:          "5.1",
			serviceProviderClusterStatus: serviceProviderClusterStatusWithActiveControlPlaneVersion("5.1"),
			nodePools:                    []*coreapi.HCPOpenShiftClusterNodePool{makeTestNodePool("workers", "4.23.0")},
			newClusterFromOld: func(c *coreapi.HCPOpenShiftCluster) {
				c.CustomerProperties.Version.ID = "5.2"
			},
			expectErrors: []utils.ExpectedError{},
		},
		{
			name:                         "rejects 4.22 to 5.0 node pool 4.20",
			oldClusterVersionID:          "4.22",
			serviceProviderClusterStatus: serviceProviderClusterStatusWithActiveControlPlaneVersion("4.22"),
			nodePools:                    []*coreapi.HCPOpenShiftClusterNodePool{makeTestNodePool("workers", "4.20.0")},
			newClusterFromOld: func(c *coreapi.HCPOpenShiftCluster) {
				c.CustomerProperties.Version.ID = "5.0"
			},
			expectErrors: []utils.ExpectedError{
				{FieldPath: "properties.version.id", Message: "incompatible with node pool"},
			},
		},
		{
			name:                         "rejects 4.23 to 5.1 node pool 4.21",
			oldClusterVersionID:          "4.23",
			serviceProviderClusterStatus: serviceProviderClusterStatusWithActiveControlPlaneVersion("4.23"),
			nodePools:                    []*coreapi.HCPOpenShiftClusterNodePool{makeTestNodePool("workers", "4.21.0")},
			newClusterFromOld: func(c *coreapi.HCPOpenShiftCluster) {
				c.CustomerProperties.Version.ID = "5.1"
			},
			expectErrors: []utils.ExpectedError{
				{FieldPath: "properties.version.id", Message: "incompatible with node pool"},
			},
		},
		{
			name:                         "rejects 4.22 to 5.0 node pool 4.23",
			oldClusterVersionID:          "4.22",
			serviceProviderClusterStatus: serviceProviderClusterStatusWithActiveControlPlaneVersion("4.22"),
			nodePools:                    []*coreapi.HCPOpenShiftClusterNodePool{makeTestNodePool("workers", "4.23.0")},
			newClusterFromOld: func(c *coreapi.HCPOpenShiftCluster) {
				c.CustomerProperties.Version.ID = "5.0"
			},
			expectErrors: []utils.ExpectedError{
				{FieldPath: "properties.version.id", Message: "incompatible with node pool"},
			},
		},
		{
			name:                         "rejects 4.22 to 5.0 mixed node pool minors",
			oldClusterVersionID:          "4.22",
			serviceProviderClusterStatus: serviceProviderClusterStatusWithActiveControlPlaneVersion("4.22"),
			nodePools: []*coreapi.HCPOpenShiftClusterNodePool{
				makeTestNodePool("workers", "4.22.0"),
				makeTestNodePool("legacy", "4.20.0"),
			},
			newClusterFromOld: func(c *coreapi.HCPOpenShiftCluster) {
				c.CustomerProperties.Version.ID = "5.0"
			},
			expectErrors: []utils.ExpectedError{
				{FieldPath: "properties.version.id", Message: "incompatible with node pool"},
			},
		},
		{
			name:                         "rejects 4.22 to 5.0 sp node pool behind customer minor",
			oldClusterVersionID:          "4.22",
			serviceProviderClusterStatus: serviceProviderClusterStatusWithActiveControlPlaneVersion("4.22"),
			nodePools:                    []*coreapi.HCPOpenShiftClusterNodePool{makeTestNodePool("workers", "4.22.0")},
			serviceProviderNodePools:     []*coreapi.ServiceProviderNodePool{makeServiceProviderNodePool("workers", "4.17.0")},
			newClusterFromOld: func(c *coreapi.HCPOpenShiftCluster) {
				c.CustomerProperties.Version.ID = "5.0"
			},
			expectErrors: []utils.ExpectedError{
				{FieldPath: "properties.version.id", Message: "incompatible with node pool"},
			},
		},
		{
			name:                         "rejects minor upgrade sp node pool two minors behind",
			oldClusterVersionID:          "4.20",
			serviceProviderClusterStatus: serviceProviderClusterStatusWithActiveControlPlaneVersion("4.20"),
			nodePools:                    []*coreapi.HCPOpenShiftClusterNodePool{makeTestNodePool("workers", "4.20.0")},
			serviceProviderNodePools:     []*coreapi.ServiceProviderNodePool{makeServiceProviderNodePool("workers", "4.17.0")},
			newClusterFromOld: func(c *coreapi.HCPOpenShiftCluster) {
				c.CustomerProperties.Version.ID = "4.21"
			},
			expectErrors: []utils.ExpectedError{
				{FieldPath: "properties.version.id", Message: "must not be more than two minor versions ahead"},
			},
		},
		{
			name:                         "rejects 4.22 to 5.0 incompatible lowest active cluster version",
			oldClusterVersionID:          "4.22",
			serviceProviderClusterStatus: serviceProviderClusterStatusWithActiveControlPlaneVersion("4.22"),
			nodePools:                    []*coreapi.HCPOpenShiftClusterNodePool{makeTestNodePool("workers", "4.22.0")},
			serviceProviderNodePools:     []*coreapi.ServiceProviderNodePool{makeServiceProviderNodePool("workers", "4.22.0", "4.17.0")},
			newClusterFromOld: func(c *coreapi.HCPOpenShiftCluster) {
				c.CustomerProperties.Version.ID = "5.0"
			},
			expectErrors: []utils.ExpectedError{
				{FieldPath: "properties.version.id", Message: "incompatible with node pool"},
			},
		},
		{
			name:                         "allows 4.22 to 5.0 compatible active cluster versions",
			oldClusterVersionID:          "4.22",
			serviceProviderClusterStatus: serviceProviderClusterStatusWithActiveControlPlaneVersion("4.22"),
			nodePools:                    []*coreapi.HCPOpenShiftClusterNodePool{makeTestNodePool("workers", "4.22.0")},
			serviceProviderNodePools:     []*coreapi.ServiceProviderNodePool{makeServiceProviderNodePool("workers", "4.22.1", "4.22.0")},
			newClusterFromOld: func(c *coreapi.HCPOpenShiftCluster) {
				c.CustomerProperties.Version.ID = "5.0"
			},
			expectErrors: []utils.ExpectedError{},
		},
		{
			name:                         "kms key version change allowed at 4.22 nightly",
			oldClusterVersionID:          "4.22",
			channelGroup:                 "nightly",
			etcd:                         kmsEtcdProfile("old-version"),
			options:                      []string{metadataapi.APIVersionOption(metadataapi.APIVersionV20260630Preview)},
			serviceProviderClusterStatus: serviceProviderClusterStatusWithActiveControlPlaneVersion("4.22.0-0.nightly-multi-2026-06-29-132714"),
			newClusterFromOld: func(c *coreapi.HCPOpenShiftCluster) {
				c.CustomerProperties.Etcd.DataEncryption.CustomerManaged.Kms.ActiveKey.Version = "new-version"
			},
			expectErrors: []utils.ExpectedError{},
		},
		{
			name:                         "kms key version change allowed at 4.22",
			oldClusterVersionID:          "4.22",
			etcd:                         kmsEtcdProfile("old-version"),
			options:                      []string{metadataapi.APIVersionOption(metadataapi.APIVersionV20260630Preview)},
			serviceProviderClusterStatus: serviceProviderClusterStatusWithActiveControlPlaneVersion("4.22.4"),
			newClusterFromOld: func(c *coreapi.HCPOpenShiftCluster) {
				c.CustomerProperties.Etcd.DataEncryption.CustomerManaged.Kms.ActiveKey.Version = "new-version"
			},
			expectErrors: []utils.ExpectedError{},
		},
		{
			name:                         "kms key version change allowed at 5.0",
			oldClusterVersionID:          "4.22",
			etcd:                         kmsEtcdProfile("old-version"),
			options:                      []string{metadataapi.APIVersionOption(metadataapi.APIVersionV20260630Preview)},
			serviceProviderClusterStatus: serviceProviderClusterStatusWithActiveControlPlaneVersion("5.0.1"),
			newClusterFromOld: func(c *coreapi.HCPOpenShiftCluster) {
				c.CustomerProperties.Etcd.DataEncryption.CustomerManaged.Kms.ActiveKey.Version = "new-version"
			},
			expectErrors: []utils.ExpectedError{},
		},
		{
			name:                         "kms key version change blocked at 4.21",
			oldClusterVersionID:          "4.21",
			etcd:                         kmsEtcdProfile("old-version"),
			options:                      []string{metadataapi.APIVersionOption(metadataapi.APIVersionV20260630Preview)},
			serviceProviderClusterStatus: serviceProviderClusterStatusWithActiveControlPlaneVersion("4.21.5"),
			newClusterFromOld: func(c *coreapi.HCPOpenShiftCluster) {
				c.CustomerProperties.Etcd.DataEncryption.CustomerManaged.Kms.ActiveKey.Version = "new-version"
			},
			expectErrors: []utils.ExpectedError{
				{FieldPath: "properties.etcd.dataEncryption.customerManaged.kms.activeKey.version", Message: "KMS key version rotation requires cluster version 4.22.0 or above"},
			},
		},
		{
			name:                         "kms key version change allowed during upgrade with lowest >= 4.22.4",
			oldClusterVersionID:          "4.22",
			etcd:                         kmsEtcdProfile("old-version"),
			options:                      []string{metadataapi.APIVersionOption(metadataapi.APIVersionV20260630Preview)},
			serviceProviderClusterStatus: serviceProviderClusterStatusWithActiveControlPlaneVersions("4.23.0", "4.22.4"),
			newClusterFromOld: func(c *coreapi.HCPOpenShiftCluster) {
				c.CustomerProperties.Etcd.DataEncryption.CustomerManaged.Kms.ActiveKey.Version = "new-version"
			},
			expectErrors: []utils.ExpectedError{},
		},
		{
			name:                         "kms key version change blocked during upgrade with lowest < 4.22.0",
			oldClusterVersionID:          "4.22",
			etcd:                         kmsEtcdProfile("old-version"),
			options:                      []string{metadataapi.APIVersionOption(metadataapi.APIVersionV20260630Preview)},
			serviceProviderClusterStatus: serviceProviderClusterStatusWithActiveControlPlaneVersions("4.22.0", "4.21.15"),
			newClusterFromOld: func(c *coreapi.HCPOpenShiftCluster) {
				c.CustomerProperties.Etcd.DataEncryption.CustomerManaged.Kms.ActiveKey.Version = "new-version"
			},
			expectErrors: []utils.ExpectedError{
				{FieldPath: "properties.etcd.dataEncryption.customerManaged.kms.activeKey.version", Message: "KMS key version rotation requires cluster version 4.22.0 or above"},
			},
		},
		{
			name:                         "no error when kms key version unchanged on old cluster",
			oldClusterVersionID:          "4.21",
			etcd:                         kmsEtcdProfile("same-version"),
			options:                      []string{metadataapi.APIVersionOption(metadataapi.APIVersionV20260630Preview)},
			serviceProviderClusterStatus: serviceProviderClusterStatusWithActiveControlPlaneVersion("4.21.0"),
			expectErrors:                 []utils.ExpectedError{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			serviceProviderCluster := &coreapi.ServiceProviderCluster{
				CosmosMetadata: coreapi.CosmosMetadata{ResourceID: serviceProviderResourceID, PartitionKey: strings.ToLower(serviceProviderResourceID.SubscriptionID)},
				Status:         tt.serviceProviderClusterStatus,
			}

			spByName := map[string]*coreapi.ServiceProviderNodePool{}
			for _, sp := range tt.serviceProviderNodePools {
				spByName[sp.ResourceID.Parent.Name] = sp
			}
			var admissionNodePools []ClusterAdmissionNodePool
			for _, nodePool := range tt.nodePools {
				admissionNodePools = append(admissionNodePools, ClusterAdmissionNodePool{
					NodePool:                nodePool,
					ServiceProviderNodePool: spByName[nodePool.Name],
				})
			}

			admissionContext := &ClusterAdmissionContext{
				ServiceProviderCluster: serviceProviderCluster,
				ClusterNodePools:       admissionNodePools,
			}

			etcd := tt.etcd
			if etcd == (coreapi.EtcdProfile{}) {
				etcd = kmsEtcdProfile("v1")
			}
			oldCluster := &coreapi.HCPOpenShiftCluster{
				TrackedResource: coreapi.NewTrackedResource(clusterResourceID, "eastus"),
				CustomerProperties: coreapi.HCPOpenShiftClusterCustomerProperties{
					Version: coreapi.VersionProfile{ID: tt.oldClusterVersionID, ChannelGroup: tt.channelGroup},
					Etcd:    etcd,
				},
			}
			newCluster := oldCluster.DeepCopy()
			if tt.newClusterFromOld != nil {
				tt.newClusterFromOld(newCluster)
			}

			errs := AdmitCluster(ctx, admissionContext, operation.Operation{Type: operation.Update, Options: tt.options}, newCluster, oldCluster)

			utils.VerifyErrorsMatch(t, tt.expectErrors, errs)
		})
	}
}

func TestAdmitCluster_PlatformResourceIDs(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	const subscriptionID = "6b690bec-0c16-4ecb-8f67-781caf40bba7"

	subnetID := metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + subscriptionID + "/resourceGroups/customer/providers/Microsoft.Network/virtualNetworks/vnet/subnets/cluster-subnet"))
	otherSubnetID := metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + subscriptionID + "/resourceGroups/other/providers/Microsoft.Network/virtualNetworks/vnet/subnets/other"))
	nsgID := metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + subscriptionID + "/resourceGroups/customer/providers/Microsoft.Network/networkSecurityGroups/cluster-nsg"))
	otherNsgID := metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + subscriptionID + "/resourceGroups/other/providers/Microsoft.Network/networkSecurityGroups/other-nsg"))

	makeCluster := func(name, managedResourceGroup string, subnet, nsg *azcorearm.ResourceID) *coreapi.HCPOpenShiftCluster {
		resourceID := metadataapi.Must(azcorearm.ParseResourceID(fmt.Sprintf(
			"/subscriptions/%s/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/%s",
			subscriptionID, name)))
		return &coreapi.HCPOpenShiftCluster{
			TrackedResource: coreapi.NewTrackedResource(resourceID, "eastus"),
			CustomerProperties: coreapi.HCPOpenShiftClusterCustomerProperties{
				Platform: coreapi.CustomerPlatformProfile{
					ManagedResourceGroup:   managedResourceGroup,
					SubnetID:               subnet,
					NetworkSecurityGroupID: nsg,
				},
			},
		}
	}

	makeNodePool := func(clusterName, nodePoolName string, subnet *azcorearm.ResourceID) *coreapi.HCPOpenShiftClusterNodePool {
		resourceID := metadataapi.Must(azcorearm.ParseResourceID(fmt.Sprintf(
			"/subscriptions/%s/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/%s/hcpOpenShiftClusterNodePools/%s",
			subscriptionID, clusterName, nodePoolName)))
		return &coreapi.HCPOpenShiftClusterNodePool{
			TrackedResource: coreapi.NewTrackedResource(resourceID, "eastus"),
			Properties: coreapi.HCPOpenShiftClusterNodePoolProperties{
				Platform: coreapi.NodePoolPlatformProfile{
					SubnetID: subnet,
				},
			},
		}
	}

	tests := []struct {
		name                  string
		subscriptionClusters  []*coreapi.HCPOpenShiftCluster
		subscriptionNodePools []*coreapi.HCPOpenShiftClusterNodePool
		newCluster            *coreapi.HCPOpenShiftCluster
		expectErrors          []utils.ExpectedError
	}{
		{
			name:                 "create with empty subscription clusters",
			subscriptionClusters: nil,
			newCluster:           makeCluster("new-cluster", "mrg-new", subnetID, nsgID),
			expectErrors:         []utils.ExpectedError{},
		},
		{
			name: "create rejects duplicate subnet",
			subscriptionClusters: []*coreapi.HCPOpenShiftCluster{
				makeCluster("existing-cluster", "mrg-existing", subnetID, nsgID),
			},
			newCluster: makeCluster("new-cluster", "mrg-new", subnetID, otherNsgID),
			expectErrors: []utils.ExpectedError{
				{FieldPath: "properties.platform.subnetId", Message: "already in use by another cluster"},
			},
		},
		{
			name: "create rejects duplicate network security group",
			subscriptionClusters: []*coreapi.HCPOpenShiftCluster{
				makeCluster("existing-cluster", "mrg-existing", subnetID, nsgID),
			},
			newCluster: makeCluster("new-cluster", "mrg-new", otherSubnetID, nsgID),
			expectErrors: []utils.ExpectedError{
				{FieldPath: "properties.platform.networkSecurityGroupId", Message: "already in use by another cluster"},
			},
		},
		{
			name: "create rejects duplicate managed resource group",
			subscriptionClusters: []*coreapi.HCPOpenShiftCluster{
				makeCluster("existing-cluster", "shared-mrg", subnetID, nsgID),
			},
			newCluster: makeCluster("new-cluster", "shared-mrg", otherSubnetID, otherNsgID),
			expectErrors: []utils.ExpectedError{
				{FieldPath: "properties.platform.managedResourceGroup", Message: "please provide a unique managed resource group name"},
			},
		},
		{
			name: "create rejects duplicate subnet used by node pool",
			subscriptionClusters: []*coreapi.HCPOpenShiftCluster{
				makeCluster("existing-cluster", "mrg-existing", otherSubnetID, nsgID),
			},
			subscriptionNodePools: []*coreapi.HCPOpenShiftClusterNodePool{
				makeNodePool("existing-cluster", "workers", subnetID),
			},
			newCluster: makeCluster("new-cluster", "mrg-new", subnetID, otherNsgID),
			expectErrors: []utils.ExpectedError{
				{FieldPath: "properties.platform.subnetId", Message: "already in use by another cluster"},
			},
		},
		{
			name: "create allows distinct platform values",
			subscriptionClusters: []*coreapi.HCPOpenShiftCluster{
				makeCluster("existing-cluster", "mrg-existing", subnetID, nsgID),
			},
			newCluster:   makeCluster("new-cluster", "mrg-new", otherSubnetID, otherNsgID),
			expectErrors: []utils.ExpectedError{},
		},
		{
			name: "create with nil new platform resource IDs returns required errors",
			subscriptionClusters: []*coreapi.HCPOpenShiftCluster{
				makeCluster("existing-cluster", "mrg-existing", subnetID, nsgID),
			},
			newCluster: makeCluster("new-cluster", "mrg-new", nil, nil),
			expectErrors: []utils.ExpectedError{
				{FieldPath: "properties.platform.subnetId", Message: "Required value"},
				{FieldPath: "properties.platform.networkSecurityGroupId", Message: "Required value"},
			},
		},
		{
			name: "create with existing cluster missing platform resource IDs returns internal errors",
			subscriptionClusters: []*coreapi.HCPOpenShiftCluster{
				makeCluster("existing-cluster", "mrg-existing", nil, nil),
			},
			newCluster: makeCluster("new-cluster", "mrg-new", subnetID, nsgID),
			expectErrors: []utils.ExpectedError{
				{FieldPath: "properties.platform.subnetId", Message: "existing cluster is missing subnetId"},
				{FieldPath: "properties.platform.networkSecurityGroupId", Message: "existing cluster is missing networkSecurityGroupId"},
			},
		},
		{
			name: "create with existing node pool missing subnet returns internal error",
			subscriptionClusters: []*coreapi.HCPOpenShiftCluster{
				makeCluster("existing-cluster", "mrg-existing", otherSubnetID, nsgID),
			},
			subscriptionNodePools: []*coreapi.HCPOpenShiftClusterNodePool{
				makeNodePool("existing-cluster", "workers", nil),
			},
			newCluster: makeCluster("new-cluster", "mrg-new", subnetID, otherNsgID),
			expectErrors: []utils.ExpectedError{
				{FieldPath: "properties.platform.subnetId", Message: "existing node pool is missing subnetId"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			admissionContext := &ClusterAdmissionContext{
				OriginalCluster:       tt.newCluster.DeepCopy(),
				SubscriptionClusters:  tt.subscriptionClusters,
				SubscriptionNodePools: tt.subscriptionNodePools,
			}

			errs := AdmitCluster(ctx, admissionContext, operation.Operation{Type: operation.Create}, tt.newCluster, nil)

			utils.VerifyErrorsMatch(t, tt.expectErrors, errs)
		})
	}
}

// TestAdmitClusterVersionID covers the version-channel admission check that
// fires when a cluster's version.id is updated. The check requires the target
// update channel ("<channelGroup>-<major>.<minor>") to be present in the
// associated HostedCluster's observed status.version.desired.channels, which the
// backend mirrors onto ServiceProviderCluster.Status.DesiredVersionChannels. The
// requested version's major.minor is derived from its ID, so patch, nightly and
// pre-release IDs all resolve to their release-line channel.
func TestAdmitClusterVersionID(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	// Matches the path admitClusterCustomerProperties passes down in production
	// (field.NewPath("properties").Child("version")).
	fldPath := field.NewPath("properties", "version")

	spcWithChannels := func(channels ...string) *coreapi.ServiceProviderCluster {
		return &coreapi.ServiceProviderCluster{
			Status: coreapi.ServiceProviderClusterStatus{
				DesiredVersionChannels: channels,
			},
		}
	}

	tests := []struct {
		name         string
		op           operation.Operation
		oldVersion   *coreapi.VersionProfile
		newVersion   *coreapi.VersionProfile
		spc          *coreapi.ServiceProviderCluster // nil models a missing/unmirrored ServiceProviderCluster
		expectErrors []utils.ExpectedError
	}{
		{
			name:         "version.id update with matching channel passes",
			op:           operation.Operation{Type: operation.Update},
			oldVersion:   &coreapi.VersionProfile{ID: "4.18", ChannelGroup: "stable"},
			newVersion:   &coreapi.VersionProfile{ID: "4.19", ChannelGroup: "stable"},
			spc:          spcWithChannels("stable-4.18", "candidate-4.19", "stable-4.19"),
			expectErrors: []utils.ExpectedError{},
		},
		{
			name:       "version.id update with no matching channel is rejected",
			op:         operation.Operation{Type: operation.Update},
			oldVersion: &coreapi.VersionProfile{ID: "4.18", ChannelGroup: "stable"},
			newVersion: &coreapi.VersionProfile{ID: "4.19", ChannelGroup: "stable"},
			spc:        spcWithChannels("stable-4.20", "candidate-4.20"),
			expectErrors: []utils.ExpectedError{
				{FieldPath: "properties.version.id", Message: `no upgrade path to update channel "stable-4.19"`},
			},
		},
		{
			// Micro/patch version: the requested channel is keyed by major.minor,
			// so "4.20.8" must resolve to the "stable-4.20" channel and pass.
			name:         "micro version resolves to major.minor channel and passes",
			op:           operation.Operation{Type: operation.Update},
			oldVersion:   &coreapi.VersionProfile{ID: "4.19", ChannelGroup: "stable"},
			newVersion:   &coreapi.VersionProfile{ID: "4.20.8", ChannelGroup: "stable"},
			spc:          spcWithChannels("stable-4.20"),
			expectErrors: []utils.ExpectedError{},
		},
		{
			// Micro version whose major.minor channel is absent is rejected; the
			// error names the derived "stable-4.20" channel, not the raw "4.20.8".
			name:       "micro version with no matching major.minor channel is rejected",
			op:         operation.Operation{Type: operation.Update},
			oldVersion: &coreapi.VersionProfile{ID: "4.19", ChannelGroup: "stable"},
			newVersion: &coreapi.VersionProfile{ID: "4.20.8", ChannelGroup: "stable"},
			spc:        spcWithChannels("stable-4.19"),
			expectErrors: []utils.ExpectedError{
				{FieldPath: "properties.version.id", Message: `no upgrade path to update channel "stable-4.20"`},
			},
		},
		{
			// Nightly pre-release: "5.0.0-0.nightly-..." must resolve to "5.0".
			name:         "nightly version resolves to major.minor channel and passes",
			op:           operation.Operation{Type: operation.Update},
			oldVersion:   &coreapi.VersionProfile{ID: "4.19", ChannelGroup: "candidate"},
			newVersion:   &coreapi.VersionProfile{ID: "5.0.0-0.nightly-2026-08-05-123456", ChannelGroup: "candidate"},
			spc:          spcWithChannels("candidate-5.0"),
			expectErrors: []utils.ExpectedError{},
		},
		{
			name:       "nightly version with no matching major.minor channel is rejected",
			op:         operation.Operation{Type: operation.Update},
			oldVersion: &coreapi.VersionProfile{ID: "4.19", ChannelGroup: "candidate"},
			newVersion: &coreapi.VersionProfile{ID: "5.0.0-0.nightly-2026-08-05-123456", ChannelGroup: "candidate"},
			spc:        spcWithChannels("candidate-4.19"),
			expectErrors: []utils.ExpectedError{
				{FieldPath: "properties.version.id", Message: `no upgrade path to update channel "candidate-5.0"`},
			},
		},
		{
			// Pre-release: "4.21.0-rc.1" must resolve to "4.21".
			name:         "pre-release version resolves to major.minor channel and passes",
			op:           operation.Operation{Type: operation.Update},
			oldVersion:   &coreapi.VersionProfile{ID: "4.20", ChannelGroup: "fast"},
			newVersion:   &coreapi.VersionProfile{ID: "4.21.0-rc.1", ChannelGroup: "fast"},
			spc:          spcWithChannels("fast-4.21"),
			expectErrors: []utils.ExpectedError{},
		},
		{
			// Fail open: the ServiceProviderCluster (and thus the channel mirror)
			// is not available, so the channel check cannot run and must not block.
			// The missing-prefetch condition is separately surfaced as an
			// InternalError by the version-skew check in admitClusterVersionProfile.
			name:         "version.id update with missing service provider cluster skips (fail open until synced)",
			op:           operation.Operation{Type: operation.Update},
			oldVersion:   &coreapi.VersionProfile{ID: "4.18", ChannelGroup: "stable"},
			newVersion:   &coreapi.VersionProfile{ID: "4.19", ChannelGroup: "stable"},
			spc:          nil,
			expectErrors: []utils.ExpectedError{},
		},
		{
			// Fail open: the backend has not yet mirrored any channels, so we
			// cannot validate the requested channel and must not block the update.
			name:         "version.id update with empty channel list skips (fail open until synced)",
			op:           operation.Operation{Type: operation.Update},
			oldVersion:   &coreapi.VersionProfile{ID: "4.18", ChannelGroup: "stable"},
			newVersion:   &coreapi.VersionProfile{ID: "4.19", ChannelGroup: "stable"},
			spc:          spcWithChannels(),
			expectErrors: []utils.ExpectedError{},
		},
		{
			name:         "unchanged version.id is a no-op",
			op:           operation.Operation{Type: operation.Update},
			oldVersion:   &coreapi.VersionProfile{ID: "4.19", ChannelGroup: "stable"},
			newVersion:   &coreapi.VersionProfile{ID: "4.19", ChannelGroup: "stable"},
			spc:          spcWithChannels(), // no channels present, but unchanged version is never validated
			expectErrors: []utils.ExpectedError{},
		},
		{
			name:         "create operation is a no-op",
			op:           operation.Operation{Type: operation.Create},
			oldVersion:   nil,
			newVersion:   &coreapi.VersionProfile{ID: "4.19", ChannelGroup: "stable"},
			spc:          nil,
			expectErrors: []utils.ExpectedError{},
		},
		{
			name:         "empty channel group skips the check",
			op:           operation.Operation{Type: operation.Update},
			oldVersion:   &coreapi.VersionProfile{ID: "4.18"},
			newVersion:   &coreapi.VersionProfile{ID: "4.19"},
			spc:          spcWithChannels("stable-4.20"),
			expectErrors: []utils.ExpectedError{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			admissionContext := &ClusterAdmissionContext{
				ServiceProviderCluster: tt.spc,
			}

			errs := admitClusterVersionID(ctx, admissionContext, tt.op, fldPath, tt.newVersion, tt.oldVersion)

			utils.VerifyErrorsMatch(t, tt.expectErrors, errs)
		})
	}
}
