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

package clustersserviceregistration

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/client-go/tools/cache"

	arohcpv1alpha1 "github.com/openshift-online/ocm-api-model/clientapi/arohcp/v1alpha1"
	ocmerrors "github.com/openshift-online/ocm-sdk-go/errors"

	fleetcontrollers "github.com/Azure/ARO-HCP/fleet/pkg/controllers/base"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/fleet"
	"github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/database/listers"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type ProvisionShardClient interface {
	GetProvisionShard(ctx context.Context, internalID api.InternalID) (*arohcpv1alpha1.ProvisionShard, error)
	ListProvisionShards() ocm.ProvisionShardListIterator
	PostProvisionShard(ctx context.Context, builder *arohcpv1alpha1.ProvisionShardBuilder) (*arohcpv1alpha1.ProvisionShard, error)
	UpdateProvisionShard(ctx context.Context, internalID api.InternalID, builder *arohcpv1alpha1.ProvisionShardBuilder) (*arohcpv1alpha1.ProvisionShard, error)
}

type clustersServiceRegistrationSyncer struct {
	fleetDBClient         database.FleetDBClient
	clustersServiceClient ProvisionShardClient
	stampLister           listers.StampLister
	region                string
}

// NewClustersServiceRegistrationController creates a StampWatchingController
// that reconciles ClustersService provision shards from ManagementCluster documents.
func NewClustersServiceRegistrationController(
	managementClusterInformer cache.SharedIndexInformer,
	stampInformer cache.SharedIndexInformer,
	fleetDBClient database.FleetDBClient,
	clustersServiceClient ProvisionShardClient,
	stampLister listers.StampLister,
	region string,
	cfg fleetcontrollers.StampWatchingControllerConfig,
) *fleetcontrollers.StampWatchingController {
	syncer := &clustersServiceRegistrationSyncer{
		fleetDBClient:         fleetDBClient,
		clustersServiceClient: clustersServiceClient,
		stampLister:           stampLister,
		region:                region,
	}

	controller := fleetcontrollers.NewStampWatchingController(
		"ClustersServiceRegistrationController",
		syncer,
		cfg,
	)

	if err := controller.QueueForInformers(fleetcontrollers.DefaultInformerResyncPeriod, managementClusterInformer, stampInformer); err != nil {
		panic(err) // coding error
	}

	return controller
}

func (s *clustersServiceRegistrationSyncer) SyncOnce(ctx context.Context, key fleetcontrollers.StampKey) error {
	managementClusterCRUD := s.fleetDBClient.Stamps().ManagementClusters(key.StampIdentifier)
	managementCluster, err := managementClusterCRUD.Get(ctx, fleet.ManagementClusterResourceName)
	if err != nil {
		if database.IsNotFoundError(err) {
			return nil
		}
		return utils.TrackError(err)
	}

	stamp, err := s.stampLister.Get(ctx, key.StampIdentifier)
	if err != nil {
		return utils.TrackError(err)
	}

	updated := managementCluster.DeepCopy()

	conditionType := string(fleet.ManagementClusterConditionClustersServiceRegistered)

	var syncErr error
	if !apimeta.IsStatusConditionTrue(stamp.Status.Conditions, string(fleet.StampConditionApproved)) {
		// an unapproved stamp is not a sync error and does not need a workqueue retry
		// we update the condition though to reflect the fact
		fleetcontrollers.SetRegistrationCondition(&updated.Status.Conditions, conditionType, fleetcontrollers.ErrStampNotApproved)
	} else {
		var shardID *api.InternalID
		shardID, syncErr = s.reconcileProvisionShard(ctx, updated)
		if shardID != nil && updated.Status.ClusterServiceProvisionShardID == nil {
			updated.Status.ClusterServiceProvisionShardID = shardID
		}
		fleetcontrollers.SetRegistrationCondition(&updated.Status.Conditions, conditionType, syncErr)
	}

	if controllerutils.NeedsUpdate(managementCluster, updated) {
		if _, writeErr := managementClusterCRUD.Replace(ctx, updated, managementCluster, nil); writeErr != nil {
			return utils.TrackError(writeErr)
		}
	}

	if syncErr != nil {
		return utils.TrackError(syncErr)
	}

	return nil
}

