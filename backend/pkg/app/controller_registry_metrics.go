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
	"github.com/Azure/ARO-HCP/backend/pkg/controllers/metrics"
)

const (
	operationPhaseMetricsControllerName = "operationphasemetrics"
	clusterMetricsControllerName        = "clustermetrics"
	clusterVersionMetricsControllerName = "clusterversionmetrics"
	nodePoolMetricsControllerName       = "nodepoolmetrics"
	externalAuthMetricsControllerName   = "externalauthmetrics"
	clusterInfoMetricsControllerName    = "clusterinfometrics"
)

func registerOperationPhaseMetricsController() ControllerRegistration {
	return ControllerRegistration{
		Workers:     1,
		instantiate: instantiateOperationPhaseMetricsController,
	}
}

func instantiateOperationPhaseMetricsController(controllerContext ControllerContext) (Runnable, error) {
	return metrics.NewController(
		"OperationPhaseMetrics", controllerContext.BackendInformers.AllOperations(), metrics.NewOperationPhaseMetricsHandler(controllerContext.MetricsRegisterer)), nil
}

func registerClusterMetricsController() ControllerRegistration {
	return ControllerRegistration{
		Workers:     1,
		instantiate: instantiateClusterMetricsController,
	}
}

func instantiateClusterMetricsController(controllerContext ControllerContext) (Runnable, error) {
	clusterInformer, _ := controllerContext.BackendInformers.Clusters()
	return metrics.NewController(
		"ClusterMetrics", clusterInformer, metrics.NewClusterMetricsHandler(controllerContext.MetricsRegisterer)), nil
}

func registerClusterVersionMetricsController() ControllerRegistration {
	return ControllerRegistration{
		Workers:     1,
		instantiate: instantiateClusterVersionMetricsController,
	}
}

func instantiateClusterVersionMetricsController(controllerContext ControllerContext) (Runnable, error) {
	serviceProviderClusterInformer, _ := controllerContext.BackendInformers.ServiceProviderClusters()
	_, unionReadDesireLister := controllerContext.UnionKubeApplierInformers.ReadDesires()
	return metrics.NewController(
		"ClusterVersionMetrics", serviceProviderClusterInformer, metrics.NewClusterVersionMetricsHandler(controllerContext.MetricsRegisterer, unionReadDesireLister)), nil
}

func registerNodePoolMetricsController() ControllerRegistration {
	return ControllerRegistration{
		Workers:     1,
		instantiate: instantiateNodePoolMetricsController,
	}
}

func instantiateNodePoolMetricsController(controllerContext ControllerContext) (Runnable, error) {
	nodePoolInformer, _ := controllerContext.BackendInformers.NodePools()
	return metrics.NewController(
		"NodePoolMetrics", nodePoolInformer, metrics.NewNodePoolMetricsHandler(controllerContext.MetricsRegisterer)), nil
}

func registerExternalAuthMetricsController() ControllerRegistration {
	return ControllerRegistration{
		Workers:     1,
		instantiate: instantiateExternalAuthMetricsController,
	}
}

func instantiateExternalAuthMetricsController(controllerContext ControllerContext) (Runnable, error) {
	externalAuthInformer, _ := controllerContext.BackendInformers.ExternalAuths()
	return metrics.NewController(
		"ExternalAuthMetrics", externalAuthInformer, metrics.NewExternalAuthMetricsHandler(controllerContext.MetricsRegisterer)), nil
}

func registerClusterInfoMetricsController() ControllerRegistration {
	return ControllerRegistration{
		Workers:     1,
		instantiate: instantiateClusterInfoMetricsController,
	}
}

func instantiateClusterInfoMetricsController(controllerContext ControllerContext) (Runnable, error) {
	serviceProviderClusterInformer, _ := controllerContext.BackendInformers.ServiceProviderClusters()
	return metrics.NewController(
		"ClusterInfoMetrics", serviceProviderClusterInformer, metrics.NewClusterInfoMetricsHandler(controllerContext.MetricsRegisterer)), nil
}
