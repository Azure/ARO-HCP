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

	"github.com/google/go-cmp/cmp"

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

// externalAuthClusterServiceUpdateDispatchSyncer calls Cluster Service's ExternalAuth PATCH when
// the ExternalAuth's dispatch-managed configuration has drifted. It reconciles a curated subset of
// fields defined by ocm.externalAuthUpdateDispatchConfig.
//
// On each reconcile, the ExternalAuth's state and the live Cluster Service external auth state
// are projected into ocm.externalAuthUpdateDispatchConfig. When the projections from both sides
// differ, it PATCHes Cluster Service.
//
// Dispatch is paired with operation state calculation in operationcontrollers
// (operation_external_auth_update_state_calculation.go): dispatch sends updates, operation state
// verifies propagation before the ARM external auth update operation succeeds.
type externalAuthClusterServiceUpdateDispatchSyncer struct {
	cooldownChecker      controllerutil.CooldownChecker
	externalAuthLister   listers.ExternalAuthLister
	resourcesDBClient    database.ResourcesDBClient
	clusterServiceClient ocm.ClusterServiceClientSpec

	// minimumReconcileTimeCooldownChecker ensures we don't hotloop from any source,
	// by ensuring that we don't reconcile more often than the cooldown time in it.
	minimumReconcileTimeCooldownChecker controllerutil.CooldownChecker
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
		cooldownChecker: controllerutils.DefaultActiveOperationPrioritizingCooldown(activeOperationLister),
		// We set minimumReconcileTimeCooldownChecker so that SyncOnce is not executed
		// more than once per minute.
		minimumReconcileTimeCooldownChecker: controllerutil.NewTimeBasedCooldownChecker(1 * time.Minute),
		externalAuthLister:                  externalAuthLister,
		resourcesDBClient:                   resourcesDBClient,
		clusterServiceClient:                clusterServiceClient,
	}
}

func needsWork(ea *api.HCPOpenShiftClusterExternalAuth) bool {
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

func (c *externalAuthClusterServiceUpdateDispatchSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPExternalAuthKey) (controllerutil.SyncResult, error) {
	logger := utils.LoggerFromContext(ctx)

	// Because this controller ends up calling Cluster Service each time it's reconciled and it's reconciled
	// while the resource exists and while it is not being deleted, we establish a minimum reconcile time cooldown
	// to avoid putting too much pressure on Cluster Service.
	// TODO in the future, we could remove this cooldown checker by persisting a hash of the update dispatch configuration
	// sent to Cluster Service and checking if it has changed since the last time we sent it.
	if !c.minimumReconcileTimeCooldownChecker.CanSync(ctx, key) {
		return controllerutil.SyncResult{}, nil
	}

	cachedExternalAuth, err := c.externalAuthLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, key.HCPExternalAuthName)
	if database.IsNotFoundError(err) {
		return controllerutil.SyncResult{}, nil
	}
	if err != nil {
		return controllerutil.SyncResult{}, utils.TrackError(fmt.Errorf("failed to get external auth from cache: %w", err))
	}
	if !needsWork(cachedExternalAuth) {
		return controllerutil.SyncResult{}, nil
	}

	externalAuthCRUD := c.resourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).ExternalAuth(key.HCPClusterName)
	externalAuth, err := externalAuthCRUD.Get(ctx, key.HCPExternalAuthName)
	if database.IsNotFoundError(err) {
		return controllerutil.SyncResult{}, nil
	}
	if err != nil {
		return controllerutil.SyncResult{}, utils.TrackError(fmt.Errorf("failed to get external auth: %w", err))
	}
	if !needsWork(externalAuth) {
		return controllerutil.SyncResult{}, nil
	}

	externalAuthCSID := externalAuth.ServiceProviderProperties.ClusterServiceID
	csExternalAuth, err := c.clusterServiceClient.GetExternalAuth(ctx, *externalAuthCSID)
	if err != nil {
		return controllerutil.SyncResult{}, utils.TrackError(fmt.Errorf("failed to get external auth from Cluster Service: %w", err))
	}

	// We check if the desired config coming from cosmos differs from the actual config coming from cluster service.
	// If it doesn't, we are done and don't need to dispatch an update. If it does, we need to dispatch an update to
	// cluster service. Comparison uses canonical JSON (sorted object keys at every level) so we can compare them
	// using direct string equality.
	desiredConfigJSON, err := ocm.ExternalAuthUpdateDispatchConfigJSONFromRP(externalAuth)
	if err != nil {
		return controllerutil.SyncResult{}, utils.TrackError(err)
	}
	actualConfigJSON, err := ocm.ExternalAuthUpdateDispatchConfigJSONFromCS(csExternalAuth)
	if err != nil {
		return controllerutil.SyncResult{}, utils.TrackError(err)
	}
	if desiredConfigJSON == actualConfigJSON {
		return controllerutil.SyncResult{}, nil
	}

	configDiff := cmp.Diff(desiredConfigJSON, actualConfigJSON)

	logger.Info("external auth update dispatch config differs between RP and CS",
		"clusterServiceID", externalAuthCSID.String(),
		"desiredConfig", desiredConfigJSON,
		"actualConfig", actualConfigJSON,
		"configDiff", configDiff,
	)

	// We marshal the external auth CS builder config we are going to submit for cs external auth update for logging purposes
	externalAuthPayload, err := c.marshalClusterServiceExternalAuthUpdatePayload(ctx, externalAuth)
	if err != nil {
		return controllerutil.SyncResult{}, utils.TrackError(fmt.Errorf("failed to marshal Cluster Service external auth update payload: %w", err))
	}

	logger.Info("dispatching external auth update to Cluster Service",
		"clusterServiceID", externalAuthCSID.String(),
		"clusterServiceExternalAuthPayload", externalAuthPayload,
	)

	csExternalAuthBuilder, err := ocm.BuildCSExternalAuth(ctx, externalAuth, true)
	if err != nil {
		return controllerutil.SyncResult{}, utils.TrackError(fmt.Errorf("failed to build CS external auth builder: %w", err))
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
			return controllerutil.SyncResult{}, nil
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
			return controllerutil.SyncResult{}, nil
		}
		return controllerutil.SyncResult{}, utils.TrackError(fmt.Errorf("failed to update cluster-service ExternalAuth: %w", err))
	}

	logger.Info("dispatched external auth update to Cluster Service", "clusterServiceID", externalAuthCSID.String())
	return controllerutil.SyncResult{}, nil
}

// marshalClusterServiceExternalAuthUpdatePayload serializes the external auth PATCH body for logging.
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
