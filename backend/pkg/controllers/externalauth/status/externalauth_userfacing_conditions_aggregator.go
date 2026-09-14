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

// userFacingConditionTypes is the static whitelist of condition types that are
// promoted from ServiceProviderExternalAuth.Status.Conditions to
// ExternalAuth.Status.UserFacingConditions.
var userFacingConditionTypes = map[string]struct{}{
	coreapi.ExternalAuthAvailableCondition: {},
}

// externalAuthUserFacingConditionsAggregator promotes whitelisted conditions
// from ServiceProviderExternalAuth.Status.Conditions onto
// HCPOpenShiftClusterExternalAuth.Status.UserFacingConditions.
//
// A condition is promoted when its Type is in the static whitelist
// (currently only "Available"). Stale conditions that existed on the
// ExternalAuth but are no longer present on the ServiceProviderExternalAuth
// are removed.
//
// Backend controllers (e.g. ExternalAuthAvailableController) write conditions
// onto ServiceProviderExternalAuth; this aggregator selectively lifts the
// user-facing subset up to the ExternalAuth resource where they are visible
// through the ARM API.
type externalAuthUserFacingConditionsAggregator struct {
	externalAuthLister                corelisters.ExternalAuthLister
	serviceProviderExternalAuthLister corelisters.ServiceProviderExternalAuthLister
	resourcesDBClient                 corecosmosstorage.ResourcesDBClient
}

var _ controllerutils.ExternalAuthSyncer = (*externalAuthUserFacingConditionsAggregator)(nil)

// NewExternalAuthUserFacingConditionsAggregatorController creates a controller
// that promotes whitelisted ServiceProviderExternalAuth conditions onto the
// external auth's Status.UserFacingConditions.
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

// isUserFacingCondition returns true if the condition type should be surfaced
// through the ARM API.
func isUserFacingCondition(condType string) bool {
	_, ok := userFacingConditionTypes[condType]
	return ok
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

	replacement := existing.DeepCopy()

	// Collect the set of condition types being promoted from ServiceProviderExternalAuth.
	promoted := make(map[string]struct{})
	for _, condition := range serviceProviderExternalAuth.Status.Conditions {
		if isUserFacingCondition(condition.Type) {
			apimeta.SetStatusCondition(&replacement.Status.UserFacingConditions, condition)
			promoted[condition.Type] = struct{}{}
		}
	}

	// Remove stale conditions that are no longer on the ServiceProviderExternalAuth.
	cleaned := make([]metav1.Condition, 0, len(replacement.Status.UserFacingConditions))
	for _, condition := range replacement.Status.UserFacingConditions {
		if isUserFacingCondition(condition.Type) {
			if _, ok := promoted[condition.Type]; !ok {
				continue
			}
		}
		cleaned = append(cleaned, condition)
	}
	replacement.Status.UserFacingConditions = cleaned

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
