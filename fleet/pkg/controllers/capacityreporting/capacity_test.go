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

package capacityreporting

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	"github.com/Azure/ARO-HCP/fleet/pkg/scheduling"
	"github.com/Azure/ARO-HCP/internal/kuberesources"
	capacityreportv1alpha1 "github.com/Azure/ARO-HCP/mgmt-agent/pkg/apis/capacityreport/v1alpha1"
)

var fixedTime = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func TestComputeObservedResources(t *testing.T) {
	tests := []struct {
		name           string
		report         *capacityreportv1alpha1.CapacityReport
		wantReportedAt *metav1.Time
		wantCurrent    corev1.ResourceList
		wantUsage      corev1.ResourceList
		wantRequests   corev1.ResourceList
	}{
		{
			name: "sums scheduling-relevant allocatable across SKUs",
			report: &capacityreportv1alpha1.CapacityReport{
				Status: capacityreportv1alpha1.CapacityReportStatus{
					LastReportedAt: ptr.To(metav1.NewTime(fixedTime)),
					Nodes: []capacityreportv1alpha1.NodeSKUCapacity{
						{
							SKU:   "Standard_D8ds_v5",
							Ready: 2,
							Allocatable: corev1.ResourceList{
								corev1.ResourceCPU:                 resource.MustParse("16"),
								corev1.ResourceMemory:              resource.MustParse("64Gi"),
								kuberesources.SwiftNICResourceName: resource.MustParse("4"),
								"hugepages-2Mi":                    resource.MustParse("0"),
							},
						},
						{
							SKU:   "Standard_D16ds_v5",
							Ready: 1,
							Allocatable: corev1.ResourceList{
								corev1.ResourceCPU:                 resource.MustParse("32"),
								corev1.ResourceMemory:              resource.MustParse("128Gi"),
								kuberesources.SwiftNICResourceName: resource.MustParse("4"),
								"ephemeral-storage":                resource.MustParse("100Gi"),
							},
						},
					},
					Usage: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("5"),
						corev1.ResourceMemory: resource.MustParse("10Gi"),
						"pods":                resource.MustParse("50"),
					},
					Requested: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("12"),
						corev1.ResourceMemory: resource.MustParse("24Gi"),
						"pods":                resource.MustParse("100"),
					},
				},
			},
			wantReportedAt: ptr.To(metav1.NewTime(fixedTime)),
			wantCurrent: corev1.ResourceList{
				corev1.ResourceCPU:                 resource.MustParse("48"),
				corev1.ResourceMemory:              resource.MustParse("192Gi"),
				kuberesources.SwiftNICResourceName: resource.MustParse("8"),
			},
			wantUsage: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("5"),
				corev1.ResourceMemory: resource.MustParse("10Gi"),
			},
			wantRequests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("12"),
				corev1.ResourceMemory: resource.MustParse("24Gi"),
			},
		},
		{
			name: "empty nodes produces empty current",
			report: &capacityreportv1alpha1.CapacityReport{
				Status: capacityreportv1alpha1.CapacityReportStatus{},
			},
			wantCurrent: corev1.ResourceList{},
		},
		{
			name: "nil usage stays nil",
			report: &capacityreportv1alpha1.CapacityReport{
				Status: capacityreportv1alpha1.CapacityReportStatus{
					Nodes: []capacityreportv1alpha1.NodeSKUCapacity{
						{
							SKU:   "Standard_D8ds_v5",
							Ready: 1,
							Allocatable: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("8"),
								corev1.ResourceMemory: resource.MustParse("32Gi"),
							},
						},
					},
				},
			},
			wantCurrent: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("8"),
				corev1.ResourceMemory: resource.MustParse("32Gi"),
			},
			wantUsage: nil,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if test.report.Status.Usage != nil {
				test.report.Status.HostedControlPlanes.Resources = &capacityreportv1alpha1.HostedControlPlaneResourceTotals{
					Usage: withZeroFloor(test.report.Status.Usage), Requested: withZeroFloor(test.report.Status.Requested),
				}
				test.report.Status.HostedControlPlanes.ReadyResources = &capacityreportv1alpha1.HostedControlPlaneResourceTotals{Usage: withZeroFloor(nil), Requested: withZeroFloor(nil)}
				test.report.Status.Usage = corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("999")}
				test.report.Status.Requested = test.report.Status.Usage.DeepCopy()
				test.wantUsage = withZeroFloor(test.wantUsage)
				test.wantRequests = withZeroFloor(test.wantRequests)
			}
			actual := ComputeObservedResources(test.report)
			assert.Equal(t, test.wantReportedAt, actual.LastReportedAt, "LastReportedAt")
			assertResourceListEqual(t, test.wantCurrent, actual.Capacity, "Current")
			if test.wantUsage != nil {
				assertResourceListEqual(t, test.wantUsage, actual.Usage, "Usage")
			} else {
				assert.Nil(t, actual.Usage, "Usage should be nil")
			}
			if test.wantRequests != nil {
				assertResourceListEqual(t, test.wantRequests, actual.Requests, "Requests")
			} else {
				assert.Nil(t, actual.Requests, "Requests should be nil")
			}
		})
	}
}

