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
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/kuberesources"
)

var pressureTestNow = time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)

func validPressureStatus() fleetapi.ManagementClusterSchedulingStatus {
	now := metav1.NewTime(pressureTestNow)
	return fleetapi.ManagementClusterSchedulingStatus{
		Conditions: []metav1.Condition{
			{Type: fleetapi.ConditionTypeCapacityDataCurrent, Status: metav1.ConditionTrue},
			{Type: fleetapi.ConditionTypeScalingDataCurrent, Status: metav1.ConditionTrue},
		},
		ObservedResources: fleetapi.ObservedResources{
			LastReportedAt: &now,
			// Deliberately smaller than the ceiling: current node counts do not
			// determine whether the management cluster has room to grow.
			Capacity: pressureResources("1", "1Gi", "1"),
			Usage:    pressureResources("0", "0", "0"),
			Requests: pressureResources("0", "0", "0"),
		},
		ScaleCeiling: fleetapi.ScaleCeiling{LastReportedAt: &now, Capacity: pressureResources("100", "100Gi", "100")},
	}
}

func pressureResources(cpu, memory, nics string) corev1.ResourceList {
	return corev1.ResourceList{corev1.ResourceCPU: resource.MustParse(cpu), corev1.ResourceMemory: resource.MustParse(memory), kuberesources.SwiftNICResourceName: resource.MustParse(nics)}
}

func pressureClusterID(name string) *azcorearm.ResourceID {
	return metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/sub/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + name))
}

func TestPressureReserves(t *testing.T) {
	settings := pressureSettings{cpuReservePercent: 10, memoryReservePercent: 20, swiftNICReservePercent: 30, scaleCeilingMaxAge: 5 * time.Minute}
	for _, test := range []struct {
		name            string
		usage, requests corev1.ResourceList
		want            metav1.ConditionStatus
		message         string
	}{
		{"explicit zero demand", pressureResources("0", "0", "0"), pressureResources("0", "0", "0"), metav1.ConditionFalse, ""},
		{"threshold equality", pressureResources("90", "80Gi", "70"), pressureResources("90", "80Gi", "70"), metav1.ConditionFalse, ""},
		{"CPU usage exceeds requests", pressureResources("90001m", "0", "0"), pressureResources("10", "0", "0"), metav1.ConditionTrue, "cpu"},
		{"CPU requests exceed usage", pressureResources("10", "0", "0"), pressureResources("90001m", "0", "0"), metav1.ConditionTrue, "cpu"},
		{"memory pressure", pressureResources("0", "81Gi", "0"), pressureResources("0", "0", "0"), metav1.ConditionTrue, "memory"},
		{"NIC pressure", pressureResources("0", "0", "71"), pressureResources("0", "0", "0"), metav1.ConditionTrue, "swift-nic"},
		{"lowest raw reserve passes but memory fails", pressureResources("88", "85Gi", "0"), pressureResources("0", "0", "0"), metav1.ConditionTrue, "memory"},
		{"overcommitted capacity has negative reserve", pressureResources("101", "0", "0"), pressureResources("0", "0", "0"), metav1.ConditionTrue, "-1.00%"},
		{"multiple resources under pressure", pressureResources("95", "90Gi", "90"), pressureResources("0", "0", "0"), metav1.ConditionTrue, "memory"},
	} {
		t.Run(test.name, func(t *testing.T) {
			status := validPressureStatus()
			status.ObservedResources.Usage, status.ObservedResources.Requests = test.usage, test.requests
			condition := evaluatePressure(status, pressureTestNow, settings)
			require.Equal(t, test.want, condition.Status, condition.Message)
			require.Contains(t, condition.Message, test.message)
		})
	}
}

func TestPressureRejectsIncompleteResourceData(t *testing.T) {
	for _, name := range []corev1.ResourceName{corev1.ResourceCPU, corev1.ResourceMemory, kuberesources.SwiftNICResourceName} {
		for _, field := range []string{"capacity", "usage", "requests"} {
			for _, invalid := range []string{"missing", "negative", "zero"} {
				if invalid == "zero" && field != "capacity" {
					continue
				}
				t.Run(string(name)+"/"+field+"/"+invalid, func(t *testing.T) {
					status := validPressureStatus()
					values := status.ScaleCeiling.Capacity
					if field == "usage" {
						values = status.ObservedResources.Usage
					}
					if field == "requests" {
						values = status.ObservedResources.Requests
					}
					switch invalid {
					case "missing":
						delete(values, name)
					case "negative":
						values[name] = resource.MustParse("-1")
					case "zero":
						values[name] = resource.MustParse("0")
					}
					condition := evaluatePressure(status, pressureTestNow, defaultPressureSettings())
					require.Equal(t, metav1.ConditionUnknown, condition.Status, condition.Message)
					require.Contains(t, condition.Message, string(name))
				})
			}
		}
	}
	t.Run("CPU pressure cannot hide missing memory", func(t *testing.T) {
		status := validPressureStatus()
		status.ObservedResources.Usage[corev1.ResourceCPU] = resource.MustParse("99")
		delete(status.ScaleCeiling.Capacity, corev1.ResourceMemory)
		require.Equal(t, metav1.ConditionUnknown, evaluatePressure(status, pressureTestNow, defaultPressureSettings()).Status)
	})
}

