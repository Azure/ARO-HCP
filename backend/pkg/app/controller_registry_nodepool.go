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

	nodepoolcreation "github.com/Azure/ARO-HCP/backend/pkg/controllers/nodepool/creation"
	nodepooldeletion "github.com/Azure/ARO-HCP/backend/pkg/controllers/nodepool/deletion"
	nodepooloperations "github.com/Azure/ARO-HCP/backend/pkg/controllers/nodepool/operations"
	nodepoolreaddesires "github.com/Azure/ARO-HCP/backend/pkg/controllers/nodepool/readdesires"
	nodepoolstatus "github.com/Azure/ARO-HCP/backend/pkg/controllers/nodepool/status"
	nodepoolupdate "github.com/Azure/ARO-HCP/backend/pkg/controllers/nodepool/update"
	nodepoolvalidation "github.com/Azure/ARO-HCP/backend/pkg/controllers/nodepool/validation"
	nodepoolversion "github.com/Azure/ARO-HCP/backend/pkg/controllers/nodepool/version"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/validationutils"
)

const (
	nodePoolClusterServiceCreateControllerName                   = "nodepoolclusterservicecreate"
	operationNodePoolCreateControllerName                        = "operationnodepoolcreate"
	operationNodePoolUpdateControllerName                        = "operationnodepoolupdate"
	operationNodePoolDeleteControllerName                        = "operationnodepooldelete"
	nodePoolDegradedAggregatorControllerName                     = "nodepooldegradedaggregator"
	nodePoolRequirementsValidAggregatorControllerName            = "nodepoolrequirementsvalidaggregator"
	azureVMSizeSupportsEphemeralOSDiskValidationControllerName   = "nodepoolvalidationazurevmsizesupportsephemeralosdiskvalidation"
	azureNodePoolVMQuotaValidationControllerName                 = "nodepoolvalidationazurenodepoolvmquotavalidation"
	nodePoolNSGBasedRequiredConnectivityValidationControllerName = "nodepoolvalidationazurenodepoolnsgbasedrequiredconnectivityvalidation"
	nodePoolVersionControllerName                                = "nodepoolversion"
	nodePoolActiveVersionControllerName                          = "nodepoolactiveversions"
	createNodePoolScopedReadDesiresControllerName                = "createnodepoolscopedreaddesires"
	createServiceProviderNodePoolControllerName                  = "createserviceprovidernodepool"
	triggerNodePoolUpgradeControllerName                         = "triggernodepoolupgrade"
	nodePoolDeletionClusterServiceDeleteDispatchControllerName   = "nodepoolclusterservicedeletedispatch"
	nodePoolClusterServiceIDClearerControllerName                = "nodepooldeletionclusterserviceidclearer"
	nodePoolChildResourcesCleanupControllerName                  = "nodepoolchildresourcescleanupcontroller"
	nodePoolDeletionControllerName                               = "nodepooldeletioncontroller"
	nodePoolClusterServiceUpdateDispatchControllerName           = "nodepoolclusterserviceupdatedispatch"
)

func registerNodePoolClusterServiceCreateController() ControllerRegistration {
	return ControllerRegistration{
		Workers:     20,
		instantiate: instantiateNodePoolClusterServiceCreateController,
	}
}

func instantiateNodePoolClusterServiceCreateController(controllerContext ControllerContext) (Runnable, error) {
	return nodepoolcreation.NewNodePoolClusterServiceCreateController(
		controllerContext.ResourcesDBClient,
		controllerContext.ClustersServiceClient,
		controllerContext.BackendInformers,
		controllerContext.UnionKubeApplierInformers,
	), nil
}

func registerOperationNodePoolCreateController() ControllerRegistration {
	return ControllerRegistration{
		Workers:     20,
		instantiate: instantiateOperationNodePoolCreateController,
	}
}

func instantiateOperationNodePoolCreateController(controllerContext ControllerContext) (Runnable, error) {
	activeOperationInformer, _ := controllerContext.BackendInformers.ActiveOperations()
	_, unionReadDesireLister := controllerContext.UnionKubeApplierInformers.ReadDesires()
	return nodepooloperations.NewOperationNodePoolCreateController(
		controllerContext.Clock,
		controllerContext.ResourcesDBClient,
		controllerContext.ClustersServiceClient,
		unionReadDesireLister,
		controllerContext.AsyncOperationNotificationClient,
		activeOperationInformer,
		controllerContext.BackendInformers,
	), nil
}

