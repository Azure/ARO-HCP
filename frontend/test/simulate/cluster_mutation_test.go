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
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"
	"github.com/stretchr/testify/require"
	"k8s.io/utils/ptr"

	hcpsdk20240610preview "github.com/Azure/ARO-HCP/test/sdk/v20240610preview/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
)

func TestFrontendClusterMutation(t *testing.T) {
	SkipIfNotSimulationTesting(t)

	ctx := context.Background()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	frontend, testInfo, err := NewFrontendFromTestingEnv(ctx, t)
	require.NoError(t, err)
	defer testInfo.Cleanup(context.Background())

	go frontend.Run(ctx, ctx.Done())

	subscriptionID := "0465bc32-c654-41b8-8d87-9815d7abe8f6" // TODO could read from JSON
	resourceGroupName := "some-resource-group"
	err = testInfo.CreateInitialCosmosContent(ctx, api.Must(fs.Sub(artifacts, "artifacts/ClusterMutation/initial-cosmos-state")))
	require.NoError(t, err)

	// create anything and round trip anything for cluster-service
	trivialPassThroughClusterServiceMock(t, testInfo)

	dirContent := api.Must(artifacts.ReadDir("artifacts/ClusterMutation"))
	for _, dirEntry := range dirContent {
		if dirEntry.Name() == "initial-cosmos-state" {
			continue
		}
		createTestDir, err := fs.Sub(artifacts, "artifacts/ClusterMutation/"+dirEntry.Name())
		require.NoError(t, err)
		currTest, err := newClusterMutationTest(ctx, createTestDir, testInfo, subscriptionID, resourceGroupName)
		require.NoError(t, err)
		t.Run(dirEntry.Name(), currTest.runTest)
	}
}

type clusterMutationTest struct {
	ctx               context.Context
	testDir           fs.FS
	testInfo          *SimulationTestInfo
	subscriptionID    string
	resourceGroupName string

	genericMutationTestInfo *genericMutationTest
}

func newClusterMutationTest(ctx context.Context, testDir fs.FS, testInfo *SimulationTestInfo, subscriptionID, resourceGroupName string) (*clusterMutationTest, error) {
	genericMutationTestInfo, err := readGenericMutationTest(testDir)
	if err != nil {
		return nil, err
	}

	return &clusterMutationTest{
		ctx:                     ctx,
		testDir:                 testDir,
		testInfo:                testInfo,
		subscriptionID:          subscriptionID,
		resourceGroupName:       resourceGroupName,
		genericMutationTestInfo: genericMutationTestInfo,
	}, nil
}

func (tt *clusterMutationTest) runTest(t *testing.T) {
	ctx := tt.ctx

	toCreate := &hcpsdk20240610preview.HcpOpenShiftCluster{}
	require.NoError(t, json.Unmarshal(tt.genericMutationTestInfo.createJSON, toCreate))
	clusterClient := tt.testInfo.Get20240610ClientFactory(tt.subscriptionID).NewHcpOpenShiftClustersClient()
	_, mutationErr := clusterClient.BeginCreateOrUpdate(ctx, tt.resourceGroupName, *toCreate.Name, *toCreate, nil)

	if tt.genericMutationTestInfo.isUpdateTest() {
		require.NoError(t, mutationErr)

		operationsIterator := tt.testInfo.DBClient.ListActiveOperationDocs(azcosmos.NewPartitionKeyString(tt.subscriptionID), nil)
		for _, operation := range operationsIterator.Items(ctx) {
			if operation.ExternalID.Name != ptr.Deref(toCreate.Name, "") {
				continue
			}
			err := tt.testInfo.UpdateClusterOperationStatus(ctx, operation, arm.ProvisioningStateSucceeded, nil)
			require.NoError(t, err)
		}
		require.NoError(t, operationsIterator.GetError())

		toUpdate := &hcpsdk20240610preview.HcpOpenShiftCluster{}
		require.NoError(t, json.Unmarshal(tt.genericMutationTestInfo.updateJSON, toUpdate))
		_, mutationErr = clusterClient.BeginCreateOrUpdate(ctx, tt.resourceGroupName, *toUpdate.Name, *toUpdate, nil)

	}

	tt.genericMutationTestInfo.verifyActualError(t, mutationErr)
	if !tt.genericMutationTestInfo.expectsResult() {
		return
	}

	// polling the result will never complete because we aren't actually working on the operation.  We want to do a GET to see
	// if the data we read back matches what we expect.
	actualCreated, err := clusterClient.Get(ctx, tt.resourceGroupName, *toCreate.Name, nil)
	require.NoError(t, err)
	tt.genericMutationTestInfo.verifyActualResult(t, actualCreated)
}
