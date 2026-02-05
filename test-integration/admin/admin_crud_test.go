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

package admin

import (
	"context"
	"embed"
	"io/fs"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/test-integration/utils/databasemutationhelpers"
	"github.com/Azure/ARO-HCP/test-integration/utils/integrationutils"
)

//go:embed artifacts
var artifacts embed.FS

func TestAdminCRUD(t *testing.T) {
	defer integrationutils.VerifyNoNewGoLeaks(t)

	integrationutils.WithAndWithoutCosmos(t, testAdminCRUD)
}

func testAdminCRUD(t *testing.T, withMock bool) {
	ctx := context.Background()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	allCRUDDirFS, err := fs.Sub(artifacts, "artifacts/AdminCRUD")
	require.NoError(t, err)

	crudSuiteDirs := api.Must(fs.ReadDir(allCRUDDirFS, "."))
	for _, crudSuiteDirEntry := range crudSuiteDirs {
		crudSuiteDir := api.Must(fs.Sub(allCRUDDirFS, crudSuiteDirEntry.Name()))
		t.Run(crudSuiteDirEntry.Name(), func(t *testing.T) {
			testAdminCRUDSuite[any](
				ctx,
				t,
				crudSuiteDir,
				withMock,
			)
		})
	}
}

func testAdminCRUDSuite[InternalAPIType any](ctx context.Context, t *testing.T, crudSuiteDir fs.FS, withMock bool) {
	testDirs := api.Must(fs.ReadDir(crudSuiteDir, "."))
	for _, testDirEntry := range testDirs {
		testDir := api.Must(fs.Sub(crudSuiteDir, testDirEntry.Name()))

		currTest, err := databasemutationhelpers.NewResourceMutationTest[InternalAPIType](
			ctx,
			testDirEntry.Name(),
			testDir,
			withMock,
		)
		require.NoError(t, err)

		t.Run(testDirEntry.Name(), currTest.RunTest)
	}
}
