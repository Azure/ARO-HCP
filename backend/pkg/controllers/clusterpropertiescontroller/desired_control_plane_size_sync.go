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

package clusterpropertiescontroller

import (
	"context"
	"fmt"
	"time"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/informers"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/internal/api"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database"
	unionkubeapplierinformers "github.com/Azure/ARO-HCP/internal/database/unioninformers/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// desiredControlPlaneSizeSyncer is a Cluster syncer that observes the
// SRE-selected DesiredHostedClusterControlPlaneSize on the ServiceProviderCluster.
//
// TODO: once cluster-service exposes a control-plane-size knob, this controller
// will forward the value into CS instead of just logging it.
type desiredControlPlaneSizeSyncer struct {
	cooldownChecker              controllerutil.CooldownChecker
	serviceProviderClusterLister listers.ServiceProviderClusterLister
	resourcesDBClient            database.ResourcesDBClient
}

var _ controllerutils.ClusterSyncer = (*desiredControlPlaneSizeSyncer)(nil)

// NewDesiredControlPlaneSizeController creates a controller that reads
// ServiceProviderClusterSpec.DesiredHostedClusterControlPlaneSize from cache
// and reconciles it with cluster-service. For now it only logs the value when
// it is set — see the syncer doc-comment for the planned CS integration.
func NewDesiredControlPlaneSizeController(
	resourcesDBClient database.ResourcesDBClient,
	activeOperationLister listers.ActiveOperationLister,
	informers informers.BackendInformers,
	kubeApplierInformers *unionkubeapplierinformers.UnionKubeApplierInformers,
) controllerutils.Controller {
	_, serviceProviderClusterLister := informers.ServiceProviderClusters()

	syncer := &desiredControlPlaneSizeSyncer{
		cooldownChecker:              controllerutils.DefaultActiveOperationPrioritizingCooldown(activeOperationLister),
		serviceProviderClusterLister: serviceProviderClusterLister,
		resourcesDBClient:            resourcesDBClient,
	}

	return controllerutils.NewClusterWatchingController(
		"DesiredControlPlaneSize",
		resourcesDBClient,
		informers,
		kubeApplierInformers,
		5*time.Minute,
		syncer,
	)
}

func (c *desiredControlPlaneSizeSyncer) CooldownChecker() controllerutil.CooldownChecker {
	return c.cooldownChecker
}

// NeedsWork reports whether the SRE has set a control-plane sizing tier on the
// ServiceProviderCluster. A nil DesiredHostedClusterControlPlaneSize means
// nothing has been requested, so there is nothing for this controller to do.
func (c *desiredControlPlaneSizeSyncer) NeedsWork(spc *api.ServiceProviderCluster) bool {
	if spc == nil {
		return false
	}
	return spc.Spec.DesiredHostedClusterControlPlaneSize != nil
}

func (c *desiredControlPlaneSizeSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	logger := utils.LoggerFromContext(ctx)

	cachedSPC, err := c.serviceProviderClusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if database.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get ServiceProviderCluster from cache: %w", err))
	}
	if !c.NeedsWork(cachedSPC) {
		return nil
	}

	// TODO: once cluster-service exposes a control-plane-size field, replace
	// this log line with the CS write that propagates the value.
	logger.Info("observed DesiredHostedClusterControlPlaneSize",
		"size", *cachedSPC.Spec.DesiredHostedClusterControlPlaneSize,
	)
	return nil
}
