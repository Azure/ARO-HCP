// Copyright 2026 Microsoft Corporation
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

package databasemutationhelpers

import (
	"context"
	"io/fs"
	"testing"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
)

// migrateCosmosStep invokes the backend cosmos-migration logic once across
// every subscription currently in the test's Resources container. Replaces
// the frontend's startup migration (which was removed when the migration
// moved into the long-running backend controller); the integration test
// still needs a one-shot trigger so it can assert on post-migration state.
type migrateCosmosStep struct {
	stepID StepID
}

func newMigrateCosmosStep(stepID StepID, _ fs.FS) (*migrateCosmosStep, error) {
	return &migrateCosmosStep{stepID: stepID}, nil
}

var _ IntegrationTestStep = &migrateCosmosStep{}

func (l *migrateCosmosStep) StepID() StepID {
	return l.stepID
}

func (l *migrateCosmosStep) RunTest(ctx context.Context, t *testing.T, stepInput StepInput) {
	// Frontend integration tests do not run kube-applier, so an empty
	// MockKubeApplierDBClients is enough: its For() returns nil and the
	// migration code already treats that as "skip kube-applier desires."
	controllers.MigrateAllSubscriptionsOrDie(ctx, stepInput.ResourcesDBClient, databasetesting.NewMockKubeApplierDBClients())
}
