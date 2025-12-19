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
	"encoding/json"
	"io/fs"
	"testing"

	"github.com/stretchr/testify/require"

	"k8s.io/utils/ptr"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/test-integration/utils/integrationutils"
	hcpsdk20240610preview "github.com/Azure/ARO-HCP/test/sdk/v20240610preview/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
)

func TestFrontendClusterMutation(t *testing.T) {
	integrationutils.SkipIfNotSimulationTesting(t)

	ctx := context.Background()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	frontend, testInfo, err := integrationutils.NewFrontendFromTestingEnv(ctx, t)
	require.NoError(t, err)
	defer testInfo.Cleanup(context.Background())

	go frontend.Run(ctx, ctx.Done())

	subscriptionID := "0465bc32-c654-41b8-8d87-9815d7abe8f6" // TODO could read from JSON
	resourceGroupName := "some-resource-group"
	err = testInfo.CreateInitialCosmosContent(ctx, api.Must(fs.Sub(artifacts, "artifacts/ClusterMutation/initial-cosmos-state")))
	require.NoError(t, err)

	// create anything and round trip anything for cluster-service
	err = integrationutils.TrivialPassThroughClusterServiceMock(t, testInfo, nil)
	require.NoError(t, err)

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
	testInfo          *integrationutils.FrontendIntegrationTestInfo
	subscriptionID    string
	resourceGroupName string

	genericMutationTestInfo *integrationutils.GenericMutationTest
}

func newClusterMutationTest(ctx context.Context, testDir fs.FS, testInfo *integrationutils.FrontendIntegrationTestInfo, subscriptionID, resourceGroupName string) (*clusterMutationTest, error) {
	genericMutationTestInfo, err := integrationutils.ReadGenericMutationTest(testDir)
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

	require.NoError(t, tt.genericMutationTestInfo.Initialize(ctx, tt.testInfo))

	toCreate := &hcpsdk20240610preview.HcpOpenShiftCluster{}
	require.NoError(t, json.Unmarshal(tt.genericMutationTestInfo.CreateJSON, toCreate))
	clusterClient := tt.testInfo.Get20240610ClientFactory(tt.subscriptionID).NewHcpOpenShiftClustersClient()
	_, mutationErr := clusterClient.BeginCreateOrUpdate(ctx, tt.resourceGroupName, *toCreate.Name, *toCreate, nil)

	if tt.genericMutationTestInfo.IsUpdateTest() || tt.genericMutationTestInfo.IsPatchTest() {
		require.NoError(t, mutationErr)
		require.NoError(t, integrationutils.MarkOperationsCompleteForName(ctx, tt.testInfo.DBClient, tt.subscriptionID, ptr.Deref(toCreate.Name, "")))
	}

	switch {
	case tt.genericMutationTestInfo.IsUpdateTest():
		toUpdate := &hcpsdk20240610preview.HcpOpenShiftCluster{}
		require.NoError(t, json.Unmarshal(tt.genericMutationTestInfo.UpdateJSON, toUpdate))
		_, mutationErr = clusterClient.BeginCreateOrUpdate(ctx, tt.resourceGroupName, *toUpdate.Name, *toUpdate, nil)

	case tt.genericMutationTestInfo.IsPatchTest():
		toPatch := &hcpsdk20240610preview.HcpOpenShiftClusterUpdate{}
		require.NoError(t, json.Unmarshal(tt.genericMutationTestInfo.PatchJSON, toPatch))
		_, mutationErr = clusterClient.BeginUpdate(ctx, tt.resourceGroupName, *toCreate.Name, *toPatch, nil)

	}

	tt.genericMutationTestInfo.VerifyActualError(t, mutationErr)
	if !tt.genericMutationTestInfo.ExpectsResult() {
		return
	}

	// polling the result will never complete because we aren't actually working on the operation.  We want to do a GET to see
	// if the data we read back matches what we expect.
	actualCreated, err := clusterClient.Get(ctx, tt.resourceGroupName, *toCreate.Name, nil)
	require.NoError(t, err)
	tt.genericMutationTestInfo.VerifyActualResult(t, actualCreated)

	currClusterFromList := &hcpsdk20240610preview.HcpOpenShiftCluster{}
	clusterPager := clusterClient.NewListByResourceGroupPager(tt.resourceGroupName, nil)
	for clusterPager.More() {
		page, err := clusterPager.NextPage(ctx)
		require.NoError(t, err)
		for _, cluster := range page.Value {
			t.Logf("Found cluster %q", ptr.Deref(cluster.Name, ""))

			if ptr.Deref(cluster.ID, "sub.ID") == ptr.Deref(actualCreated.ID, "actualCreated.ID") {
				obj := *cluster
				currClusterFromList = &obj
			}
		}
	}
	require.NotNil(t, currClusterFromList)
	require.Equal(t, actualCreated.HcpOpenShiftCluster, *currClusterFromList)
}
