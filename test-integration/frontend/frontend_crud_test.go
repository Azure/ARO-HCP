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
	"github.com/Azure/ARO-HCP/test-integration/utils/databasemutationhelpers"
	"github.com/Azure/ARO-HCP/test-integration/utils/integrationutils"
)

func TestFrontendCRUD(t *testing.T) {
	integrationutils.SkipIfNotSimulationTesting(t)

	ctx := context.Background()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	allCRUDDirFS, err := fs.Sub(artifacts, "artifacts/FrontendCRUD")
	require.NoError(t, err)

	crudSuiteDirs := api.Must(fs.ReadDir(allCRUDDirFS, "."))
	for _, crudSuiteDirEntry := range crudSuiteDirs {
		crudSuiteDir := api.Must(fs.Sub(allCRUDDirFS, crudSuiteDirEntry.Name()))
		t.Run(crudSuiteDirEntry.Name(), func(t *testing.T) {
			testCRUDSuite(
				ctx,
				t,
				databasemutationhelpers.NothingCRUDSpecializer{},
				crudSuiteDir)
		})
	}
}
