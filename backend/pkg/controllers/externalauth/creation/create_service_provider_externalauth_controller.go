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

package creation

import (
	"context"
	"fmt"
	"time"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/informers/coreinformers"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// createServiceProviderExternalAuthSyncer ensures a ServiceProviderExternalAuth
// document exists for every HCPOpenShiftClusterExternalAuth. Consumer backend
// controllers (status, aggregation) read the ServiceProviderExternalAuth through
// a cached lister and bail out when it is missing; this syncer is the single
// place in backend controllers that actually creates the document.
type createServiceProviderExternalAuthSyncer struct {
	resourcesDBClient                 corecosmosstorage.ResourcesDBClient
	externalAuthLister                corelisters.ExternalAuthLister
	serviceProviderExternalAuthLister corelisters.ServiceProviderExternalAuthLister
}

var _ controllerutils.ExternalAuthSyncer = (*createServiceProviderExternalAuthSyncer)(nil)

// NewCreateServiceProviderExternalAuthController wires the controller that creates
// missing ServiceProviderExternalAuth documents.
func NewCreateServiceProviderExternalAuthController(
	resourcesDBClient corecosmosstorage.ResourcesDBClient,
	externalAuthLister corelisters.ExternalAuthLister,
	serviceProviderExternalAuthLister corelisters.ServiceProviderExternalAuthLister,
	backendInformers coreinformers.BackendInformers,
) controllerutils.Controller {
	syncer := &createServiceProviderExternalAuthSyncer{
		resourcesDBClient:                 resourcesDBClient,
		externalAuthLister:                externalAuthLister,
		serviceProviderExternalAuthLister: serviceProviderExternalAuthLister,
	}

	return controllerutils.NewExternalAuthWatchingController(
		"CreateServiceProviderExternalAuth",
		resourcesDBClient,
		backendInformers,
		1*time.Minute,
		syncer,
	)
}

// SyncOnce creates a ServiceProviderExternalAuth for the given ExternalAuth when
// one does not already exist. The lister is consulted first so steady-state
// runs avoid a Cosmos round-trip; if it is missing, GetOrCreate is called and
// any 409 conflict is handled by the underlying helper.
//
// Once the parent external auth is marked for deletion the deletion pipeline
// removes the ServiceProviderExternalAuth as a child resource; recreating it
// here would prevent that pipeline from converging, so we short-circuit.
func (c *createServiceProviderExternalAuthSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPExternalAuthKey) error {
	existingExternalAuth, err := c.externalAuthLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, key.HCPExternalAuthName)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get ExternalAuth from lister: %w", err))
	}
	if existingExternalAuth.ServiceProviderProperties.DeletionTimestamp != nil {
		return nil
	}

	_, err = c.serviceProviderExternalAuthLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, key.HCPExternalAuthName)
	if err == nil {
		return nil
	}
	if !cosmosstorageutils.IsNotFoundError(err) {
		return utils.TrackError(fmt.Errorf("failed to get ServiceProviderExternalAuth from lister: %w", err))
	}

	if _, err := corecosmosstorage.GetOrCreateServiceProviderExternalAuth(ctx, c.resourcesDBClient, key.GetResourceID()); err != nil {
		return utils.TrackError(fmt.Errorf("failed to create ServiceProviderExternalAuth: %w", err))
	}

	return nil
}
