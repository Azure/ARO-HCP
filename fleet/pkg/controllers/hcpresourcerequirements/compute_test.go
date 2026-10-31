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
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/Azure/ARO-HCP/fleet/pkg/controllers/capacityreporting"
	"github.com/Azure/ARO-HCP/fleet/pkg/scheduling"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/apihelpers/fleetapihelpers"
	"github.com/Azure/ARO-HCP/internal/apihelpers/kubeapplierapihelpers"
	"github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/fleetcosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/fleetlistertesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/kubeapplierlistertesting"
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
			wantUsage:      nil,
			wantRequests:   nil,
			wantSampleSize: 0,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			for _, report := range test.reports {
				report.Status.HostedControlPlanes.ReadyResources = &capacityreportv1alpha1.HostedControlPlaneResourceTotals{Usage: zeroFloor(report.Status.Usage), Requested: zeroFloor(report.Status.Requested)}
				report.Status.Usage = corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("999")}
				report.Status.Requested = report.Status.Usage.DeepCopy()
				report.Status.HostedControlPlanes.Resources = &capacityreportv1alpha1.HostedControlPlaneResourceTotals{Usage: zeroFloor(report.Status.Usage), Requested: zeroFloor(report.Status.Requested)}
			}
			test.wantUsage = zeroFloor(test.wantUsage)
			test.wantRequests = zeroFloor(test.wantRequests)
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

func zeroFloor(values corev1.ResourceList) corev1.ResourceList {
	if values == nil {
		return nil
	}
	result := values.DeepCopy()
	for _, name := range scheduling.Resources() {
		if _, present := result[name]; !present {
			result[name] = resource.MustParse("0")
		}
	}
	return result
}

func readyTotals(cpu string) *capacityreportv1alpha1.HostedControlPlaneResourceTotals {
	values := zeroFloor(corev1.ResourceList{corev1.ResourceCPU: resource.MustParse(cpu)})
	return &capacityreportv1alpha1.HostedControlPlaneResourceTotals{Usage: values, Requested: values.DeepCopy()}
}

