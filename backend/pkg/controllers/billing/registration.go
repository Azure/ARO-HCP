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

package billing

import (
	"strings"
	"time"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerconfig"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
)

func registerOrphanedBillingCleanupController() controllerconfig.ControllerRegistration {
	return controllerconfig.ControllerRegistration{
		Workers:     20,
		Instantiate: controllerconfig.WithCacheSyncs(instantiateOrphanedBillingCleanupController, false),
	}
}

func instantiateOrphanedBillingCleanupController(controllerContext controllerconfig.ControllerContext) (controllerconfig.Runnable, error) {
	_, clusterLister := controllerContext.BackendInformers.Clusters()
	_, billingLister := controllerContext.BackendInformers.BillingDocs()
	return NewOrphanedBillingCleanupController(controllerContext.Clock, controllerContext.BillingDBClient, clusterLister, billingLister), nil
}

func registerCreateBillingDocController() controllerconfig.ControllerRegistration {
	return controllerconfig.ControllerRegistration{
		Workers:     20,
		Instantiate: controllerconfig.WithCacheSyncs(instantiateCreateBillingDocController, true),
	}
}

func instantiateCreateBillingDocController(controllerContext controllerconfig.ControllerContext) (controllerconfig.Runnable, error) {
	_, clusterLister := controllerContext.BackendInformers.Clusters()
	_, billingLister := controllerContext.BackendInformers.BillingDocs()
	return controllerutils.NewClusterWatchingController(
		CreateBillingDocControllerName, controllerContext.ResourcesDBClient, controllerContext.BackendInformers, controllerContext.UnionKubeApplierInformers, 60*time.Second,
		NewCreateBillingDocController(controllerContext.Clock, controllerContext.AzureLocation, controllerContext.ResourcesDBClient, controllerContext.BillingDBClient, clusterLister, billingLister)), nil
}

func Register(registry map[string]controllerconfig.ControllerRegistration) {
	registry[strings.ToLower(OrphanedBillingCleanupControllerName)] = registerOrphanedBillingCleanupController()
	registry[strings.ToLower(CreateBillingDocControllerName)] = registerCreateBillingDocController()
}
