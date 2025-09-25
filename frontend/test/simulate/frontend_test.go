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

package simulate

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"testing"

	csarhcpv1alpha1 "github.com/openshift-online/ocm-api-model/clientapi/arohcp/v1alpha1"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/ocm"
)

func TestFrontendClusterRead(t *testing.T) {
	if os.Getenv("FRONTEND_SIMULATION_TESTING") != "true" {
		t.Skip("Skipping test")
	}
	ctx := context.Background()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	frontend, testInfo, err := NewFrontendFromTestingEnv(ctx, t)
	require.NoError(t, err)
	defer testInfo.Cleanup(context.Background())

	go frontend.Run(ctx, ctx.Done())

	err = testInfo.CreateInitialCosmosContent(ctx, api.Must(fs.Sub(artifacts, "artifacts/ClusterReadOldData/initial-cosmos-state")))
	require.NoError(t, err)

	clusterServiceCluster, err := csarhcpv1alpha1.UnmarshalCluster(api.Must(artifacts.ReadFile("artifacts/ClusterReadOldData/initial-cluster-service-state/02-some-cluster.json")))
	require.NoError(t, err)
	testInfo.MockClusterServiceClient.EXPECT().GetCluster(gomock.Any(), api.Must(ocm.NewInternalID("/api/aro_hcp/v1alpha1/clusters/fixed-value"))).Return(clusterServiceCluster, nil)

	subscriptionID := "0465bc32-c654-41b8-8d87-9815d7abe8f6" // TODO could read from JSON
	hcpClientFactory, err := testInfo.Get20240610ClientFactory(subscriptionID)
	require.NoError(t, err)

	resourceGroup := "some-resource-group"
	hcpClusterName := "some-hcp-cluster"
	hcpCluster, err := hcpClientFactory.NewHcpOpenShiftClustersClient().Get(ctx, resourceGroup, hcpClusterName, nil)
	require.NoError(t, err)

	actualJSON, err := json.MarshalIndent(hcpCluster, "", "    ")
	require.NoError(t, err)

	actualMap := map[string]any{}
	require.NoError(t, json.Unmarshal(actualJSON, &actualMap))
	expectedMap := map[string]any{}
	require.NoError(t, json.Unmarshal(api.Must(artifacts.ReadFile("artifacts/ClusterReadOldData/some-hcp-cluster--expected.json")), &expectedMap))

	require.Equal(t, expectedMap, actualMap)
}
