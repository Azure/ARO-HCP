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

package operationtesting

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kruntime "k8s.io/apimachinery/pkg/runtime"

	secretsyncv1alpha1 "sigs.k8s.io/secrets-store-sync-controller/api/v1alpha1"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/Azure/ARO-HCP/backend/pkg/kubeapplierhelpers"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/apihelpers/kubeapplierapihelpers"
)

// NewHostedClusterReadDesire builds a kube-applier ReadDesire fixture wrapping the
// given HostedCluster. When no conditions are supplied it defaults to a successful
// observation. Shared across the resource operations test packages.
func NewHostedClusterReadDesire(t *testing.T, hostedCluster *v1beta1.HostedCluster, conditions ...metav1.Condition) *kubeapplierapi.ReadDesire {
	t.Helper()
	raw, err := json.Marshal(hostedCluster)
	require.NoError(t, err)
	if conditions == nil {
		// Default: kube-applier successfully observed the target.
		conditions = []metav1.Condition{
			{Type: kubeapplierapi.ConditionTypeSuccessful, Status: metav1.ConditionTrue, Reason: kubeapplierapi.ConditionReasonNoErrors},
		}
	}

	resourceID := metadataapi.Must(azcorearm.ParseResourceID(
		kubeapplierapihelpers.ToClusterScopedReadDesireResourceIDString(
			TestSubscriptionID, TestResourceGroupName, TestClusterName, kubeapplierhelpers.ReadDesireNameReadonlyHostedCluster)))

	return &kubeapplierapi.ReadDesire{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(resourceID.SubscriptionID),
		},
		Status: kubeapplierapi.ReadDesireStatus{
			Conditions:  conditions,
			KubeContent: &kruntime.RawExtension{Raw: raw},
		},
	}
}

// NewIngressSecretSyncReadDesire builds a kube-applier ReadDesire fixture wrapping the
// ingress wildcard certificate's SecretSync object (see kubeapplierhelpers.IngressSecretSyncDesireName).
// secretSyncConditions are the SecretSync object's own status conditions, as reported by
// secrets-store-sync-controller (e.g. a "SecretCreated" condition type). When none are
// supplied it defaults to a successful first sync.
func NewIngressSecretSyncReadDesire(t *testing.T, secretSyncConditions ...metav1.Condition) *kubeapplierapi.ReadDesire {
	t.Helper()
	if secretSyncConditions == nil {
		secretSyncConditions = []metav1.Condition{
			{Type: "SecretCreated", Status: metav1.ConditionTrue, Reason: "CreateSuccessful"},
		}
	}
	secretSync := &secretsyncv1alpha1.SecretSync{
		Status: secretsyncv1alpha1.SecretSyncStatus{
			Conditions: secretSyncConditions,
		},
	}
	raw, err := json.Marshal(secretSync)
	require.NoError(t, err)

	resourceID := metadataapi.Must(azcorearm.ParseResourceID(
		kubeapplierapihelpers.ToClusterScopedReadDesireResourceIDString(
			TestSubscriptionID, TestResourceGroupName, TestClusterName, kubeapplierhelpers.IngressSecretSyncDesireName)))

	return &kubeapplierapi.ReadDesire{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(resourceID.SubscriptionID),
		},
		Status: kubeapplierapi.ReadDesireStatus{
			// Default: kube-applier successfully observed the target.
			Conditions:  []metav1.Condition{{Type: kubeapplierapi.ConditionTypeSuccessful, Status: metav1.ConditionTrue, Reason: kubeapplierapi.ConditionReasonNoErrors}},
			KubeContent: &kruntime.RawExtension{Raw: raw},
		},
	}
}
