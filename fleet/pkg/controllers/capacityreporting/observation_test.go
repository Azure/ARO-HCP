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
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/fleetcosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/kubeapplierlistertesting"
	capacityreportv1alpha1 "github.com/Azure/ARO-HCP/mgmt-agent/pkg/apis/capacityreport/v1alpha1"
)

func malformedContent() *runtime.RawExtension {
	return &runtime.RawExtension{Raw: []byte(`{"status":`)}
}

func TestCapacityObservationOutcomes(t *testing.T) {
	for _, test := range []struct {
		name   string
		status metav1.ConditionStatus
		reason string
	}{
		{"missing", metav1.ConditionUnknown, "ReadDesireUnavailable"},
		{"unobserved", metav1.ConditionUnknown, "ReadDesireUnobserved"},
		{"unknown", metav1.ConditionUnknown, "ReadDesireUnobserved"},
		{"absent", metav1.ConditionFalse, "CapacityReportAbsent"},
		{"failed-retained", metav1.ConditionFalse, "ReadDesireFailed"},
		{"failed-malformed", metav1.ConditionFalse, "ReadDesireFailed"},
		{"degraded-retained", metav1.ConditionFalse, "ReadDesireDegraded"},
		{"malformed", metav1.ConditionFalse, "MalformedCapacityReport"},
		{"null", metav1.ConditionFalse, "MalformedCapacityReport"},
		{"source-failed", metav1.ConditionFalse, "ReportNotCurrent"},
		{"missing-totals", metav1.ConditionUnknown, "MissingResourceTotals"},
		{"partial", metav1.ConditionUnknown, "MissingResourceTotals"},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := t.Context()
			database := fleetcosmosstoragetesting.NewMockFleetDBClient()
			lister := &kubeapplierlistertesting.SliceReadDesireLister{}
			syncer := &capacityReportingSyncer{fleetDBClient: database, readDesireLister: lister}
			first := metav1.NewTime(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
			report := &capacityreportv1alpha1.CapacityReport{Status: capacityreportv1alpha1.CapacityReportStatus{
				LastReportedAt:      &first,
				Conditions:          []metav1.Condition{{Type: capacityreportv1alpha1.ConditionTypeReportCurrent, Status: metav1.ConditionTrue}},
				HostedControlPlanes: capacityreportv1alpha1.HostedControlPlanes{Resources: &capacityreportv1alpha1.HostedControlPlaneResourceTotals{Usage: withZeroFloor(nil), Requested: withZeroFloor(nil)}},
			}}
			lister.Desires = []*kubeapplierapi.ReadDesire{buildTestReadDesire(report)}
			require.NoError(t, syncer.SyncOnce(ctx, testKey()))
			crud := database.Stamps().ManagementClusters(testStampIdentifier).Scheduling()
			good, err := crud.Get(ctx, fleetapi.SchedulingResourceName)
			require.NoError(t, err)
			require.True(t, meta.IsStatusConditionTrue(good.Status.Conditions, fleetapi.ConditionTypeCapacityDataCurrent))
			pending := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/sub/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/pending"))
			good.Status.PendingAssignedClusters = []*azcorearm.ResourceID{pending}
			good, err = crud.Replace(ctx, good, nil)
			require.NoError(t, err)
			report.Status.HostedControlPlanes.ReadyResourceIDs = []string{pending.String()}
			report.Status.HostedControlPlanes.Resources.Usage[corev1.ResourceCPU] = resource.MustParse("100")
			switch test.name {
			case "source-failed":
				report.Status.Conditions[0].Status = metav1.ConditionFalse
			case "missing-totals":
				report.Status.HostedControlPlanes.Resources = nil
			case "partial":
				delete(report.Status.HostedControlPlanes.Resources.Requested, corev1.ResourceMemory)
			}
			desire := buildTestReadDesire(report)
			switch test.name {
			case "unobserved":
				desire.Status.Conditions = nil
			case "unknown":
				desire.Status.Conditions[0].Status = metav1.ConditionUnknown
			case "absent":
				desire.Status.KubeContent = nil
			case "failed-retained":
				desire.Status.Conditions[0].Status = metav1.ConditionFalse
			case "failed-malformed":
				desire.Status.Conditions[0].Status = metav1.ConditionFalse
				desire.Status.KubeContent = malformedContent()
			case "degraded-retained":
				desire.Status.Conditions = append(desire.Status.Conditions, metav1.Condition{Type: kubeapplierapi.ConditionTypeDegraded, Status: metav1.ConditionTrue})
			case "malformed":
				desire.Status.KubeContent = &runtime.RawExtension{Raw: []byte(`{"status":`)}
			case "null":
				desire.Status.KubeContent = &runtime.RawExtension{Raw: []byte(`null`)}
			}
			lister.Desires = []*kubeapplierapi.ReadDesire{desire}
			if test.name == "missing" {
				lister.Desires = nil
			}
			require.NoError(t, syncer.SyncOnce(ctx, testKey()))
			failed, err := crud.Get(ctx, fleetapi.SchedulingResourceName)
			require.NoError(t, err)
			condition := meta.FindStatusCondition(failed.Status.Conditions, fleetapi.ConditionTypeCapacityDataCurrent)
			require.Equal(t, test.status, condition.Status)
			require.Equal(t, test.reason, condition.Reason)
			require.Equal(t, good.Status.ObservedResources, failed.Status.ObservedResources)
			require.Equal(t, good.Status.ReadyResourceIDs, failed.Status.ReadyResourceIDs)
			require.Equal(t, good.Status.NotReadyResourceIDs, failed.Status.NotReadyResourceIDs)
			require.Equal(t, good.Status.PendingAssignedClusters, failed.Status.PendingAssignedClusters)
			require.NoError(t, syncer.SyncOnce(ctx, testKey()))
			stable, err := crud.Get(ctx, fleetapi.SchedulingResourceName)
			require.NoError(t, err)
			require.Equal(t, failed.CosmosETag, stable.CosmosETag)
			report.Status.Conditions[0].Status = metav1.ConditionTrue
			report.Status.HostedControlPlanes.Resources = &capacityreportv1alpha1.HostedControlPlaneResourceTotals{Usage: withZeroFloor(nil), Requested: withZeroFloor(nil)}
			lister.Desires = []*kubeapplierapi.ReadDesire{buildTestReadDesire(report)}
			require.NoError(t, syncer.SyncOnce(ctx, testKey()))
			recovered, err := crud.Get(ctx, fleetapi.SchedulingResourceName)
			require.NoError(t, err)
			require.True(t, meta.IsStatusConditionTrue(recovered.Status.Conditions, fleetapi.ConditionTypeCapacityDataCurrent))
			require.Empty(t, recovered.Status.PendingAssignedClusters)
			require.Len(t, recovered.Status.ReadyResourceIDs, 1)
		})
	}
}

