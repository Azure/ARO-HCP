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
	"io/fs"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

	"github.com/Azure/ARO-HCP/frontend/test/simulate/databasemutationhelpers"
	"github.com/Azure/ARO-HCP/internal/api"
)

func TestDatabaseCRUD(t *testing.T) {
	SkipIfNotSimulationTesting(t)

	ctx := context.Background()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	_, testInfo, err := NewFrontendFromTestingEnv(ctx, t)
	require.NoError(t, err)
	defer testInfo.Cleanup(context.Background())

	allCRUDDirFS, err := fs.Sub(artifacts, "artifacts/DatabaseCRUD")
	require.NoError(t, err)

	crudSuiteDirs := api.Must(fs.ReadDir(allCRUDDirFS, "."))
	for _, crudSuiteDirEntry := range crudSuiteDirs {
		crudSuiteDir := api.Must(fs.Sub(allCRUDDirFS, crudSuiteDirEntry.Name()))
		switch crudSuiteDirEntry.Name() {
		case "ControllerCRUD":
			t.Run(crudSuiteDirEntry.Name(), func(t *testing.T) {
				testCRUDSuite(
					ctx,
					t,
					databasemutationhelpers.ControllerCRUDSpecializer{},
					testInfo.CosmosResourcesContainer(),
					crudSuiteDir)
			})

		case "OperationCRUD":
			t.Run(crudSuiteDirEntry.Name(), func(t *testing.T) {
				testCRUDSuite(
					ctx,
					t,
					databasemutationhelpers.OperationCRUDSpecializer{},
					testInfo.CosmosResourcesContainer(),
					crudSuiteDir)
			})

		default:
			t.Fatalf("unknown crud suite dir: %s", crudSuiteDirEntry.Name())
		}
	}
}

func testCRUDSuite[InternalAPIType any](ctx context.Context, t *testing.T, specializer databasemutationhelpers.ResourceCRUDTestSpecializer[InternalAPIType], cosmosContainer *azcosmos.ContainerClient, crudSuiteDir fs.FS) {
	testDirs := api.Must(fs.ReadDir(crudSuiteDir, "."))
	for _, testDirEntry := range testDirs {
		testDir := api.Must(fs.Sub(crudSuiteDir, testDirEntry.Name()))

		currTest, err := databasemutationhelpers.NewResourceMutationTest(
			ctx,
			specializer,
			cosmosContainer,
			testDirEntry.Name(),
			testDir,
		)
		require.NoError(t, err)

		t.Run(testDirEntry.Name(), currTest.RunTest)
	}
}
