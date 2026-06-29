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
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	sharedstatus "github.com/Azure/ARO-HCP/backend/pkg/controllers/shared/status"
	"github.com/Azure/ARO-HCP/internal/api"
)

// Shared resource-identity constants used across the aggregator tests.
const (
	testSubscriptionID    = "00000000-0000-0000-0000-000000000000"
	testResourceGroupName = "test-rg"
	testClusterName       = "test-cluster"
	testExternalAuthName  = "test-externalauth"
)

// fixedNow is the synthetic "now" used by every aggregator test case so
// that inertia windows can be computed deterministically.
var fixedNow = time.Date(2026, 6, 4, 12, 0, 0, 0, time.UTC)

// alwaysSyncCooldownChecker permits every sync attempt. Aggregator unit
// tests don't exercise the cooldown path.
type alwaysSyncCooldownChecker struct{}

func (alwaysSyncCooldownChecker) CanSync(_ context.Context, _ any) bool { return true }

// controllerUnder builds an api.Controller doc that is a direct child of
// the given parent resource ID (cluster, node pool, or external auth) with
// the given controller name, carrying a Degraded condition that has held
// `age` long.
func controllerUnder(parentResourceID *azcorearm.ResourceID, controllerName string, status metav1.ConditionStatus, reason, message string, age time.Duration) *api.Controller {
	rid := api.Must(azcorearm.ParseResourceID(parentResourceID.String() + "/" + api.ControllerResourceTypeName + "/" + controllerName))
	return &api.Controller{
		CosmosMetadata: api.CosmosMetadata{
			ResourceID:   rid,
			PartitionKey: strings.ToLower(rid.SubscriptionID),
		},
		ExternalID: parentResourceID,
		Status: api.ControllerStatus{
			Conditions: []metav1.Condition{
				{
					Type:               sharedstatus.DegradedConditionType,
					Status:             status,
					Reason:             reason,
					Message:            message,
					LastTransitionTime: metav1.NewTime(fixedNow.Add(-age)),
				},
			},
		},
	}
}