func TestEvaluateCapacityCondition(t *testing.T) {
	reportCurrentTrue := []metav1.Condition{
		{Type: capacityreportv1alpha1.ConditionTypeReportCurrent, Status: metav1.ConditionTrue},
	}
	reportCurrentFalse := []metav1.Condition{
		{Type: capacityreportv1alpha1.ConditionTypeReportCurrent, Status: metav1.ConditionFalse},
	}

	tests := []struct {
		name       string
		report     *capacityreportv1alpha1.CapacityReport
		wantStatus metav1.ConditionStatus
		wantReason string
	}{
		{
			name: "ReportCurrent true produces DataCollected",
			report: &capacityreportv1alpha1.CapacityReport{
				Status: capacityreportv1alpha1.CapacityReportStatus{
					Conditions:          reportCurrentTrue,
					HostedControlPlanes: capacityreportv1alpha1.HostedControlPlanes{Resources: &capacityreportv1alpha1.HostedControlPlaneResourceTotals{Usage: withZeroFloor(nil), Requested: withZeroFloor(nil)}},
				},
			},
			wantStatus: metav1.ConditionTrue,
			wantReason: "DataCollected",
		},
		{
			name: "ReportCurrent false produces ReportNotCurrent",
			report: &capacityreportv1alpha1.CapacityReport{
				Status: capacityreportv1alpha1.CapacityReportStatus{
					Conditions: reportCurrentFalse,
				},
			},
			wantStatus: metav1.ConditionFalse,
			wantReason: "ReportNotCurrent",
		},
		{
			name: "nil conditions produces ReportNotCurrent",
			report: &capacityreportv1alpha1.CapacityReport{
				Status: capacityreportv1alpha1.CapacityReportStatus{
					Conditions: nil,
				},
			},
			wantStatus: metav1.ConditionFalse,
			wantReason: "ReportNotCurrent",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			condition := evaluateCapacityCondition(test.report)
			assert.Equal(t, "CapacityDataCurrent", condition.Type, "Type")
			assert.Equal(t, test.wantStatus, condition.Status, "Status")
			assert.Equal(t, test.wantReason, condition.Reason, "Reason")
		})
	}
}

func withZeroFloor(values corev1.ResourceList) corev1.ResourceList {
	result := values.DeepCopy()
	if result == nil {
		result = corev1.ResourceList{}
	}
	for _, name := range scheduling.Resources() {
		if _, present := result[name]; !present {
			result[name] = resource.MustParse("0")
		}
	}
	return result
}

func TestNewResourceTotalsAvailability(t *testing.T) {
	for _, scenario := range []string{"missing", "nil-maps", "empty-maps", "partial", "usage-only", "requests-only", "zero"} {
		t.Run(scenario, func(t *testing.T) {
			report := &capacityreportv1alpha1.CapacityReport{}
			report.Status.Conditions = []metav1.Condition{{Type: capacityreportv1alpha1.ConditionTypeReportCurrent, Status: metav1.ConditionTrue}}
			report.Status.Usage = withZeroFloor(corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("99")})
			report.Status.Requested = report.Status.Usage.DeepCopy()
			if scenario != "missing" {
				report.Status.HostedControlPlanes.Resources = &capacityreportv1alpha1.HostedControlPlaneResourceTotals{Usage: withZeroFloor(nil), Requested: withZeroFloor(nil)}
			}
			if scenario == "partial" {
				delete(report.Status.HostedControlPlanes.Resources.Requested, corev1.ResourceMemory)
			}
			if scenario == "usage-only" {
				report.Status.HostedControlPlanes.Resources.Requested = nil
			}
			if scenario == "requests-only" {
				report.Status.HostedControlPlanes.Resources.Usage = nil
			}
			if scenario == "nil-maps" {
				report.Status.HostedControlPlanes.Resources = &capacityreportv1alpha1.HostedControlPlaneResourceTotals{}
			}
			if scenario == "empty-maps" {
				report.Status.HostedControlPlanes.Resources = &capacityreportv1alpha1.HostedControlPlaneResourceTotals{Usage: corev1.ResourceList{}, Requested: corev1.ResourceList{}}
			}
			observed := ComputeObservedResources(report)
			condition := evaluateCapacityCondition(report)
			require.Equal(t, metav1.ConditionTrue, condition.Status)
			if scenario == "zero" || scenario == "partial" || scenario == "usage-only" || scenario == "requests-only" {
				assertResourceListEqual(t, report.Status.HostedControlPlanes.Resources.Usage, observed.Usage, "preserved usage")
				assertResourceListEqual(t, report.Status.HostedControlPlanes.Resources.Requested, observed.Requests, "preserved requests")
			} else {
				require.Nil(t, observed.Usage)
				require.Nil(t, observed.Requests)
			}
			encoded, err := json.Marshal(observed)
			require.NoError(t, err)
			var serialized map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(encoded, &serialized))
			for name, values := range map[string]corev1.ResourceList{"usage": observed.Usage, "requests": observed.Requests} {
				if len(values) == 0 {
					require.NotContains(t, serialized, name)
					continue
				}
				var quantities map[string]string
				require.NoError(t, json.Unmarshal(serialized[name], &quantities))
				require.Len(t, quantities, len(values))
				for resourceName := range values {
					require.Equal(t, "0", quantities[string(resourceName)])
				}
			}
		})
	}
}

func assertResourceListEqual(t *testing.T, expected, actual corev1.ResourceList, label string) {
	t.Helper()
	require.Equal(t, len(expected), len(actual), "%s: length mismatch (expected %v, got %v)", label, expected, actual)
	for name, expectedQuantity := range expected {
		actualQuantity, ok := actual[name]
		require.True(t, ok, "%s: missing resource %q", label, name)
		assert.Zero(t, expectedQuantity.Cmp(actualQuantity), "%s[%s]: expected %s, got %s", label, name, expectedQuantity.String(), actualQuantity.String())
	}
}
