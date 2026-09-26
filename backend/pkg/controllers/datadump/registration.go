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

package datadump

import (
	"strings"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerconfig"
)

func registerSubscriptionNonClusterDataDumpController() controllerconfig.ControllerRegistration {
	return controllerconfig.ControllerRegistration{
		Workers:     20,
		Instantiate: controllerconfig.WithCacheSyncs(instantiateSubscriptionNonClusterDataDumpController, false),
	}
}

func instantiateSubscriptionNonClusterDataDumpController(controllerContext controllerconfig.ControllerContext) (controllerconfig.Runnable, error) {
	return NewSubscriptionNonClusterDataDumpController(controllerContext.ResourcesDBClient, controllerContext.BackendInformers), nil
}

func registerClusterRecursiveDataDumpController() controllerconfig.ControllerRegistration {
	return controllerconfig.ControllerRegistration{
		Workers:     20,
		Instantiate: controllerconfig.WithCacheSyncs(instantiateClusterRecursiveDataDumpController, true),
	}
}

func instantiateClusterRecursiveDataDumpController(controllerContext controllerconfig.ControllerContext) (controllerconfig.Runnable, error) {
	_, activeOperationLister := controllerContext.BackendInformers.ActiveOperations()
	_, managementClusterLister := controllerContext.FleetInformers.ManagementClusters()
	return NewClusterRecursiveDataDumpController(controllerContext.ResourcesDBClient, controllerContext.KubeApplierDBClients, managementClusterLister, activeOperationLister, controllerContext.BackendInformers, controllerContext.UnionKubeApplierInformers), nil
}

func registerCsStateDumpController() controllerconfig.ControllerRegistration {
	return controllerconfig.ControllerRegistration{
		Workers:     20,
		Instantiate: controllerconfig.WithCacheSyncs(instantiateCsStateDumpController, true),
	}
}

func instantiateCsStateDumpController(controllerContext controllerconfig.ControllerContext) (controllerconfig.Runnable, error) {
	_, activeOperationLister := controllerContext.BackendInformers.ActiveOperations()
	return NewCSStateDumpController(controllerContext.ResourcesDBClient, activeOperationLister, controllerContext.BackendInformers, controllerContext.UnionKubeApplierInformers, controllerContext.ClustersServiceClient), nil
}

func registerBillingDumpController() controllerconfig.ControllerRegistration {
	return controllerconfig.ControllerRegistration{
		Workers:     20,
		Instantiate: controllerconfig.WithCacheSyncs(instantiateBillingDumpController, true),
	}
}

func instantiateBillingDumpController(controllerContext controllerconfig.ControllerContext) (controllerconfig.Runnable, error) {
	_, activeOperationLister := controllerContext.BackendInformers.ActiveOperations()
	return NewBillingDumpController(controllerContext.ResourcesDBClient, controllerContext.BillingDBClient, activeOperationLister, controllerContext.BackendInformers, controllerContext.UnionKubeApplierInformers), nil
}

func registerManagementClusterDumpController() controllerconfig.ControllerRegistration {
	return controllerconfig.ControllerRegistration{
		Workers:     20,
		Instantiate: controllerconfig.WithCacheSyncs(instantiateManagementClusterDumpController, false),
	}
}

func instantiateManagementClusterDumpController(controllerContext controllerconfig.ControllerContext) (controllerconfig.Runnable, error) {
	_, managementClusterLister := controllerContext.FleetInformers.ManagementClusters()
	return NewManagementClusterDataDumpController(controllerContext.FleetDBClient, managementClusterLister, controllerContext.FleetInformers), nil
}

func Register(registry map[string]controllerconfig.ControllerRegistration) {
	registry[strings.ToLower(SubscriptionNonClusterDataDumpControllerName)] = registerSubscriptionNonClusterDataDumpController()
	registry[strings.ToLower(DataDumpControllerName)] = registerClusterRecursiveDataDumpController()
	registry[strings.ToLower(CSStateDumpControllerName)] = registerCsStateDumpController()
	registry[strings.ToLower(BillingDumpControllerName)] = registerBillingDumpController()
	registry[strings.ToLower(ManagementClusterDataDumpControllerName)] = registerManagementClusterDumpController()
}
