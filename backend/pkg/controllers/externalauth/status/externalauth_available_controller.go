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
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
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
// conditions from the ReadDesire cache and maps them onto per-client Available
// conditions on ServiceProviderExternalAuth.Status.Conditions.
//
// Each declared client in ExternalAuth.Properties.Clients gets its own
// condition type (e.g. ConsoleAvailable, CliAvailable). Public clients are
// always Available; confidential clients reflect the HostedCluster's OIDC
// status. The ExternalAuthUserFacingConditionsAggregator then promotes these
// onto ExternalAuth.Status.UserFacingConditions for ARM API visibility.
type externalAuthAvailableController struct {
	externalAuthLister                corelisters.ExternalAuthLister
	serviceProviderExternalAuthLister corelisters.ServiceProviderExternalAuthLister
	readDesireLister                  kubeapplierlisters.ReadDesireLister
	resourcesDBClient                 corecosmosstorage.ResourcesDBClient
}

var _ controllerutils.ExternalAuthSyncer = (*externalAuthAvailableController)(nil)

// NewExternalAuthAvailableController creates a controller that reads
// HostedCluster OIDCClientStatus conditions via the ReadDesire cache and maps
// them onto per-client conditions on ServiceProviderExternalAuth.Status.Conditions.
// The aggregator is responsible for promoting these to user-facing.
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

func (c *externalAuthAvailableController) needsWork(externalAuth *coreapi.HCPOpenShiftClusterExternalAuth) bool {
	return externalAuth.ServiceProviderProperties.DeletionTimestamp == nil && externalAuth.ServiceProviderProperties.ClusterServiceID != nil
}

func (c *externalAuthAvailableController) SyncOnce(ctx context.Context, key controllerutils.HCPExternalAuthKey) error {
	existing, err := c.externalAuthLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, key.HCPExternalAuthName)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get ExternalAuth from cache: %w", err))
	}

	if !c.needsWork(existing) {
		return nil
	}

	serviceProviderExternalAuth, err := c.serviceProviderExternalAuthLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, key.HCPExternalAuthName)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get ServiceProviderExternalAuth from cache: %w", err))
	}

	conditions, err := c.determinePerClientConditions(ctx, existing, key)
	if err != nil {
		return err
	}

	replacement := serviceProviderExternalAuth.DeepCopy()
	for _, condition := range conditions {
		apimeta.SetStatusCondition(&replacement.Status.Conditions, condition)
	}
	if equality.Semantic.DeepEqual(serviceProviderExternalAuth.Status.Conditions, replacement.Status.Conditions) {
		return nil
	}

	serviceProviderExternalAuthCRUD := c.resourcesDBClient.ServiceProviderExternalAuths(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, key.HCPExternalAuthName)
	_, err = serviceProviderExternalAuthCRUD.Replace(ctx, replacement, nil)
	if cosmosstorageutils.IsPreconditionFailedError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to replace ServiceProviderExternalAuth: %w", err))
	}
	return nil
}

