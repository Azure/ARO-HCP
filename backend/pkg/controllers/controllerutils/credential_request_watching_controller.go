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
	unionkubeapplierinformers "github.com/Azure/ARO-HCP/internal/database/unioninformers/kubeapplier"
)

// CredentialRequestSyncer is the interface that credential-request-watching
// controllers must implement. It mirrors ClusterSyncer but is keyed on
// SystemAdminCredentialRequestKey.
type CredentialRequestSyncer interface {
	SyncOnce(ctx context.Context, keyObj SystemAdminCredentialRequestKey) error
	CooldownChecker() controllerutil.CooldownChecker
}

type credentialRequestWatchingController struct {
	name   string
	syncer CredentialRequestSyncer

	resourcesDBClient database.ResourcesDBClient
}

// NewCredentialRequestWatchingController creates a controller that fires on
// individual SystemAdminCredentialRequest informer events rather than
// cluster-level events. This ensures controllers react immediately when a
// credential request is created or updated, instead of relying on periodic
// cluster resync.
//
// kubeApplierInformers is optional: when non-nil, the controller also enqueues
// on ReadDesire events from the union kube-applier informer surface (the same
// pattern as ClusterWatchingController, but walking up to the credential
// request resource type instead of the cluster type).
func NewCredentialRequestWatchingController(
	name string,
	resourcesDBClient database.ResourcesDBClient,
	backendInformers informers.BackendInformers,
	kubeApplierInformers *unionkubeapplierinformers.UnionKubeApplierInformers,
	resyncDuration time.Duration,
	syncer CredentialRequestSyncer,
) Controller {

	credSyncer := &credentialRequestWatchingController{
		name:              name,
		resourcesDBClient: resourcesDBClient,
		syncer:            syncer,
	}
	controller := newGenericWatchingController(name, api.SystemAdminCredentialRequestResourceType, credSyncer)

	// this happens when unit tests don't want triggering. This isn't beautiful, but fails to do nothing which is pretty safe.
	if backendInformers != nil {
		credentialRequestInformer, _ := backendInformers.SystemAdminCredentialRequests()
		err := controller.QueueForInformers(resyncDuration, credentialRequestInformer)
		if err != nil {
			panic(err) // coding error
		}
	}

	if kubeApplierInformers != nil {
		// ReadDesires for credential requests sit two levels below the credential
		// request: .../hcpOpenShiftClusters/<cluster>/systemAdminCredentialRequests/<cred>/readDesires/<name>
		// maxDepth of 1 means we walk up one parent to the credential request.
		readDesireInformer, _ := kubeApplierInformers.ReadDesires()
		if err := controller.QueueForInformersWithMaxDepth(resyncDuration, 1, readDesireInformer); err != nil {
			panic(err) // coding error
		}
	}

	return controller
}

func (c *credentialRequestWatchingController) SyncOnce(ctx context.Context, key SystemAdminCredentialRequestKey) error {
	defer utilruntime.HandleCrash(DegradedControllerPanicHandler(
		ctx,
		c.resourcesDBClient.SystemAdminCredentialRequests(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName).Controllers(key.CredentialName),
		c.name,
		key.InitialController))

	syncErr := c.syncer.SyncOnce(ctx, key) // we'll handle this in a moment.

	controllerWriteErr := WriteController(
		ctx,
		c.resourcesDBClient.SystemAdminCredentialRequests(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName).Controllers(key.CredentialName),
		c.name,
		key.InitialController,
		ReportSyncError(syncErr),
	)

	return errors.Join(syncErr, controllerWriteErr)
}

func (c *credentialRequestWatchingController) CooldownChecker() controllerutil.CooldownChecker {
	return c.syncer.CooldownChecker()
}

func (c *credentialRequestWatchingController) MakeKey(resourceID *azcorearm.ResourceID) SystemAdminCredentialRequestKey {
	// resourceID is of type systemAdminCredentialRequests, nested under the cluster:
	// /subscriptions/<sub>/resourceGroups/<rg>/providers/microsoft.redhatopenshift/hcpopenshiftclusters/<cluster>/systemAdminCredentialRequests/<credName>
	return SystemAdminCredentialRequestKey{
		SubscriptionID:    resourceID.SubscriptionID,
		ResourceGroupName: resourceID.ResourceGroupName,
		HCPClusterName:    resourceID.Parent.Name,
		CredentialName:    resourceID.Name,
	}
}
