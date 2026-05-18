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

type ExternalAuthSyncer interface {
	SyncOnce(ctx context.Context, keyObj HCPExternalAuthKey) error
	CooldownChecker() controllerutil.CooldownChecker
}

type externalAuthWatchingController struct {
	name   string
	syncer ExternalAuthSyncer

	resourcesDBClient database.ResourcesDBClient
}

// NewExternalAuthWatchingController periodically looks up all ExternalAuths and queues them
// cooldownDuration is how long to wait before allowing a new notification to fire the controller.
// Since our detection of change is coarse, we are being triggered every few second without new information.
// Until we get a changefeed, the cooldownDuration value is effectively the min resync time.
// This does NOT prevent us from re-executing on errors, so errors will continue to trigger fast checks as expected.
func NewExternalAuthWatchingController(
	name string,
	resourcesDBClient database.ResourcesDBClient,
	informers informers.BackendInformers,
	resyncDuration time.Duration,
	syncer ExternalAuthSyncer,
) Controller {

	externalAuthController := &externalAuthWatchingController{
		name:              name,
		resourcesDBClient: resourcesDBClient,
		syncer:            syncer,
	}

	externalAuthGenericWatchingController := newGenericWatchingController(name, api.ExternalAuthResourceType, externalAuthController)

	// this happens when unit tests don't want triggering.  This isn't beautiful, but fails to do nothing which is pretty safe.
	if informers != nil {
		externalAuthInformer, _ := informers.ExternalAuths()
		err := externalAuthGenericWatchingController.QueueForInformers(resyncDuration, externalAuthInformer)
		if err != nil {
			panic(err) // coding error
		}
	}

	return externalAuthGenericWatchingController
}

func (c *externalAuthWatchingController) SyncOnce(ctx context.Context, key HCPExternalAuthKey) error {
	defer utilruntime.HandleCrash(DegradedControllerPanicHandler(
		ctx,
		c.resourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).ExternalAuth(key.HCPClusterName).Controllers(key.HCPExternalAuthName),
		c.name,
		key.InitialController))

	syncErr := c.syncer.SyncOnce(ctx, key) // we'll handle this is a moment.

	controllerWriteErr := WriteController(
		ctx,
		c.resourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).ExternalAuth(key.HCPClusterName).Controllers(key.HCPExternalAuthName),
		c.name,
		key.InitialController,
		ReportSyncError(syncErr),
	)

	return errors.Join(syncErr, controllerWriteErr)
}

func (c *externalAuthWatchingController) CooldownChecker() controllerutil.CooldownChecker {
	return c.syncer.CooldownChecker()
}

func (c *externalAuthWatchingController) MakeKey(resourceID *azcorearm.ResourceID) HCPExternalAuthKey {
	return HCPExternalAuthKey{
		SubscriptionID:      resourceID.SubscriptionID,
		ResourceGroupName:   resourceID.ResourceGroupName,
		HCPClusterName:      resourceID.Parent.Name,
		HCPExternalAuthName: resourceID.Name,
	}
}
