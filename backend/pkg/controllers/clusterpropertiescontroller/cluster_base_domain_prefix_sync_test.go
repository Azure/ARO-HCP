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

package clusterpropertiescontroller

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
	"github.com/Azure/ARO-HCP/internal/ocm"
)

func TestClusterBaseDomainPrefixSyncer_SyncOnce(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name                     string
		existingCluster          *api.HCPOpenShiftCluster
		csDomainPrefix           string
		expectCSGetCluster       bool
		expectedBaseDomainPrefix string
	}{
		{
			name: "short-circuit when base domain prefix already set",
			existingCluster: newTestCluster(testClusterName, func(c *api.HCPOpenShiftCluster) {
				c.CustomerProperties.DNS.BaseDomainPrefix = testBaseDomainPrefix
			}),
			expectedBaseDomainPrefix: testBaseDomainPrefix,
		},
		{
			name:                     "sync base domain prefix from Cluster Service when missing",
			existingCluster:          newTestCluster(testClusterName),
			csDomainPrefix:           testBaseDomainPrefix,
			expectCSGetCluster:       true,
			expectedBaseDomainPrefix: testBaseDomainPrefix,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			mockResourcesDB, err := databasetesting.NewMockResourcesDBClientWithResources(ctx, []any{tc.existingCluster})
			require.NoError(t, err)

			mockCSClient := ocm.NewMockClusterServiceClientSpec(ctrl)
			if tc.expectCSGetCluster {
				csCluster, err := arohcpv1alpha1.NewCluster().DomainPrefix(tc.csDomainPrefix).Build()
				require.NoError(t, err)
				mockCSClient.EXPECT().
					GetCluster(gomock.Any(), api.Must(api.NewInternalID(testClusterServiceIDStr))).
					Return(csCluster, nil)
			}

			syncer := &clusterBaseDomainPrefixSyncer{
				cooldownChecker:      &alwaysSyncCooldownChecker{},
				resourcesDBClient:    mockResourcesDB,
				clusterServiceClient: mockCSClient,
			}

			key := controllerutils.HCPClusterKey{
				SubscriptionID:    testSubscriptionID,
				ResourceGroupName: testResourceGroupName,
				HCPClusterName:    tc.existingCluster.Name,
			}
			err = syncer.SyncOnce(ctx, key)
			require.NoError(t, err)

			updatedCluster, err := mockResourcesDB.HCPClusters(testSubscriptionID, testResourceGroupName).Get(ctx, tc.existingCluster.Name)
			require.NoError(t, err)

			assert.Equal(t, tc.expectedBaseDomainPrefix, updatedCluster.CustomerProperties.DNS.BaseDomainPrefix)
		})
	}
}
