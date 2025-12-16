package controllertesthelpers

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path"
	"testing"

	"github.com/Azure/ARO-HCP/backend/controllers"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/utils"
	"github.com/Azure/ARO-HCP/test-integration/utils/databasemutationhelpers"
	"github.com/Azure/ARO-HCP/test-integration/utils/integrationutils"
	"github.com/stretchr/testify/require"
)

type ControllerInitializerFunc func(ctx context.Context, t *testing.T, cosmosClient database.DBClient) (controller controllers.Controller, testMemory map[string]any)

type ControllerVerifierFunc func(ctx context.Context, t *testing.T, controller controllers.Controller, testMemory map[string]any)

type BasicControllerTest struct {
	Name          string
	ControllerKey controllers.HCPClusterKey
	ArtifactDir   fs.FS

	ControllerInitializerFn ControllerInitializerFunc
	ControllerVerifierFn    ControllerVerifierFunc
}

func (tc *BasicControllerTest) RunTest(t *testing.T) {
	integrationutils.SkipIfNotSimulationTesting(t)

	ctx := context.Background()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// this forces every test to have its own directory and a couple permanent fixtures
	testDir, err := fs.Sub(tc.ArtifactDir, tc.Name)
	require.NoError(t, err)

	logger := utils.LoggerFromContext(ctx)
	logger = tc.ControllerKey.AddLoggerValues(logger)
	ctx = utils.ContextWithLogger(ctx, logger)

	cosmosTestInfo, err := integrationutils.NewCosmosFromTestingEnv(ctx)
	require.NoError(t, err)
	defer cosmosTestInfo.Cleanup(context.Background())

	initialState, err := fs.Sub(testDir, path.Join("00-load-initial-state"))
	require.NoError(t, err)
	if fsMightContainFiles(initialState) {
		loadInitialStateStep, err := databasemutationhelpers.NewLoadStep(
			databasemutationhelpers.NewStepID(00, "load", "initial-state"),
			cosmosTestInfo.CosmosResourcesContainer(),
			initialState,
		)
		require.NoError(t, err)
		loadInitialStateStep.RunTest(ctx, t)
	}

	controllerInstance, testMemory := tc.ControllerInitializerFn(ctx, t, cosmosTestInfo.DBClient)
	err = controllerInstance.SyncOnce(ctx, tc.ControllerKey)
	require.NoError(t, err)

	endState, err := fs.Sub(testDir, path.Join("99-cosmosCompare-end-state"))
	require.NoError(t, err)
	if fsMightContainFiles(endState) {
		verifyEndStateStep, err := databasemutationhelpers.NewCosmosCompareStep(
			databasemutationhelpers.NewStepID(99, "cosmosCompare", "end-state"),
			cosmosTestInfo.CosmosResourcesContainer(),
			endState,
		)
		require.NoError(t, err)
		verifyEndStateStep.RunTest(ctx, t)
	}

	tc.ControllerVerifierFn(ctx, t, controllerInstance, testMemory)

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
