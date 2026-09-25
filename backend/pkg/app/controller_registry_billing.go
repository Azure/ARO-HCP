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

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/billing"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
)

const (
	orphanedBillingCleanupControllerName = "orphanedbillingcleanup"
	createBillingDocControllerName       = "createbillingdoc"
)

func registerOrphanedBillingCleanupController() ControllerRegistration {
	return ControllerRegistration{
		Workers:     20,
		instantiate: instantiateOrphanedBillingCleanupController,
	}
}

func instantiateOrphanedBillingCleanupController(controllerContext ControllerContext) (Runnable, error) {
	_, clusterLister := controllerContext.BackendInformers.Clusters()
	_, billingLister := controllerContext.BackendInformers.BillingDocs()
	return billing.NewOrphanedBillingCleanupController(controllerContext.Clock, controllerContext.BillingDBClient, clusterLister, billingLister), nil
}

func registerCreateBillingDocController() ControllerRegistration {
	return ControllerRegistration{
		Workers:     20,
		instantiate: instantiateCreateBillingDocController,
	}
}

func instantiateCreateBillingDocController(controllerContext ControllerContext) (Runnable, error) {
	_, clusterLister := controllerContext.BackendInformers.Clusters()
	_, billingLister := controllerContext.BackendInformers.BillingDocs()
	return controllerutils.NewClusterWatchingController(
		"CreateBillingDoc", controllerContext.ResourcesDBClient, controllerContext.BackendInformers, controllerContext.UnionKubeApplierInformers, 60*time.Second,
		billing.NewCreateBillingDocController(controllerContext.Clock, controllerContext.AzureLocation, controllerContext.ResourcesDBClient, controllerContext.BillingDBClient, clusterLister, billingLister)), nil
}
