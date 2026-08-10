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

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/Azure/ARO-HCP/backend/pkg/kubeapplierhelpers"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
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
		kubeapplierapi.ToClusterScopedReadDesireResourceIDString(
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
