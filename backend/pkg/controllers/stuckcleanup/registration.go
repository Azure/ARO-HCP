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

package stuckcleanup

import (
	"strings"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerconfig"
)

func registerStuckClusterCleanupPreflightController() controllerconfig.ControllerRegistration {
	return controllerconfig.ControllerRegistration{
		Workers:     1,
		Instantiate: controllerconfig.WithCacheSyncs(instantiateStuckClusterCleanupPreflightController, false),
	}
}

func instantiateStuckClusterCleanupPreflightController(controllerContext controllerconfig.ControllerContext) (controllerconfig.Runnable, error) {
	_, readDesireLister := controllerContext.UnionKubeApplierInformers.ReadDesires()
	_, applyDesireLister := controllerContext.UnionKubeApplierInformers.ApplyDesires()
	return NewStuckClusterCleanupPreflightController(
		controllerContext.FleetDBClient,
		controllerContext.ResourcesDBClient,
		controllerContext.ClustersServiceClient,
		controllerContext.KubeApplierDBClients,
		readDesireLister,
		applyDesireLister,
		controllerContext.FleetInformers,
	), nil
}

// Register adds this package's controllers to the backend registry.
func Register(registry map[string]controllerconfig.ControllerRegistration) {
	registry[strings.ToLower(StuckClusterCleanupPreflightControllerName)] = registerStuckClusterCleanupPreflightController()
}
