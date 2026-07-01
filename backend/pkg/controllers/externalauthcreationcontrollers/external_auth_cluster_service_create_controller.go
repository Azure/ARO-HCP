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

package externalauthcreationcontrollers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"
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

const ExternalAuthClusterServiceCreateControllerName = "ExternalAuthClusterServiceCreate"

type externalAuthClusterServiceCreateSyncer struct {
	cooldownChecker       controllerutil.CooldownChecker
	resourcesDBClient     database.ResourcesDBClient
	externalAuthLister    listers.ExternalAuthLister
	clusterLister         listers.ClusterLister
	controllerLister      listers.ControllerLister
	clustersServiceClient ocm.ClusterServiceClientSpec
}

func NewExternalAuthClusterServiceCreateController(
	resourcesDBClient database.ResourcesDBClient,
	clustersServiceClient ocm.ClusterServiceClientSpec,
	activeOperationLister listers.ActiveOperationLister,
	informers informers.BackendInformers,
) controllerutils.Controller {
	_, externalAuthLister := informers.ExternalAuths()
	_, clusterLister := informers.Clusters()
	_, controllerLister := informers.Controllers()
	syncer := &externalAuthClusterServiceCreateSyncer{
		cooldownChecker:       controllerutils.DefaultActiveOperationPrioritizingCooldown(activeOperationLister),
		resourcesDBClient:     resourcesDBClient,
		externalAuthLister:    externalAuthLister,
		clusterLister:         clusterLister,
		controllerLister:      controllerLister,
		clustersServiceClient: clustersServiceClient,
	}

	return controllerutils.NewExternalAuthWatchingController(
		ExternalAuthClusterServiceCreateControllerName,
		resourcesDBClient,
		informers,
		time.Minute,
		syncer,
	)
}

func (c *externalAuthClusterServiceCreateSyncer) needsWork(externalAuth *api.HCPOpenShiftClusterExternalAuth) bool {
	return externalAuth.ServiceProviderProperties.DeletionTimestamp == nil &&
		(externalAuth.ServiceProviderProperties.ClusterServiceID == nil || len(externalAuth.ServiceProviderProperties.ClusterServiceID.String()) == 0)
}

func (c *externalAuthClusterServiceCreateSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPExternalAuthKey) error {
	logger := utils.LoggerFromContext(ctx)

	externalAuth, err := c.externalAuthLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, key.HCPExternalAuthName)
	if database.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(err)
	}

	if !c.needsWork(externalAuth) {
		return nil
	}

	// For the ExternalAuth, we retrieve from the actual database because we are about to use its data to interact with cluster-service.
	externalAuth, err = c.resourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).ExternalAuth(key.HCPClusterName).Get(ctx, key.HCPExternalAuthName)
	if database.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(err)
	}

	if !c.needsWork(externalAuth) {
		return nil
	}

	// For the Cluster, we retrieve from the cache because we are not about to use its data to interact with cluster-service. At
	// the moment we only use the ClusterServiceID to interact with cluster-service, which shouldn't change over time once set.
	// If at some point this controller evolves to use other Cluster properties that will be sent to cluster-service and that
	// can change over time, we will need to retrieve from the database instead.
	cluster, err := c.clusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if err != nil {
		return utils.TrackError(err)
	}
	if cluster.ServiceProviderProperties.ClusterServiceID == nil || len(cluster.ServiceProviderProperties.ClusterServiceID.String()) == 0 {
		return utils.TrackError(fmt.Errorf("cluster %s has no ClusterServiceID", key.HCPClusterName))
	}
	clusterCSInternalID := *cluster.ServiceProviderProperties.ClusterServiceID

	// GET must target the same href POST would use: {clusterHref}/external_auth_config/external_auths/{id} where id is
	// lowercased ARM name (see ocm.BuildCSExternalAuth). We reconstruct it here:
	csExternalAuthHREF := ocm.GenerateAROHCPExternalAuthHREF(clusterCSInternalID.ID(), strings.ToLower(key.HCPExternalAuthName))
	externalAuthCSInternalID, err := api.NewInternalID(csExternalAuthHREF)
	if err != nil {
		return utils.TrackError(fmt.Errorf("build external auth internal ID for adoption lookup: %w", err))
	}

	existing, err := c.findCSExternalAuth(ctx, externalAuthCSInternalID)
	if err != nil {
		return utils.TrackError(err)
	}

	if existing == nil {
		csExternalAuthBuilder, err := ocm.BuildCSExternalAuth(ctx, externalAuth, false)
		if err != nil {
			return utils.TrackError(err)
		}
		logger.Info("performing POST external auth to Cluster Service", "cs_external_auth_href", csExternalAuthHREF, "external_auth_resource_id", externalAuth.ID.String())
		_, err = c.clustersServiceClient.PostExternalAuth(ctx, clusterCSInternalID, csExternalAuthBuilder)
		if c.isOCMErrorBadRequest(err) {
			logger.Error(err, "CS external auth POST returned OCM error with HTTP 400 status code", "cs_external_auth_href", csExternalAuthHREF, "external_auth_resource_id", externalAuth.ID.String())
		}
		if err != nil {
			return utils.TrackError(err)
		}
	}

	logger.Info("setting external auth cluster service ID in external auth cosmos document", "external_auth_cluster_service_id", externalAuthCSInternalID.String())
	replacement := externalAuth.DeepCopy()
	replacement.ServiceProviderProperties.ClusterServiceID = externalAuthCSInternalID.DeepCopy() // DeepCopy() to avoid referencing the original pointer
	_, err = c.resourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).ExternalAuth(key.HCPClusterName).Replace(ctx, replacement, nil)
	if database.IsPreconditionFailedError(err) {
		// if we have a conflict error, then we're guaranteed that our informer will eventually see an update and trigger us again.
		return nil
	}
	if err != nil {
		return utils.TrackError(err)
	}

	return nil
}

// findCSExternalAuth performs GetExternalAuth for the given Cluster Service external auth InternalID.
// It returns (nil, nil) when CS responds with 404.
func (c *externalAuthClusterServiceCreateSyncer) findCSExternalAuth(ctx context.Context, externalAuthInternalID api.InternalID) (*arohcpv1alpha1.ExternalAuth, error) {
	ea, err := c.clustersServiceClient.GetExternalAuth(ctx, externalAuthInternalID)
	if err != nil {
		var ocmErr *ocmerrors.Error
		if errors.As(err, &ocmErr) && ocmErr.Status() == http.StatusNotFound {
			return nil, nil
		}
		return nil, err
	}
	return ea, nil
}

func (c *externalAuthClusterServiceCreateSyncer) CooldownChecker() controllerutil.CooldownChecker {
	return c.cooldownChecker
}

func (c *externalAuthClusterServiceCreateSyncer) isOCMErrorBadRequest(err error) bool {
	var ocmErr *ocmerrors.Error
	return err != nil && errors.As(err, &ocmErr) && ocmErr.Status() == http.StatusBadRequest
}
