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
	"github.com/openshift/hypershift/api/hypershift/v1beta1"

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
	// ExternalAuthOIDCClientsDegradedControllerName is the controller name used for
	// metrics labels, ctx values, log fields, and the Controller document name.
	ExternalAuthOIDCClientsDegradedControllerName = "ExternalAuthOIDCClientsDegradedController"
)

// externalAuthOIDCClientsDegradedController reads the HostedCluster's
// OIDCClientStatus conditions from the ReadDesire cache and produces a
// single OIDCClientsDegraded condition on
// ServiceProviderExternalAuth.Status.Conditions.
//
// The controller evaluates all declared clients. For each client it checks
// the Degraded and Available conditions from the Hypershift-reported
// OIDCClientStatus. A client is degraded if Degraded=True or
// Available=False.
//
// If ANY client is degraded: OIDCClientsDegraded=True with a composite
// message listing only the degraded clients.
// If ALL clients are healthy: OIDCClientsDegraded=False.
//
// The ExternalAuthUserFacingConditionsAggregator then reads this condition
// (along with any future internal conditions) and produces a single
// user-facing Degraded condition on ExternalAuth.Status.UserFacingConditions.
type externalAuthOIDCClientsDegradedController struct {
	externalAuthLister                corelisters.ExternalAuthLister
	serviceProviderExternalAuthLister corelisters.ServiceProviderExternalAuthLister
	readDesireLister                  kubeapplierlisters.ReadDesireLister
	resourcesDBClient                 corecosmosstorage.ResourcesDBClient
}

var _ controllerutils.ExternalAuthSyncer = (*externalAuthOIDCClientsDegradedController)(nil)

// NewExternalAuthOIDCClientsDegradedController creates a controller that reads
// HostedCluster OIDCClientStatus conditions via the ReadDesire cache and maps
// them onto an OIDCClientsDegraded condition on
// ServiceProviderExternalAuth.Status.Conditions. The
// ExternalAuthUserFacingConditionsAggregator is responsible for promoting
// this into the user-facing Degraded condition.
func NewExternalAuthOIDCClientsDegradedController(
	resourcesDBClient corecosmosstorage.ResourcesDBClient,
	externalAuthLister corelisters.ExternalAuthLister,
	serviceProviderExternalAuthLister corelisters.ServiceProviderExternalAuthLister,
	readDesireLister kubeapplierlisters.ReadDesireLister,
	informers coreinformers.BackendInformers,
) controllerutils.Controller {
	syncer := &externalAuthOIDCClientsDegradedController{
		externalAuthLister:                externalAuthLister,
		serviceProviderExternalAuthLister: serviceProviderExternalAuthLister,
		readDesireLister:                  readDesireLister,
		resourcesDBClient:                 resourcesDBClient,
	}
	return controllerutils.NewExternalAuthWatchingController(
		ExternalAuthOIDCClientsDegradedControllerName,
		resourcesDBClient,
		informers,
		1*time.Minute,
		syncer,
	)
}

func (c *externalAuthOIDCClientsDegradedController) needsWork(externalAuth *coreapi.HCPOpenShiftClusterExternalAuth) bool {
	return externalAuth.ServiceProviderProperties.DeletionTimestamp == nil && externalAuth.ServiceProviderProperties.ClusterServiceID != nil
}

