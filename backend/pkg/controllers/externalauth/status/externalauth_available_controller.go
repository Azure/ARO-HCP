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

package status

import (
	"context"
	"fmt"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/api/equality"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	configv1 "github.com/openshift/api/config/v1"

	"github.com/Azure/ARO-HCP/backend/pkg/kubeapplierhelpers"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/informers/coreinformers"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/database/listers/kubeapplierlisters"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const (
	ExternalAuthAvailableControllerName = "ExternalAuthAvailableController"
)

// externalAuthAvailableController reads the HostedCluster's OIDCClientStatus
// conditions from the ReadDesire cache and maps them onto
// ServiceProviderExternalAuth.Status.Conditions as an "Available" condition.
// The ExternalAuthUserFacingAggregator then surfaces these onto
// ExternalAuth.Status.UserFacingConditions.
type externalAuthAvailableController struct {
	externalAuthLister                corelisters.ExternalAuthLister
	serviceProviderExternalAuthLister corelisters.ServiceProviderExternalAuthLister
	readDesireLister                  kubeapplierlisters.ReadDesireLister
	resourcesDBClient                 corecosmosstorage.ResourcesDBClient
}

var _ controllerutils.ExternalAuthSyncer = (*externalAuthAvailableController)(nil)

// NewExternalAuthAvailableController creates a controller that reads
// HostedCluster OIDCClientStatus conditions via the ReadDesire cache and maps
// them onto ServiceProviderExternalAuth.Status.Conditions as an "Available" condition.
func NewExternalAuthAvailableController(
	resourcesDBClient corecosmosstorage.ResourcesDBClient,
	externalAuthLister corelisters.ExternalAuthLister,
	serviceProviderExternalAuthLister corelisters.ServiceProviderExternalAuthLister,
	readDesireLister kubeapplierlisters.ReadDesireLister,
	informers coreinformers.BackendInformers,
) controllerutils.Controller {
	syncer := &externalAuthAvailableController{
		externalAuthLister:                externalAuthLister,
		serviceProviderExternalAuthLister: serviceProviderExternalAuthLister,
		readDesireLister:                  readDesireLister,
		resourcesDBClient:                 resourcesDBClient,
	}
	return controllerutils.NewExternalAuthWatchingController(
		ExternalAuthAvailableControllerName,
		resourcesDBClient,
		informers,
		1*time.Minute,
		syncer,
	)
}

func (c *externalAuthAvailableController) SyncOnce(ctx context.Context, key controllerutils.HCPExternalAuthKey) error {
	existing, err := c.externalAuthLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, key.HCPExternalAuthName)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get ExternalAuth from cache: %w", err))
	}

	if existing.ServiceProviderProperties.DeletionTimestamp != nil {
		return nil
	}
	if existing.ServiceProviderProperties.ClusterServiceID == nil {
		return nil
	}

	spea, err := c.serviceProviderExternalAuthLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, key.HCPExternalAuthName)
	if cosmosstorageutils.IsNotFoundError(err) {
		// CreateServiceProviderExternalAuth will populate it. We'll be re-enqueued via the SPEA informer.
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get ServiceProviderExternalAuth from cache: %w", err))
	}

	condition, err := c.determineAvailableCondition(ctx, existing, key)
	if err != nil {
		return err
	}

	replacement := spea.DeepCopy()
	apimeta.SetStatusCondition(&replacement.Status.Conditions, condition)
	if equality.Semantic.DeepEqual(spea.Status.Conditions, replacement.Status.Conditions) {
		return nil
	}

	speaCRUD := c.resourcesDBClient.ServiceProviderExternalAuths(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, key.HCPExternalAuthName)
	_, err = speaCRUD.Replace(ctx, replacement, nil)
	if cosmosstorageutils.IsPreconditionFailedError(err) {
		return nil
	}
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to replace ServiceProviderExternalAuth: %w", err))
	}
	return nil
}

func (c *externalAuthAvailableController) determineAvailableCondition(
	ctx context.Context,
	externalAuth *coreapi.HCPOpenShiftClusterExternalAuth,
	key controllerutils.HCPExternalAuthKey,
) (metav1.Condition, error) {
	hostedCluster, err := kubeapplierhelpers.GetCachedHostedClusterForCluster(
		ctx,
		c.readDesireLister,
		key.SubscriptionID,
		key.ResourceGroupName,
		key.HCPClusterName,
	)
	if err != nil {
		return metav1.Condition{}, utils.TrackError(err)
	}
	if hostedCluster == nil {
		return metav1.Condition{
			Type:    coreapi.ExternalAuthAvailableCondition,
			Status:  metav1.ConditionFalse,
			Reason:  coreapi.ExternalAuthReasonHostedClusterNotReady,
			Message: "Waiting for HostedCluster to be observed",
		}, nil
	}

	if hostedCluster.Status.Configuration == nil {
		return metav1.Condition{
			Type:    coreapi.ExternalAuthAvailableCondition,
			Status:  metav1.ConditionUnknown,
			Reason:  coreapi.ExternalAuthReasonHostedClusterNotReady,
			Message: "HostedCluster authentication status not yet available",
		}, nil
	}

	oidcClientStatusByComponent := matchingOIDCClientStatuses(externalAuth, hostedCluster.Status.Configuration.Authentication.OIDCClients)
	if len(oidcClientStatusByComponent) == 0 {
		return metav1.Condition{
			Type:    coreapi.ExternalAuthAvailableCondition,
			Status:  metav1.ConditionFalse,
			Reason:  coreapi.ExternalAuthReasonAwaitingSecret,
			Message: "OIDC client status not yet reported by the hosted cluster",
		}, nil
	}

	return worstAvailableCondition(oidcClientStatusByComponent), nil
}