func TestScalingFailureRetention(t *testing.T) {
	for _, initiallyMissing := range []bool{true, false} {
		t.Run(map[bool]string{true: "initial", false: "last-good"}[initiallyMissing], func(t *testing.T) {
			ctx := t.Context()
			database := fleetcosmosstoragetesting.NewMockFleetDBClient()
			syncer := &scaleCeilingReportingSyncer{fleetDBClient: database}
			crud := database.Stamps().ManagementClusters(testStampIdentifier).Scheduling()
			first := metav1.NewTime(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
			capacity := corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("8")}
			if !initiallyMissing {
				require.NoError(t, syncer.persistMaxCapacity(ctx, testStampIdentifier, capacity, &first))
			}
			require.NoError(t, syncer.persistScalingFailure(ctx, testStampIdentifier, "MissingInput", "awaiting input"))
			failed, err := crud.Get(ctx, fleetapi.SchedulingResourceName)
			require.NoError(t, err)
			if initiallyMissing {
				require.NotNil(t, failed.Status.ScaleCeiling.LastReportedAt)
			} else {
				require.Equal(t, capacity, failed.Status.ScaleCeiling.Capacity)
				require.True(t, first.Equal(failed.Status.ScaleCeiling.LastReportedAt))
			}
			require.NoError(t, syncer.persistScalingFailure(ctx, testStampIdentifier, "MissingInput", "awaiting input"))
			stable, err := crud.Get(ctx, fleetapi.SchedulingResourceName)
			require.NoError(t, err)
			require.Equal(t, failed.CosmosETag, stable.CosmosETag)
			require.NoError(t, syncer.persistMaxCapacity(ctx, testStampIdentifier, capacity, &first))
			recovered, err := crud.Get(ctx, fleetapi.SchedulingResourceName)
			require.NoError(t, err)
			require.True(t, meta.IsStatusConditionTrue(recovered.Status.Conditions, fleetapi.ConditionTypeScalingDataCurrent))
		})
	}
}

func TestScalingRetentionRequiresUsableState(t *testing.T) {
	for _, scenario := range []string{"zero", "positive", "empty", "unrelated", "negative", "not-current"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := t.Context()
			database := fleetcosmosstoragetesting.NewMockFleetDBClient()
			syncer := &scaleCeilingReportingSyncer{fleetDBClient: database}
			capacity := withZeroFloor(nil)
			switch scenario {
			case "positive", "not-current":
				capacity[corev1.ResourceCPU] = resource.MustParse("8")
			case "empty":
				capacity = corev1.ResourceList{}
			case "unrelated":
				capacity = corev1.ResourceList{corev1.ResourcePods: resource.MustParse("5")}
			case "negative":
				capacity[corev1.ResourceCPU] = resource.MustParse("-1")
			}
			first := metav1.NewTime(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
			require.NoError(t, syncer.persistMaxCapacity(ctx, testStampIdentifier, capacity, &first))
			crud := database.Stamps().ManagementClusters(testStampIdentifier).Scheduling()
			before, err := crud.Get(ctx, fleetapi.SchedulingResourceName)
			require.NoError(t, err)
			if scenario == "not-current" {
				meta.SetStatusCondition(&before.Status.Conditions, metav1.Condition{Type: fleetapi.ConditionTypeScalingDataCurrent, Status: metav1.ConditionFalse, Reason: "Unusable"})
				before, err = crud.Replace(ctx, before, nil)
				require.NoError(t, err)
			}
			require.NoError(t, syncer.persistScalingFailure(ctx, testStampIdentifier, "AzureError", "failed refresh"))
			after, err := crud.Get(ctx, fleetapi.SchedulingResourceName)
			require.NoError(t, err)
			if scenario == "zero" || scenario == "positive" {
				require.Equal(t, before, after)
			} else {
				require.Equal(t, metav1.ConditionUnknown, meta.FindStatusCondition(after.Status.Conditions, fleetapi.ConditionTypeScalingDataCurrent).Status)
			}
		})
	}
}
