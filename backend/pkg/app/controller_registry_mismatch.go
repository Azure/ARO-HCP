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

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/mismatch"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
)

const (
	clusterServiceMatchingClusterControllerName = "clusterservicematchingclusters"
	deleteOrphanedCosmosResourcesControllerName = "deleteorphanedcosmosresources"
	missingResourceIDControllerName             = "missingresourceid"
	backfillClusterUIDControllerName            = "backfillclusteruid"
)

func registerClusterServiceMatchingClusterController() ControllerRegistration {
	return ControllerRegistration{
		Workers:     20,
		instantiate: instantiateClusterServiceMatchingClusterController,
	}
}

func instantiateClusterServiceMatchingClusterController(controllerContext ControllerContext) (Runnable, error) {
	_, subscriptionLister := controllerContext.BackendInformers.Subscriptions()
	return mismatch.NewClusterServiceClusterMatchingController(controllerContext.Clock, controllerContext.ResourcesDBClient, subscriptionLister, controllerContext.ClustersServiceClient), nil
}

func registerDeleteOrphanedCosmosResourcesController() ControllerRegistration {
	return ControllerRegistration{
		Workers:     10,
		instantiate: instantiateDeleteOrphanedCosmosResourcesController,
	}
}

func instantiateDeleteOrphanedCosmosResourcesController(controllerContext ControllerContext) (Runnable, error) {
	_, subscriptionLister := controllerContext.BackendInformers.Subscriptions()
	_, managementClusterLister := controllerContext.FleetInformers.ManagementClusters()
	return mismatch.NewDeleteOrphanedCosmosResourcesController(controllerContext.ResourcesDBClient, controllerContext.KubeApplierDBClients, subscriptionLister, managementClusterLister), nil
}

func registerMissingResourceIDController() ControllerRegistration {
	return ControllerRegistration{
		Workers:     20,
		instantiate: instantiateMissingResourceIDController,
	}
}

func instantiateMissingResourceIDController(controllerContext ControllerContext) (Runnable, error) {
	return mismatch.NewMissingResourceIDController(controllerContext.ResourcesDBClient), nil
}

func registerBackfillClusterUIDController() ControllerRegistration {
	return ControllerRegistration{
		Workers:     20,
		instantiate: instantiateBackfillClusterUIDController,
	}
}

func instantiateBackfillClusterUIDController(controllerContext ControllerContext) (Runnable, error) {
	_, clusterLister := controllerContext.BackendInformers.Clusters()
	return controllerutils.NewClusterWatchingController(
		"BackfillClusterUID", controllerContext.ResourcesDBClient, controllerContext.BackendInformers, controllerContext.UnionKubeApplierInformers, 60*time.Minute,
		mismatch.NewBackfillClusterUIDController(controllerContext.Clock, controllerContext.ResourcesDBClient, controllerContext.BillingDBClient, clusterLister)), nil
}
