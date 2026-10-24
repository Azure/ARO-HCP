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

	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/test-integration/utils/databasemutationhelpers"
	"github.com/Azure/ARO-HCP/test-integration/utils/integrationutils"
)

func TestFrontendCRUD(t *testing.T) {
	defer integrationutils.VerifyNoNewGoLeaks(t)
	integrationutils.WithAndWithoutCosmos(t, testFrontendCRUD)
}

func testFrontendCRUD(t *testing.T, withMock bool) {
	ctx := context.Background()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	allCRUDDirFS, err := fs.Sub(artifacts, "artifacts/FrontendCRUD")
	require.NoError(t, err)

	crudSuiteDirs := metadataapi.Must(fs.ReadDir(allCRUDDirFS, "."))
	for _, crudSuiteDirEntry := range crudSuiteDirs {
		crudSuiteDir := metadataapi.Must(fs.Sub(allCRUDDirFS, crudSuiteDirEntry.Name()))
		t.Run(crudSuiteDirEntry.Name(), func(t *testing.T) {
			if crudSuiteDirEntry.Name() == "OpenShiftVersions" {
				for _, apiVersion := range integrationutils.AllAPIVersions() {
					t.Run(apiVersion, func(t *testing.T) {
						for _, testDir := range metadataapi.Must(fs.ReadDir(crudSuiteDir, ".")) {
							fixture := metadataapi.Must(fs.Sub(crudSuiteDir, testDir.Name()))
							currTest, err := databasemutationhelpers.NewUntypedResourceMutationTest(ctx, testDir.Name(), fixture, withMock)
							require.NoError(t, err)
							currTest.APIVersion = apiVersion
							t.Run(testDir.Name(), currTest.RunTest)
						}
					})
				}
				return
			}
			testUntypedCRUDSuite(
				ctx,
				t,
				crudSuiteDir,
				withMock)
		})
	}
}
