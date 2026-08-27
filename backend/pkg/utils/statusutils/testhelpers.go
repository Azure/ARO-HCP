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

package statusutils

import (
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
)

// Shared resource-identity constants used across the aggregator tests.
const (
	TestSubscriptionID    = "00000000-0000-0000-0000-000000000000"
	TestResourceGroupName = "test-rg"
	TestClusterName       = "test-cluster"
	TestNodePoolName      = "test-nodepool"
	TestExternalAuthName  = "test-externalauth"
)

// FixedNow is the synthetic "now" used by the aggregator and UnionCondition
// tests so the inertia arithmetic is reproducible. Pick a value that doesn't
// sit on a daylight-savings boundary.
var FixedNow = time.Date(2026, 6, 4, 12, 0, 0, 0, time.UTC)

// ControllerUnder builds an coreapi.Controller doc that is a direct child of
// the given parent resource ID (cluster, node pool, or external auth) with
// the given controller name, carrying a Degraded condition that has held
// `age` long.
func ControllerUnder(parentResourceID *azcorearm.ResourceID, controllerName string, status metav1.ConditionStatus, reason, message string, age time.Duration) *coreapi.Controller {
	rid := metadataapi.Must(azcorearm.ParseResourceID(parentResourceID.String() + "/" + coreapi.ControllerResourceTypeName + "/" + controllerName))
	return &coreapi.Controller{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   rid,
			PartitionKey: strings.ToLower(rid.SubscriptionID),
		},
		ExternalID: parentResourceID,
		Status: coreapi.ControllerStatus{
			Conditions: []metav1.Condition{
				{
					Type:               DegradedConditionType,
					Status:             status,
					Reason:             reason,
					Message:            message,
					LastTransitionTime: metav1.NewTime(FixedNow.Add(-age)),
				},
			},
		},
	}
}

// DegradedConditionAged returns a Degraded metav1.Condition of the given
// status/reason/message whose LastTransitionTime is `age` before FixedNow, so
// the inertia arithmetic in the aggregator and helper tests is reproducible.
func DegradedConditionAged(status metav1.ConditionStatus, reason, message string, age time.Duration) metav1.Condition {
	return metav1.Condition{
		Type:               DegradedConditionType,
		Status:             status,
		Reason:             reason,
		Message:            message,
		LastTransitionTime: metav1.NewTime(FixedNow.Add(-age)),
	}
}

// ApplyDesireUnder builds a kubeapplierapi.ApplyDesire nested directly under
// the given cluster resource ID, carrying the supplied conditions. Pass no
// conditions to model a desire that has not reported a Degraded condition yet.
func ApplyDesireUnder(parentResourceID *azcorearm.ResourceID, name string, conditions ...metav1.Condition) *kubeapplierapi.ApplyDesire {
	rid := metadataapi.Must(azcorearm.ParseResourceID(parentResourceID.String() + "/" + kubeapplierapi.ApplyDesireResourceTypeName + "/" + name))
	return &kubeapplierapi.ApplyDesire{
		CosmosMetadata: coreapi.CosmosMetadata{ResourceID: rid},
		Status:         kubeapplierapi.ApplyDesireStatus{Conditions: conditions},
	}
}

// ReadDesireUnder builds a kubeapplierapi.ReadDesire nested directly under the
// given cluster resource ID, carrying the supplied conditions. Pass no
// conditions to model a desire that has not reported a Degraded condition yet.
func ReadDesireUnder(parentResourceID *azcorearm.ResourceID, name string, conditions ...metav1.Condition) *kubeapplierapi.ReadDesire {
	rid := metadataapi.Must(azcorearm.ParseResourceID(parentResourceID.String() + "/" + kubeapplierapi.ReadDesireResourceTypeName + "/" + name))
	return &kubeapplierapi.ReadDesire{
		CosmosMetadata: coreapi.CosmosMetadata{ResourceID: rid},
		Status:         kubeapplierapi.ReadDesireStatus{Conditions: conditions},
	}
}
