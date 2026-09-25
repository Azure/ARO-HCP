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
	"github.com/Azure/ARO-HCP/backend/pkg/controllers/datadump"
)

const (
	subscriptionNonClusterDataDumpControllerName = "subscriptionnonclusterdatadump"
	clusterRecursiveDataDumpControllerName       = "datadump"
	csStateDumpControllerName                    = "csstatedump"
	billingDumpControllerName                    = "billingdump"
	managementClusterDumpControllerName          = "managementclusterdatadump"
)

func registerSubscriptionNonClusterDataDumpController() ControllerRegistration {
	return ControllerRegistration{
		Workers:     20,
		instantiate: instantiateSubscriptionNonClusterDataDumpController,
	}
}

func instantiateSubscriptionNonClusterDataDumpController(controllerContext ControllerContext) (Runnable, error) {
	return datadump.NewSubscriptionNonClusterDataDumpController(controllerContext.ResourcesDBClient, controllerContext.BackendInformers), nil
}

func registerClusterRecursiveDataDumpController() ControllerRegistration {
	return ControllerRegistration{
		Workers:     20,
		instantiate: instantiateClusterRecursiveDataDumpController,
	}
}

func instantiateClusterRecursiveDataDumpController(controllerContext ControllerContext) (Runnable, error) {
	_, activeOperationLister := controllerContext.BackendInformers.ActiveOperations()
	_, managementClusterLister := controllerContext.FleetInformers.ManagementClusters()
	return datadump.NewClusterRecursiveDataDumpController(controllerContext.ResourcesDBClient, controllerContext.KubeApplierDBClients, managementClusterLister, activeOperationLister, controllerContext.BackendInformers, controllerContext.UnionKubeApplierInformers), nil
}

func registerCsStateDumpController() ControllerRegistration {
	return ControllerRegistration{
		Workers:     20,
		instantiate: instantiateCsStateDumpController,
	}
}

func instantiateCsStateDumpController(controllerContext ControllerContext) (Runnable, error) {
	_, activeOperationLister := controllerContext.BackendInformers.ActiveOperations()
	return datadump.NewCSStateDumpController(controllerContext.ResourcesDBClient, activeOperationLister, controllerContext.BackendInformers, controllerContext.UnionKubeApplierInformers, controllerContext.ClustersServiceClient), nil
}

func registerBillingDumpController() ControllerRegistration {
	return ControllerRegistration{
		Workers:     20,
		instantiate: instantiateBillingDumpController,
	}
}

func instantiateBillingDumpController(controllerContext ControllerContext) (Runnable, error) {
	_, activeOperationLister := controllerContext.BackendInformers.ActiveOperations()
	return datadump.NewBillingDumpController(controllerContext.ResourcesDBClient, controllerContext.BillingDBClient, activeOperationLister, controllerContext.BackendInformers, controllerContext.UnionKubeApplierInformers), nil
}

func registerManagementClusterDumpController() ControllerRegistration {
	return ControllerRegistration{
		Workers:     20,
		instantiate: instantiateManagementClusterDumpController,
	}
}

func instantiateManagementClusterDumpController(controllerContext ControllerContext) (Runnable, error) {
	_, managementClusterLister := controllerContext.FleetInformers.ManagementClusters()
	return datadump.NewManagementClusterDataDumpController(controllerContext.FleetDBClient, managementClusterLister, controllerContext.FleetInformers), nil
}
