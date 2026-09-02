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

package base

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/go-logr/logr"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/tools/cache"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/fleetcosmosstorage"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// ManagementClusterKey identifies a management cluster in the workqueue.
type ManagementClusterKey struct {
	StampIdentifier string
}

func (k ManagementClusterKey) GetResourceID() *azcorearm.ResourceID {
	return metadataapi.Must(fleetapi.ToManagementClusterResourceID(k.StampIdentifier))
}

func (k ManagementClusterKey) AddLoggerValues(logger logr.Logger) logr.Logger {
	return logger.WithValues(
		utils.LogValues{}.
			AddLogValuesForResourceID(k.GetResourceID())...)
}

func (k ManagementClusterKey) InitialController(controllerName string) *coreapi.Controller {
	resourceID := metadataapi.Must(azcorearm.ParseResourceID(
		k.GetResourceID().String() + "/" + fleetapi.ControllerResourceTypeName + "/" + controllerName,
	))
	return &coreapi.Controller{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(k.StampIdentifier),
		},
		ExternalID: k.GetResourceID(),
		Status: coreapi.ControllerStatus{
			Conditions: []metav1.Condition{},
		},
	}
}

// ManagementClusterSyncer is the interface that concrete management cluster
// controllers implement.
type ManagementClusterSyncer interface {
	SyncOnce(ctx context.Context, key ManagementClusterKey) error
	CooldownChecker() controllerutils.CooldownChecker
}

type managementClusterWatchingController struct {
	name          string
	syncer        ManagementClusterSyncer
	fleetDBClient fleetcosmosstorage.FleetDBClient
}

// NewManagementClusterWatchingController creates a controller that watches
// management cluster informers and automatically writes a controller document
// under the management cluster after each sync.
func NewManagementClusterWatchingController(
	name string,
	fleetDBClient fleetcosmosstorage.FleetDBClient,
	managementClusterInformer cache.SharedIndexInformer,
	resyncDuration time.Duration,
	syncer ManagementClusterSyncer,
) Controller {
	mcSyncer := &managementClusterWatchingController{
		name:          name,
		syncer:        syncer,
		fleetDBClient: fleetDBClient,
	}
	mcController := controllerutils.NewGenericWatchingController(
		name, fleetapi.ManagementClusterResourceType, mcSyncer, ReconcileTotal,
	)

	if err := mcController.QueueForInformers(resyncDuration, managementClusterInformer); err != nil {
		panic(err) // coding error
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

func (c *managementClusterWatchingController) CooldownChecker() controllerutils.CooldownChecker {
	return c.syncer.CooldownChecker()
}

func (c *managementClusterWatchingController) MakeKey(resourceID *azcorearm.ResourceID) ManagementClusterKey {
	if resourceID.Parent == nil {
		panic(fmt.Sprintf("management cluster resource ID %q has no parent", resourceID.String()))
	}
	return ManagementClusterKey{
		StampIdentifier: resourceID.Parent.Name,
	}
}