// determinePerClientConditions reads HostedCluster OIDC client status from the
// ReadDesire cache and returns one Available condition per declared client. When
// err is nil, the returned conditions are always populated with valid Type,
// Status, Reason, and Message.
//
// Public clients are always True/OIDCConfigAvailable (they do not need secrets).
// Confidential clients reflect the HostedCluster's OIDC status.
func (c *externalAuthAvailableController) determinePerClientConditions(ctx context.Context, externalAuth *coreapi.HCPOpenShiftClusterExternalAuth, key controllerutils.HCPExternalAuthKey) ([]metav1.Condition, error) {
	if len(externalAuth.Properties.Clients) == 0 {
		return nil, nil
	}

	hostedCluster, err := kubeapplierhelpers.GetCachedHostedClusterForCluster(
		ctx,
		c.readDesireLister,
		key.SubscriptionID,
		key.ResourceGroupName,
		key.HCPClusterName,
	)
	if err != nil {
		return nil, utils.TrackError(err)
	}

	conditions := make([]metav1.Condition, 0, len(externalAuth.Properties.Clients))

	for _, client := range externalAuth.Properties.Clients {
		condType := coreapi.PerClientAvailableConditionType(client.Component.Name)

		if client.Type == metadataapi.ExternalAuthClientTypePublic {
			conditions = append(conditions, metav1.Condition{
				Type:    condType,
				Status:  metav1.ConditionTrue,
				Reason:  coreapi.ExternalAuthReasonOIDCConfigAvailable,
				Message: "Public client does not require a secret",
			})
			continue
		}

		if hostedCluster == nil {
			conditions = append(conditions, metav1.Condition{
				Type:    condType,
				Status:  metav1.ConditionFalse,
				Reason:  coreapi.ExternalAuthReasonHostedClusterNotReady,
				Message: "Waiting for HostedCluster to be observed",
			})
			continue
		}

		if hostedCluster.Status.Configuration == nil {
			conditions = append(conditions, metav1.Condition{
				Type:    condType,
				Status:  metav1.ConditionUnknown,
				Reason:  coreapi.ExternalAuthReasonHostedClusterNotReady,
				Message: "HostedCluster authentication status not yet available",
			})
			continue
		}

		oidcStatus := c.findOIDCClientStatus(client.Component.Name, client.Component.AuthClientNamespace, hostedCluster.Status.Configuration.Authentication.OIDCClients)
		if oidcStatus == nil {
			conditions = append(conditions, metav1.Condition{
				Type:    condType,
				Status:  metav1.ConditionFalse,
				Reason:  coreapi.ExternalAuthReasonHostedClusterNotReady,
				Message: "OIDC client status not yet reported by the hosted cluster",
			})
			continue
		}

		conditions = append(conditions, c.mapSingleClientConditions(condType, oidcStatus.Conditions, client.Type))
	}

	return conditions, nil
}

// findOIDCClientStatus returns the OIDCClientStatus matching the given
// component name and namespace (case-insensitive), or nil if not found.
func (c *externalAuthAvailableController) findOIDCClientStatus(name, namespace string, observed []configv1.OIDCClientStatus) *configv1.OIDCClientStatus {
	key := c.oidcClientKey(name, namespace)
	for i := range observed {
		if c.oidcClientKey(observed[i].ComponentName, observed[i].ComponentNamespace) == key {
			return &observed[i]
		}
	}
	return nil
}

func (c *externalAuthAvailableController) oidcClientKey(name, namespace string) string {
	return strings.ToLower(name) + "/" + strings.ToLower(namespace)
}

// mapSingleClientConditions translates one OIDCClientStatus's conditions into
// a per-client Available condition for the ServiceProviderExternalAuth.
func (c *externalAuthAvailableController) mapSingleClientConditions(condType string, conditions []metav1.Condition, clientType metadataapi.ExternalAuthClientType) metav1.Condition {
	degraded := apimeta.FindStatusCondition(conditions, "Degraded")
	if degraded != nil && degraded.Status == metav1.ConditionTrue {
		if degraded.Reason == coreapi.HostedClusterOIDCClientSecretGet && clientType == metadataapi.ExternalAuthClientTypeConfidential {
			return metav1.Condition{
				Type:    condType,
				Status:  metav1.ConditionFalse,
				Reason:  coreapi.ExternalAuthConfidentialReasonAwaitingSecret,
				Message: "The external auth provider is waiting for the client secret to be created in the openshift-config namespace",
			}
		}
		return metav1.Condition{
			Type:    condType,
			Status:  metav1.ConditionFalse,
			Reason:  degraded.Reason,
			Message: degraded.Message,
		}
	}

	available := apimeta.FindStatusCondition(conditions, "Available")
	if available != nil && available.Status == metav1.ConditionTrue &&
		available.Reason == coreapi.HostedClusterOIDCConfigAvailable {
		return metav1.Condition{
			Type:    condType,
			Status:  metav1.ConditionTrue,
			Reason:  coreapi.ExternalAuthReasonOIDCConfigAvailable,
			Message: available.Message,
		}
	}

	if available != nil && available.Status == metav1.ConditionFalse {
		return metav1.Condition{
			Type:    condType,
			Status:  metav1.ConditionFalse,
			Reason:  available.Reason,
			Message: available.Message,
		}
	}

	return metav1.Condition{
		Type:    condType,
		Status:  metav1.ConditionFalse,
		Reason:  coreapi.ExternalAuthReasonHostedClusterNotReady,
		Message: "OIDC client conditions do not indicate readiness",
	}
}
