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

package app

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/Azure/ARO-HCP/arohcp-cosmos-client/api/kubeapplierapi"
)

func TestUnblockHostedClusterHasDistinctOperationMetric(t *testing.T) {
	metrics := newDesireOperationMetrics(prometheus.NewRegistry())
	desire := &kubeapplierapi.ApplyDesire{
		Spec: kubeapplierapi.ApplyDesireSpec{
			Type:       kubeapplierapi.ApplyDesireTypeUnblockHostedCluster,
			TargetItem: kubeapplierapi.ResourceReference{Resource: "namespaces"},
		},
		Status: kubeapplierapi.ApplyDesireStatus{
			Conditions: []metav1.Condition{{
				Type: kubeapplierapi.ConditionTypeSuccessful, Status: metav1.ConditionTrue,
				Reason: kubeapplierapi.ConditionReasonNoErrors,
			}},
		},
	}
	metrics.recordApplyDesireOperation(desire)
	require.Equal(t, float64(1), testutil.ToFloat64(metrics.operationsTotal.WithLabelValues(
		OperationUnblockHostedCluster, "", "namespaces", "True", kubeapplierapi.ConditionReasonNoErrors)))
	require.Contains(t, initCounts()["apply"], kubeapplierapi.ConditionTypeHostedClusterUnblocked)
}