func registerOperationNodePoolUpdateController() ControllerRegistration {
	return ControllerRegistration{
		Workers:     20,
		instantiate: instantiateOperationNodePoolUpdateController,
	}
}

func instantiateOperationNodePoolUpdateController(controllerContext ControllerContext) (Runnable, error) {
	activeOperationInformer, _ := controllerContext.BackendInformers.ActiveOperations()
	_, unionReadDesireLister := controllerContext.UnionKubeApplierInformers.ReadDesires()
	return nodepooloperations.NewOperationNodePoolUpdateController(
		controllerContext.Clock,
		controllerContext.ResourcesDBClient,
		controllerContext.ClustersServiceClient,
		unionReadDesireLister,
		controllerContext.AsyncOperationNotificationClient,
		activeOperationInformer,
		controllerContext.BackendInformers,
	), nil
}

func registerOperationNodePoolDeleteController() ControllerRegistration {
	return ControllerRegistration{
		Workers:     20,
		instantiate: instantiateOperationNodePoolDeleteController,
	}
}

func instantiateOperationNodePoolDeleteController(controllerContext ControllerContext) (Runnable, error) {
	activeOperationInformer, _ := controllerContext.BackendInformers.ActiveOperations()
	return nodepooloperations.NewOperationNodePoolDeleteController(
		controllerContext.Clock,
		controllerContext.ResourcesDBClient,
		controllerContext.ClustersServiceClient,
		controllerContext.AsyncOperationNotificationClient,
		activeOperationInformer,
	), nil
}

func registerNodePoolDegradedAggregatorController() ControllerRegistration {
	return ControllerRegistration{
		Workers:     20,
		instantiate: instantiateNodePoolDegradedAggregatorController,
	}
}

func instantiateNodePoolDegradedAggregatorController(controllerContext ControllerContext) (Runnable, error) {
	_, nodePoolLister := controllerContext.BackendInformers.NodePools()
	_, controllerLister := controllerContext.BackendInformers.Controllers()
	return nodepoolstatus.NewNodePoolDegradedAggregatorController(
		controllerContext.ResourcesDBClient,
		nodePoolLister,
		controllerLister,
		controllerContext.BackendInformers,
		controllerContext.UnionKubeApplierInformers,
		controllerContext.Clock,
	), nil
}

func registerNodePoolRequirementsValidAggregatorController() ControllerRegistration {
	return ControllerRegistration{
		Workers:     20,
		instantiate: instantiateNodePoolRequirementsValidAggregatorController,
	}
}

func instantiateNodePoolRequirementsValidAggregatorController(controllerContext ControllerContext) (Runnable, error) {
	_, nodePoolLister := controllerContext.BackendInformers.NodePools()
	_, serviceProviderNodePoolLister := controllerContext.BackendInformers.ServiceProviderNodePools()
	return nodepoolstatus.NewNodePoolRequirementsValidAggregatorController(
		controllerContext.ResourcesDBClient,
		nodePoolLister,
		serviceProviderNodePoolLister,
		controllerContext.BackendInformers,
	), nil
}

func registerAzureVMSizeSupportsEphemeralOSDiskValidationController() ControllerRegistration {
	return ControllerRegistration{
		Workers:     20,
		instantiate: instantiateAzureVMSizeSupportsEphemeralOSDiskValidationController,
	}
}

func instantiateAzureVMSizeSupportsEphemeralOSDiskValidationController(controllerContext ControllerContext) (Runnable, error) {
	_, serviceProviderNodePoolLister := controllerContext.BackendInformers.ServiceProviderNodePools()
	return nodepoolvalidation.NewNodePoolValidationController(
		validationutils.NewAzureVMSizeSupportsEphemeralOSDiskValidation(controllerContext.VirtualMachineResourceSKUsCachedReaderController),
		controllerContext.ResourcesDBClient,
		serviceProviderNodePoolLister,
		controllerContext.BackendInformers,
		controllerContext.UnionKubeApplierInformers,
	), nil
}

func registerAzureNodePoolVMQuotaValidationController() ControllerRegistration {
	return ControllerRegistration{
		Workers:     20,
		instantiate: instantiateAzureNodePoolVMQuotaValidationController,
	}
}