func TestPressureCollectionHealthAndAge(t *testing.T) {
	for _, input := range []string{"capacity", "scaling"} {
		for _, invalid := range []string{"condition missing", "failed", "unknown", "timestamp missing", "timestamp zero", "future", "stale", "exact age"} {
			t.Run(input+"/"+invalid, func(t *testing.T) {
				status := validPressureStatus()
				conditionType := fleetapi.ConditionTypeCapacityDataCurrent
				timestamp := &status.ObservedResources.LastReportedAt
				if input == "scaling" {
					conditionType = fleetapi.ConditionTypeScalingDataCurrent
					timestamp = &status.ScaleCeiling.LastReportedAt
				}
				switch invalid {
				case "condition missing":
					meta.RemoveStatusCondition(&status.Conditions, conditionType)
				case "failed", "unknown":
					condition := meta.FindStatusCondition(status.Conditions, conditionType)
					condition.Status = metav1.ConditionFalse
					condition.Reason = "InjectedCollectionFailure"
					if invalid == "unknown" {
						condition.Status = metav1.ConditionUnknown
					}
				case "timestamp missing":
					*timestamp = nil
				case "timestamp zero":
					*timestamp = &metav1.Time{}
				case "future":
					next := metav1.NewTime(pressureTestNow.Add(time.Minute))
					*timestamp = &next
				case "stale":
					old := metav1.NewTime(pressureTestNow.Add(-5*time.Minute - time.Nanosecond))
					*timestamp = &old
				case "exact age":
					old := metav1.NewTime(pressureTestNow.Add(-5 * time.Minute))
					*timestamp = &old
				}
				condition := evaluatePressure(status, pressureTestNow, defaultPressureSettings())
				want := metav1.ConditionUnknown
				if invalid == "exact age" {
					want = metav1.ConditionFalse
				}
				require.Equal(t, want, condition.Status, condition.Message)
				if want == metav1.ConditionUnknown {
					require.NotEmpty(t, condition.Reason)
					require.NotEmpty(t, condition.Message)
					if invalid == "failed" {
						require.Contains(t, condition.Message, "InjectedCollectionFailure")
					}
				}
			})
		}
	}
}

func TestPressureUnreflectedReservations(t *testing.T) {
	status := validPressureStatus()
	ha, single, nonSwift := pressureClusterID("ha"), pressureClusterID("single"), pressureClusterID("nonSwift")
	ready, notReady := pressureClusterID("ready"), pressureClusterID("notReady")
	status.PendingAssignedClusters = []*azcorearm.ResourceID{ha, single, nonSwift, ready, notReady, pressureClusterID("HA")}
	status.PendingSwiftNICReservations = map[string]int64{
		strings.ToLower(ha.String()): 3, strings.ToLower(single.String()): 1, strings.ToLower(nonSwift.String()): 0,
	}
	status.ReadyResourceIDs = []*azcorearm.ResourceID{ready}
	status.NotReadyResourceIDs = []*azcorearm.ResourceID{notReady}
	// Already-observed assignments need no extra reservation metadata. Duplicate
	// IDs, including different casing, must not reserve twice.
	count, err := unreflectedSwiftNICReservations(status)
	require.NoError(t, err)
	require.Equal(t, int64(4), count)
	for _, test := range []struct {
		name     string
		metadata map[string]int64
	}{
		{"legacy missing reservation", nil},
		{"negative reservation", map[string]int64{strings.ToLower(ha.String()): -1}},
		{"unsupported reservation", map[string]int64{strings.ToLower(ha.String()): 2}},
	} {
		t.Run(test.name, func(t *testing.T) {
			status.PendingSwiftNICReservations = test.metadata
			condition := evaluatePressure(status, pressureTestNow, defaultPressureSettings())
			require.Equal(t, metav1.ConditionUnknown, condition.Status)
			require.Equal(t, "InvalidPendingReservation", condition.Reason)
		})
	}
}

func TestPressureReservationBecomesObserved(t *testing.T) {
	status := validPressureStatus()
	id := pressureClusterID("new")
	status.ObservedResources.Usage[kuberesources.SwiftNICResourceName] = resource.MustParse("60")
	status.PendingAssignedClusters = []*azcorearm.ResourceID{id}
	status.PendingSwiftNICReservations = map[string]int64{strings.ToLower(id.String()): 3}
	settings := defaultPressureSettings()
	settings.swiftNICReservePercent = 38
	require.Equal(t, metav1.ConditionTrue, evaluatePressure(status, pressureTestNow, settings).Status, "pending commitment leaves 37%% reserve")
	settings.swiftNICReservePercent = 36
	require.Equal(t, metav1.ConditionFalse, evaluatePressure(status, pressureTestNow, settings).Status)
	// Observation includes this HCP even while it is not ready; the stale
	// pending entry must not subtract another three NICs.
	status.NotReadyResourceIDs = []*azcorearm.ResourceID{id}
	status.ObservedResources.Usage[kuberesources.SwiftNICResourceName] = resource.MustParse("63")
	require.Equal(t, metav1.ConditionFalse, evaluatePressure(status, pressureTestNow, settings).Status, "observed commitment still leaves 37%% reserve")
}
