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

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"k8s.io/apimachinery/pkg/api/equality"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/component-base/metrics/legacyregistry"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/statusutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/informers/coreinformers"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const (
	// ExternalAuthUserFacingConditionsDegradedAggregatorControllerName is the controller name used for
	// metrics labels, ctx values, log fields, and the Controller document name.
	ExternalAuthUserFacingConditionsDegradedAggregatorControllerName = "ExternalAuthUserFacingConditionsDegradedAggregator"
)

// allowedDegradedConditionTypes is the explicit allowlist of
// ServiceProviderExternalAuth condition types that contribute to the
// user-facing Degraded condition. Only conditions whose Type appears in
// this list are aggregated; all others are silently ignored, preventing
// internal-only conditions from leaking into the user-facing API.
//
// Each allowlisted condition type becomes a source whose Type is used as the
// prefix in the aggregated Degraded message (e.g.
// "OIDCClientsDegraded: console: secret missing"). This allows different
// internal condition types to be aggregated into a single user-facing
// Degraded condition.
//
// To add a new contributor, append its condition type here.
var allowedDegradedConditionTypes = map[string]struct{}{
	coreapi.ExternalAuthOIDCClientsDegradedCondition: {},
}

// externalAuthUserFacingConditionsDegradedAggregator aggregates allowlisted conditions
// from ServiceProviderExternalAuth.Status.Conditions into a single user-facing
// Degraded condition on HCPOpenShiftClusterExternalAuth.Status.UserFacingConditions.
//
// Only conditions whose Type is in allowedDegradedConditionTypes are considered.
// The allowlisted conditions are passed to statusutils.AggregateExternalAuthUserFacingDegradedCondition,
// which produces the final Degraded condition:
//   - All allowlisted conditions False -> Degraded=False / AsExpected / "All is well"
//   - Any allowlisted condition True -> Degraded=True / ExternalAuthDegraded
//     with messages prefixed by source condition type
//   - All allowlisted conditions Unknown (none True) -> Degraded=Unknown / ExternalAuthDegradedStateUnknown
//     with messages prefixed by source condition type
//
// When no allowlisted condition types exist on the ServiceProviderExternalAuth
// (i.e. the internal controllers have not reported yet), the aggregator produces
// Degraded=Unknown / EvaluationIncomplete to avoid a false-negative where the
// absence of data is misinterpreted as "all is well".
type externalAuthUserFacingConditionsDegradedAggregator struct {
	externalAuthLister                corelisters.ExternalAuthLister
	serviceProviderExternalAuthLister corelisters.ServiceProviderExternalAuthLister
	resourcesDBClient                 corecosmosstorage.ResourcesDBClient
	// degradedGauge backs backend_externalauth_degraded{resource_id, controller}:
	// set to 1 when the ExternalAuth is user-facing degraded
	// (UserFacingConditions[Degraded]=True) and 0 otherwise.
	degradedGauge *prometheus.GaugeVec
}

var _ controllerutils.ExternalAuthSyncer = (*externalAuthUserFacingConditionsDegradedAggregator)(nil)

// NewExternalAuthUserFacingConditionsDegradedAggregatorController creates a controller
// that aggregates allowlisted ServiceProviderExternalAuth conditions into a
// single user-facing Degraded condition on the external auth's
// Status.UserFacingConditions.
func NewExternalAuthUserFacingConditionsDegradedAggregatorController(
	resourcesDBClient corecosmosstorage.ResourcesDBClient,
	externalAuthLister corelisters.ExternalAuthLister,
	serviceProviderExternalAuthLister corelisters.ServiceProviderExternalAuthLister,
	informers coreinformers.BackendInformers,
) controllerutils.Controller {
	syncer := &externalAuthUserFacingConditionsDegradedAggregator{
		externalAuthLister:                externalAuthLister,
		serviceProviderExternalAuthLister: serviceProviderExternalAuthLister,
		resourcesDBClient:                 resourcesDBClient,
		degradedGauge: promauto.With(legacyregistry.Registerer()).NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "backend_externalauth_degraded",
				Help: "Whether an external auth is user-facing degraded (1) or not (0).",
			},
			[]string{"resource_id", "controller"},
		),
	}
	return controllerutils.NewExternalAuthWatchingController(
		ExternalAuthUserFacingConditionsDegradedAggregatorControllerName,
		resourcesDBClient,
		informers,
		1*time.Minute,
		syncer,
	)
}

func (c *externalAuthUserFacingConditionsDegradedAggregator) needsWork(externalAuth *coreapi.ExternalAuth) bool {
	return externalAuth.ServiceProviderProperties.DeletionTimestamp == nil
}

