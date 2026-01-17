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

package frontend

import (
	"context"
	"embed"
	"encoding/json"
	"io/fs"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	csarhcpv1alpha1 "github.com/openshift-online/ocm-api-model/clientapi/arohcp/v1alpha1"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/test-integration/utils/integrationutils"
)

//go:embed artifacts/*
var artifacts embed.FS

func TestFrontendClusterRead(t *testing.T) {
	integrationutils.SkipIfNotSimulationTesting(t)

	ctx := context.Background()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	frontend, testInfo, err := integrationutils.NewFrontendFromTestingEnv(ctx, t)
	require.NoError(t, err)
	defer testInfo.Cleanup(context.Background())

	go frontend.Run(ctx, ctx.Done())

	subscriptionID := "0465bc32-c654-41b8-8d87-9815d7abe8f6" // TODO could read from JSON
	err = testInfo.CreateInitialCosmosContent(ctx, api.Must(fs.Sub(artifacts, "artifacts/ClusterReadOldData/initial-cosmos-state")))
	require.NoError(t, err)

	clusterServiceCluster, err := csarhcpv1alpha1.UnmarshalCluster(api.Must(artifacts.ReadFile("artifacts/ClusterReadOldData/initial-cluster-service-state/02-some-cluster.json")))
	require.NoError(t, err)
	testInfo.MockClusterServiceClient.EXPECT().GetCluster(gomock.Any(), api.Must(api.NewInternalID("/api/aro_hcp/v1alpha1/clusters/fixed-value"))).Return(clusterServiceCluster, nil).AnyTimes()
	testInfo.MockClusterServiceClient.EXPECT().DeleteCluster(gomock.Any(), api.Must(api.NewInternalID("/api/aro_hcp/v1alpha1/clusters/fixed-value"))).Return(nil)

	resourceGroup := "some-resource-group"
	hcpClusterName := "some-hcp-cluster"
	hcpCluster, err := testInfo.Get20240610ClientFactory(subscriptionID).NewHcpOpenShiftClustersClient().Get(ctx, resourceGroup, hcpClusterName, nil)
	require.NoError(t, err)

	actualJSON, err := json.MarshalIndent(hcpCluster, "", "    ")
	require.NoError(t, err)

	actualMap := map[string]any{}
	require.NoError(t, json.Unmarshal(actualJSON, &actualMap))
	expectedMap := map[string]any{}
	require.NoError(t, json.Unmarshal(api.Must(artifacts.ReadFile("artifacts/ClusterReadOldData/some-hcp-cluster--expected.json")), &expectedMap))
	require.Equal(t, expectedMap, actualMap)

	_, err = testInfo.Get20240610ClientFactory(subscriptionID).NewHcpOpenShiftClustersClient().BeginDelete(ctx, resourceGroup, hcpClusterName, nil)
	require.NoError(t, err)
	// the poller will never be done because we aren't running the backend.  Just let it be.
}
