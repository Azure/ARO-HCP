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

package externalauth

import (
	"strings"

	utilsclock "k8s.io/utils/clock"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerconfig"
	externalauthcreation "github.com/Azure/ARO-HCP/backend/pkg/controllers/externalauth/creation"
	externalauthdeletion "github.com/Azure/ARO-HCP/backend/pkg/controllers/externalauth/deletion"
	externalauthoperations "github.com/Azure/ARO-HCP/backend/pkg/controllers/externalauth/operations"
	externalauthstatus "github.com/Azure/ARO-HCP/backend/pkg/controllers/externalauth/status"
	externalauthupdate "github.com/Azure/ARO-HCP/backend/pkg/controllers/externalauth/update"
)

func registerExternalAuthClusterServiceCreateController() controllerconfig.ControllerRegistration {
	return controllerconfig.ControllerRegistration{
		Workers:     20,
		Instantiate: controllerconfig.WithCacheSyncs(instantiateExternalAuthClusterServiceCreateController, false),
	}
}

func instantiateExternalAuthClusterServiceCreateController(controllerContext controllerconfig.ControllerContext) (controllerconfig.Runnable, error) {
	return externalauthcreation.NewExternalAuthClusterServiceCreateController(
		controllerContext.ResourcesDBClient,
		controllerContext.ClustersServiceClient,
		controllerContext.BackendInformers,
	), nil
}

func registerOperationExternalAuthCreateController() controllerconfig.ControllerRegistration {
	return controllerconfig.ControllerRegistration{
		Workers:     20,
		Instantiate: controllerconfig.WithCacheSyncs(instantiateOperationExternalAuthCreateController, false),
	}
}

