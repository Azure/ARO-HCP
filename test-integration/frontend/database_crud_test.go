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
	"io/fs"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/test-integration/utils/databasemutationhelpers"
	"github.com/Azure/ARO-HCP/test-integration/utils/integrationutils"
)

func TestDatabaseCRUD(t *testing.T) {
	integrationutils.SkipIfNotSimulationTesting(t)

	ctx := context.Background()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	allCRUDDirFS, err := fs.Sub(artifacts, "artifacts/DatabaseCRUD")
	require.NoError(t, err)

	crudSuiteDirs := api.Must(fs.ReadDir(allCRUDDirFS, "."))
	for _, crudSuiteDirEntry := range crudSuiteDirs {
		crudSuiteDir := api.Must(fs.Sub(allCRUDDirFS, crudSuiteDirEntry.Name()))
		switch crudSuiteDirEntry.Name() {
		case "ControllerCRUD":
			t.Run(crudSuiteDirEntry.Name(), func(t *testing.T) {
				testCRUDSuite[api.Controller](
					ctx,
					t,
					crudSuiteDir)
			})

		case "OperationCRUD":
			t.Run(crudSuiteDirEntry.Name(), func(t *testing.T) {
				testCRUDSuite[api.Operation](
					ctx,
					t,
					crudSuiteDir)
			})

		case "SubscriptionCRUD":
			t.Run(crudSuiteDirEntry.Name(), func(t *testing.T) {
				testCRUDSuite[arm.Subscription](
					ctx,
					t,
					crudSuiteDir)
			})

		case "ServiceProviderClusterCRUD":
			t.Run(crudSuiteDirEntry.Name(), func(t *testing.T) {
				testCRUDSuite[api.ServiceProviderCluster](
					ctx,
					t,
					crudSuiteDir)
			})

		case "UntypedCRUD":
			t.Run(crudSuiteDirEntry.Name(), func(t *testing.T) {
				testCRUDSuite[database.TypedDocument](
					ctx,
					t,
					crudSuiteDir)
			})

		default:
			t.Fatalf("unknown crud suite dir: %s", crudSuiteDirEntry.Name())
		}
	}
}

func testCRUDSuite[InternalAPIType any](ctx context.Context, t *testing.T, crudSuiteDir fs.FS) {
	testDirs := api.Must(fs.ReadDir(crudSuiteDir, "."))
	for _, testDirEntry := range testDirs {
		testDir := api.Must(fs.Sub(crudSuiteDir, testDirEntry.Name()))

		currTest, err := databasemutationhelpers.NewResourceMutationTest[InternalAPIType](
			ctx,
			testDirEntry.Name(),
			testDir,
		)
		require.NoError(t, err)

		t.Run(testDirEntry.Name(), currTest.RunTest)
	}
}
