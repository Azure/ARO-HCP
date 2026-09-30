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
// To add a new contributor, append its condition type here.
var allowedDegradedConditionTypes = map[string]struct{}{
	coreapi.ExternalAuthOIDCClientsDegradedCondition: {},
}

// externalAuthUserFacingConditionsDegradedAggregator aggregates allowlisted conditions
// from ServiceProviderExternalAuth.Status.Conditions into a single user-facing
// OIDCClientsDegraded condition on HCPOpenShiftClusterExternalAuth.Status.UserFacingConditions.
//
// Only conditions whose Type is in allowedDegradedConditionTypes are considered.
// Each allowlisted condition is converted into a SourcedCondition and passed to
// statusutils.UnionCondition, which handles the aggregation:
//   - Zero allowlisted sources -> OIDCClientsDegraded=False/AsExpected/"All is well"
//   - All allowlisted sources False -> not emitted -> same good default
//   - Any allowlisted source True -> OIDCClientsDegraded=True with aggregated reason/message
//
// Nil inertia is used because the internal condition already has its own inertia
// from the OIDC controller.
//
// This design allows future internal conditions to contribute to the user-facing
// OIDCClientsDegraded condition by adding their type to allowedDegradedConditionTypes,
// without changing the aggregation logic.
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

func (c *externalAuthUserFacingConditionsDegradedAggregator) SyncOnce(ctx context.Context, key controllerutils.HCPExternalAuthKey) error {
	existing, err := c.externalAuthLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, key.HCPExternalAuthName)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get ExternalAuth from cache: %w", err))
	}

	serviceProviderExternalAuth, err := c.serviceProviderExternalAuthLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, key.HCPExternalAuthName)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get ServiceProviderExternalAuth from cache: %w", err))
	}

	sources := collectAllowlistedSources(serviceProviderExternalAuth.Status.Conditions)
	aggregated := statusutils.UnionCondition(
		coreapi.ExternalAuthOIDCClientsDegradedCondition,
		metav1.ConditionFalse,
		nil,
		time.Now(),
		sources...,
	)

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

// collectAllowlistedSources converts allowlisted SPEA conditions into
// SourcedCondition entries for UnionCondition. The condition type is used
// as the source name, giving the standard "Source: message" formatting.
// Only conditions whose Type is in allowedDegradedConditionTypes are
// included; all others are silently ignored. Conditions with Status=False
// are not emitted, matching the report-only-degraded pattern used by the
// cluster/nodepool aggregators.
func collectAllowlistedSources(serviceProviderExternalAuthConditions []metav1.Condition) []statusutils.SourcedCondition {
	var sources []statusutils.SourcedCondition
	for _, condition := range serviceProviderExternalAuthConditions {
		if _, allowed := allowedDegradedConditionTypes[condition.Type]; !allowed {
			continue
		}
		if condition.Status == metav1.ConditionFalse {
			continue
		}
		sources = append(sources, statusutils.SourcedCondition{
			ControllerName: condition.Type,
			Condition: metav1.Condition{
				Type:               coreapi.ExternalAuthOIDCClientsDegradedCondition,
				Status:             condition.Status,
				Reason:             condition.Reason,
				Message:            condition.Message,
				LastTransitionTime: condition.LastTransitionTime,
			},
		})
	}
	return sources
}