func instantiateAzureNodePoolVMQuotaValidationController(controllerContext ControllerContext) (Runnable, error) {
	_, serviceProviderNodePoolLister := controllerContext.BackendInformers.ServiceProviderNodePools()
	return nodepoolvalidation.NewNodePoolValidationController(
		validationutils.NewAzureNodePoolVMQuotaValidation(controllerContext.VirtualMachineResourceSKUsCachedReaderController, controllerContext.FPAClientBuilder),
		controllerContext.ResourcesDBClient,
		serviceProviderNodePoolLister,
		controllerContext.BackendInformers,
		controllerContext.UnionKubeApplierInformers,
	), nil
}

func registerNodePoolNSGBasedRequiredConnectivityValidationController() ControllerRegistration {
	return ControllerRegistration{
		Workers:     20,
		instantiate: instantiateNodePoolNSGBasedRequiredConnectivityValidationController,
	}
}

func instantiateNodePoolNSGBasedRequiredConnectivityValidationController(controllerContext ControllerContext) (Runnable, error) {
	_, serviceProviderNodePoolLister := controllerContext.BackendInformers.ServiceProviderNodePools()
	return nodepoolvalidation.NewNodePoolValidationController(
		validationutils.NewAzureNodePoolNSGBasedRequiredConnectivityValidation(controllerContext.SMIClientBuilder),
		controllerContext.ResourcesDBClient,
		serviceProviderNodePoolLister,
		controllerContext.BackendInformers,
		controllerContext.UnionKubeApplierInformers,
	), nil
}

func registerNodePoolVersionController() ControllerRegistration {
	return ControllerRegistration{
		Workers:     20,
		instantiate: instantiateNodePoolVersionController,
	}
}

func instantiateNodePoolVersionController(controllerContext ControllerContext) (Runnable, error) {
	_, subscriptionLister := controllerContext.BackendInformers.Subscriptions()
	_, unionReadDesireLister := controllerContext.UnionKubeApplierInformers.ReadDesires()
	return nodepoolversion.NewNodePoolVersionController(
		controllerContext.ResourcesDBClient,
		subscriptionLister,
		controllerContext.BackendInformers,
		controllerContext.UnionKubeApplierInformers,
		unionReadDesireLister,
	), nil
}

func registerNodePoolActiveVersionController() ControllerRegistration {
	return ControllerRegistration{
		Workers:     20,
		instantiate: instantiateNodePoolActiveVersionController,
	}
}

func instantiateNodePoolActiveVersionController(controllerContext ControllerContext) (Runnable, error) {
	_, unionReadDesireLister := controllerContext.UnionKubeApplierInformers.ReadDesires()
	return nodepoolversion.NewNodePoolActiveVersionController(
		controllerContext.ResourcesDBClient,
		controllerContext.BackendInformers,
		controllerContext.UnionKubeApplierInformers,
		unionReadDesireLister,
	), nil
}

func registerCreateNodePoolScopedReadDesiresController() ControllerRegistration {
	return ControllerRegistration{
		Workers:     20,
		instantiate: instantiateCreateNodePoolScopedReadDesiresController,
	}
}

func instantiateCreateNodePoolScopedReadDesiresController(controllerContext ControllerContext) (Runnable, error) {
	_, activeOperationLister := controllerContext.BackendInformers.ActiveOperations()
	_, serviceProviderClusterLister := controllerContext.BackendInformers.ServiceProviderClusters()
	_, unionReadDesireLister := controllerContext.UnionKubeApplierInformers.ReadDesires()
	return nodepoolreaddesires.NewCreateNodePoolScopedReadDesiresController(
		activeOperationLister, controllerContext.ResourcesDBClient, controllerContext.KubeApplierDBClients,
		serviceProviderClusterLister, unionReadDesireLister,
		controllerContext.BackendInformers, controllerContext.MaestroSourceEnvironmentIdentifier,
	), nil
}

func registerCreateServiceProviderNodePoolController() ControllerRegistration {
	return ControllerRegistration{
		Workers:     20,
		instantiate: instantiateCreateServiceProviderNodePoolController,
	}
}

