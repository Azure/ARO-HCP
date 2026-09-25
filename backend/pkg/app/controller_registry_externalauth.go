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
	utilsclock "k8s.io/utils/clock"

	externalauthcreation "github.com/Azure/ARO-HCP/backend/pkg/controllers/externalauth/creation"
	externalauthdeletion "github.com/Azure/ARO-HCP/backend/pkg/controllers/externalauth/deletion"
	externalauthoperations "github.com/Azure/ARO-HCP/backend/pkg/controllers/externalauth/operations"
	externalauthstatus "github.com/Azure/ARO-HCP/backend/pkg/controllers/externalauth/status"
	externalauthupdate "github.com/Azure/ARO-HCP/backend/pkg/controllers/externalauth/update"
)

const (
	externalAuthClusterServiceCreateControllerName                 = "externalauthclusterservicecreate"
	operationExternalAuthCreateControllerName                      = "operationexternalauthcreate"
	operationExternalAuthUpdateControllerName                      = "operationexternalauthupdate"
	operationExternalAuthDeleteControllerName                      = "operationexternalauthdelete"
	externalAuthDegradedAggregatorControllerName                   = "externalauthdegradedaggregator"
	externalAuthDeletionClusterServiceDeleteDispatchControllerName = "externalauthclusterservicedeletedispatch"
	externalAuthClusterServiceIDClearerControllerName              = "externalauthdeletionclusterserviceidclearer"
	externalAuthChildResourcesCleanupControllerName                = "externalauthchildresourcescleanupcontroller"
	externalAuthDeletionControllerName                             = "externalauthdeletioncontroller"
	externalAuthClusterServiceUpdateDispatchControllerName         = "externalauthclusterserviceupdatedispatch"
)

func registerExternalAuthClusterServiceCreateController() ControllerRegistration {
	return ControllerRegistration{
		Workers:     20,
		instantiate: instantiateExternalAuthClusterServiceCreateController,
	}
}

func instantiateExternalAuthClusterServiceCreateController(controllerContext ControllerContext) (Runnable, error) {
	return externalauthcreation.NewExternalAuthClusterServiceCreateController(
		controllerContext.ResourcesDBClient,
		controllerContext.ClustersServiceClient,
		controllerContext.BackendInformers,
	), nil
}

func registerOperationExternalAuthCreateController() ControllerRegistration {
	return ControllerRegistration{
		Workers:     20,
		instantiate: instantiateOperationExternalAuthCreateController,
	}
}

func instantiateOperationExternalAuthCreateController(controllerContext ControllerContext) (Runnable, error) {
	activeOperationInformer, _ := controllerContext.BackendInformers.ActiveOperations()
	return externalauthoperations.NewOperationExternalAuthCreateController(
		controllerContext.Clock,
		controllerContext.ResourcesDBClient,
		controllerContext.ClustersServiceClient,
		controllerContext.AsyncOperationNotificationClient,
		activeOperationInformer,
		controllerContext.BackendInformers,
	), nil
}

func registerOperationExternalAuthUpdateController() ControllerRegistration {
	return ControllerRegistration{
		Workers:     20,
		instantiate: instantiateOperationExternalAuthUpdateController,
	}
}

func instantiateOperationExternalAuthUpdateController(controllerContext ControllerContext) (Runnable, error) {
	activeOperationInformer, _ := controllerContext.BackendInformers.ActiveOperations()
	_, unionReadDesireLister := controllerContext.UnionKubeApplierInformers.ReadDesires()
	return externalauthoperations.NewOperationExternalAuthUpdateController(
		controllerContext.Clock,
		controllerContext.ResourcesDBClient,
		controllerContext.ClustersServiceClient,
		unionReadDesireLister,
		controllerContext.AsyncOperationNotificationClient,
		activeOperationInformer,
		controllerContext.BackendInformers,
	), nil
}

func registerOperationExternalAuthDeleteController() ControllerRegistration {
	return ControllerRegistration{
		Workers:     20,
		instantiate: instantiateOperationExternalAuthDeleteController,
	}
}

