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

package metrics

import (
	"strings"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerconfig"
)

func registerOperationPhaseMetricsController() controllerconfig.ControllerRegistration {
	return controllerconfig.ControllerRegistration{
		Workers:     1,
		Instantiate: controllerconfig.WithCacheSyncs(instantiateOperationPhaseMetricsController, false),
	}
}

func instantiateOperationPhaseMetricsController(controllerContext controllerconfig.ControllerContext) (controllerconfig.Runnable, error) {
	return NewController(
		OperationPhaseMetricsControllerName, controllerContext.BackendInformers.AllOperations(), NewOperationPhaseMetricsHandler(controllerContext.MetricsRegisterer)), nil
}

func registerClusterMetricsController() controllerconfig.ControllerRegistration {
	return controllerconfig.ControllerRegistration{
		Workers:     1,
		Instantiate: controllerconfig.WithCacheSyncs(instantiateClusterMetricsController, false),
	}
}

func instantiateClusterMetricsController(controllerContext controllerconfig.ControllerContext) (controllerconfig.Runnable, error) {
	clusterInformer, _ := controllerContext.BackendInformers.Clusters()
	return NewController(
		ClusterMetricsControllerName, clusterInformer, NewClusterMetricsHandler(controllerContext.MetricsRegisterer)), nil
}

func registerClusterVersionMetricsController() controllerconfig.ControllerRegistration {
	return controllerconfig.ControllerRegistration{
		Workers:     1,
		Instantiate: controllerconfig.WithCacheSyncs(instantiateClusterVersionMetricsController, true),
	}
}

func instantiateClusterVersionMetricsController(controllerContext controllerconfig.ControllerContext) (controllerconfig.Runnable, error) {
	serviceProviderClusterInformer, _ := controllerContext.BackendInformers.ServiceProviderClusters()
	_, unionReadDesireLister := controllerContext.UnionKubeApplierInformers.ReadDesires()
	return NewController(
		ClusterVersionMetricsControllerName, serviceProviderClusterInformer, NewClusterVersionMetricsHandler(controllerContext.MetricsRegisterer, unionReadDesireLister)), nil
}

func registerNodePoolMetricsController() controllerconfig.ControllerRegistration {
	return controllerconfig.ControllerRegistration{
		Workers:     1,
		Instantiate: controllerconfig.WithCacheSyncs(instantiateNodePoolMetricsController, false),
	}
}

func instantiateNodePoolMetricsController(controllerContext controllerconfig.ControllerContext) (controllerconfig.Runnable, error) {
	nodePoolInformer, _ := controllerContext.BackendInformers.NodePools()
	return NewController(
		NodePoolMetricsControllerName, nodePoolInformer, NewNodePoolMetricsHandler(controllerContext.MetricsRegisterer)), nil
}

func registerExternalAuthMetricsController() controllerconfig.ControllerRegistration {
	return controllerconfig.ControllerRegistration{
		Workers:     1,
		Instantiate: controllerconfig.WithCacheSyncs(instantiateExternalAuthMetricsController, false),
	}
}

func instantiateExternalAuthMetricsController(controllerContext controllerconfig.ControllerContext) (controllerconfig.Runnable, error) {
	externalAuthInformer, _ := controllerContext.BackendInformers.ExternalAuths()
	return NewController(
		ExternalAuthMetricsControllerName, externalAuthInformer, NewExternalAuthMetricsHandler(controllerContext.MetricsRegisterer)), nil
}

func registerClusterInfoMetricsController() controllerconfig.ControllerRegistration {
	return controllerconfig.ControllerRegistration{
		Workers:     1,
		Instantiate: controllerconfig.WithCacheSyncs(instantiateClusterInfoMetricsController, false),
	}
}

func instantiateClusterInfoMetricsController(controllerContext controllerconfig.ControllerContext) (controllerconfig.Runnable, error) {
	serviceProviderClusterInformer, _ := controllerContext.BackendInformers.ServiceProviderClusters()
	return NewController(
		ClusterInfoMetricsControllerName, serviceProviderClusterInformer, NewClusterInfoMetricsHandler(controllerContext.MetricsRegisterer)), nil
}

func Register(registry map[string]controllerconfig.ControllerRegistration) {
	registry[strings.ToLower(OperationPhaseMetricsControllerName)] = registerOperationPhaseMetricsController()
	registry[strings.ToLower(ClusterMetricsControllerName)] = registerClusterMetricsController()
	registry[strings.ToLower(ClusterVersionMetricsControllerName)] = registerClusterVersionMetricsController()
	registry[strings.ToLower(NodePoolMetricsControllerName)] = registerNodePoolMetricsController()
	registry[strings.ToLower(ExternalAuthMetricsControllerName)] = registerExternalAuthMetricsController()
	registry[strings.ToLower(ClusterInfoMetricsControllerName)] = registerClusterInfoMetricsController()
}