func (s *clustersServiceRegistrationSyncer) reconcileProvisionShard(
	ctx context.Context,
	managementCluster *fleet.ManagementCluster,
) (*api.InternalID, error) {
	logger := utils.LoggerFromContext(ctx)

	existingID, err := s.findExistingProvisionShard(ctx, managementCluster)
	if err != nil {
		return nil, err
	}

	if existingID != nil {
		builder, err := buildProvisionShardForUpdate(managementCluster)
		if err != nil {
			return nil, err
		}
		updated, err := s.clustersServiceClient.UpdateProvisionShard(ctx, *existingID, builder)
		if err != nil {
			return nil, fmt.Errorf("updating provision shard: %w", err)
		}
		shardID, err := api.NewInternalID(updated.HREF())
		if err != nil {
			return nil, fmt.Errorf("parsing updated provision shard HREF: %w", err)
		}
		logger.Info("provision shard updated", "provisionShardID", shardID)
		return &shardID, nil
	}

	createBuilder, err := buildProvisionShardForCreate(managementCluster, s.region)
	if err != nil {
		return nil, fmt.Errorf("building provision shard: %w", err)
	}
	created, err := s.clustersServiceClient.PostProvisionShard(ctx, createBuilder)
	if err != nil {
		return nil, fmt.Errorf("creating provision shard: %w", err)
	}
	createdID, err := api.NewInternalID(created.HREF())
	if err != nil {
		return nil, fmt.Errorf("parsing created provision shard HREF: %w", err)
	}

	// CS API ignores the status field on create (defaults to maintenance).
	// A separate update is always needed to set the desired status.
	updateBuilder, err := buildProvisionShardForUpdate(managementCluster)
	if err != nil {
		return nil, err
	}
	if _, err := s.clustersServiceClient.UpdateProvisionShard(ctx, createdID, updateBuilder); err != nil {
		return nil, fmt.Errorf("setting provision shard status after create: %w", err)
	}

	logger.Info("provision shard created", "provisionShardID", createdID)
	return &createdID, nil
}

func (s *clustersServiceRegistrationSyncer) findExistingProvisionShard(
	ctx context.Context,
	managementCluster *fleet.ManagementCluster,
) (*api.InternalID, error) {
	if managementCluster.Status.ClusterServiceProvisionShardID != nil {
		storedID := *managementCluster.Status.ClusterServiceProvisionShardID
		_, err := s.clustersServiceClient.GetProvisionShard(ctx, storedID)
		if err == nil {
			return &storedID, nil
		}
		var ocmError *ocmerrors.Error
		if !errors.As(err, &ocmError) || ocmError.Status() != http.StatusNotFound {
			return nil, fmt.Errorf("getting provision shard: %w", err)
		}
	}
	existingShardID, err := s.findProvisionShardByAKSResourceID(ctx, managementCluster.Status.AKSResourceID.String())
	if err != nil {
		return nil, fmt.Errorf("searching for provision shard by AKS resource ID: %w", err)
	}
	return existingShardID, nil
}

func (s *clustersServiceRegistrationSyncer) findProvisionShardByAKSResourceID(ctx context.Context, aksResourceID string) (*api.InternalID, error) {
	iter := s.clustersServiceClient.ListProvisionShards()
	for shard := range iter.Items(ctx) {
		if strings.EqualFold(shard.AzureShard().AksManagementClusterResourceId(), aksResourceID) {
			shardID, err := api.NewInternalID(shard.HREF())
			if err != nil {
				return nil, fmt.Errorf("parsing provision shard HREF: %w", err)
			}
			return &shardID, nil
		}
	}
	if err := iter.GetError(); err != nil {
		return nil, err
	}
	return nil, nil
}
