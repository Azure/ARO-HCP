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

package status

import (
	"context"
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/api/equality"
	apimeta "k8s.io/apimachinery/pkg/api/meta"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/controllers/statusutil"
	"github.com/Azure/ARO-HCP/backend/pkg/informers"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const (
	// clusterRequirementsValidAggregatorControllerName is the controller name used for
	// metrics labels, ctx values, log fields, and the Controller document name.
	clusterRequirementsValidAggregatorControllerName = "ClusterRequirementsValidAggregator"
)

// clusterRequirementsValidAggregator surfaces ServiceProviderCluster.Status.Validations
// up onto HCPOpenShiftCluster.Status.UserFacingConditions as a single
// RequirementsValid condition.
//
// Failed or Unknown validations drive RequirementsValid=False/Degraded, with
// messages aggregated in the same "Source: message" form used by the Degraded
// aggregator. When every validation is True (or none exist) the condition is True/Valid.
type clusterRequirementsValidAggregator struct {
	clusterLister                listers.ClusterLister
	serviceProviderClusterLister listers.ServiceProviderClusterLister
	resourcesDBClient            database.ResourcesDBClient
}

var _ controllerutils.ClusterSyncer = (*clusterRequirementsValidAggregator)(nil)

// NewClusterRequirementsValidAggregatorController creates a controller that
// aggregates ServiceProviderCluster validations onto the cluster's
// Status.UserFacingConditions as RequirementsValid.
func NewClusterRequirementsValidAggregatorController(
	resourcesDBClient database.ResourcesDBClient,
	clusterLister listers.ClusterLister,
	serviceProviderClusterLister listers.ServiceProviderClusterLister,
	informers informers.BackendInformers,
) controllerutils.Controller {
	syncer := &clusterRequirementsValidAggregator{
		clusterLister:                clusterLister,
		serviceProviderClusterLister: serviceProviderClusterLister,
		resourcesDBClient:            resourcesDBClient,
	}
	return controllerutils.NewClusterWatchingController(
		clusterRequirementsValidAggregatorControllerName,
		resourcesDBClient,
		informers,
		nil,
		1*time.Minute,
		syncer,
	)
}

func (c *clusterRequirementsValidAggregator) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	existing, err := c.clusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if database.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get Cluster from cache: %w", err))
	}
	if !c.needsWork(existing) {
		return nil
	}

	serviceProviderCluster, err := c.serviceProviderClusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if database.IsNotFoundError(err) {
		// CreateServiceProviderCluster will populate it. We'll be re-enqueued via the ServiceProviderCluster informer.
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get ServiceProviderCluster from cache: %w", err))
	}

	aggregated := statusutil.AggregateRequirementsValidCondition(serviceProviderCluster.Status.Validations)

	replacement := existing.DeepCopy()
	apimeta.SetStatusCondition(&replacement.Status.UserFacingConditions, aggregated)
	if equality.Semantic.DeepEqual(existing.Status.UserFacingConditions, replacement.Status.UserFacingConditions) {
		return nil
	}

	clusterCRUD := c.resourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName)
	_, err = clusterCRUD.Replace(ctx, replacement, nil)
	if database.IsPreconditionFailedError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to replace Cluster: %w", err))
	}
	return nil
}

// needsWork reports whether this aggregator should update UserFacingConditions
// for the given cluster. Deleting clusters are skipped.
func (c *clusterRequirementsValidAggregator) needsWork(cluster *api.HCPOpenShiftCluster) bool {
	return cluster.ServiceProviderProperties.DeletionTimestamp == nil
}
