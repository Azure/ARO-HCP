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
	externalAuthUserFacingConditionsAggregatorControllerName = "ExternalAuthUserFacingConditionsAggregator"
)

// externalAuthUserFacingConditionsAggregator aggregates all conditions from
// ServiceProviderExternalAuth.Status.Conditions into a single user-facing
// Degraded condition on HCPOpenShiftClusterExternalAuth.Status.UserFacingConditions.
//
// If ANY ServiceProviderExternalAuth condition has Status=True (meaning something is degraded), the
// aggregator produces Degraded=True with reason ExternalAuthProvider and a
// composite message prefixed by the source condition type.
//
// If ALL ServiceProviderExternalAuth conditions are False or absent, the aggregator produces
// Degraded=False with reason AsExpected.
//
// This design allows future internal conditions (beyond OIDC client state) to
// automatically contribute to the user-facing Degraded condition without
// changing the aggregator.
type externalAuthUserFacingConditionsAggregator struct {
	externalAuthLister                corelisters.ExternalAuthLister
	serviceProviderExternalAuthLister corelisters.ServiceProviderExternalAuthLister
	resourcesDBClient                 corecosmosstorage.ResourcesDBClient
}

var _ controllerutils.ExternalAuthSyncer = (*externalAuthUserFacingConditionsAggregator)(nil)

// NewExternalAuthUserFacingConditionsAggregatorController creates a controller
// that aggregates ServiceProviderExternalAuth conditions into a single
// user-facing Degraded condition on the external auth's
// Status.UserFacingConditions.
func NewExternalAuthUserFacingConditionsAggregatorController(
	resourcesDBClient corecosmosstorage.ResourcesDBClient,
	externalAuthLister corelisters.ExternalAuthLister,
	serviceProviderExternalAuthLister corelisters.ServiceProviderExternalAuthLister,
	informers coreinformers.BackendInformers,
) controllerutils.Controller {
	syncer := &externalAuthUserFacingConditionsAggregator{
		externalAuthLister:                externalAuthLister,
		serviceProviderExternalAuthLister: serviceProviderExternalAuthLister,
		resourcesDBClient:                 resourcesDBClient,
	}
	return controllerutils.NewExternalAuthWatchingController(
		externalAuthUserFacingConditionsAggregatorControllerName,
		resourcesDBClient,
		informers,
		1*time.Minute,
		syncer,
	)
}

func (c *externalAuthUserFacingConditionsAggregator) SyncOnce(ctx context.Context, key controllerutils.HCPExternalAuthKey) error {
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

	degraded := aggregateDegradedCondition(serviceProviderExternalAuth.Status.Conditions)

	replacement := existing.DeepCopy()
	apimeta.SetStatusCondition(&replacement.Status.UserFacingConditions, degraded)

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

// aggregateDegradedCondition produces a single Degraded condition from the
// ServiceProviderExternalAuth internal conditions. If any condition is True, the result is
// Degraded=True with a composite message; otherwise Degraded=False.
func aggregateDegradedCondition(serviceProviderExternalAuthConditions []metav1.Condition) metav1.Condition {
	var trueParts []string
	for _, condition := range serviceProviderExternalAuthConditions {
		if condition.Status == metav1.ConditionTrue {
			trueParts = append(trueParts, fmt.Sprintf("%s: %s", condition.Type, condition.Message))
		}
	}

	if len(trueParts) > 0 {
		return metav1.Condition{
			Type:    statusutils.DegradedConditionType,
			Status:  metav1.ConditionTrue,
			Reason:  coreapi.ExternalAuthUserFacingDegradedReason,
			Message: strings.Join(trueParts, "\n"),
		}
	}

	return metav1.Condition{
		Type:    statusutils.DegradedConditionType,
		Status:  metav1.ConditionFalse,
		Reason:  coreapi.ExternalAuthUserFacingDegradedReasonAsExpected,
		Message: coreapi.ExternalAuthMessageAllOperational,
	}
}
