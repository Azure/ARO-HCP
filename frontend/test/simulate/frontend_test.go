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
	"fmt"
	"io/fs"
	"os"
	"strings"
	"testing"

	hcpapi20240610 "github.com/Azure/ARO-HCP/internal/api/v20240610preview/generated"
	csarhcpv1alpha1 "github.com/openshift-online/ocm-api-model/clientapi/arohcp/v1alpha1"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"k8s.io/apimachinery/pkg/util/rand"

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

	subscriptionID := "0465bc32-c654-41b8-8d87-9815d7abe8f6" // TODO could read from JSON
	err = testInfo.CreateInitialCosmosContent(ctx, api.Must(fs.Sub(artifacts, "artifacts/ClusterReadOldData/initial-cosmos-state")))
	require.NoError(t, err)

	clusterServiceCluster, err := csarhcpv1alpha1.UnmarshalCluster(api.Must(artifacts.ReadFile("artifacts/ClusterReadOldData/initial-cluster-service-state/02-some-cluster.json")))
	require.NoError(t, err)
	testInfo.MockClusterServiceClient.EXPECT().GetCluster(gomock.Any(), api.Must(ocm.NewInternalID("/api/aro_hcp/v1alpha1/clusters/fixed-value"))).Return(clusterServiceCluster, nil)

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
}

func TestFrontendClusterCreate(t *testing.T) {
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

	subscriptionID := "0465bc32-c654-41b8-8d87-9815d7abe8f6" // TODO could read from JSON
	resourceGroupName := "some-resource-group"
	err = testInfo.CreateInitialCosmosContent(ctx, api.Must(fs.Sub(artifacts, "artifacts/ClusterCreateData/initial-cosmos-state")))
	require.NoError(t, err)

	// create anything and round trip anything for cluster-service
	internalIDToCluster := map[string]*csarhcpv1alpha1.Cluster{}
	testInfo.MockClusterServiceClient.EXPECT().PostCluster(gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, builder *csarhcpv1alpha1.ClusterBuilder) (*csarhcpv1alpha1.Cluster, error) {
		internalID := "/api/clusters_mgmt/v1/clusters/" + rand.String(10)
		builder = builder.HREF(internalID)
		ret, err := builder.Build()
		if err != nil {
			return nil, err
		}

		internalIDToCluster[internalID] = ret
		return ret, nil
	})
	testInfo.MockClusterServiceClient.EXPECT().GetCluster(gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, id ocm.InternalID) (*csarhcpv1alpha1.Cluster, error) {
		ret := internalIDToCluster[id.String()]
		if ret != nil {
			return ret, nil
		}
		return nil, fmt.Errorf("not found: %q", id.String())
	})

	dirContent := api.Must(artifacts.ReadDir("artifacts/ClusterCreateData"))
	for _, dirEntry := range dirContent {
		if dirEntry.Name() == "initial-cosmos-state" {
			continue
		}
		createTestDir, err := fs.Sub(artifacts, "artifacts/ClusterCreateData/"+dirEntry.Name())
		require.NoError(t, err)
		currTest := newClusterCreateTest(ctx, createTestDir, testInfo, subscriptionID, resourceGroupName)
		t.Run(dirEntry.Name(), currTest.runTest)
	}
}

type clusterCreateTest struct {
	ctx               context.Context
	testDir           fs.FS
	testInfo          *SimulationTestInfo
	subscriptionID    string
	resourceGroupName string
}

func newClusterCreateTest(ctx context.Context, testDir fs.FS, testInfo *SimulationTestInfo, subscriptionID, resourceGroupName string) *clusterCreateTest {
	return &clusterCreateTest{
		ctx:               ctx,
		testDir:           testDir,
		testInfo:          testInfo,
		subscriptionID:    subscriptionID,
		resourceGroupName: resourceGroupName,
	}
}

func (tt *clusterCreateTest) runTest(t *testing.T) {
	ctx := tt.ctx

	var err error
	expectedErrors := []string{}
	expectedJSON := []byte{}

	expectedJSON, err = fs.ReadFile(tt.testDir, "expected.json")
	switch {
	case os.IsNotExist(err):
		expectedErrorBytes, err := fs.ReadFile(tt.testDir, "expected-error.txt")
		require.NoError(t, err)
		expectedErrors = strings.Split(string(expectedErrorBytes), "\n")

	case err != nil:
		t.Fatal(err)
	}

	toCreate := &hcpapi20240610.HcpOpenShiftCluster{}
	require.NoError(t, json.Unmarshal(api.Must(fs.ReadFile(tt.testDir, "create.json")), toCreate))

	hcpClient := tt.testInfo.Get20240610ClientFactory(tt.subscriptionID).NewHcpOpenShiftClustersClient()
	_, err = hcpClient.BeginCreateOrUpdate(ctx, tt.resourceGroupName, *toCreate.Name, *toCreate, nil)
	if len(expectedErrors) > 0 {
		require.Error(t, err)
		for _, expectedError := range expectedErrors {
			require.Contains(t, err.Error(), expectedError)
		}
		return
	}
	require.NoError(t, err)

	// polling the result will never complete because we aren't actually working on the operation.  We want to do a GET to see
	// if the data we read back matches what we expect.
	actualCreated, err := hcpClient.Get(ctx, tt.resourceGroupName, *toCreate.Name, nil)
	require.NoError(t, err)

	actualJSON, err := json.MarshalIndent(actualCreated, "", "    ")
	require.NoError(t, err)
	actualMap := map[string]any{}
	require.NoError(t, json.Unmarshal(actualJSON, &actualMap))
	expectedMap := map[string]any{}
	require.NoError(t, json.Unmarshal(expectedJSON, &expectedMap))

	require.Equal(t, expectedMap, actualMap)
}
