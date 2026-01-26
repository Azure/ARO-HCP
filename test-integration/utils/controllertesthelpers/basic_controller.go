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

package controllertesthelpers

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path"
	"testing"

	"github.com/neilotoole/slogt"
	"github.com/stretchr/testify/require"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
	"github.com/Azure/ARO-HCP/test-integration/backend/livelisters"
	"github.com/Azure/ARO-HCP/test-integration/utils/databasemutationhelpers"
	"github.com/Azure/ARO-HCP/test-integration/utils/integrationutils"
)

type ControllerInitializationInput struct {
	CosmosClient         database.DBClient
	SubscriptionLister   listers.SubscriptionLister
	ClusterServiceClient ocm.ClusterServiceClientSpec
}

type ControllerInitializerFunc func(ctx context.Context, t *testing.T, input *ControllerInitializationInput) (controller controllerutils.Controller, testMemory map[string]any)

type ControllerVerifierFunc func(ctx context.Context, t *testing.T, controller controllerutils.Controller, testMemory map[string]any, input *ControllerInitializationInput)

type BasicControllerTest struct {
	Name          string
	ControllerKey controllerutils.HCPClusterKey
	ArtifactDir   fs.FS

	ControllerInitializerFn ControllerInitializerFunc
	ControllerVerifierFn    ControllerVerifierFunc
	WithMock                bool
}

func (tc *BasicControllerTest) RunTest(t *testing.T) {
	ctx := context.Background()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// this forces every test to have its own directory and a couple permanent fixtures
	testDir, err := fs.Sub(tc.ArtifactDir, tc.Name)
	require.NoError(t, err)

	ctx = utils.ContextWithLogger(ctx, slogt.New(t, slogt.JSON()))
	logger := utils.LoggerFromContext(ctx)
	logger = tc.ControllerKey.AddLoggerValues(logger)
	ctx = utils.ContextWithLogger(ctx, logger)

	var storageIntegrationTestInfo integrationutils.StorageIntegrationTestInfo
	if tc.WithMock {
		storageIntegrationTestInfo, err = integrationutils.NewMockCosmosFromTestingEnv(ctx, t)
	} else {
		storageIntegrationTestInfo, err = integrationutils.NewCosmosFromTestingEnv(ctx, t)
	}
	require.NoError(t, err)
	require.NoError(t, err)
	defer storageIntegrationTestInfo.Cleanup(utils.ContextWithLogger(context.Background(), slogt.New(t, slogt.JSON())))
	clusterServiceMockInfo := integrationutils.NewClusterServiceMock(t, storageIntegrationTestInfo.GetArtifactDir())
	defer clusterServiceMockInfo.Cleanup(utils.ContextWithLogger(context.Background(), slogt.New(t, slogt.JSON())))
	stepInput := databasemutationhelpers.NewCosmosStepInput(storageIntegrationTestInfo)
	stepInput.ClusterServiceMockInfo = clusterServiceMockInfo

	initialCosmosState, err := fs.Sub(testDir, path.Join("00-load-initial-state"))
	require.NoError(t, err)
	if fsMightContainFiles(initialCosmosState) {
		loadInitialStateStep, err := databasemutationhelpers.NewLoadCosmosStep(
			databasemutationhelpers.NewStepID(00, "load", "initial-state"),
			initialCosmosState,
		)
		require.NoError(t, err)
		loadInitialStateStep.RunTest(ctx, t, *stepInput)
	}

	initialClusterServiceState, err := fs.Sub(testDir, path.Join("00-loadClusterService-initial_state"))
	require.NoError(t, err)
	if fsMightContainFiles(initialClusterServiceState) {
		loadInitialStateStep, err := databasemutationhelpers.NewLoadClusterServiceStep(
			databasemutationhelpers.NewStepID(00, "loadClusterService", "initial-state"),
			initialClusterServiceState,
		)
		require.NoError(t, err)
		loadInitialStateStep.RunTest(ctx, t, *stepInput)
	}

	controllerInput := &ControllerInitializationInput{
		CosmosClient:         storageIntegrationTestInfo.CosmosClient(),
		SubscriptionLister:   livelisters.NewSubscriptionLiveLister(storageIntegrationTestInfo.CosmosClient()),
		ClusterServiceClient: clusterServiceMockInfo.MockClusterServiceClient,
	}

	controllerInstance, testMemory := tc.ControllerInitializerFn(ctx, t, controllerInput)
	err = controllerInstance.SyncOnce(ctx, tc.ControllerKey)
	require.NoError(t, err)

	endState, err := fs.Sub(testDir, path.Join("99-cosmosCompare-end-state"))
	require.NoError(t, err)
	if fsMightContainFiles(endState) {
		verifyEndStateStep, err := databasemutationhelpers.NewCosmosCompareStep(
			databasemutationhelpers.NewStepID(99, "cosmosCompare", "end-state"),
			endState,
		)
		require.NoError(t, err)
		verifyEndStateStep.RunTest(ctx, t, *stepInput)
	}

	tc.ControllerVerifierFn(ctx, t, controllerInstance, testMemory, controllerInput)

}

func fsMightContainFiles(dir fs.FS) bool {
	if _, err := fs.ReadDir(dir, "."); err == nil {
		return true
	} else if errors.Is(err, os.ErrNotExist) {
		return false
	} else {
		return true
	}
}
