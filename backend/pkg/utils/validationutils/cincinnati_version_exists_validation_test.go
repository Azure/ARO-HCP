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

package validationutils

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/blang/semver/v4"
	"github.com/go-logr/logr"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	configv1 "github.com/openshift/api/config/v1"
	cvocincinnati "github.com/openshift/cluster-version-operator/pkg/cincinnati"
	"github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/Azure/ARO-HCP/backend/pkg/kubeapplierhelpers"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
	"github.com/Azure/ARO-HCP/internal/cincinnati"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/listers/kubeapplierlisters"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// testHostedClusterUUID is the cluster UUID carried by the HostedCluster mirror in
// newHostedClusterReadDesire. Cincinnati keys its update graph on this value.
const testHostedClusterUUID = "00000000-0000-0000-0000-000000000000"

func newTestClusterForCincinnatiVersionExistenceValidation(t *testing.T) *coreapi.Cluster {
	t.Helper()
	resourceID := parseClusterResourceID(t,
		"/subscriptions/"+testSubscriptionID+
			"/resourceGroups/"+testResourceGroup+
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/"+testClusterName)
	return &coreapi.Cluster{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(resourceID.SubscriptionID),
		},
		TrackedResource: coreapi.TrackedResource{
			Resource: coreapi.Resource{
				ID:   resourceID,
				Name: testClusterName,
				Type: coreapi.ClusterResourceType.String(),
			},
			Location: testLocation,
		},
	}
}

func newTestNodePoolForCincinnatiVersionExistenceValidation(t *testing.T, versionID, channelGroup string) *coreapi.NodePool {
	t.Helper()
	resourceID := parseClusterResourceID(t,
		"/subscriptions/"+testSubscriptionID+
			"/resourceGroups/"+testResourceGroup+
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/"+testClusterName+
			"/nodePools/"+testNodePoolName)
	return &coreapi.NodePool{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(resourceID.SubscriptionID),
		},
		TrackedResource: coreapi.TrackedResource{
			Resource: coreapi.Resource{
				ID:   resourceID,
				Name: testNodePoolName,
				Type: coreapi.NodePoolResourceType.String(),
			},
			Location: testLocation,
		},
		Properties: coreapi.NodePoolProperties{
			Version: coreapi.NodePoolVersionProfile{
				ID:           versionID,
				ChannelGroup: channelGroup,
			},
		},
	}
}

// newTestHostedClusterReadDesire builds a ReadDesire whose kubeContent is a HostedCluster
// carrying clusterUUID, matching what the kube-applier mirror writes.
func newTestHostedClusterReadDesire(t *testing.T, clusterUUID string) *kubeapplierapi.ReadDesire {
	t.Helper()
	raw, err := json.Marshal(&v1beta1.HostedCluster{Spec: v1beta1.HostedClusterSpec{ClusterID: clusterUUID}})
	require.NoError(t, err)
	return &kubeapplierapi.ReadDesire{
		Status: kubeapplierapi.ReadDesireStatus{
			Conditions: []metav1.Condition{{
				Type:   kubeapplierapi.ConditionTypeSuccessful,
				Status: metav1.ConditionTrue,
				Reason: "Observed",
			}},
			KubeContent: &runtime.RawExtension{Raw: raw},
		},
	}
}

func parseClusterResourceID(t *testing.T, id string) *azcorearm.ResourceID {
	t.Helper()
	resourceID, err := azcorearm.ParseResourceID(id)
	require.NoError(t, err)
	return resourceID
}

