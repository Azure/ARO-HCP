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

// Hypershift OIDCClientStatus condition reasons set at runtime by the
// Hypershift operator. These are not exported by the Hypershift Go library,
// so we define them locally and match them against the ReadDesire cache.
//
// OCP versions that include Hypershift with ExternalAuth OIDC support
// (4.17+) report OIDCClientStatus.Conditions on the HostedCluster.
// Earlier versions do not populate the field; the controller treats a
// missing status entry as degraded with a generic message.
const (
	// hypershiftOIDCConfigAvailable is the Available condition reason when
	// the OIDC client configuration is fully operational.
	hypershiftOIDCConfigAvailable = "OIDCConfigAvailable"

	// hypershiftOIDCClientSecretGet is the Degraded condition reason when
	// the operator cannot find/read the client secret the user must create
	// in the openshift-config namespace.
	hypershiftOIDCClientSecretGet = "OIDCClientSecretGet"

	// hypershiftOIDCIssuerURLInvalid is the Degraded condition reason when
	// the issuer URL configured for the external auth provider is invalid.
	hypershiftOIDCIssuerURLInvalid = "OIDCIssuerURLInvalid"
)

// externalAuthOIDCClientsDegradedController reads the HostedCluster's
// OIDCClientStatus conditions from the ReadDesire cache and produces a
// single OIDCClientsDegraded condition on
// ServiceProviderExternalAuth.Status.Conditions.
//
// The controller evaluates all declared clients. For each client it checks
// the Degraded and Available conditions from the Hypershift-reported
// OIDCClientStatus. A client is considered healthy only when
// Degraded=False AND Available=True; all other combinations (including
// Unknown) are treated as degraded.
//
// If ANY client is degraded: OIDCClientsDegraded=True with a composite
// message listing only the degraded clients.
// If ALL clients are healthy: OIDCClientsDegraded=False.
//
// The ExternalAuthUserFacingDegradedAggregator then reads this condition
// (along with any future internal conditions) and produces a single
// user-facing Degraded condition on ExternalAuth.Status.UserFacingConditions.
//
// IMPORTANT: Messages written to this condition are surfaced verbatim to
// end users through the ARM API. Never include internal implementation
// details, Hypershift-internal reasons, or cluster infrastructure paths
// in condition messages.
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
// ExternalAuthUserFacingDegradedAggregator is responsible for promoting
// this into the user-facing Degraded condition.
//
// Messages written to this condition are surfaced to end users through the
// ARM API. Ensure all messages are safe for external consumption.
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

