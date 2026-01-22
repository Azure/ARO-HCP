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
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"k8s.io/utils/ptr"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/test-integration/utils/integrationutils"
	hcpsdk20240610preview "github.com/Azure/ARO-HCP/test/sdk/v20240610preview/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
)

func TestFrontendExternalAuthMutation(t *testing.T) {
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
	err = testInfo.CreateInitialCosmosContent(ctx, api.Must(fs.Sub(artifacts, "artifacts/ExternalAuthMutation/initial-cosmos-state")))
	require.NoError(t, err)

	// create anything and round trip anything for nodePool-service
	// this happens here because the mock is associated with frontend. it's a little awkward to add instances, but we'll deal
	err = testInfo.AddContent(t, api.Must(fs.Sub(artifacts, "artifacts/ExternalAuthMutation/initial-cluster-service-state")))
	require.NoError(t, err)

	dirContent := api.Must(artifacts.ReadDir("artifacts/ExternalAuthMutation"))
	for _, dirEntry := range dirContent {
		if dirEntry.Name() == "initial-cosmos-state" || dirEntry.Name() == "initial-cluster-service-state" {
			continue
		}
		createTestDir, err := fs.Sub(artifacts, "artifacts/ExternalAuthMutation/"+dirEntry.Name())
		require.NoError(t, err)
		currTest, err := newExternalAuthMutationTest(ctx, createTestDir, testInfo, subscriptionID, resourceGroupName)
		require.NoError(t, err)
		t.Run(dirEntry.Name(), currTest.runTest)
	}
}

type externalAuthMutationTest struct {
	ctx               context.Context
	testDir           fs.FS
	testInfo          *integrationutils.FrontendIntegrationTestInfo
	subscriptionID    string
	resourceGroupName string

	genericMutationTestInfo *integrationutils.GenericMutationTest
}

func newExternalAuthMutationTest(ctx context.Context, testDir fs.FS, testInfo *integrationutils.FrontendIntegrationTestInfo, subscriptionID, resourceGroupName string) (*externalAuthMutationTest, error) {
	genericMutationTestInfo, err := integrationutils.ReadGenericMutationTest(testDir)
	if err != nil {
		return nil, err
	}

	return &externalAuthMutationTest{
		ctx:                     ctx,
		testDir:                 testDir,
		testInfo:                testInfo,
		subscriptionID:          subscriptionID,
		resourceGroupName:       resourceGroupName,
		genericMutationTestInfo: genericMutationTestInfo,
	}, nil
}

func (tt *externalAuthMutationTest) runTest(t *testing.T) {
	ctx := tt.ctx

	require.NoError(t, tt.genericMutationTestInfo.Initialize(ctx, tt.testInfo))

	// better solutions welcome to be coded. This is simple and works for the moment.
	hcpClusterName := strings.Split(t.Name(), "/")[1]
	toCreate := &hcpsdk20240610preview.ExternalAuth{}
	require.NoError(t, json.Unmarshal(tt.genericMutationTestInfo.CreateJSON, toCreate))
	externalAuthClient := tt.testInfo.Get20240610ClientFactory(tt.subscriptionID).NewExternalAuthsClient()
	_, mutationErr := externalAuthClient.BeginCreateOrUpdate(ctx, tt.resourceGroupName, hcpClusterName, *toCreate.Name, *toCreate, nil)

	if tt.genericMutationTestInfo.IsUpdateTest() || tt.genericMutationTestInfo.IsPatchTest() {
		require.NoError(t, mutationErr)
		require.NoError(t, integrationutils.MarkOperationsCompleteForName(ctx, tt.testInfo.DBClient, tt.subscriptionID, ptr.Deref(toCreate.Name, "")))
	}

	switch {
	case tt.genericMutationTestInfo.IsUpdateTest():
		toUpdate := &hcpsdk20240610preview.ExternalAuth{}
		require.NoError(t, json.Unmarshal(tt.genericMutationTestInfo.UpdateJSON, toUpdate))
		_, mutationErr = externalAuthClient.BeginCreateOrUpdate(ctx, tt.resourceGroupName, hcpClusterName, *toUpdate.Name, *toUpdate, nil)

	case tt.genericMutationTestInfo.IsPatchTest():
		toPatch := &hcpsdk20240610preview.ExternalAuthUpdate{}
		require.NoError(t, json.Unmarshal(tt.genericMutationTestInfo.PatchJSON, toPatch))
		_, mutationErr = externalAuthClient.BeginUpdate(ctx, tt.resourceGroupName, hcpClusterName, *toCreate.Name, *toPatch, nil)
	}

	tt.genericMutationTestInfo.VerifyActualError(t, mutationErr)
	if !tt.genericMutationTestInfo.ExpectsResult() {
		return
	}

	// polling the result will never complete because we aren't actually working on the operation.  We want to do a GET to see
	// if the data we read back matches what we expect.
	actualCreated, err := externalAuthClient.Get(ctx, tt.resourceGroupName, hcpClusterName, *toCreate.Name, nil)
	require.NoError(t, err)
	tt.genericMutationTestInfo.VerifyActualResult(t, actualCreated)

	currExternalAuthFromList := &hcpsdk20240610preview.ExternalAuth{}
	externalAuthPager := externalAuthClient.NewListByParentPager(tt.resourceGroupName, hcpClusterName, nil)
	for externalAuthPager.More() {
		page, err := externalAuthPager.NextPage(ctx)
		require.NoError(t, err)
		for _, externalAuth := range page.Value {
			t.Logf("Found cluster %q", ptr.Deref(externalAuth.Name, ""))

			if ptr.Deref(externalAuth.ID, "sub.ID") == ptr.Deref(actualCreated.ID, "actualCreated.ID") {
				obj := *externalAuth
				currExternalAuthFromList = &obj
			}
		}
	}
	require.NotNil(t, currExternalAuthFromList)
	require.Equal(t, actualCreated.ExternalAuth, *currExternalAuthFromList)
}