func instantiateOperationExternalAuthDeleteController(controllerContext ControllerContext) (Runnable, error) {
	activeOperationInformer, _ := controllerContext.BackendInformers.ActiveOperations()
	return externalauthoperations.NewOperationExternalAuthDeleteController(
		controllerContext.Clock,
		controllerContext.ResourcesDBClient,
		controllerContext.ClustersServiceClient,
		controllerContext.AsyncOperationNotificationClient,
		activeOperationInformer,
	), nil
}

func registerExternalAuthDegradedAggregatorController() ControllerRegistration {
	return ControllerRegistration{
		Workers:     20,
		instantiate: instantiateExternalAuthDegradedAggregatorController,
	}
}

func instantiateExternalAuthDegradedAggregatorController(controllerContext ControllerContext) (Runnable, error) {
	_, externalAuthLister := controllerContext.BackendInformers.ExternalAuths()
	_, controllerLister := controllerContext.BackendInformers.Controllers()
	return externalauthstatus.NewExternalAuthDegradedAggregatorController(
		controllerContext.ResourcesDBClient,
		externalAuthLister,
		controllerLister,
		controllerContext.BackendInformers,
		controllerContext.Clock,
	), nil
}

func registerExternalAuthDeletionClusterServiceDeleteDispatchController() ControllerRegistration {
	return ControllerRegistration{
		Workers:     20,
		instantiate: instantiateExternalAuthDeletionClusterServiceDeleteDispatchController,
	}
}

func instantiateExternalAuthDeletionClusterServiceDeleteDispatchController(controllerContext ControllerContext) (Runnable, error) {
	return externalauthdeletion.NewExternalAuthClusterServiceDeleteDispatchController(
		utilsclock.RealClock{},
		controllerContext.ResourcesDBClient,
		controllerContext.ClustersServiceClient,
		controllerContext.BackendInformers,
	), nil
}

func registerExternalAuthClusterServiceIDClearerController() ControllerRegistration {
	return ControllerRegistration{
		Workers:     20,
		instantiate: instantiateExternalAuthClusterServiceIDClearerController,
	}
}

func instantiateExternalAuthClusterServiceIDClearerController(controllerContext ControllerContext) (Runnable, error) {
	return externalauthdeletion.NewExternalAuthClusterServiceIDClearerController(
		controllerContext.ResourcesDBClient,
		controllerContext.ClustersServiceClient,
		controllerContext.BackendInformers,
	), nil
}

func registerExternalAuthChildResourcesCleanupController() ControllerRegistration {
	return ControllerRegistration{
		Workers:     20,
		instantiate: instantiateExternalAuthChildResourcesCleanupController,
	}
}

func instantiateExternalAuthChildResourcesCleanupController(controllerContext ControllerContext) (Runnable, error) {
	return externalauthdeletion.NewExternalAuthChildResourcesCleanupController(
		controllerContext.ResourcesDBClient,
		controllerContext.BackendInformers,
	), nil
}

func registerExternalAuthDeletionController() ControllerRegistration {
	return ControllerRegistration{
		Workers:     20,
		instantiate: instantiateExternalAuthDeletionController,
	}
}

func instantiateExternalAuthDeletionController(controllerContext ControllerContext) (Runnable, error) {
	return externalauthdeletion.NewExternalAuthDeletionController(
		controllerContext.ResourcesDBClient,
		controllerContext.BackendInformers,
	), nil
}

func registerExternalAuthClusterServiceUpdateDispatchController() ControllerRegistration {
	return ControllerRegistration{
		Workers:     20,
		instantiate: instantiateExternalAuthClusterServiceUpdateDispatchController,
	}
}

func instantiateExternalAuthClusterServiceUpdateDispatchController(controllerContext ControllerContext) (Runnable, error) {
	_, activeOperationLister := controllerContext.BackendInformers.ActiveOperations()
	return externalauthupdate.NewExternalAuthClusterServiceUpdateDispatchController(
		controllerContext.ResourcesDBClient,
		controllerContext.ClustersServiceClient,
		activeOperationLister,
		controllerContext.BackendInformers,
	), nil
}