// matchingOIDCClientStatuses returns HostedCluster OIDC client statuses that
// correspond to this ExternalAuth's clients (component name + namespace).
// Matching is case-insensitive. When the ExternalAuth has no clients, all
// observed OIDC clients are returned.
func matchingOIDCClientStatuses(externalAuth *coreapi.HCPOpenShiftClusterExternalAuth, observed []configv1.OIDCClientStatus) []configv1.OIDCClientStatus {
	if len(externalAuth.Properties.Clients) == 0 {
		return observed
	}
	wanted := make(map[string]struct{}, len(externalAuth.Properties.Clients))
	for _, client := range externalAuth.Properties.Clients {
		wanted[oidcClientKey(client.Component.Name, client.Component.AuthClientNamespace)] = struct{}{}
	}
	oidcClientStatusByComponent := make([]configv1.OIDCClientStatus, 0, len(observed))
	for _, client := range observed {
		if _, ok := wanted[oidcClientKey(client.ComponentName, client.ComponentNamespace)]; ok {
			oidcClientStatusByComponent = append(oidcClientStatusByComponent, client)
		}
	}
	return oidcClientStatusByComponent
}

func oidcClientKey(name, namespace string) string {
	return strings.ToLower(name) + "/" + strings.ToLower(namespace)
}

// conditionPriority returns a numeric priority for a mapped Available condition.
// Lower values are worse (and win in aggregation).
func conditionPriority(c metav1.Condition) int {
	switch {
	case c.Status == metav1.ConditionFalse && c.Reason == coreapi.ExternalAuthReasonAwaitingSecret:
		return 0
	case c.Status == metav1.ConditionFalse:
		return 1
	default:
		return 2
	}
}

// worstAvailableCondition maps each matched OIDCClientStatus's conditions to
// our user-facing Available condition and returns the worst across all clients.
func worstAvailableCondition(oidcClientStatusByComponent []configv1.OIDCClientStatus) metav1.Condition {
	worst := mapSingleClientConditions(oidcClientStatusByComponent[0].Conditions)
	for _, client := range oidcClientStatusByComponent[1:] {
		candidate := mapSingleClientConditions(client.Conditions)
		if conditionPriority(candidate) < conditionPriority(worst) {
			worst = candidate
		}
	}
	return worst
}

// mapSingleClientConditions translates one OIDCClientStatus's conditions into
// our user-facing Available condition.
func mapSingleClientConditions(conditions []metav1.Condition) metav1.Condition {
	degraded := apimeta.FindStatusCondition(conditions, "Degraded")
	if degraded != nil && degraded.Status == metav1.ConditionTrue {
		if degraded.Reason == coreapi.HCReasonOIDCClientSecretGet {
			return metav1.Condition{
				Type:    coreapi.ExternalAuthAvailableCondition,
				Status:  metav1.ConditionFalse,
				Reason:  coreapi.ExternalAuthReasonAwaitingSecret,
				Message: "The external auth provider is waiting for the client secret to be created in the openshift-config namespace",
			}
		}
		return metav1.Condition{
			Type:    coreapi.ExternalAuthAvailableCondition,
			Status:  metav1.ConditionFalse,
			Reason:  degraded.Reason,
			Message: degraded.Message,
		}
	}

	available := apimeta.FindStatusCondition(conditions, "Available")
	if available != nil && available.Status == metav1.ConditionTrue &&
		available.Reason == coreapi.HCReasonOIDCConfigAvailable {
		return metav1.Condition{
			Type:    coreapi.ExternalAuthAvailableCondition,
			Status:  metav1.ConditionTrue,
			Reason:  coreapi.ExternalAuthReasonOIDCConfigAvailable,
			Message: available.Message,
		}
	}

	if available != nil && available.Status == metav1.ConditionFalse {
		return metav1.Condition{
			Type:    coreapi.ExternalAuthAvailableCondition,
			Status:  metav1.ConditionFalse,
			Reason:  coreapi.ExternalAuthReasonAwaitingSecret,
			Message: available.Message,
		}
	}

	return metav1.Condition{
		Type:    coreapi.ExternalAuthAvailableCondition,
		Status:  metav1.ConditionFalse,
		Reason:  coreapi.ExternalAuthReasonAwaitingSecret,
		Message: "OIDC client conditions do not indicate readiness",
	}
}