func (c *externalAuthUserFacingConditionsDegradedAggregator) SyncOnce(ctx context.Context, key controllerutils.HCPExternalAuthKey) error {
	existing, err := c.externalAuthLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, key.HCPExternalAuthName)
	if cosmosstorageutils.IsNotFoundError(err) {
		// ExternalAuth deleted — set gauge to 0 so stale degraded=1 doesn't linger.
		c.recordDegradedGaugeForKey(key, metav1.ConditionFalse)
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

	var aggregated metav1.Condition
	if !c.hasAllowlistedCondition(serviceProviderExternalAuth.Status.Conditions) {
		aggregated = metav1.Condition{
			Type:    statusutils.DegradedConditionType,
			Status:  metav1.ConditionUnknown,
			Reason:  coreapi.ExternalAuthUserFacingDegradedReasonEvaluationIncomplete,
			Message: "Waiting for conditions to be evaluated",
		}
	} else {
		allowlisted := c.collectAllowlisted(serviceProviderExternalAuth.Status.Conditions)
		aggregated = statusutils.AggregateExternalAuthUserFacingDegradedCondition(
			allowlisted,
		)
	}

	replacement := existing.DeepCopy()
	apimeta.SetStatusCondition(&replacement.Status.UserFacingConditions, aggregated)

	// Record the gauge regardless of whether we write to Cosmos
	c.recordDegradedGauge(existing.ResourceID, aggregated.Status)

	if equality.Semantic.DeepEqual(existing.Status.UserFacingConditions, replacement.Status.UserFacingConditions) {
		return nil
	}

	externalAuthCRUD := c.resourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).ExternalAuth(key.HCPClusterName)
	_, err = externalAuthCRUD.Replace(ctx, replacement, nil)
	if cosmosstorageutils.IsPreconditionFailedError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to replace ExternalAuth: %w", err))
	}
	return nil
}

// recordDegradedGauge sets backend_externalauth_degraded to 1 if the
// user-facing Degraded condition is True, 0 otherwise.
func (c *externalAuthUserFacingConditionsDegradedAggregator) recordDegradedGauge(
	externalAuthResourceID *azcorearm.ResourceID,
	status metav1.ConditionStatus,
) {
	if externalAuthResourceID == nil {
		return
	}
	resourceID := strings.ToLower(externalAuthResourceID.String())
	c.setDegradedGauge(resourceID, status)
}

// recordDegradedGaugeForKey sets backend_externalauth_degraded using key components
// when the ResourceID is not available (e.g., on NotFound).
func (c *externalAuthUserFacingConditionsDegradedAggregator) recordDegradedGaugeForKey(
	key controllerutils.HCPExternalAuthKey,
	status metav1.ConditionStatus,
) {
	resourceID := strings.ToLower(fmt.Sprintf(
		"/subscriptions/%s/resourcegroups/%s/providers/microsoft.redhatopenshift/hcpopenshiftclusters/%s/externalauths/%s",
		key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, key.HCPExternalAuthName,
	))
	c.setDegradedGauge(resourceID, status)
}

// setDegradedGauge sets the gauge value for the given resource ID.
func (c *externalAuthUserFacingConditionsDegradedAggregator) setDegradedGauge(resourceID string, status metav1.ConditionStatus) {
	value := 0.0
	if status == metav1.ConditionTrue {
		value = 1.0
	}

	c.degradedGauge.With(prometheus.Labels{
		"resource_id": resourceID,
		"controller":  ExternalAuthUserFacingConditionsDegradedAggregatorControllerName,
	}).Set(value)
}

// hasAllowlistedCondition reports whether any condition in the slice has a
// Type that appears in allowedDegradedConditionTypes. This distinguishes
// "internal controllers have not reported yet" (no allowlisted conditions)
// from "all is well" (allowlisted conditions present but all False).
func (c *externalAuthUserFacingConditionsDegradedAggregator) hasAllowlistedCondition(conditions []metav1.Condition) bool {
	for _, c := range conditions {
		if _, ok := allowedDegradedConditionTypes[c.Type]; ok {
			return true
		}
	}
	return false
}

// collectAllowlisted filters ServiceProviderExternalAuth conditions to only
// those whose Type is in allowedDegradedConditionTypes. The returned
// conditions retain their original Type, Status, Reason, and Message so that
// AggregateExternalAuthUserFacingDegradedCondition can use the Type as the source-name prefix in
// the aggregated message.
func (c *externalAuthUserFacingConditionsDegradedAggregator) collectAllowlisted(conditions []metav1.Condition) []metav1.Condition {
	var result []metav1.Condition
	for _, c := range conditions {
		if _, ok := allowedDegradedConditionTypes[c.Type]; ok {
			result = append(result, c)
		}
	}
	return result
}