func instantiateCreateServiceProviderNodePoolController(controllerContext ControllerContext) (Runnable, error) {
	_, nodePoolLister := controllerContext.BackendInformers.NodePools()
	_, serviceProviderNodePoolLister := controllerContext.BackendInformers.ServiceProviderNodePools()
	return nodepoolcreation.NewCreateServiceProviderNodePoolController(
		controllerContext.ResourcesDBClient,
		nodePoolLister,
		serviceProviderNodePoolLister,
		controllerContext.BackendInformers,
	), nil
}

func registerTriggerNodePoolUpgradeController() ControllerRegistration {
	return ControllerRegistration{
		Workers:     20,
		instantiate: instantiateTriggerNodePoolUpgradeController,
	}
}

func instantiateTriggerNodePoolUpgradeController(controllerContext ControllerContext) (Runnable, error) {
	_, nodePoolLister := controllerContext.BackendInformers.NodePools()
	_, serviceProviderNodePoolLister := controllerContext.BackendInformers.ServiceProviderNodePools()
	return nodepoolversion.NewTriggerNodePoolUpgradeController(
		controllerContext.ResourcesDBClient,
		nodePoolLister,
		controllerContext.ClustersServiceClient,
		serviceProviderNodePoolLister,
		controllerContext.BackendInformers,
		controllerContext.UnionKubeApplierInformers,
	), nil
}

func registerNodePoolDeletionClusterServiceDeleteDispatchController() ControllerRegistration {
	return ControllerRegistration{
		Workers:     20,
		instantiate: instantiateNodePoolDeletionClusterServiceDeleteDispatchController,
	}
}

func instantiateNodePoolDeletionClusterServiceDeleteDispatchController(controllerContext ControllerContext) (Runnable, error) {
	return nodepooldeletion.NewNodePoolClusterServiceDeleteDispatchController(
		utilsclock.RealClock{},
		controllerContext.ResourcesDBClient,
		controllerContext.ClustersServiceClient,
		controllerContext.BackendInformers,
		controllerContext.UnionKubeApplierInformers,
	), nil
}

func registerNodePoolClusterServiceIDClearerController() ControllerRegistration {
	return ControllerRegistration{
		Workers:     20,
		instantiate: instantiateNodePoolClusterServiceIDClearerController,
	}
}

func instantiateNodePoolClusterServiceIDClearerController(controllerContext ControllerContext) (Runnable, error) {
	return nodepooldeletion.NewNodePoolClusterServiceIDClearerController(
		controllerContext.ResourcesDBClient,
		controllerContext.ClustersServiceClient,
		controllerContext.BackendInformers,
		controllerContext.UnionKubeApplierInformers,
	), nil
}

func registerNodePoolChildResourcesCleanupController() ControllerRegistration {
	return ControllerRegistration{
		Workers:     20,
		instantiate: instantiateNodePoolChildResourcesCleanupController,
	}
}

func instantiateNodePoolChildResourcesCleanupController(controllerContext ControllerContext) (Runnable, error) {
	return nodepooldeletion.NewNodePoolChildResourcesCleanupController(
		controllerContext.ResourcesDBClient,
		controllerContext.KubeApplierDBClients,
		controllerContext.BackendInformers,
		controllerContext.UnionKubeApplierInformers,
	), nil
}

func registerNodePoolDeletionController() ControllerRegistration {
	return ControllerRegistration{
		Workers:     20,
		instantiate: instantiateNodePoolDeletionController,
	}
}

func instantiateNodePoolDeletionController(controllerContext ControllerContext) (Runnable, error) {
	return nodepooldeletion.NewNodePoolDeletionController(
		controllerContext.ResourcesDBClient,
		controllerContext.BackendInformers,
		controllerContext.UnionKubeApplierInformers,
		controllerContext.KubeApplierDBClients,
	), nil
}

func registerNodePoolClusterServiceUpdateDispatchController() ControllerRegistration {
	return ControllerRegistration{
		Workers:     20,
		instantiate: instantiateNodePoolClusterServiceUpdateDispatchController,
	}
}

func instantiateNodePoolClusterServiceUpdateDispatchController(controllerContext ControllerContext) (Runnable, error) {
	return nodepoolupdate.NewNodePoolClusterServiceUpdateDispatchController(
		controllerContext.ResourcesDBClient,
		controllerContext.ClustersServiceClient,
		controllerContext.BackendInformers,
	), nil
}
