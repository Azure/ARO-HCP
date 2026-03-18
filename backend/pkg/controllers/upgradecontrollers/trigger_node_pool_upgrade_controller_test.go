// Copyright 2026 Microsoft Corporation
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package upgradecontrollers

import (
	"context"
	"fmt"
	"testing"

	"github.com/blang/semver/v4"
	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"

	"k8s.io/utils/ptr"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/ocm"
)

func TestTriggerNodePoolUpgradeSyncer_CreateUpgradePolicyIfNeeded(t *testing.T) {
	testNodePoolServiceID, _ := api.NewInternalID("/api/aro_hcp/v1alpha1/clusters/test-cluster-id/node_pools/test-nodepool-id")

	tests := []struct {
		name                         string
		desiredVersion               *semver.Version
		nodePoolServiceID            api.InternalID
		mockSetup                    func(*ocm.MockClusterServiceClientSpec)
		expectError                  bool
		expectedErrorContains        string
		expectPolicyCreation         bool
		expectedCreatedPolicyVersion string
	}{
		{
			name:              "latest existing policy matches desired version - returns nil",
			desiredVersion:    ptr.To(semver.MustParse("4.19.20")),
			nodePoolServiceID: testNodePoolServiceID,
			mockSetup: func(mc *ocm.MockClusterServiceClientSpec) {
				latestPolicy := api.Must(arohcpv1alpha1.NewNodePoolUpgradePolicy().Version("4.19.20").Build())
				olderPolicy := api.Must(arohcpv1alpha1.NewNodePoolUpgradePolicy().Version("4.19.15").Build())

				mc.EXPECT().
					ListNodePoolUpgradePolicies(testNodePoolServiceID, "creation_timestamp desc").
					Return(ocm.NewSimpleNodePoolUpgradePolicyListIterator([]*arohcpv1alpha1.NodePoolUpgradePolicy{latestPolicy, olderPolicy}, nil))
			},
			expectError:          false,
			expectPolicyCreation: false,
		},
		{
			name:              "latest existing policy differs from desired version - creates upgrade policy",
			desiredVersion:    ptr.To(semver.MustParse("4.19.20")),
			nodePoolServiceID: testNodePoolServiceID,
			mockSetup: func(mc *ocm.MockClusterServiceClientSpec) {
				latestPolicy := api.Must(arohcpv1alpha1.NewNodePoolUpgradePolicy().Version("4.19.18").Build())
				olderPolicy := api.Must(arohcpv1alpha1.NewNodePoolUpgradePolicy().Version("4.19.15").Build())

				mc.EXPECT().
					ListNodePoolUpgradePolicies(testNodePoolServiceID, "creation_timestamp desc").
					Return(ocm.NewSimpleNodePoolUpgradePolicyListIterator([]*arohcpv1alpha1.NodePoolUpgradePolicy{latestPolicy, olderPolicy}, nil))

				expectedBuilder := arohcpv1alpha1.NewNodePoolUpgradePolicy().Version("4.19.20")
				mc.EXPECT().
					PostNodePoolUpgradePolicy(
						context.Background(),
						testNodePoolServiceID,
						expectedBuilder,
					).
					Return(api.Must(expectedBuilder.Build()), nil)
			},
			expectError:                  false,
			expectPolicyCreation:         true,
			expectedCreatedPolicyVersion: "4.19.20",
		},
		{
			name:              "no existing policies - creates upgrade policy",
			desiredVersion:    ptr.To(semver.MustParse("4.19.20")),
			nodePoolServiceID: testNodePoolServiceID,
			mockSetup: func(mc *ocm.MockClusterServiceClientSpec) {
				mc.EXPECT().
					ListNodePoolUpgradePolicies(testNodePoolServiceID, "creation_timestamp desc").
					Return(ocm.NewSimpleNodePoolUpgradePolicyListIterator([]*arohcpv1alpha1.NodePoolUpgradePolicy{}, nil))

				expectedBuilder := arohcpv1alpha1.NewNodePoolUpgradePolicy().Version("4.19.20")
				mc.EXPECT().
					PostNodePoolUpgradePolicy(
						context.Background(),
						testNodePoolServiceID,
						expectedBuilder,
					).
					Return(api.Must(expectedBuilder.Build()), nil)
			},
			expectError:                  false,
			expectPolicyCreation:         true,
			expectedCreatedPolicyVersion: "4.19.20",
		},
		{
			name:              "list node pool upgrade policies fails - returns error",
			desiredVersion:    ptr.To(semver.MustParse("4.19.20")),
			nodePoolServiceID: testNodePoolServiceID,
			mockSetup: func(mc *ocm.MockClusterServiceClientSpec) {
				mc.EXPECT().
					ListNodePoolUpgradePolicies(testNodePoolServiceID, "creation_timestamp desc").
					Return(ocm.NewSimpleNodePoolUpgradePolicyListIterator(nil, fmt.Errorf("cluster service unavailable")))
			},
			expectError:           true,
			expectedErrorContains: "failed to list node pool upgrade policies",
			expectPolicyCreation:  false,
		},
		{
			name:              "post node pool upgrade policy fails - returns error",
			desiredVersion:    ptr.To(semver.MustParse("4.19.20")),
			nodePoolServiceID: testNodePoolServiceID,
			mockSetup: func(mc *ocm.MockClusterServiceClientSpec) {
				mc.EXPECT().
					ListNodePoolUpgradePolicies(testNodePoolServiceID, "creation_timestamp desc").
					Return(ocm.NewSimpleNodePoolUpgradePolicyListIterator([]*arohcpv1alpha1.NodePoolUpgradePolicy{}, nil))

				// Policy creation fails
				mc.EXPECT().
					PostNodePoolUpgradePolicy(
						context.Background(),
						testNodePoolServiceID,
						arohcpv1alpha1.NewNodePoolUpgradePolicy().Version("4.19.20"),
					).
					Return(nil, fmt.Errorf("cluster service API error"))
			},
			expectError:           true,
			expectedErrorContains: "failed to create node pool upgrade policy",
			expectPolicyCreation:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			mockClusterServiceClient := ocm.NewMockClusterServiceClientSpec(ctrl)
			tt.mockSetup(mockClusterServiceClient)

			syncer := &triggerNodePoolUpgradeSyncer{
				clusterServiceClient: mockClusterServiceClient,
			}

			ctx := context.Background()
			err := syncer.createUpgradePolicyIfNeeded(ctx, tt.desiredVersion, tt.nodePoolServiceID)

			if tt.expectError {
				assert.Error(t, err)
				assert.NotEmpty(t, tt.expectedErrorContains, "expectedErrorContains should be set when expectError is true")
				assert.ErrorContains(t, err, tt.expectedErrorContains)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