func (c *externalAuthOIDCClientsDegradedController) SyncOnce(ctx context.Context, key controllerutils.HCPExternalAuthKey) error {
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

	condition, err := c.determineOIDCClientsDegradedCondition(ctx, existing, key)
	if err != nil {
		return err
	}
	if condition == nil {
		return nil
	}

	replacement := serviceProviderExternalAuth.DeepCopy()
	apimeta.SetStatusCondition(&replacement.Status.Conditions, *condition)
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

// degradedClientMessage holds the user-facing message for a single degraded
// client, prefixed with its component name.
type degradedClientMessage struct {
	componentName string
	message       string
}

// determineOIDCClientsDegradedCondition reads HostedCluster OIDC client status
// from the ReadDesire cache and returns a single OIDCClientsDegraded condition.
// Returns nil when no clients are declared.
//
// If ANY client is degraded: OIDCClientsDegraded=True/OIDCClientDegradation
// with a composite message listing only degraded clients ("\n"-separated).
// If ALL clients are healthy: OIDCClientsDegraded=False/AsExpected.
func (c *externalAuthOIDCClientsDegradedController) determineOIDCClientsDegradedCondition(ctx context.Context, externalAuth *coreapi.HCPOpenShiftClusterExternalAuth, key controllerutils.HCPExternalAuthKey) (*metav1.Condition, error) {
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

	var degradedMessages []degradedClientMessage

	for _, client := range externalAuth.Properties.Clients {
		message := c.evaluateClient(client, hostedCluster)
		if message != nil {
			degradedMessages = append(degradedMessages, *message)
		}
	}

	if len(degradedMessages) == 0 {
		return &metav1.Condition{
			Type:    coreapi.ExternalAuthOIDCClientsDegradedCondition,
			Status:  metav1.ConditionFalse,
			Reason:  coreapi.ExternalAuthOIDCClientsDegradedReasonAsExpected,
			Message: coreapi.ExternalAuthMessageAllOperational,
		}, nil
	}

	parts := make([]string, 0, len(degradedMessages))
	for _, degradedMessage := range degradedMessages {
		parts = append(parts, fmt.Sprintf("%s: %s", degradedMessage.componentName, degradedMessage.message))
	}

	return &metav1.Condition{
		Type:    coreapi.ExternalAuthOIDCClientsDegradedCondition,
		Status:  metav1.ConditionTrue,
		Reason:  coreapi.ExternalAuthOIDCClientsDegradedReasonDegradation,
		Message: strings.Join(parts, "\n"),
	}, nil
}

// evaluateClient checks whether a single client is degraded. Returns nil if
// the client is healthy, or a degradedClientMessage if it is degraded.
func (c *externalAuthOIDCClientsDegradedController) evaluateClient(client coreapi.ExternalAuthClientProfile, hostedCluster *v1beta1.HostedCluster) *degradedClientMessage {
	if hostedCluster == nil {
		return &degradedClientMessage{
			componentName: client.Component.Name,
			message:       coreapi.ExternalAuthMessageGenericNotWorking,
		}
	}

	if hostedCluster.Status.Configuration == nil {
		return &degradedClientMessage{
			componentName: client.Component.Name,
			message:       coreapi.ExternalAuthMessageGenericNotWorking,
		}
	}

	oidcStatus := c.findOIDCClientStatus(client.Component.Name, client.Component.AuthClientNamespace, hostedCluster.Status.Configuration.Authentication.OIDCClients)
	if oidcStatus == nil {
		return &degradedClientMessage{
			componentName: client.Component.Name,
			message:       coreapi.ExternalAuthMessageGenericNotWorking,
		}
	}

	return c.mapSingleClientDegradation(oidcStatus.Conditions, client)
}

// findOIDCClientStatus returns the OIDCClientStatus matching the given
// component name and namespace (case-insensitive), or nil if not found.
func (c *externalAuthOIDCClientsDegradedController) findOIDCClientStatus(name, namespace string, observed []configv1.OIDCClientStatus) *configv1.OIDCClientStatus {
	key := c.oidcClientKey(name, namespace)
	for i := range observed {
		if c.oidcClientKey(observed[i].ComponentName, observed[i].ComponentNamespace) == key {
			return &observed[i]
		}
	}
	return nil
}

func (c *externalAuthOIDCClientsDegradedController) oidcClientKey(name, namespace string) string {
	return strings.ToLower(name) + "/" + strings.ToLower(namespace)
}

// mapSingleClientDegradation checks a single OIDCClientStatus's conditions
// and returns a degradedClientMessage if the client is degraded, or nil if
// it is healthy.
//
// Check order:
//  1. Degraded=True: map known reasons to user-friendly messages.
//  2. Available=False (when Degraded is not True): generic message.
//  3. Degraded=False AND Available=True: healthy, return nil.
func (c *externalAuthOIDCClientsDegradedController) mapSingleClientDegradation(conditions []metav1.Condition, client coreapi.ExternalAuthClientProfile) *degradedClientMessage {
	degraded := apimeta.FindStatusCondition(conditions, "Degraded")
	if degraded != nil && degraded.Status == metav1.ConditionTrue {
		message := mapDegradedReasonToMessage(degraded.Reason)
		return &degradedClientMessage{
			componentName: client.Component.Name,
			message:       message,
		}
	}

	available := apimeta.FindStatusCondition(conditions, "Available")
	if available != nil && available.Status == metav1.ConditionFalse {
		return &degradedClientMessage{
			componentName: client.Component.Name,
			message:       coreapi.ExternalAuthMessageGenericNotWorking,
		}
	}

	return nil
}

// mapDegradedReasonToMessage maps a Hypershift Degraded condition reason to a
// user-friendly message.
func mapDegradedReasonToMessage(reason string) string {
	switch reason {
	case coreapi.HostedClusterOIDCClientSecretGet:
		return coreapi.ExternalAuthMessageAwaitingSecret
	case coreapi.HostedClusterOIDCIssuerURLInvalid:
		return coreapi.ExternalAuthMessageIssuerURLInvalid
	default:
		return coreapi.ExternalAuthMessageGenericNotWorking
	}
}
