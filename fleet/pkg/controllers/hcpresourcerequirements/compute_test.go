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

package hcpresourcerequirements

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/Azure/ARO-HCP/internal/kuberesources"
	capacityreportv1alpha1 "github.com/Azure/ARO-HCP/mgmt-agent/pkg/apis/capacityreport/v1alpha1"
)

func makeResourceIDs(count int) []string {
	ids := make([]string, count)
	for i := range count {
		ids[i] = fmt.Sprintf("/subscriptions/sub/resourceGroups/rg/providers/Microsoft.RedHatOpenshift/hcpOpenShiftClusters/cluster-%d", i)
	}
	return ids
}

func TestComputeAverageRequirements(t *testing.T) {
	tests := []struct {
		name           string
		reports        []*capacityreportv1alpha1.CapacityReport
		wantUsage      corev1.ResourceList
		wantRequests   corev1.ResourceList
		wantSampleSize int
	}{
		{
			name:           "no reports returns zero sample size",
			reports:        nil,
			wantUsage:      nil,
			wantRequests:   nil,
			wantSampleSize: 0,
		},
		{
			name:           "empty reports returns zero sample size",
			reports:        []*capacityreportv1alpha1.CapacityReport{},
			wantUsage:      nil,
			wantRequests:   nil,
			wantSampleSize: 0,
		},
		{
			name: "single MC with 10 ready HCPs",
			reports: []*capacityreportv1alpha1.CapacityReport{
				{
					Status: capacityreportv1alpha1.CapacityReportStatus{
						Usage: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("20"),
							corev1.ResourceMemory: resource.MustParse("100Gi"),
						},
						Requested: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("40"),
							corev1.ResourceMemory: resource.MustParse("200Gi"),
						},
						HostedControlPlanes: capacityreportv1alpha1.HostedControlPlanes{ReadyResourceIDs: makeResourceIDs(10)},
					},
				},
			},
			wantUsage: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("2"),
				corev1.ResourceMemory: resource.MustParse("10Gi"),
			},
			wantRequests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("4"),
				corev1.ResourceMemory: resource.MustParse("20Gi"),
			},
			wantSampleSize: 10,
		},
		{
			name: "two MCs aggregated correctly",
			reports: []*capacityreportv1alpha1.CapacityReport{
				{
					Status: capacityreportv1alpha1.CapacityReportStatus{
						Usage: corev1.ResourceList{
							corev1.ResourceCPU:                 resource.MustParse("10"),
							corev1.ResourceMemory:              resource.MustParse("40Gi"),
							kuberesources.SwiftNICResourceName: resource.MustParse("5"),
						},
						Requested: corev1.ResourceList{
							corev1.ResourceCPU:                 resource.MustParse("20"),
							corev1.ResourceMemory:              resource.MustParse("80Gi"),
							kuberesources.SwiftNICResourceName: resource.MustParse("5"),
						},
						HostedControlPlanes: capacityreportv1alpha1.HostedControlPlanes{ReadyResourceIDs: makeResourceIDs(5)},
					},
				},
				{
					Status: capacityreportv1alpha1.CapacityReportStatus{
						Usage: corev1.ResourceList{
							corev1.ResourceCPU:                 resource.MustParse("30"),
							corev1.ResourceMemory:              resource.MustParse("120Gi"),
							kuberesources.SwiftNICResourceName: resource.MustParse("15"),
						},
						Requested: corev1.ResourceList{
							corev1.ResourceCPU:                 resource.MustParse("60"),
							corev1.ResourceMemory:              resource.MustParse("240Gi"),
							kuberesources.SwiftNICResourceName: resource.MustParse("15"),
						},
						HostedControlPlanes: capacityreportv1alpha1.HostedControlPlanes{ReadyResourceIDs: makeResourceIDs(15)},
					},
				},
			},
			// Total: 40 CPU usage, 160Gi mem usage, 20 swift-nic usage across 20 HCPs
			wantUsage: corev1.ResourceList{
				corev1.ResourceCPU:                 resource.MustParse("2"),
				corev1.ResourceMemory:              resource.MustParse("8Gi"),
				kuberesources.SwiftNICResourceName: resource.MustParse("1"),
			},
			// Total: 80 CPU requested, 320Gi mem requested, 20 swift-nic requested across 20 HCPs
			wantRequests: corev1.ResourceList{
				corev1.ResourceCPU:                 resource.MustParse("4"),
				corev1.ResourceMemory:              resource.MustParse("16Gi"),
				kuberesources.SwiftNICResourceName: resource.MustParse("1"),
			},
			wantSampleSize: 20,
		},
		{
			name: "non-round values preserve original scale",
			reports: []*capacityreportv1alpha1.CapacityReport{
				{
					Status: capacityreportv1alpha1.CapacityReportStatus{
						Usage: corev1.ResourceList{
							corev1.ResourceCPU:                 resource.MustParse("1272m"),
							corev1.ResourceMemory:              resource.MustParse("6442768Ki"),
							kuberesources.SwiftNICResourceName: resource.MustParse("3"),
						},
						Requested: corev1.ResourceList{
							corev1.ResourceCPU:                 resource.MustParse("3265m"),
							corev1.ResourceMemory:              resource.MustParse("8423Mi"),
							kuberesources.SwiftNICResourceName: resource.MustParse("3"),
						},
						HostedControlPlanes: capacityreportv1alpha1.HostedControlPlanes{ReadyResourceIDs: makeResourceIDs(1)},
					},
				},
			},
			wantUsage: corev1.ResourceList{
				corev1.ResourceCPU:                 resource.MustParse("1272m"),
				corev1.ResourceMemory:              resource.MustParse("6442768Ki"),
				kuberesources.SwiftNICResourceName: resource.MustParse("3"),
			},
			wantRequests: corev1.ResourceList{
				corev1.ResourceCPU:                 resource.MustParse("3265m"),
				corev1.ResourceMemory:              resource.MustParse("8423Mi"),
				kuberesources.SwiftNICResourceName: resource.MustParse("3"),
			},
			wantSampleSize: 1,
		},
		{
			name: "non-scheduling resources are filtered out",
			reports: []*capacityreportv1alpha1.CapacityReport{
				{
					Status: capacityreportv1alpha1.CapacityReportStatus{
						Usage: corev1.ResourceList{
							corev1.ResourceCPU: resource.MustParse("10"),
							"pods":             resource.MustParse("50"),
						},
						Requested: corev1.ResourceList{
							corev1.ResourceCPU: resource.MustParse("20"),
							"hugepages-2Mi":    resource.MustParse("0"),
						},
						HostedControlPlanes: capacityreportv1alpha1.HostedControlPlanes{ReadyResourceIDs: makeResourceIDs(5)},
					},
				},
			},
			wantUsage: corev1.ResourceList{
				corev1.ResourceCPU: resource.MustParse("2"),
			},
			wantRequests: corev1.ResourceList{
				corev1.ResourceCPU: resource.MustParse("4"),
			},
			wantSampleSize: 5,
		},
		{
			name: "nil usage with non-nil requested",
			reports: []*capacityreportv1alpha1.CapacityReport{
				{
					Status: capacityreportv1alpha1.CapacityReportStatus{
						Usage: nil,
						Requested: corev1.ResourceList{
							corev1.ResourceCPU: resource.MustParse("10"),
						},
						HostedControlPlanes: capacityreportv1alpha1.HostedControlPlanes{ReadyResourceIDs: makeResourceIDs(5)},
					},
				},
			},
			wantUsage: nil,
			wantRequests: corev1.ResourceList{
				corev1.ResourceCPU: resource.MustParse("2"),
			},
			wantSampleSize: 5,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			averageUsage, averageRequests, sampleSize := computeAverageRequirements(test.reports)

			assert.Equal(t, test.wantSampleSize, sampleSize, "SampleSize")

			if test.wantUsage == nil {
				assert.Nil(t, averageUsage, "AverageUsage should be nil")
			} else {
				assertResourceListEqual(t, test.wantUsage, averageUsage, "AverageUsage")
			}

			if test.wantRequests == nil {
				assert.Nil(t, averageRequests, "AverageRequests should be nil")
			} else {
				assertResourceListEqual(t, test.wantRequests, averageRequests, "AverageRequests")
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
