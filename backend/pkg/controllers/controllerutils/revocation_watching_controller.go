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

// RevocationSyncer is the interface that revocation-watching controllers must
// implement. It mirrors ClusterSyncer but is keyed on
// SystemAdminCredentialRevocationKey.
type RevocationSyncer interface {
	SyncOnce(ctx context.Context, keyObj SystemAdminCredentialRevocationKey) error
	CooldownChecker() controllerutil.CooldownChecker
}

type revocationWatchingController struct {
	name   string
	syncer RevocationSyncer

	resourcesDBClient database.ResourcesDBClient
}

// NewRevocationWatchingController creates a controller that fires on individual
// SystemAdminCredentialRevocation informer events. Each revocation drives its own
// lifecycle (marking credential requests for deletion, managing the CRR desires,
// and final teardown), so keying on the revocation lets a small set of focused
// controllers react immediately to revocation changes and re-poll on resync.
func NewRevocationWatchingController(
	name string,
	resourcesDBClient database.ResourcesDBClient,
	backendInformers informers.BackendInformers,
	resyncDuration time.Duration,
	syncer RevocationSyncer,
) Controller {

	revocationSyncer := &revocationWatchingController{
		name:              name,
		resourcesDBClient: resourcesDBClient,
		syncer:            syncer,
	}
	controller := newGenericWatchingController(name, api.SystemAdminCredentialRevocationResourceType, revocationSyncer)

	// this happens when unit tests don't want triggering. This isn't beautiful, but fails to do nothing which is pretty safe.
	if backendInformers != nil {
		revocationInformer, _ := backendInformers.SystemAdminCredentialRevocations()
		err := controller.QueueForInformers(resyncDuration, revocationInformer)
		if err != nil {
			panic(err) // coding error
		}
	}

	return controller
}

func (c *revocationWatchingController) SyncOnce(ctx context.Context, key SystemAdminCredentialRevocationKey) error {
	defer utilruntime.HandleCrash(DegradedControllerPanicHandler(
		ctx,
		c.resourcesDBClient.SystemAdminCredentialRevocations(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName).Controllers(key.RevocationName),
		c.name,
		key.InitialController))

	syncErr := c.syncer.SyncOnce(ctx, key)

	controllerWriteErr := WriteController(
		ctx,
		c.resourcesDBClient.SystemAdminCredentialRevocations(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName).Controllers(key.RevocationName),
		c.name,
		key.InitialController,
		ReportSyncError(syncErr),
	)

	return errors.Join(syncErr, controllerWriteErr)
}

func (c *revocationWatchingController) CooldownChecker() controllerutil.CooldownChecker {
	return c.syncer.CooldownChecker()
}

func (c *revocationWatchingController) MakeKey(resourceID *azcorearm.ResourceID) SystemAdminCredentialRevocationKey {
	// resourceID is of type systemAdminCredentialRevocations, nested under the cluster:
	// /subscriptions/<sub>/resourceGroups/<rg>/providers/microsoft.redhatopenshift/hcpopenshiftclusters/<cluster>/systemAdminCredentialRevocations/<name>
	return SystemAdminCredentialRevocationKey{
		SubscriptionID:    resourceID.SubscriptionID,
		ResourceGroupName: resourceID.ResourceGroupName,
		HCPClusterName:    resourceID.Parent.Name,
		RevocationName:    resourceID.Name,
	}
}
