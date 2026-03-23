// Copyright 2025 Microsoft Corporation
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

package controllerutils

import (
	"context"
	"errors"
	"time"

	utilruntime "k8s.io/apimachinery/pkg/util/runtime"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/backend/pkg/informers"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/database"
)

type ClusterSyncer interface {
	SyncOnce(ctx context.Context, keyObj HCPClusterKey) error
	CooldownChecker() CooldownChecker
}

type clusterWatchingController struct {
	name   string
	syncer ClusterSyncer

	cosmosClient database.DBClient
}

// NewClusterWatchingController periodically looks up all clusters and queues them
// cooldownDuration is how long to wait before allowing a new notification to fire the controller.
// Since our detection of change is coarse, we are being triggered every few second without new information.
// Until we get a changefeed, the cooldownDuration value is effectively the min resync time.
// This does NOT prevent us from re-executing on errors, so errors will continue to trigger fast checks as expected.
func NewClusterWatchingController(
	name string,
	cosmosClient database.DBClient,
	informers informers.BackendInformers,
	resyncDuration time.Duration,
	syncer ClusterSyncer,
) Controller {

	clusterSyncer := &clusterWatchingController{
		name:         name,
		cosmosClient: cosmosClient,
		syncer:       syncer,
	}
	clusterController := newGenericWatchingController(name, api.ClusterResourceType, clusterSyncer)

	// this happens when unit tests don't want triggering.  This isn't beautiful, but fails to do nothing which is pretty safe.
	if informers != nil {
		clusterInformer, _ := informers.Clusters()
		serviceProviderInformer, _ := informers.ServiceProviderClusters()
		managementClusterContentInformer, _ := informers.ManagementClusterContents()
		err := clusterController.QueueForInformers(resyncDuration, clusterInformer, serviceProviderInformer, managementClusterContentInformer)
		if err != nil {
			panic(err) // coding error
		}
	}

	return clusterController
}

func (c *clusterWatchingController) SyncOnce(ctx context.Context, key HCPClusterKey) error {
	defer utilruntime.HandleCrash(DegradedControllerPanicHandler(
		ctx,
		c.cosmosClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).Controllers(key.HCPClusterName),
		c.name,
		key.InitialController))

	syncErr := c.syncer.SyncOnce(ctx, key) // we'll handle this is a moment.

	controllerWriteErr := WriteController(
		ctx,
		c.cosmosClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).Controllers(key.HCPClusterName),
		c.name,
		key.InitialController,
		ReportSyncError(syncErr),
	)

	return errors.Join(syncErr, controllerWriteErr)
}

func (c *clusterWatchingController) CooldownChecker() CooldownChecker {
	return c.syncer.CooldownChecker()
}

func (c *clusterWatchingController) MakeKey(resourceID *azcorearm.ResourceID) HCPClusterKey {
	return HCPClusterKey{
		SubscriptionID:    resourceID.SubscriptionID,
		ResourceGroupName: resourceID.ResourceGroupName,
		HCPClusterName:    resourceID.Name,
	}
}
