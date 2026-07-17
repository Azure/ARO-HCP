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

	hsv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/listertesting"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
)

const (
	testHostedClusterNamespace = "ocm-production-abc123"
	testHostedClusterName      = "cluster1"
	testControlPlaneNamespace  = "ocm-production-abc123-cluster1"
)

func TestServiceProviderClusterPropertiesSyncer_SyncOnce(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name                           string
		existingCluster                *api.HCPOpenShiftCluster
		existingSPC                    *api.ServiceProviderCluster
		readDesire                     *kubeapplier.ReadDesire
		wantErr                        bool
		expectedHostedClusterNamespace string
		expectedControlPlaneNamespace  string
	}{
		{
			name:            "sync namespaces from HostedCluster ReadDesire",
			existingCluster: newTestCluster(testClusterName),
			existingSPC:     newTestServiceProviderCluster(testClusterName, nil, nil),
			readDesire: newTestHostedClusterReadDesire(t, func(hc *hsv1beta1.HostedCluster) {
				hc.Namespace = testHostedClusterNamespace
				hc.Name = testHostedClusterName
			}),
			expectedHostedClusterNamespace: testHostedClusterNamespace,
			expectedControlPlaneNamespace:  testControlPlaneNamespace,
		},
		{
			name:            "short-circuit when namespaces already match",
			existingCluster: newTestCluster(testClusterName),
			existingSPC: func() *api.ServiceProviderCluster {
				spc := newTestServiceProviderCluster(testClusterName, nil, nil)
				spc.Status.HostedClusterNamespace = testHostedClusterNamespace
				spc.Status.ControlPlaneNamespace = testControlPlaneNamespace
				return spc
			}(),
			readDesire: newTestHostedClusterReadDesire(t, func(hc *hsv1beta1.HostedCluster) {
				hc.Namespace = testHostedClusterNamespace
				hc.Name = testHostedClusterName
			}),
			expectedHostedClusterNamespace: testHostedClusterNamespace,
			expectedControlPlaneNamespace:  testControlPlaneNamespace,
		},
		{
			name:            "no-op when HostedCluster ReadDesire not found",
			existingCluster: newTestCluster(testOtherClusterName),
			existingSPC:     newTestServiceProviderCluster(testOtherClusterName, nil, nil),
			readDesire:      nil,
		},
		{
			name:            "no-op when HostedCluster has empty namespace",
			existingCluster: newTestCluster(testClusterName),
			existingSPC:     newTestServiceProviderCluster(testClusterName, nil, nil),
			readDesire: newTestHostedClusterReadDesire(t, func(hc *hsv1beta1.HostedCluster) {
				hc.Namespace = ""
				hc.Name = testHostedClusterName
			}),
		},
		{
			name:            "no-op when HostedCluster has empty name",
			existingCluster: newTestCluster(testClusterName),
			existingSPC:     newTestServiceProviderCluster(testClusterName, nil, nil),
			readDesire: newTestHostedClusterReadDesire(t, func(hc *hsv1beta1.HostedCluster) {
				hc.Namespace = testHostedClusterNamespace
				hc.Name = ""
			}),
		},
		{
			name:            "no-op when ServiceProviderCluster not found",
			existingCluster: newTestCluster(testClusterName),
			existingSPC:     nil,
			readDesire: newTestHostedClusterReadDesire(t, func(hc *hsv1beta1.HostedCluster) {
				hc.Namespace = testHostedClusterNamespace
				hc.Name = testHostedClusterName
			}),
		},
		{
			name:            "dots in name are replaced with dashes in control plane namespace",
			existingCluster: newTestCluster(testClusterName),
			existingSPC:     newTestServiceProviderCluster(testClusterName, nil, nil),
			readDesire: newTestHostedClusterReadDesire(t, func(hc *hsv1beta1.HostedCluster) {
				hc.Namespace = "ns"
				hc.Name = "my.dotted.name"
			}),
			expectedHostedClusterNamespace: "ns",
			expectedControlPlaneNamespace:  "ns-my-dotted-name",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()

			resources := []any{tc.existingCluster}
			if tc.existingSPC != nil {
				resources = append(resources, tc.existingSPC)
			}
			mockResourcesDB, err := databasetesting.NewMockResourcesDBClientWithResources(ctx, resources)
			require.NoError(t, err)

			readDesireLister, err := newSeededReadDesireLister(ctx, tc.readDesire)
			require.NoError(t, err)

			syncer := &serviceProviderClusterPropertiesSyncer{
				serviceProviderClusterLister: &listertesting.DBServiceProviderClusterLister{ResourcesDBClient: mockResourcesDB},
				resourcesDBClient:            mockResourcesDB,
				readDesireLister:             readDesireLister,
			}

			key := controllerutils.HCPClusterKey{
				SubscriptionID:    testSubscriptionID,
				ResourceGroupName: testResourceGroupName,
				HCPClusterName:    tc.existingCluster.Name,
			}
			err = syncer.SyncOnce(ctx, key)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)

			if tc.existingSPC == nil {
				return
			}

			updatedSPC, err := mockResourcesDB.ServiceProviderClusters(
				testSubscriptionID, testResourceGroupName, tc.existingCluster.Name,
			).Get(ctx, api.ServiceProviderClusterResourceName)
			require.NoError(t, err)

			assert.Equal(t, tc.expectedHostedClusterNamespace, updatedSPC.Status.HostedClusterNamespace)
			assert.Equal(t, tc.expectedControlPlaneNamespace, updatedSPC.Status.ControlPlaneNamespace)
		})
	}
}
