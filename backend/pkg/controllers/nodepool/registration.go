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

package nodepool

import (
	"strings"

	utilsclock "k8s.io/utils/clock"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerconfig"
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

func registerNodePoolClusterServiceCreateController() controllerconfig.ControllerRegistration {
	return controllerconfig.ControllerRegistration{
		Workers:     20,
		Instantiate: controllerconfig.WithCacheSyncs(instantiateNodePoolClusterServiceCreateController, true),
	}
}

func instantiateNodePoolClusterServiceCreateController(controllerContext controllerconfig.ControllerContext) (controllerconfig.Runnable, error) {
	return nodepoolcreation.NewNodePoolClusterServiceCreateController(
		controllerContext.ResourcesDBClient,
		controllerContext.ClustersServiceClient,
		controllerContext.BackendInformers,
		controllerContext.UnionKubeApplierInformers,
	), nil
}

func registerOperationNodePoolCreateController() controllerconfig.ControllerRegistration {
	return controllerconfig.ControllerRegistration{
		Workers:     20,
		Instantiate: controllerconfig.WithCacheSyncs(instantiateOperationNodePoolCreateController, true),
	}
}

func instantiateOperationNodePoolCreateController(controllerContext controllerconfig.ControllerContext) (controllerconfig.Runnable, error) {
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

func registerOperationNodePoolUpdateController() controllerconfig.ControllerRegistration {
	return controllerconfig.ControllerRegistration{
		Workers:     20,
		Instantiate: controllerconfig.WithCacheSyncs(instantiateOperationNodePoolUpdateController, true),
	}
}

func instantiateOperationNodePoolUpdateController(controllerContext controllerconfig.ControllerContext) (controllerconfig.Runnable, error) {
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

func registerOperationNodePoolDeleteController() controllerconfig.ControllerRegistration {
	return controllerconfig.ControllerRegistration{
		Workers:     20,
		Instantiate: controllerconfig.WithCacheSyncs(instantiateOperationNodePoolDeleteController, false),
	}
}

func instantiateOperationNodePoolDeleteController(controllerContext controllerconfig.ControllerContext) (controllerconfig.Runnable, error) {
	activeOperationInformer, _ := controllerContext.BackendInformers.ActiveOperations()
	return nodepooloperations.NewOperationNodePoolDeleteController(
		controllerContext.Clock,
		controllerContext.ResourcesDBClient,
		controllerContext.ClustersServiceClient,
		controllerContext.AsyncOperationNotificationClient,
		activeOperationInformer,
	), nil
}

func registerNodePoolDegradedAggregatorController() controllerconfig.ControllerRegistration {
	return controllerconfig.ControllerRegistration{
		Workers:     20,
		Instantiate: controllerconfig.WithCacheSyncs(instantiateNodePoolDegradedAggregatorController, true),
	}
}

func instantiateNodePoolDegradedAggregatorController(controllerContext controllerconfig.ControllerContext) (controllerconfig.Runnable, error) {
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

func registerNodePoolRequirementsValidAggregatorController() controllerconfig.ControllerRegistration {
	return controllerconfig.ControllerRegistration{
		Workers:     20,
		Instantiate: controllerconfig.WithCacheSyncs(instantiateNodePoolRequirementsValidAggregatorController, false),
	}
}

func instantiateNodePoolRequirementsValidAggregatorController(controllerContext controllerconfig.ControllerContext) (controllerconfig.Runnable, error) {
	_, nodePoolLister := controllerContext.BackendInformers.NodePools()
	_, serviceProviderNodePoolLister := controllerContext.BackendInformers.ServiceProviderNodePools()
	return nodepoolstatus.NewNodePoolRequirementsValidAggregatorController(
		controllerContext.ResourcesDBClient,
		nodePoolLister,
		serviceProviderNodePoolLister,
		controllerContext.BackendInformers,
	), nil
}

func registerAzureVMSizeSupportsEphemeralOSDiskValidationController() controllerconfig.ControllerRegistration {
	return controllerconfig.ControllerRegistration{
		Workers:     20,
		Instantiate: controllerconfig.WithCacheSyncs(instantiateAzureVMSizeSupportsEphemeralOSDiskValidationController, true),
	}
}

func instantiateAzureVMSizeSupportsEphemeralOSDiskValidationController(controllerContext controllerconfig.ControllerContext) (controllerconfig.Runnable, error) {
	_, serviceProviderNodePoolLister := controllerContext.BackendInformers.ServiceProviderNodePools()
	return nodepoolvalidation.NewNamedNodePoolValidationController(
		nodepoolvalidation.NodePoolValidationAzureVMSizeSupportsEphemeralOSDiskValidationControllerName,
		validationutils.NewAzureVMSizeSupportsEphemeralOSDiskValidation(controllerContext.VirtualMachineResourceSKUsCachedReaderController),
		controllerContext.ResourcesDBClient,
		serviceProviderNodePoolLister,
		controllerContext.BackendInformers,
		controllerContext.UnionKubeApplierInformers,
	), nil
}

func registerAzureNodePoolVMQuotaValidationController() controllerconfig.ControllerRegistration {
	return controllerconfig.ControllerRegistration{
		Workers:     20,
		Instantiate: controllerconfig.WithCacheSyncs(instantiateAzureNodePoolVMQuotaValidationController, true),
	}
}

func instantiateAzureNodePoolVMQuotaValidationController(controllerContext controllerconfig.ControllerContext) (controllerconfig.Runnable, error) {
	_, serviceProviderNodePoolLister := controllerContext.BackendInformers.ServiceProviderNodePools()
	return nodepoolvalidation.NewNamedNodePoolValidationController(
		nodepoolvalidation.NodePoolValidationAzureNodePoolVMQuotaValidationControllerName,
		validationutils.NewAzureNodePoolVMQuotaValidation(controllerContext.VirtualMachineResourceSKUsCachedReaderController, controllerContext.FPAClientBuilder),
		controllerContext.ResourcesDBClient,
		serviceProviderNodePoolLister,
		controllerContext.BackendInformers,
		controllerContext.UnionKubeApplierInformers,
	), nil
}

func registerNodePoolNSGBasedRequiredConnectivityValidationController() controllerconfig.ControllerRegistration {
	return controllerconfig.ControllerRegistration{
		Workers:     20,
		Instantiate: controllerconfig.WithCacheSyncs(instantiateNodePoolNSGBasedRequiredConnectivityValidationController, true),
	}
}

func instantiateNodePoolNSGBasedRequiredConnectivityValidationController(controllerContext controllerconfig.ControllerContext) (controllerconfig.Runnable, error) {
	_, serviceProviderNodePoolLister := controllerContext.BackendInformers.ServiceProviderNodePools()
	return nodepoolvalidation.NewNamedNodePoolValidationController(
		nodepoolvalidation.NodePoolValidationAzureNodePoolNSGBasedRequiredConnectivityValidationControllerName,
		validationutils.NewAzureNodePoolNSGBasedRequiredConnectivityValidation(controllerContext.SMIClientBuilder),
		controllerContext.ResourcesDBClient,
		serviceProviderNodePoolLister,
		controllerContext.BackendInformers,
		controllerContext.UnionKubeApplierInformers,
	), nil
}

func registerNodePoolVersionController() controllerconfig.ControllerRegistration {
	return controllerconfig.ControllerRegistration{
		Workers:     20,
		Instantiate: controllerconfig.WithCacheSyncs(instantiateNodePoolVersionController, true),
	}
}

func instantiateNodePoolVersionController(controllerContext controllerconfig.ControllerContext) (controllerconfig.Runnable, error) {
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

func registerNodePoolActiveVersionController() controllerconfig.ControllerRegistration {
	return controllerconfig.ControllerRegistration{
		Workers:     20,
		Instantiate: controllerconfig.WithCacheSyncs(instantiateNodePoolActiveVersionController, true),
	}
}

func instantiateNodePoolActiveVersionController(controllerContext controllerconfig.ControllerContext) (controllerconfig.Runnable, error) {
	_, unionReadDesireLister := controllerContext.UnionKubeApplierInformers.ReadDesires()
	return nodepoolversion.NewNodePoolActiveVersionController(
		controllerContext.ResourcesDBClient,
		controllerContext.BackendInformers,
		controllerContext.UnionKubeApplierInformers,
		unionReadDesireLister,
	), nil
}

func registerCreateNodePoolScopedReadDesiresController() controllerconfig.ControllerRegistration {
	return controllerconfig.ControllerRegistration{
		Workers:     20,
		Instantiate: controllerconfig.WithCacheSyncs(instantiateCreateNodePoolScopedReadDesiresController, true),
	}
}

func instantiateCreateNodePoolScopedReadDesiresController(controllerContext controllerconfig.ControllerContext) (controllerconfig.Runnable, error) {
	_, activeOperationLister := controllerContext.BackendInformers.ActiveOperations()
	_, serviceProviderClusterLister := controllerContext.BackendInformers.ServiceProviderClusters()
	_, unionReadDesireLister := controllerContext.UnionKubeApplierInformers.ReadDesires()
	return nodepoolreaddesires.NewCreateNodePoolScopedReadDesiresController(
		activeOperationLister, controllerContext.ResourcesDBClient, controllerContext.KubeApplierDBClients,
		serviceProviderClusterLister, unionReadDesireLister,
		controllerContext.BackendInformers, controllerContext.MaestroSourceEnvironmentIdentifier,
	), nil
}

func registerCreateServiceProviderNodePoolController() controllerconfig.ControllerRegistration {
	return controllerconfig.ControllerRegistration{
		Workers:     20,
		Instantiate: controllerconfig.WithCacheSyncs(instantiateCreateServiceProviderNodePoolController, false),
	}
}

func instantiateCreateServiceProviderNodePoolController(controllerContext controllerconfig.ControllerContext) (controllerconfig.Runnable, error) {
	_, nodePoolLister := controllerContext.BackendInformers.NodePools()
	_, serviceProviderNodePoolLister := controllerContext.BackendInformers.ServiceProviderNodePools()
	return nodepoolcreation.NewCreateServiceProviderNodePoolController(
		controllerContext.ResourcesDBClient,
		nodePoolLister,
		serviceProviderNodePoolLister,
		controllerContext.BackendInformers,
	), nil
}

func registerTriggerNodePoolUpgradeController() controllerconfig.ControllerRegistration {
	return controllerconfig.ControllerRegistration{
		Workers:     20,
		Instantiate: controllerconfig.WithCacheSyncs(instantiateTriggerNodePoolUpgradeController, true),
	}
}

func instantiateTriggerNodePoolUpgradeController(controllerContext controllerconfig.ControllerContext) (controllerconfig.Runnable, error) {
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

func registerNodePoolDeletionClusterServiceDeleteDispatchController() controllerconfig.ControllerRegistration {
	return controllerconfig.ControllerRegistration{
		Workers:     20,
		Instantiate: controllerconfig.WithCacheSyncs(instantiateNodePoolDeletionClusterServiceDeleteDispatchController, true),
	}
}

func instantiateNodePoolDeletionClusterServiceDeleteDispatchController(controllerContext controllerconfig.ControllerContext) (controllerconfig.Runnable, error) {
	return nodepooldeletion.NewNodePoolClusterServiceDeleteDispatchController(
		utilsclock.RealClock{},
		controllerContext.ResourcesDBClient,
		controllerContext.ClustersServiceClient,
		controllerContext.BackendInformers,
		controllerContext.UnionKubeApplierInformers,
	), nil
}

func registerNodePoolClusterServiceIDClearerController() controllerconfig.ControllerRegistration {
	return controllerconfig.ControllerRegistration{
		Workers:     20,
		Instantiate: controllerconfig.WithCacheSyncs(instantiateNodePoolClusterServiceIDClearerController, true),
	}
}

func instantiateNodePoolClusterServiceIDClearerController(controllerContext controllerconfig.ControllerContext) (controllerconfig.Runnable, error) {
	return nodepooldeletion.NewNodePoolClusterServiceIDClearerController(
		controllerContext.ResourcesDBClient,
		controllerContext.ClustersServiceClient,
		controllerContext.BackendInformers,
		controllerContext.UnionKubeApplierInformers,
	), nil
}

func registerNodePoolChildResourcesCleanupController() controllerconfig.ControllerRegistration {
	return controllerconfig.ControllerRegistration{
		Workers:     20,
		Instantiate: controllerconfig.WithCacheSyncs(instantiateNodePoolChildResourcesCleanupController, true),
	}
}

func instantiateNodePoolChildResourcesCleanupController(controllerContext controllerconfig.ControllerContext) (controllerconfig.Runnable, error) {
	return nodepooldeletion.NewNodePoolChildResourcesCleanupController(
		controllerContext.ResourcesDBClient,
		controllerContext.KubeApplierDBClients,
		controllerContext.BackendInformers,
		controllerContext.UnionKubeApplierInformers,
	), nil
}

func registerNodePoolDeletionController() controllerconfig.ControllerRegistration {
	return controllerconfig.ControllerRegistration{
		Workers:     20,
		Instantiate: controllerconfig.WithCacheSyncs(instantiateNodePoolDeletionController, true),
	}
}

func instantiateNodePoolDeletionController(controllerContext controllerconfig.ControllerContext) (controllerconfig.Runnable, error) {
	return nodepooldeletion.NewNodePoolDeletionController(
		controllerContext.ResourcesDBClient,
		controllerContext.BackendInformers,
		controllerContext.UnionKubeApplierInformers,
		controllerContext.KubeApplierDBClients,
	), nil
}

func registerNodePoolClusterServiceUpdateDispatchController() controllerconfig.ControllerRegistration {
	return controllerconfig.ControllerRegistration{
		Workers:     20,
		Instantiate: controllerconfig.WithCacheSyncs(instantiateNodePoolClusterServiceUpdateDispatchController, false),
	}
}

func instantiateNodePoolClusterServiceUpdateDispatchController(controllerContext controllerconfig.ControllerContext) (controllerconfig.Runnable, error) {
	return nodepoolupdate.NewNodePoolClusterServiceUpdateDispatchController(
		controllerContext.ResourcesDBClient,
		controllerContext.ClustersServiceClient,
		controllerContext.BackendInformers,
	), nil
}

func Register(registry map[string]controllerconfig.ControllerRegistration) {
	registry[strings.ToLower(nodepoolcreation.NodePoolClusterServiceCreateControllerName)] = registerNodePoolClusterServiceCreateController()
	registry[strings.ToLower(nodepooloperations.OperationNodePoolCreateControllerName)] = registerOperationNodePoolCreateController()
	registry[strings.ToLower(nodepooloperations.OperationNodePoolUpdateControllerName)] = registerOperationNodePoolUpdateController()
	registry[strings.ToLower(nodepooloperations.OperationNodePoolDeleteControllerName)] = registerOperationNodePoolDeleteController()
	registry[strings.ToLower(nodepoolstatus.NodePoolDegradedAggregatorControllerName)] = registerNodePoolDegradedAggregatorController()
	registry[strings.ToLower(nodepoolstatus.NodePoolRequirementsValidAggregatorControllerName)] = registerNodePoolRequirementsValidAggregatorController()
	registry[strings.ToLower(nodepoolvalidation.NodePoolValidationAzureVMSizeSupportsEphemeralOSDiskValidationControllerName)] = registerAzureVMSizeSupportsEphemeralOSDiskValidationController()
	registry[strings.ToLower(nodepoolvalidation.NodePoolValidationAzureNodePoolVMQuotaValidationControllerName)] = registerAzureNodePoolVMQuotaValidationController()
	registry[strings.ToLower(nodepoolvalidation.NodePoolValidationAzureNodePoolNSGBasedRequiredConnectivityValidationControllerName)] = registerNodePoolNSGBasedRequiredConnectivityValidationController()
	registry[strings.ToLower(nodepoolversion.NodepoolVersionControllerName)] = registerNodePoolVersionController()
	registry[strings.ToLower(nodepoolversion.NodePoolActiveVersionsControllerName)] = registerNodePoolActiveVersionController()
	registry[strings.ToLower(nodepoolreaddesires.CreateNodePoolScopedReadDesiresControllerName)] = registerCreateNodePoolScopedReadDesiresController()
	registry[strings.ToLower(nodepoolcreation.CreateServiceProviderNodePoolControllerName)] = registerCreateServiceProviderNodePoolController()
	registry[strings.ToLower(nodepoolversion.TriggerNodePoolUpgradeControllerName)] = registerTriggerNodePoolUpgradeController()
	registry[strings.ToLower(nodepooldeletion.NodePoolClusterServiceDeleteDispatchControllerName)] = registerNodePoolDeletionClusterServiceDeleteDispatchController()
	registry[strings.ToLower(nodepooldeletion.NodePoolDeletionClusterServiceIDClearerControllerName)] = registerNodePoolClusterServiceIDClearerController()
	registry[strings.ToLower(nodepooldeletion.NodePoolChildResourcesCleanupControllerControllerName)] = registerNodePoolChildResourcesCleanupController()
	registry[strings.ToLower(nodepooldeletion.NodePoolDeletionControllerControllerName)] = registerNodePoolDeletionController()
	registry[strings.ToLower(nodepoolupdate.NodePoolClusterServiceUpdateDispatchControllerName)] = registerNodePoolClusterServiceUpdateDispatchController()
}