func TestSyncOnceEligibilityAndConfigurationRetirement(t *testing.T) {
	for _, scenario := range []string{"current", "old", "future", "no-timestamp", "noncurrent", "no-condition", "no-ready", "legacy", "partial", "missing", "unmirrored", "malformed"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := t.Context()
			database := fleetcosmosstoragetesting.NewMockFleetDBClient()
			stamps := &fleetlistertesting.SliceStampLister{}
			desires := &kubeapplierlistertesting.SliceReadDesireLister{}
			eligible := scenario == "current" || scenario == "old" || scenario == "future" || scenario == "no-timestamp"
			for index, stamp := range []string{"first", "second"} {
				stamps.Stamps = append(stamps.Stamps, &fleetapi.Stamp{CosmosMetadata: coreapi.CosmosMetadata{ResourceID: metadataapi.Must(fleetapihelpers.ToStampResourceID(stamp))}})
				now := metav1.Now()
				report := &capacityreportv1alpha1.CapacityReport{Status: capacityreportv1alpha1.CapacityReportStatus{
					LastReportedAt: &now,
					Conditions:     []metav1.Condition{{Type: capacityreportv1alpha1.ConditionTypeReportCurrent, Status: metav1.ConditionTrue}},
					Usage:          readyTotals("999").Usage, Requested: readyTotals("999").Requested,
					HostedControlPlanes: capacityreportv1alpha1.HostedControlPlanes{
						ReadyResourceIDs: makeResourceIDs(2), ReadyResources: readyTotals("4"), Resources: readyTotals("999"),
						NotReadyResourceIDs:          []string{makeResourceIDs(1)[0] + "-not-ready"},
						ByClusterSizingConfiguration: []capacityreportv1alpha1.HostedControlPlanesBySize{{Size: "small", ReadyResources: *readyTotals("4"), ReadyCount: 2}},
					},
				}}
				if index == 1 {
					report.Status.HostedControlPlanes.ReadyResources = readyTotals("12")
					report.Status.HostedControlPlanes.ByClusterSizingConfiguration[0].ReadyResources = *readyTotals("12")
					switch scenario {
					case "old":
						report.Status.LastReportedAt = &metav1.Time{Time: time.Now().Add(-24 * time.Hour)}
					case "future":
						report.Status.LastReportedAt = &metav1.Time{Time: time.Now().Add(24 * time.Hour)}
					case "no-timestamp":
						report.Status.LastReportedAt = nil
					case "noncurrent":
						report.Status.Conditions[0].Status = metav1.ConditionFalse
					case "no-condition":
						report.Status.Conditions = nil
					case "no-ready":
						report.Status.HostedControlPlanes.ReadyResourceIDs = nil
					case "legacy":
						report.Status.HostedControlPlanes.ReadyResources = nil
					case "partial":
						delete(report.Status.HostedControlPlanes.ReadyResources.Requested, corev1.ResourceMemory)
					case "missing":
						continue
					}
				}
				desire := controllerutils.BuildReadDesire(kubeapplierapihelpers.ToManagementClusterScopedReadDesireResourceIDString(stamp, capacityreporting.ReadDesireName), metadataapi.Must(fleetapihelpers.ToManagementClusterResourceID(stamp)), capacityreporting.CapacityReportTarget)
				encoded, err := json.Marshal(report)
				require.NoError(t, err)
				desire.Status.KubeContent = &runtime.RawExtension{Raw: encoded}
				if index == 1 && scenario == "unmirrored" {
					desire.Status.KubeContent = nil
				}
				if index == 1 && scenario == "malformed" {
					desire.Status.KubeContent.Raw = []byte("invalid")
				}
				desires.Desires = append(desires.Desires, desire)
			}
			controller := NewController(time.Minute, database, desires, stamps)
			require.NoError(t, controller.syncOnce(ctx))
			wantCount, wantCPU := 2, "2"
			if eligible {
				wantCount, wantCPU = 4, "4"
			}
			for _, name := range []string{"default", "small"} {
				expectedCount, expectedCPU := wantCount, wantCPU
				if name == "small" && (scenario == "legacy" || scenario == "partial") {
					expectedCount, expectedCPU = 4, "4"
				}
				document, err := database.HCPResourceRequirements().Get(ctx, name)
				require.NoError(t, err)
				require.Equal(t, expectedCount, document.Status.SampleSize)
				assertResourceListEqual(t, readyTotals(expectedCPU).Usage, document.Status.AverageUsage, name)
				assertResourceListEqual(t, readyTotals(expectedCPU).Requested, document.Status.AverageRequests, name)
				require.True(t, meta.IsStatusConditionTrue(document.Status.Conditions, fleetapi.ConditionTypeDataCurrent))
			}
			previous, err := database.HCPResourceRequirements().Get(ctx, "small")
			require.NoError(t, err)
			for _, desire := range desires.Desires {
				if desire.Status.KubeContent == nil || !json.Valid(desire.Status.KubeContent.Raw) {
					continue
				}
				var report capacityreportv1alpha1.CapacityReport
				require.NoError(t, json.Unmarshal(desire.Status.KubeContent.Raw, &report))
				report.Status.HostedControlPlanes.ByClusterSizingConfiguration = nil
				encoded, err := json.Marshal(report)
				require.NoError(t, err)
				desire.Status.KubeContent.Raw = encoded
			}
			require.NoError(t, controller.syncOnce(ctx))
			disappeared, err := database.HCPResourceRequirements().Get(ctx, "small")
			require.NoError(t, err)
			require.True(t, meta.IsStatusConditionFalse(disappeared.Status.Conditions, fleetapi.ConditionTypeDataCurrent))
			stillCurrent, err := database.HCPResourceRequirements().Get(ctx, "default")
			require.NoError(t, err)
			require.Equal(t, wantCount, stillCurrent.Status.SampleSize)
			require.True(t, meta.IsStatusConditionTrue(stillCurrent.Status.Conditions, fleetapi.ConditionTypeDataCurrent))
			desires.Desires = []*kubeapplierapi.ReadDesire{}
			require.NoError(t, controller.syncOnce(ctx))
			retired, err := database.HCPResourceRequirements().Get(ctx, "small")
			require.NoError(t, err)
			require.Equal(t, disappeared.CosmosETag, retired.CosmosETag)
			require.Equal(t, disappeared.InstanceVersion, retired.InstanceVersion)
			require.True(t, meta.IsStatusConditionFalse(retired.Status.Conditions, fleetapi.ConditionTypeDataCurrent))
			require.Zero(t, retired.Status.SampleSize)
			require.Equal(t, previous.Status.LastReportedAt, retired.Status.LastReportedAt)
			assertResourceListEqual(t, previous.Status.AverageUsage, retired.Status.AverageUsage, "retained usage")
			assertResourceListEqual(t, previous.Status.AverageRequests, retired.Status.AverageRequests, "retained requests")
			defaultDocument, err := database.HCPResourceRequirements().Get(ctx, "default")
			require.NoError(t, err)
			require.Zero(t, defaultDocument.Status.SampleSize)
			require.Nil(t, defaultDocument.Status.AverageUsage)
			require.True(t, meta.IsStatusConditionFalse(defaultDocument.Status.Conditions, fleetapi.ConditionTypeDataCurrent))
		})
	}
}

