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
	"time"

	"k8s.io/apimachinery/pkg/api/equality"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

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
	coreapi.ExternalAuthOIDCClientsDegradedCondition:            {},
	coreapi.ExternalAuthOIDCIssuerCACertificateInvalidCondition: {},
}

// externalAuthUserFacingConditionsDegradedAggregator aggregates allowlisted conditions
// from ServiceProviderExternalAuth.Status.Conditions into a single user-facing
// Degraded condition on HCPOpenShiftClusterExternalAuth.Status.UserFacingConditions.
//
// Only conditions whose Type is in allowedDegradedConditionTypes are considered.
// For any allowlisted Type that hasn't been reported yet, an Unknown
// condition is injected so the downstream aggregator always receives a complete
// set and the user can see which specific contributors have and haven't reported.
//
// The complete set is passed to statusutils.AggregateExternalAuthUserFacingDegradedCondition,
// which produces the final Degraded condition:
//   - All conditions False -> Degraded=False / AsExpected / "All is well"
//   - Any condition True -> Degraded=True / ExternalAuthDegraded
//     with messages prefixed by source condition type
//   - Otherwise (some Unknown, none True) -> Degraded=Unknown / ExternalAuthDegradedStateUnknown
//     with messages showing which contributors are still waiting
type externalAuthUserFacingConditionsDegradedAggregator struct {
	externalAuthLister                corelisters.ExternalAuthLister
	serviceProviderExternalAuthLister corelisters.ServiceProviderExternalAuthLister
	resourcesDBClient                 corecosmosstorage.ResourcesDBClient
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

	allowlisted := c.collectAllowlistedConditions(serviceProviderExternalAuth.Status.Conditions)
	aggregated := statusutils.AggregateExternalAuthUserFacingDegradedCondition(allowlisted)

	replacement := existing.DeepCopy()
	apimeta.SetStatusCondition(&replacement.Status.UserFacingConditions, aggregated)
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

// collectAllowlistedConditions iterates through conditions, collecting every
// allowlisted condition. For any allowlisted Type
// that is absent from the input, an Unknown condition is injected
// so that the downstream aggregator always receives a complete set.
//
// This avoids the false-negative where partial data (e.g. only one
// contributor reporting False) would be misinterpreted as "all is well",
// while still showing which specific contributors have and haven't reported.
//
// The input conditions are assumed to have unique Types.
func (c *externalAuthUserFacingConditionsDegradedAggregator) collectAllowlistedConditions(conditions []metav1.Condition) []metav1.Condition {
	seenConditionTypes := make(map[string]struct{}, len(allowedDegradedConditionTypes))
	allowlisted := make([]metav1.Condition, 0, len(allowedDegradedConditionTypes))
	for _, cond := range conditions {
		if _, ok := allowedDegradedConditionTypes[cond.Type]; !ok {
			continue
		}
		seenConditionTypes[cond.Type] = struct{}{}
		allowlisted = append(allowlisted, cond)
	}
	for condType := range allowedDegradedConditionTypes {
		if _, ok := seenConditionTypes[condType]; !ok {
			allowlisted = append(allowlisted, metav1.Condition{
				Type:    condType,
				Status:  metav1.ConditionUnknown,
				Reason:  coreapi.ExternalAuthUserFacingDegradedReasonEvaluationIncomplete,
				Message: "Waiting for condition to be evaluated",
			})
		}
	}
	return allowlisted
}
