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

package controllerutils

import (
	"context"
	"errors"
	"time"

	utilruntime "k8s.io/apimachinery/pkg/util/runtime"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/backend/pkg/informers"
	"github.com/Azure/ARO-HCP/internal/api"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database"
)

type SystemAdminRevocationSyncer interface {
	SyncOnce(ctx context.Context, keyObj HCPSystemAdminRevocationKey) error
	CooldownChecker() controllerutil.CooldownChecker
}

type systemAdminRevocationWatchingController struct {
	name   string
	syncer SystemAdminRevocationSyncer

	resourcesDBClient database.ResourcesDBClient
}

// NewSystemAdminRevocationWatchingController periodically looks up all
// SystemAdminRevocations and queues them. resyncDuration is the
// cooldown before allowing a new notification to fire the controller.
func NewSystemAdminRevocationWatchingController(
	name string,
	resourcesDBClient database.ResourcesDBClient,
	informers informers.BackendInformers,
	resyncDuration time.Duration,
	syncer SystemAdminRevocationSyncer,
) Controller {
	revocationController := &systemAdminRevocationWatchingController{
		name:              name,
		resourcesDBClient: resourcesDBClient,
		syncer:            syncer,
	}
	genericController := newGenericWatchingController(name, api.SystemAdminRevocationResourceType, revocationController)

	if informers != nil {
		revocationInformer, _ := informers.SystemAdminRevocations()
		err := genericController.QueueForInformers(resyncDuration, revocationInformer)
		if err != nil {
			panic(err) // coding error
		}
	}

	return genericController
}

func (c *systemAdminRevocationWatchingController) SyncOnce(ctx context.Context, key HCPSystemAdminRevocationKey) error {
	defer utilruntime.HandleCrash(DegradedControllerPanicHandler(
		ctx,
		c.resourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).SystemAdminRevocations(key.HCPClusterName).Controllers(key.HCPSystemAdminRevocationName),
		c.name,
		key.InitialController))

	syncErr := c.syncer.SyncOnce(ctx, key)

	controllerWriteErr := WriteController(
		ctx,
		c.resourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).SystemAdminRevocations(key.HCPClusterName).Controllers(key.HCPSystemAdminRevocationName),
		c.name,
		key.InitialController,
		ReportSyncError(syncErr),
	)

	return errors.Join(syncErr, controllerWriteErr)
}

func (c *systemAdminRevocationWatchingController) CooldownChecker() controllerutil.CooldownChecker {
	return c.syncer.CooldownChecker()
}

func (c *systemAdminRevocationWatchingController) MakeKey(resourceID *azcorearm.ResourceID) HCPSystemAdminRevocationKey {
	return HCPSystemAdminRevocationKey{
		SubscriptionID:               resourceID.SubscriptionID,
		ResourceGroupName:            resourceID.ResourceGroupName,
		HCPClusterName:               resourceID.Parent.Name,
		HCPSystemAdminRevocationName: resourceID.Name,
	}
}