func TestConfigurationRequirements(t *testing.T) {
	first := &capacityreportv1alpha1.CapacityReport{}
	first.Status.HostedControlPlanes = capacityreportv1alpha1.HostedControlPlanes{
		ReadyResourceIDs: makeResourceIDs(4), ReadyResources: readyTotals("20"),
		ByClusterSizingConfiguration: []capacityreportv1alpha1.HostedControlPlanesBySize{{Size: "small", ReadyResources: *readyTotals("4"), ReadyCount: 2}},
	}
	second := first.DeepCopy()
	second.Status.HostedControlPlanes.ReadyResourceIDs = makeResourceIDs(8)
	second.Status.HostedControlPlanes.ReadyResources = readyTotals("40")
	second.Status.HostedControlPlanes.ByClusterSizingConfiguration[0].ReadyResources = *readyTotals("20")
	second.Status.HostedControlPlanes.ByClusterSizingConfiguration[0].ReadyCount = 6
	reports := []*capacityreportv1alpha1.CapacityReport{first, second}
	usage, requests, count := computeConfigurationRequirements(reports, "small")
	require.Equal(t, 8, count)
	assertResourceListEqual(t, readyTotals("3").Usage, usage, "weighted usage")
	assertResourceListEqual(t, readyTotals("3").Requested, requests, "weighted requests")
	usage, _, count = computeAverageRequirements(reports)
	require.Equal(t, 12, count)
	assertResourceListEqual(t, readyTotals("5").Usage, usage, "overall includes unattributed")
	zeroReport := first.DeepCopy()
	zeroReport.Status.HostedControlPlanes.ReadyResources = readyTotals("0")
	usage, requests, count = computeAverageRequirements([]*capacityreportv1alpha1.CapacityReport{zeroReport})
	require.Equal(t, 4, count)
	assertResourceListEqual(t, readyTotals("0").Usage, usage, "overall explicit zero usage")
	assertResourceListEqual(t, readyTotals("0").Requested, requests, "overall explicit zero requests")
	for _, scenario := range []string{"missing", "partial", "zero", "no-ready", "missing-bucket", "partial-bucket"} {
		t.Run(scenario, func(t *testing.T) {
			report := first.DeepCopy()
			switch scenario {
			case "missing":
				report.Status.HostedControlPlanes.ReadyResources = nil
			case "partial":
				delete(report.Status.HostedControlPlanes.ReadyResources.Usage, corev1.ResourceCPU)
			case "zero":
				report.Status.HostedControlPlanes.ByClusterSizingConfiguration[0].ReadyResources = *readyTotals("0")
			case "no-ready":
				report.Status.HostedControlPlanes.ByClusterSizingConfiguration[0].ReadyCount = 0
			case "missing-bucket":
				report.Status.HostedControlPlanes.ByClusterSizingConfiguration = nil
			case "partial-bucket":
				delete(report.Status.HostedControlPlanes.ByClusterSizingConfiguration[0].ReadyResources.Requested, corev1.ResourceMemory)
			}
			usage, requests, count := computeConfigurationRequirements([]*capacityreportv1alpha1.CapacityReport{report}, "small")
			switch scenario {
			case "missing", "partial":
				require.Equal(t, 2, count)
				assertResourceListEqual(t, readyTotals("2").Usage, usage, "selected bucket usage")
				assertResourceListEqual(t, readyTotals("2").Requested, requests, "selected bucket requests")
				usage, requests, count = computeAverageRequirements([]*capacityreportv1alpha1.CapacityReport{report})
				require.Zero(t, count)
				require.Nil(t, usage)
				require.Nil(t, requests)
			case "zero":
				require.Equal(t, 2, count)
				assertResourceListEqual(t, readyTotals("0").Usage, usage, "explicit zero")
			default:
				require.Zero(t, count)
				require.Nil(t, usage)
				require.Nil(t, requests)
			}
			usage, requests, count = computeConfigurationRequirements([]*capacityreportv1alpha1.CapacityReport{report, second}, "small")
			expectedCount, expectedCPU := 6, "3"
			switch scenario {
			case "missing", "partial":
				expectedCount, expectedCPU = 8, "3"
			case "zero":
				expectedCount, expectedCPU = 8, "2"
			}
			require.Equal(t, expectedCount, count)
			assertResourceListEqual(t, readyTotals(expectedCPU).Usage, usage, "mixed usage")
			assertResourceListEqual(t, readyTotals(expectedCPU).Requested, requests, "mixed requests")
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
