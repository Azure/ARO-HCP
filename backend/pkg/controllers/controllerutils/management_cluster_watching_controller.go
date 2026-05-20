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

	"github.com/go-logr/logr"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/fleet"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database"
	dbinformers "github.com/Azure/ARO-HCP/internal/database/informers"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type ManagementClusterKey struct {
	StampIdentifier string `json:"stampIdentifier"`
}

func (k ManagementClusterKey) GetResourceID() *azcorearm.ResourceID {
	return api.Must(fleet.ToManagementClusterResourceID(k.StampIdentifier))
}

func (k ManagementClusterKey) AddLoggerValues(logger logr.Logger) logr.Logger {
	return logger.WithValues(
		utils.LogValues{}.
			AddLogValuesForResourceID(k.GetResourceID())...)
}

func (k ManagementClusterKey) InitialController(controllerName string) *api.Controller {
	resourceID := api.Must(azcorearm.ParseResourceID(k.GetResourceID().String() + "/" + fleet.ControllerResourceTypeName + "/" + controllerName))
	return &api.Controller{
		CosmosMetadata: api.CosmosMetadata{
			ResourceID: resourceID,
		},
		ExternalID: k.GetResourceID(),
		Status: api.ControllerStatus{
			Conditions: []metav1.Condition{},
		},
	}
}

type ManagementClusterSyncer interface {
	SyncOnce(ctx context.Context, key ManagementClusterKey) error
	CooldownChecker() controllerutil.CooldownChecker
}

type managementClusterWatchingController struct {
	name          string
	syncer        ManagementClusterSyncer
	fleetDBClient database.FleetDBClient
}

// NewManagementClusterWatchingController periodically looks up all management clusters and queues them.
func NewManagementClusterWatchingController(
	name string,
	fleetDBClient database.FleetDBClient,
	fleetInformers dbinformers.FleetInformers,
	resyncDuration time.Duration,
	syncer ManagementClusterSyncer,
) Controller {
	mcSyncer := &managementClusterWatchingController{
		name:          name,
		syncer:        syncer,
		fleetDBClient: fleetDBClient,
	}
	mcController := newGenericWatchingController(name, fleet.ManagementClusterResourceType, mcSyncer)

	// this happens when unit tests don't want triggering.  This isn't beautiful, but fails to do nothing which is pretty safe.
	if fleetInformers != nil {
		managementClusterInformer, _ := fleetInformers.ManagementClusters()
		err := mcController.QueueForInformers(resyncDuration, managementClusterInformer)
		if err != nil {
			panic(err) // coding error
		}
	}

	return mcController
}

func (c *managementClusterWatchingController) SyncOnce(ctx context.Context, key ManagementClusterKey) error {
	controllerCRUD := c.fleetDBClient.Stamps().ManagementClusters(key.StampIdentifier).Controllers()

	defer utilruntime.HandleCrash(DegradedControllerPanicHandler(
		ctx,
		controllerCRUD,
		c.name,
		key.InitialController))

	syncErr := c.syncer.SyncOnce(ctx, key)

	controllerWriteErr := WriteController(
		ctx,
		controllerCRUD,
		c.name,
		key.InitialController,
		ReportSyncError(syncErr),
	)

	return errors.Join(syncErr, controllerWriteErr)
}

func (c *managementClusterWatchingController) CooldownChecker() controllerutil.CooldownChecker {
	return c.syncer.CooldownChecker()
}

func (c *managementClusterWatchingController) MakeKey(resourceID *azcorearm.ResourceID) ManagementClusterKey {
	return ManagementClusterKey{
		StampIdentifier: resourceID.Parent.Name,
	}
}
