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

package clusterresources

import (
	"strings"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerconfig"
)

func registerClusterResourcesController() controllerconfig.ControllerRegistration {
	return controllerconfig.ControllerRegistration{
		Workers:     20,
		Instantiate: controllerconfig.WithCacheSyncs(instantiateClusterResourcesController, true),
	}
}

func instantiateClusterResourcesController(controllerContext controllerconfig.ControllerContext) (controllerconfig.Runnable, error) {
	return NewClusterResourcesController(
		controllerContext.ResourcesDBClient,
		controllerContext.KubeApplierDBClients,
		controllerContext.BackendInformers,
		controllerContext.UnionKubeApplierInformers,
		controllerContext.ClustersServiceClient,
	), nil
}

func Register(registry map[string]controllerconfig.ControllerRegistration) {
	registry[strings.ToLower(ClusterResourcesControllerName)] = registerClusterResourcesController()
}
