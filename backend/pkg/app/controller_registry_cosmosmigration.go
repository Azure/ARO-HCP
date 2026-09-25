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

package app

import (
	"time"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/cosmosmigration"
)

const (
	cosmosMigrationControllerName = "cosmosmigration"
)

func registerCosmosMigrationController() ControllerRegistration {
	return ControllerRegistration{
		Workers:     5,
		instantiate: instantiateCosmosMigrationController,
	}
}

func instantiateCosmosMigrationController(controllerContext ControllerContext) (Runnable, error) {
	return cosmosmigration.NewCosmosMigrationController(
		controllerContext.ResourcesDBClient,
		controllerContext.KubeApplierDBClients,
		controllerContext.BackendInformers,
		5*time.Minute,
	), nil
}
