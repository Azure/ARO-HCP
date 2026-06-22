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

package externalauthupdate

import (
	"bytes"
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

type externalAuthClusterServiceUpdateDispatchSyncer struct {
	cooldownChecker      controllerutil.CooldownChecker
	externalAuthLister   listers.ExternalAuthLister
	resourcesDBClient    database.ResourcesDBClient
	clusterServiceClient ocm.ClusterServiceClientSpec
}

var _ controllerutils.ExternalAuthSyncer = (*externalAuthClusterServiceUpdateDispatchSyncer)(nil)

func NewExternalAuthClusterServiceUpdateDispatchController(
	resourcesDBClient database.ResourcesDBClient,
	clusterServiceClient ocm.ClusterServiceClientSpec,
	activeOperationLister listers.ActiveOperationLister,
	backendInformers informers.BackendInformers,
) controllerutils.Controller {
	_, externalAuthLister := backendInformers.ExternalAuths()
	syncer := NewExternalAuthClusterServiceUpdateDispatchSyncer(
		resourcesDBClient,
		clusterServiceClient,
		activeOperationLister,
		externalAuthLister,
	)

	return controllerutils.NewExternalAuthWatchingController(
		"ExternalAuthClusterServiceUpdateDispatch",
		resourcesDBClient,
		backendInformers,
		time.Minute,
		syncer,
	)
}

func NewExternalAuthClusterServiceUpdateDispatchSyncer(
	resourcesDBClient database.ResourcesDBClient,
	clusterServiceClient ocm.ClusterServiceClientSpec,
	activeOperationLister listers.ActiveOperationLister,
	externalAuthLister listers.ExternalAuthLister,
) controllerutils.ExternalAuthSyncer {
	return &externalAuthClusterServiceUpdateDispatchSyncer{
		cooldownChecker:      controllerutils.DefaultActiveOperationPrioritizingCooldown(activeOperationLister),
		externalAuthLister:   externalAuthLister,
		resourcesDBClient:    resourcesDBClient,
		clusterServiceClient: clusterServiceClient,
	}
}

func externalAuthShouldProceed(ea *api.HCPOpenShiftClusterExternalAuth) bool {
	if ea.ServiceProviderProperties.DeletionTimestamp != nil {
		return false
	}

	csID := ea.ServiceProviderProperties.ClusterServiceID
	if csID == nil || len(csID.String()) == 0 {
		return false
	}

	return true
}

func (c *externalAuthClusterServiceUpdateDispatchSyncer) CooldownChecker() controllerutil.CooldownChecker {
	return c.cooldownChecker
}

func (c *externalAuthClusterServiceUpdateDispatchSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPExternalAuthKey) error {
	logger := utils.LoggerFromContext(ctx)

	cachedExternalAuth, err := c.externalAuthLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, key.HCPExternalAuthName)
	if database.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get external auth from cache: %w", err))
	}
	if !externalAuthShouldProceed(cachedExternalAuth) {
		return nil
	}

	externalAuthCRUD := c.resourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).ExternalAuth(key.HCPClusterName)
	externalAuth, err := externalAuthCRUD.Get(ctx, key.HCPExternalAuthName)
	if database.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get external auth: %w", err))
	}
	if !externalAuthShouldProceed(externalAuth) {
		return nil
	}

	externalAuthCSID := externalAuth.ServiceProviderProperties.ClusterServiceID
	csExternalAuth, err := c.clusterServiceClient.GetExternalAuth(ctx, *externalAuthCSID)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get external auth from Cluster Service: %w", err))
	}

	needsUpdate, err := ocm.ExternalAuthUpdateDispatchConfigDiffers(externalAuth, csExternalAuth)
	if err != nil {
		return err
	}
	if !needsUpdate {
		return nil
	}

	desiredConfigJSON, err := ocm.ExternalAuthUpdateDispatchConfigJSONFromRP(externalAuth)
	if err != nil {
		return err
	}
	actualConfigJSON, err := ocm.ExternalAuthUpdateDispatchConfigJSONFromCS(csExternalAuth)
	if err != nil {
		return err
	}

	logger.Info("external auth update dispatch config differs between RP and CS",
		"clusterServiceID", externalAuthCSID.String(),
		"desiredConfig", desiredConfigJSON,
		"actualConfig", actualConfigJSON,
	)

	externalAuthPayload, err := c.marshalClusterServiceExternalAuthUpdatePayload(ctx, externalAuth)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to marshal Cluster Service external auth update payload: %w", err))
	}

	logger.Info("dispatching external auth update to Cluster Service",
		"clusterServiceID", externalAuthCSID.String(),
		"clusterServiceExternalAuthPayload", externalAuthPayload,
	)

	csExternalAuthBuilder, err := ocm.BuildCSExternalAuth(ctx, externalAuth, true)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to build CS external auth builder: %w", err))
	}

	_, err = c.clusterServiceClient.UpdateExternalAuth(ctx, *externalAuthCSID, csExternalAuthBuilder)
	if err != nil {
		var ocmError *ocmerrors.Error
		// XXX Matching an error message is brittle, but Clusters Service
		//     returns 400 Bad Request for a wide range of errors and there
		//     is no other information in the response to distinguish them.
		//
		//     If the error is indicating that an external auth is not in
		//     an updatable state because the external auth's parent cluster is not
		//     in an updatable state, we return without error and retry again on the
		//     next sync. This can happen for example when the CS cluster is being updated.
		if errors.As(err, &ocmError) && ocmError.Status() == http.StatusBadRequest &&
			strings.Contains(ocmError.Reason(), "ExternalAuths can only be updated on clusters in an updatable state") &&
			strings.Contains(ocmError.Reason(), "cluster requested is in") &&
			strings.Contains(ocmError.Reason(), "state.") {
			logger.Info("Cluster Service rejected external auth update because the external auth's parent cluster is not updatable. Retrying on next sync.",
				"clusterServiceID", externalAuthCSID.String(),
				"error", err.Error(),
			)
			return nil
		}
		// XXX Matching an error message is brittle, but Clusters Service
		//     returns 400 Bad Request for a wide range of errors and there
		//     is no other information in the response to distinguish them.
		//
		//     If the error is indicating that the console clients cannot be configured without any ready node pool,
		//     we return without error and retry again on the next sync.
		if errors.As(err, &ocmError) && ocmError.Status() == http.StatusBadRequest &&
			strings.Contains(ocmError.Reason(), "console clients cannot be configured without a ready node pool") &&
			strings.Contains(ocmError.Reason(), "Please ensure at least one node pool is in 'ready' state before configuring console authentication") {
			logger.Info("Cluster Service rejected external auth update because the console clients cannot be configured without a ready node pool. Retrying on next sync.",
				"clusterServiceID", externalAuthCSID.String(),
				"error", err.Error(),
			)
			return nil
		}
		return utils.TrackError(fmt.Errorf("failed to update cluster-service ExternalAuth: %w", err))
	}

	logger.Info("requested cluster-service ExternalAuth update", "clusterServiceID", externalAuthCSID.String())
	return nil
}

func (c *externalAuthClusterServiceUpdateDispatchSyncer) marshalClusterServiceExternalAuthUpdatePayload(ctx context.Context, externalAuth *api.HCPOpenShiftClusterExternalAuth) (string, error) {
	builder, err := ocm.BuildCSExternalAuth(ctx, externalAuth, true)
	if err != nil {
		return "", err
	}

	csExternalAuth, err := builder.Build()
	if err != nil {
		return "", err
	}

	var buf bytes.Buffer
	if err := arohcpv1alpha1.MarshalExternalAuth(csExternalAuth, &buf); err != nil {
		return "", err
	}

	return buf.String(), nil
}