func TestNodePoolCincinnatiVersionExistsValidation_Validate(t *testing.T) {
	ctx := utils.ContextWithLogger(context.Background(), logr.Discard())
	cluster := newTestClusterForCincinnatiVersionExistenceValidation(t)
	subscription := &coreapi.Subscription{}

	// expectHostedClusterNotFound reports the HostedCluster mirror as absent, so the
	// validation falls back to an empty cluster UUID.
	expectHostedClusterNotFound := func(lister *kubeapplierlisters.MockReadDesireLister) {
		lister.EXPECT().
			GetForCluster(gomock.Any(), testSubscriptionID, testResourceGroup, testClusterName, kubeapplierhelpers.ReadDesireNameReadonlyHostedCluster).
			Return(nil, cosmosstorageutils.NewNotFoundError()).
			AnyTimes()
	}

	tests := []struct {
		name                       string
		nodePool                   *coreapi.NodePool
		setupMockCincinnatiCache   func(cache *cincinnati.MockClientCache, client *cincinnati.MockClient)
		setupMockReadDesireLister  func(lister *kubeapplierlisters.MockReadDesireLister)
		wantOutcome                OutcomeType
		wantReason                 string
		wantInternalMessageContain string
		wantUserMessageContain     string
	}{
		{
			name:                      "failed when version ID is invalid semver",
			nodePool:                  newTestNodePoolForCincinnatiVersionExistenceValidation(t, "not-a-version", "stable"),
			setupMockReadDesireLister: expectHostedClusterNotFound,
			wantOutcome:               OutcomeTypeFailed,
			wantReason:                "InvalidVersionFormat",
			wantUserMessageContain:    "Invalid version format",
		},
		{
			name:                      "failed when version ID is not a full x.y.z version",
			nodePool:                  newTestNodePoolForCincinnatiVersionExistenceValidation(t, "4.18", "stable"),
			setupMockReadDesireLister: expectHostedClusterNotFound,
			wantOutcome:               OutcomeTypeFailed,
			wantReason:                "InvalidVersionFormat",
			wantUserMessageContain:    "Invalid version format",
		},
		{
			name:                      "failed when version ID has invalid prerelease metadata",
			nodePool:                  newTestNodePoolForCincinnatiVersionExistenceValidation(t, "4.18.0-rc!", "stable"),
			setupMockReadDesireLister: expectHostedClusterNotFound,
			wantOutcome:               OutcomeTypeFailed,
			wantReason:                "InvalidVersionFormat",
			wantUserMessageContain:    "Invalid version format",
		},
		{
			name:                      "passed when version exists in Cincinnati",
			nodePool:                  newTestNodePoolForCincinnatiVersionExistenceValidation(t, "4.18.3", "stable"),
			setupMockReadDesireLister: expectHostedClusterNotFound,
			setupMockCincinnatiCache: func(cache *cincinnati.MockClientCache, client *cincinnati.MockClient) {
				cache.EXPECT().
					GetOrCreateClient(gomock.Any()).
					Return(client)

				version := semver.MustParse("4.18.3")
				client.EXPECT().
					GetUpdates(
						gomock.Any(),
						gomock.Any(),
						"multi",
						"multi",
						"stable-4.18",
						version,
					).
					Return(configv1.Release{}, nil, nil, nil)
			},
			wantOutcome:                OutcomeTypePassed,
			wantReason:                 coreapi.ControllerConditionReasonAsExpected,
			wantUserMessageContain:     "Version 4.18.3 is available",
			wantInternalMessageContain: "version 4.18.3 is available in Cincinnati channel stable-4.18",
		},
		{
			name:     "passed and resolves the client using the cached HostedCluster UUID",
			nodePool: newTestNodePoolForCincinnatiVersionExistenceValidation(t, "4.18.3", "stable"),
			setupMockReadDesireLister: func(lister *kubeapplierlisters.MockReadDesireLister) {
				lister.EXPECT().
					GetForCluster(gomock.Any(), testSubscriptionID, testResourceGroup, testClusterName, kubeapplierhelpers.ReadDesireNameReadonlyHostedCluster).
					Return(newTestHostedClusterReadDesire(t, testHostedClusterUUID), nil).
					AnyTimes()
			},
			setupMockCincinnatiCache: func(cache *cincinnati.MockClientCache, client *cincinnati.MockClient) {
				cache.EXPECT().
					GetOrCreateClient(uuid.MustParse(testHostedClusterUUID)).
					Return(client)

				version := semver.MustParse("4.18.3")
				client.EXPECT().
					GetUpdates(gomock.Any(), gomock.Any(), "multi", "multi", "stable-4.18", version).
					Return(configv1.Release{}, nil, nil, nil)
			},
			wantOutcome:            OutcomeTypePassed,
			wantReason:             coreapi.ControllerConditionReasonAsExpected,
			wantUserMessageContain: "Version 4.18.3 is available",
		},
		{
			name:                      "failed when version not found in Cincinnati",
			nodePool:                  newTestNodePoolForCincinnatiVersionExistenceValidation(t, "5.0.0-rc.1", "candidate"),
			setupMockReadDesireLister: expectHostedClusterNotFound,
			setupMockCincinnatiCache: func(cache *cincinnati.MockClientCache, client *cincinnati.MockClient) {
				cache.EXPECT().
					GetOrCreateClient(gomock.Any()).
					Return(client)

				version := semver.MustParse("5.0.0-rc.1")
				client.EXPECT().
					GetUpdates(
						gomock.Any(),
						gomock.Any(), // Cincinnati URI
						"multi",
						"multi",
						"candidate-5.0",
						version,
					).
					Return(configv1.Release{}, nil, nil, &cvocincinnati.Error{
						Reason: "VersionNotFound",
					})
			},
			wantOutcome:                OutcomeTypeFailed,
			wantReason:                 "VersionNotFound",
			wantInternalMessageContain: "version 5.0.0-rc.1 not available in Cincinnati channel candidate-5.0",
			wantUserMessageContain:     "Version \"5.0.0-rc.1\" in channel group \"candidate\" is not available",
		},
		{
			name:                      "unknown when Cincinnati returns transient error",
			nodePool:                  newTestNodePoolForCincinnatiVersionExistenceValidation(t, "4.18.0", "stable"),
			setupMockReadDesireLister: expectHostedClusterNotFound,
			setupMockCincinnatiCache: func(cache *cincinnati.MockClientCache, client *cincinnati.MockClient) {
				cache.EXPECT().
					GetOrCreateClient(gomock.Any()).
					Return(client)

				version := semver.MustParse("4.18.0")
				client.EXPECT().
					GetUpdates(
						gomock.Any(),
						gomock.Any(),
						"multi",
						"multi",
						"stable-4.18",
						version,
					).
					Return(configv1.Release{}, nil, nil, &cvocincinnati.Error{
						Reason: "InternalError",
					})
			},
			wantOutcome:                OutcomeTypeUnknown,
			wantReason:                 "CincinnatiUnavailable",
			wantInternalMessageContain: "cincinnati query failed",
			wantUserMessageContain:     "Unable to verify that version is available due to temporary service issue",
		},
		{
			name:                      "unknown when Cincinnati returns non-Cincinnati error",
			nodePool:                  newTestNodePoolForCincinnatiVersionExistenceValidation(t, "4.18.0", "stable"),
			setupMockReadDesireLister: expectHostedClusterNotFound,
			setupMockCincinnatiCache: func(cache *cincinnati.MockClientCache, client *cincinnati.MockClient) {
				cache.EXPECT().
					GetOrCreateClient(gomock.Any()).
					Return(client)

				version := semver.MustParse("4.18.0")
				client.EXPECT().
					GetUpdates(
						gomock.Any(),
						gomock.Any(),
						"multi",
						"multi",
						"stable-4.18",
						version,
					).
					Return(configv1.Release{}, nil, nil, errors.New("some other error"))
			},
			wantOutcome:                OutcomeTypeUnknown,
			wantReason:                 "InternalError",
			wantInternalMessageContain: "failed to query Cincinnati",
			wantUserMessageContain:     "Unable to verify that version is available",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			cincinnatiCache := cincinnati.NewMockClientCache(ctrl)
			cincinnatiClient := cincinnati.NewMockClient(ctrl)
			readDesireLister := kubeapplierlisters.NewMockReadDesireLister(ctrl)

			tt.setupMockReadDesireLister(readDesireLister)

			if tt.setupMockCincinnatiCache != nil {
				tt.setupMockCincinnatiCache(cincinnatiCache, cincinnatiClient)
			}

			validation := NewNodePoolCincinnatiVersionExistsValidation(readDesireLister, cincinnatiCache)
			result := validation.Validate(ctx, cluster, subscription, tt.nodePool)
			require.NoError(t, result.Validate(), "validation result should be well-formed")

			// A pass deliberately overrides the framework's 12h default:
			// version and channel group are customer-mutable, so a passed result
			// goes stale as soon as a different version is requested.
			wantRetryBase := 60 * time.Second
			if result.Outcome.Type == OutcomeTypePassed {
				wantRetryBase = time.Hour
			}
			require.NotNil(t, result.EarliestRetryAfter, "every outcome schedules a cooldown")
			assert.GreaterOrEqual(t, *result.EarliestRetryAfter, wantRetryBase, "cooldown below jitter range")
			assert.LessOrEqual(t, *result.EarliestRetryAfter, wantRetryBase+wantRetryBase/2, "cooldown above jitter range")

			assert.Equal(t, tt.wantOutcome, result.Outcome.Type, "outcome type mismatch")

			// Check reason and user message based on outcome type
			switch result.Outcome.Type {
			case OutcomeTypePassed:
				assert.Equal(t, tt.wantReason, result.Outcome.Passed.Reason)
				if tt.wantUserMessageContain != "" {
					assert.Contains(t, result.Outcome.Passed.UserMessage, tt.wantUserMessageContain)
				}
			case OutcomeTypeFailed:
				assert.Equal(t, tt.wantReason, result.Outcome.Failed.Reason)
				if tt.wantUserMessageContain != "" {
					assert.Contains(t, result.Outcome.Failed.UserMessage, tt.wantUserMessageContain)
				}
			case OutcomeTypeSkipped:
				assert.Equal(t, tt.wantReason, result.Outcome.Skipped.Reason)
				if tt.wantUserMessageContain != "" {
					assert.Contains(t, result.Outcome.Skipped.UserMessage, tt.wantUserMessageContain)
				}
			case OutcomeTypeUnknown:
				assert.Equal(t, tt.wantReason, result.Outcome.Unknown.Reason)
				if tt.wantUserMessageContain != "" {
					assert.Contains(t, result.Outcome.Unknown.UserMessage, tt.wantUserMessageContain)
				}
			}

			// Check internal message
			if tt.wantInternalMessageContain != "" {
				assert.Contains(t, result.InternalMessage(), tt.wantInternalMessageContain, "internal message mismatch")
			}
		})
	}
}