func instantiateOperationExternalAuthCreateController(controllerContext controllerconfig.ControllerContext) (controllerconfig.Runnable, error) {
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

func registerOperationExternalAuthUpdateController() controllerconfig.ControllerRegistration {
	return controllerconfig.ControllerRegistration{
		Workers:     20,
		Instantiate: controllerconfig.WithCacheSyncs(instantiateOperationExternalAuthUpdateController, true),
	}
}

func instantiateOperationExternalAuthUpdateController(controllerContext controllerconfig.ControllerContext) (controllerconfig.Runnable, error) {
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

func registerOperationExternalAuthDeleteController() controllerconfig.ControllerRegistration {
	return controllerconfig.ControllerRegistration{
		Workers:     20,
		Instantiate: controllerconfig.WithCacheSyncs(instantiateOperationExternalAuthDeleteController, false),
	}
}

func instantiateOperationExternalAuthDeleteController(controllerContext controllerconfig.ControllerContext) (controllerconfig.Runnable, error) {
	activeOperationInformer, _ := controllerContext.BackendInformers.ActiveOperations()
	return externalauthoperations.NewOperationExternalAuthDeleteController(
		controllerContext.Clock,
		controllerContext.ResourcesDBClient,
		controllerContext.ClustersServiceClient,
		controllerContext.AsyncOperationNotificationClient,
		activeOperationInformer,
	), nil
}

func registerExternalAuthDegradedAggregatorController() controllerconfig.ControllerRegistration {
	return controllerconfig.ControllerRegistration{
		Workers:     20,
		Instantiate: controllerconfig.WithCacheSyncs(instantiateExternalAuthDegradedAggregatorController, false),
	}
}

func instantiateExternalAuthDegradedAggregatorController(controllerContext controllerconfig.ControllerContext) (controllerconfig.Runnable, error) {
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

func registerExternalAuthDeletionClusterServiceDeleteDispatchController() controllerconfig.ControllerRegistration {
	return controllerconfig.ControllerRegistration{
		Workers:     20,
		Instantiate: controllerconfig.WithCacheSyncs(instantiateExternalAuthDeletionClusterServiceDeleteDispatchController, false),
	}
}

func instantiateExternalAuthDeletionClusterServiceDeleteDispatchController(controllerContext controllerconfig.ControllerContext) (controllerconfig.Runnable, error) {
	return externalauthdeletion.NewExternalAuthClusterServiceDeleteDispatchController(
		utilsclock.RealClock{},
		controllerContext.ResourcesDBClient,
		controllerContext.ClustersServiceClient,
		controllerContext.BackendInformers,
	), nil
}

func registerExternalAuthClusterServiceIDClearerController() controllerconfig.ControllerRegistration {
	return controllerconfig.ControllerRegistration{
		Workers:     20,
		Instantiate: controllerconfig.WithCacheSyncs(instantiateExternalAuthClusterServiceIDClearerController, false),
	}
}

func instantiateExternalAuthClusterServiceIDClearerController(controllerContext controllerconfig.ControllerContext) (controllerconfig.Runnable, error) {
	return externalauthdeletion.NewExternalAuthClusterServiceIDClearerController(
		controllerContext.ResourcesDBClient,
		controllerContext.ClustersServiceClient,
		controllerContext.BackendInformers,
	), nil
}

func registerExternalAuthChildResourcesCleanupController() controllerconfig.ControllerRegistration {
	return controllerconfig.ControllerRegistration{
		Workers:     20,
		Instantiate: controllerconfig.WithCacheSyncs(instantiateExternalAuthChildResourcesCleanupController, false),
	}
}

func instantiateExternalAuthChildResourcesCleanupController(controllerContext controllerconfig.ControllerContext) (controllerconfig.Runnable, error) {
	return externalauthdeletion.NewExternalAuthChildResourcesCleanupController(
		controllerContext.ResourcesDBClient,
		controllerContext.BackendInformers,
	), nil
}

func registerExternalAuthDeletionController() controllerconfig.ControllerRegistration {
	return controllerconfig.ControllerRegistration{
		Workers:     20,
		Instantiate: controllerconfig.WithCacheSyncs(instantiateExternalAuthDeletionController, false),
	}
}

func instantiateExternalAuthDeletionController(controllerContext controllerconfig.ControllerContext) (controllerconfig.Runnable, error) {
	return externalauthdeletion.NewExternalAuthDeletionController(
		controllerContext.ResourcesDBClient,
		controllerContext.BackendInformers,
	), nil
}

func registerExternalAuthClusterServiceUpdateDispatchController() controllerconfig.ControllerRegistration {
	return controllerconfig.ControllerRegistration{
		Workers:     20,
		Instantiate: controllerconfig.WithCacheSyncs(instantiateExternalAuthClusterServiceUpdateDispatchController, false),
	}
}

func instantiateExternalAuthClusterServiceUpdateDispatchController(controllerContext controllerconfig.ControllerContext) (controllerconfig.Runnable, error) {
	_, activeOperationLister := controllerContext.BackendInformers.ActiveOperations()
	return externalauthupdate.NewExternalAuthClusterServiceUpdateDispatchController(
		controllerContext.ResourcesDBClient,
		controllerContext.ClustersServiceClient,
		activeOperationLister,
		controllerContext.BackendInformers,
	), nil
}

func Register(registry map[string]controllerconfig.ControllerRegistration) {
	registry[strings.ToLower(externalauthcreation.ExternalAuthClusterServiceCreateControllerName)] = registerExternalAuthClusterServiceCreateController()
	registry[strings.ToLower(externalauthoperations.OperationExternalAuthCreateControllerName)] = registerOperationExternalAuthCreateController()
	registry[strings.ToLower(externalauthoperations.OperationExternalAuthUpdateControllerName)] = registerOperationExternalAuthUpdateController()
	registry[strings.ToLower(externalauthoperations.OperationExternalAuthDeleteControllerName)] = registerOperationExternalAuthDeleteController()
	registry[strings.ToLower(externalauthstatus.ExternalAuthDegradedAggregatorControllerName)] = registerExternalAuthDegradedAggregatorController()
	registry[strings.ToLower(externalauthdeletion.ExternalAuthClusterServiceDeleteDispatchControllerName)] = registerExternalAuthDeletionClusterServiceDeleteDispatchController()
	registry[strings.ToLower(externalauthdeletion.ExternalAuthDeletionClusterServiceIDClearerControllerName)] = registerExternalAuthClusterServiceIDClearerController()
	registry[strings.ToLower(externalauthdeletion.ExternalAuthChildResourcesCleanupControllerControllerName)] = registerExternalAuthChildResourcesCleanupController()
	registry[strings.ToLower(externalauthdeletion.ExternalAuthDeletionControllerControllerName)] = registerExternalAuthDeletionController()
	registry[strings.ToLower(externalauthupdate.ExternalAuthClusterServiceUpdateDispatchControllerName)] = registerExternalAuthClusterServiceUpdateDispatchController()
}
