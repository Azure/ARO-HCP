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

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"
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

	var syncErr error
	if !apimeta.IsStatusConditionTrue(stamp.Status.Conditions, string(fleet.StampConditionApproved)) {
		// an unapproved stamp is not a sync error
		// the controller will wake up when the stamp is approved and try again
		// we update the condition though to reflect the fact
		fleetcontrollers.SetRegistrationCondition(&updated.Status.Conditions, string(fleet.ManagementClusterConditionClustersServiceRegistered), fleetcontrollers.ErrStampNotApproved)
	} else {
		var shardID *api.InternalID
		shardID, syncErr = s.reconcileProvisionShard(ctx, updated)
		if shardID != nil {
			updated.Status.ClusterServiceProvisionShardID = shardID
		}
		fleetcontrollers.SetRegistrationCondition(&updated.Status.Conditions, string(fleet.ManagementClusterConditionClustersServiceRegistered), syncErr)
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

	existingID, existing, err := s.findExistingProvisionShard(ctx, managementCluster)
	if err != nil {
		return nil, err
	}

	// shard exists
	if existingID != nil {
		if err := s.updateShardStatusIfNeeded(ctx, *existingID, existing, managementCluster); err != nil {
			return nil, err
		}
		return existingID, nil
	}

	// shard does not exist yet
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

	// CS ignores status on create (defaults to maintenance), so a separate update may be needed.
	if err := s.updateShardStatusIfNeeded(ctx, createdID, created, managementCluster); err != nil {
		return nil, fmt.Errorf("setting provision shard status after create: %w", err)
	}

	logger.Info("provision shard created", "provisionShardID", createdID)
	return &createdID, nil
}

func (s *clustersServiceRegistrationSyncer) updateShardStatusIfNeeded(
	ctx context.Context,
	shardID api.InternalID,
	shard *arohcpv1alpha1.ProvisionShard,
	managementCluster *fleet.ManagementCluster,
) error {
	builder, err := provisionShardStatusUpdateBuilder(shard, managementCluster.Spec.SchedulingPolicy)
	if err != nil {
		return err
	}
	if builder == nil {
		return nil
	}
	if _, err := s.clustersServiceClient.UpdateProvisionShard(ctx, shardID, builder); err != nil {
		return fmt.Errorf("updating provision shard status: %w", err)
	}
	logger := utils.LoggerFromContext(ctx)
	logger.Info("provision shard status updated", "provisionShardID", shardID)
	return nil
}

// findExistingProvisionShard looks up the provision shard for this management
// cluster. If we have a stored shard ID, we fetch it directly and verify its
// identity fields. A stored shard that 404s is a hard error — a previously
// registered shard disappearing indicates data corruption or unauthorized
// deletion that requires operator investigation. Without a stored ID, we scan
// all shards by AKS resource ID and consumer name. CS enforces uniqueness on
// both (immutable after create), so duplicates or partial matches indicate
// data corruption, not a race.
func (s *clustersServiceRegistrationSyncer) findExistingProvisionShard(
	ctx context.Context,
	managementCluster *fleet.ManagementCluster,
) (*api.InternalID, *arohcpv1alpha1.ProvisionShard, error) {
	if managementCluster.Status.AKSResourceID == nil {
		return nil, nil, fmt.Errorf("AKSResourceID is required")
	}
	aksResourceID := managementCluster.Status.AKSResourceID.String()
	consumerName := managementCluster.Status.MaestroConsumerName
	if len(consumerName) == 0 {
		return nil, nil, fmt.Errorf("MaestroConsumerName is required")
	}

	storedID := managementCluster.Status.ClusterServiceProvisionShardID
	if storedID != nil {
		return s.getByStoredID(ctx, *storedID, aksResourceID, consumerName)
	}

	return searchByIdentityKeys(ctx, s.clustersServiceClient, aksResourceID, consumerName)
}

// getByStoredID fetches the shard by its stored ID, verifies its identity
// fields match, and returns an error if the expected shard is not found (404).
func (s *clustersServiceRegistrationSyncer) getByStoredID(
	ctx context.Context,
	storedID api.InternalID,
	aksResourceID, consumerName string,
) (*api.InternalID, *arohcpv1alpha1.ProvisionShard, error) {
	shard, err := s.clustersServiceClient.GetProvisionShard(ctx, storedID)
	if err != nil {
		if isOCMNotFound(err) {
			return nil, nil, fmt.Errorf("stored provision shard %s not found in ClustersService — manual investigation required", storedID)
		}
		return nil, nil, fmt.Errorf("getting provision shard: %w", err)
	}
	if !strings.EqualFold(shard.AzureShard().AksManagementClusterResourceId(), aksResourceID) {
		return nil, nil, fmt.Errorf("stored shard %s: AKS resource ID mismatch: got %q, expected %q", storedID, shard.AzureShard().AksManagementClusterResourceId(), aksResourceID)
	}
	if shard.MaestroConfig().ConsumerName() != consumerName {
		return nil, nil, fmt.Errorf("stored shard %s: consumer name mismatch: got %q, expected %q", storedID, shard.MaestroConfig().ConsumerName(), consumerName)
	}
	return &storedID, shard, nil
}

func isOCMNotFound(err error) bool {
	var ocmError *ocmerrors.Error
	return errors.As(err, &ocmError) && ocmError.Status() == http.StatusNotFound
}
