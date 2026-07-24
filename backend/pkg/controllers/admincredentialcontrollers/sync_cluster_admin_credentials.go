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

package admincredentialcontrollers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"k8s.io/apimachinery/pkg/api/equality"

	ocmerrors "github.com/openshift-online/ocm-sdk-go/errors"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/informers"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/internal/api"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// syncClusterAdminCredentialsSyncer polls Clusters Service for each
// ClusterAdminCredential document under a cluster and updates Cosmos state.
// A CS 404 means the credential is gone (expired / revoked / deleted), so the
// Cosmos document is deleted.
type syncClusterAdminCredentialsSyncer struct {
	resourcesDBClient     database.ResourcesDBClient
	clusterLister         listers.ClusterLister
	adminCredentialLister listers.AdminCredentialLister
	clustersServiceClient ocm.ClusterServiceClientSpec

	// minimumReconcileTimeCooldownChecker ensures we don't hotloop from any source,
	// by ensuring that we don't reconcile more often than the cooldown time in it.
	minimumReconcileTimeCooldownChecker controllerutil.CooldownChecker
}

var _ controllerutils.ClusterSyncer = (*syncClusterAdminCredentialsSyncer)(nil)

func NewSyncClusterAdminCredentialsController(
	resourcesDBClient database.ResourcesDBClient,
	clustersServiceClient ocm.ClusterServiceClientSpec,
	informers informers.BackendInformers,
) controllerutils.Controller {
	_, clusterLister := informers.Clusters()
	_, adminCredentialLister := informers.AdminCredentials()
	syncer := &syncClusterAdminCredentialsSyncer{
		resourcesDBClient:     resourcesDBClient,
		clusterLister:         clusterLister,
		adminCredentialLister: adminCredentialLister,
		clustersServiceClient: clustersServiceClient,
		// SyncOnce calls Clusters Service for each credential, so keep a minimum
		// reconcile interval to avoid putting too much pressure on CS.
		minimumReconcileTimeCooldownChecker: controllerutil.NewTimeBasedCooldownChecker(30 * time.Second),
	}
	return controllerutils.NewClusterWatchingController(
		"SyncClusterAdminCredentials",
		resourcesDBClient,
		informers,
		nil,
		30*time.Second,
		syncer,
	)
}

func (c *syncClusterAdminCredentialsSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	logger := utils.LoggerFromContext(ctx)

	// Because this controller ends up calling Cluster Service each time it's reconciled,
	// establish a minimum reconcile time cooldown to avoid putting too much pressure on CS.
	if !c.minimumReconcileTimeCooldownChecker.CanSync(ctx, key) {
		return nil
	}

	cluster, err := c.clusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if database.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(err)
	}
	if cluster.ServiceProviderProperties.DeletionTimestamp != nil {
		return nil
	}

	creds, err := c.adminCredentialLister.ListForCluster(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if err != nil {
		return utils.TrackError(err)
	}

	crud := c.resourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).AdminCredentials(key.HCPClusterName)

	var errs []error
	for _, cred := range creds {
		err := c.syncAdminCredential(ctx, crud, cred)
		if err != nil {
			logger.Error(err, "failed to sync ClusterAdminCredential", "admin_credential_name", cred.ResourceID.Name)
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (c *syncClusterAdminCredentialsSyncer) syncAdminCredential(ctx context.Context, crud database.ResourceCRUD[api.ClusterAdminCredential, *api.ClusterAdminCredential], cred *api.ClusterAdminCredential) error {
	logger := utils.LoggerFromContext(ctx)

	if len(cred.ClusterServiceInternalID.String()) == 0 {
		return fmt.Errorf("ClusterAdminCredential %s has unexpected empty ClusterServiceInternalID", cred.ResourceID.Name)
	}

	csCred, err := c.clustersServiceClient.GetBreakGlassCredential(ctx, cred.ClusterServiceInternalID)
	var ocmError *ocmerrors.Error
	if errors.As(err, &ocmError) && ocmError.Status() == http.StatusNotFound {
		logger.Info("break-glass credential not found on CS side, deleting cosmos ClusterAdminCredential", "admin_credential_resource_id", cred.ResourceID.String())
		err := crud.Delete(ctx, cred.ResourceID.Name)
		if database.IsNotFoundError(err) {
			return nil
		}
		if err != nil {
			return utils.TrackError(err)
		}
		return nil
	}
	if err != nil {
		return utils.TrackError(err)
	}

	replacement := cred.DeepCopy()
	var clusterAdminCredentialStatus api.ClusterAdminCredentialStatus
	// TODO if CS reports empty values should we mirror those or should we do a noop for those?
	if csCred.Status() != "" {
		clusterAdminCredentialStatus, err = ocm.ConvertCStoClusterAdminCredentialStatus(csCred.Status())
		if err != nil {
			return utils.TrackError(err)
		}
	}

	replacement.Status = clusterAdminCredentialStatus
	replacement.ExpirationTimestamp = csCred.ExpirationTimestamp()
	replacement.Kubeconfig = csCred.Kubeconfig()

	if equality.Semantic.DeepEqual(cred, replacement) {
		return nil
	}

	_, err = crud.Replace(ctx, replacement, nil)
	if database.IsPreconditionFailedError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(err)
	}

	return nil
}