func (c *externalAuthOIDCClientsDegradedController) needsWork(externalAuth *coreapi.ExternalAuth) bool {
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

	hostedCluster, err := kubeapplierhelpers.GetCachedHostedClusterForCluster(
		ctx,
		c.readDesireLister,
		key.SubscriptionID,
		key.ResourceGroupName,
		key.HCPClusterName,
	)
	if err != nil {
		return utils.TrackError(err)
	}
	if hostedCluster == nil {
		return nil
	}

	condition := c.determineOIDCClientsDegradedCondition(existing, hostedCluster)

	replacement := serviceProviderExternalAuth.DeepCopy()
	apimeta.SetStatusCondition(&replacement.Status.Conditions, condition)
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
// client, identified by its component name.
type degradedClientMessage struct {
	componentName string
	message       string
}

// determineOIDCClientsDegradedCondition evaluates HostedCluster OIDC client
// status and returns a single OIDCClientsDegraded condition.
//
// If ANY client is degraded: OIDCClientsDegraded=True/OIDCClientDegradation
// with a composite message listing only degraded clients ("\n"-separated).
// If ALL clients are healthy (or none declared): OIDCClientsDegraded=False/AsExpected.
//
// The hostedCluster parameter must not be nil; SyncOnce handles the nil case
// by returning early before calling this method.
func (c *externalAuthOIDCClientsDegradedController) determineOIDCClientsDegradedCondition(externalAuth *coreapi.ExternalAuth, hostedCluster *v1beta1.HostedCluster) metav1.Condition {
	if len(externalAuth.Properties.Clients) == 0 {
		return metav1.Condition{
			Type:    coreapi.ExternalAuthOIDCClientsDegradedCondition,
			Status:  metav1.ConditionFalse,
			Reason:  coreapi.ExternalAuthOIDCClientsDegradedReasonAsExpected,
			Message: coreapi.ExternalAuthMessageAllOperational,
		}
	}

	var degradedMessages []degradedClientMessage

	for i := range externalAuth.Properties.Clients {
		message := evaluateExternalAuthClient(&externalAuth.Properties.Clients[i], hostedCluster)
		if message != nil {
			degradedMessages = append(degradedMessages, *message)
		}
	}

	if len(degradedMessages) == 0 {
		return metav1.Condition{
			Type:    coreapi.ExternalAuthOIDCClientsDegradedCondition,
			Status:  metav1.ConditionFalse,
			Reason:  coreapi.ExternalAuthOIDCClientsDegradedReasonAsExpected,
			Message: coreapi.ExternalAuthMessageAllOperational,
		}
	}

	parts := make([]string, 0, len(degradedMessages))
	for _, degradedMessage := range degradedMessages {
		parts = append(parts, fmt.Sprintf("%s: %s", degradedMessage.componentName, degradedMessage.message))
	}

	return metav1.Condition{
		Type:    coreapi.ExternalAuthOIDCClientsDegradedCondition,
		Status:  metav1.ConditionTrue,
		Reason:  coreapi.ExternalAuthOIDCClientsDegradedReasonDegradation,
		Message: strings.Join(parts, "\n"),
	}
}

// evaluateExternalAuthClient checks whether a single external auth client is
// degraded. Returns nil if the client is healthy, or a degradedClientMessage
// if it is degraded.
//
// The hostedCluster parameter must not be nil.
func evaluateExternalAuthClient(externalAuthClient *coreapi.ExternalAuthClientProfile, hostedCluster *v1beta1.HostedCluster) *degradedClientMessage {
	if hostedCluster.Status.Configuration == nil {
		return &degradedClientMessage{
			componentName: externalAuthClient.Component.Name,
			message:       coreapi.ExternalAuthMessageGenericNotWorking,
		}
	}

	oidcStatus := findOIDCClientStatus(externalAuthClient.Component.Name, externalAuthClient.Component.AuthClientNamespace, hostedCluster.Status.Configuration.Authentication.OIDCClients)
	if oidcStatus == nil {
		return &degradedClientMessage{
			componentName: externalAuthClient.Component.Name,
			message:       coreapi.ExternalAuthMessageGenericNotWorking,
		}
	}

	return mapSingleClientDegradation(oidcStatus.Conditions, externalAuthClient)
}

// findOIDCClientStatus returns the OIDCClientStatus matching the given
// component name and namespace (case-insensitive), or nil if not found.
func findOIDCClientStatus(componentName, componentNamespace string, observed []configv1.OIDCClientStatus) *configv1.OIDCClientStatus {
	key := oidcClientKey(componentName, componentNamespace)
	for i := range observed {
		if oidcClientKey(observed[i].ComponentName, observed[i].ComponentNamespace) == key {
			return &observed[i]
		}
	}
	return nil
}

func oidcClientKey(componentName, componentNamespace string) string {
	return strings.ToLower(componentName) + "/" + strings.ToLower(componentNamespace)
}

// mapSingleClientDegradation checks a single OIDCClientStatus's conditions
// and returns a degradedClientMessage if the client is degraded, or nil if
// it is healthy.
//
// A client is considered healthy only when Degraded=False AND
// Available=True. All other combinations (including Unknown or missing
// conditions) are treated as degraded:
//
//  1. Degraded=True: map known reasons to user-friendly messages.
//  2. Degraded is not False (Unknown/missing): generic message.
//  3. Available is not True (False/Unknown/missing): generic message.
//  4. Degraded=False AND Available=True: healthy, return nil.
func mapSingleClientDegradation(conditions []metav1.Condition, externalAuthClient *coreapi.ExternalAuthClientProfile) *degradedClientMessage {
	degraded := apimeta.FindStatusCondition(conditions, "Degraded")
	if degraded != nil && degraded.Status == metav1.ConditionTrue {
		message := mapDegradedReasonToMessage(degraded.Reason)
		return &degradedClientMessage{
			componentName: externalAuthClient.Component.Name,
			message:       message,
		}
	}

	available := apimeta.FindStatusCondition(conditions, "Available")

	isHealthy := degraded != nil && degraded.Status == metav1.ConditionFalse &&
		available != nil && available.Status == metav1.ConditionTrue

	if isHealthy {
		return nil
	}

	return &degradedClientMessage{
		componentName: externalAuthClient.Component.Name,
		message:       coreapi.ExternalAuthMessageGenericNotWorking,
	}
}

// mapDegradedReasonToMessage maps a Hypershift Degraded condition reason to a
// user-friendly message. Unknown reasons are mapped to a generic message to
// avoid leaking internal details.
func mapDegradedReasonToMessage(reason string) string {
	switch reason {
	case hypershiftOIDCClientSecretGet:
		return coreapi.ExternalAuthMessageAwaitingSecret
	case hypershiftOIDCIssuerURLInvalid:
		return coreapi.ExternalAuthMessageIssuerURLInvalid
	default:
		return coreapi.ExternalAuthMessageGenericNotWorking
	}
}
