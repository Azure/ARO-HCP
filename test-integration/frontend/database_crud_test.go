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

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/test-integration/utils/databasemutationhelpers"
	"github.com/Azure/ARO-HCP/test-integration/utils/integrationutils"
)

func TestDatabaseCRUD(t *testing.T) {
	defer integrationutils.VerifyNoNewGoLeaks(t)
	integrationutils.WithAndWithoutCosmos(t, testDatabaseCRUD)
}

func testDatabaseCRUD(t *testing.T, withMock bool) {
	ctx := context.Background()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	allCRUDDirFS, err := fs.Sub(artifacts, "artifacts/DatabaseCRUD")
	require.NoError(t, err)

	crudSuiteDirs := metadataapi.Must(fs.ReadDir(allCRUDDirFS, "."))
	for _, crudSuiteDirEntry := range crudSuiteDirs {
		crudSuiteDir := metadataapi.Must(fs.Sub(allCRUDDirFS, crudSuiteDirEntry.Name()))
		switch crudSuiteDirEntry.Name() {
		case "ControllerCRUD":
			t.Run(crudSuiteDirEntry.Name(), func(t *testing.T) {
				testCRUDSuite[coreapi.Controller, *coreapi.Controller](
					ctx,
					t,
					crudSuiteDir,
					withMock)

			})

		case "OperationCRUD":
			t.Run(crudSuiteDirEntry.Name(), func(t *testing.T) {
				testCRUDSuite[coreapi.Operation, *coreapi.Operation](
					ctx,
					t,
					crudSuiteDir,
					withMock)

			})

		case "SubscriptionCRUD":
			t.Run(crudSuiteDirEntry.Name(), func(t *testing.T) {
				testCRUDSuite[coreapi.Subscription, *coreapi.Subscription](
					ctx,
					t,
					crudSuiteDir,
					withMock)

			})

		case "ServiceProviderClusterCRUD":
			t.Run(crudSuiteDirEntry.Name(), func(t *testing.T) {
				testCRUDSuite[coreapi.ServiceProviderCluster, *coreapi.ServiceProviderCluster](
					ctx,
					t,
					crudSuiteDir,
					withMock)
			})

		case "UntypedCRUD":
			t.Run(crudSuiteDirEntry.Name(), func(t *testing.T) {
				testUntypedCRUDSuite(
					ctx,
					t,
					crudSuiteDir,
					withMock)
			})

		case "ServiceProviderNodePoolCRUD":
			t.Run(crudSuiteDirEntry.Name(), func(t *testing.T) {
				testCRUDSuite[coreapi.ServiceProviderNodePool, *coreapi.ServiceProviderNodePool](
					ctx,
					t,
					crudSuiteDir,
					withMock)
			})

		default:
			t.Fatalf("unknown crud suite dir: %s", crudSuiteDirEntry.Name())
		}
	}
}

func testCRUDSuite[InternalAPIType any, InternalAPITypePointer coreapi.CosmosMetadataAccessorPtr[InternalAPIType]](ctx context.Context, t *testing.T, crudSuiteDir fs.FS, withMock bool) {
	testDirs := metadataapi.Must(fs.ReadDir(crudSuiteDir, "."))
	for _, testDirEntry := range testDirs {
		testDir := metadataapi.Must(fs.Sub(crudSuiteDir, testDirEntry.Name()))

		currTest, err := databasemutationhelpers.NewResourceMutationTest[InternalAPIType, InternalAPITypePointer](
			ctx,
			testDirEntry.Name(),
			testDir,
			withMock,
		)
		require.NoError(t, err)

		t.Run(testDirEntry.Name(), currTest.RunTest)
	}
}

// testUntypedCRUDSuite mirrors testCRUDSuite for the UntypedCRUD test suite which
// operates on raw TypedDocument values that don't implement CosmosMetadataAccessor.
// All actual steps in this suite are untyped-* variants that do not require a typed
// CRUD client, so we instantiate with cosmosstorageutils.TypedDocument and skip the constraint.
func testUntypedCRUDSuite(ctx context.Context, t *testing.T, crudSuiteDir fs.FS, withMock bool) {
	testDirs := metadataapi.Must(fs.ReadDir(crudSuiteDir, "."))
	for _, testDirEntry := range testDirs {
		testDir := metadataapi.Must(fs.Sub(crudSuiteDir, testDirEntry.Name()))

		currTest, err := databasemutationhelpers.NewUntypedResourceMutationTest(
			ctx,
			testDirEntry.Name(),
			testDir,
			withMock,
		)
		require.NoError(t, err)

		t.Run(testDirEntry.Name(), currTest.RunTest)
	}
}
