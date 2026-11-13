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
	"fmt"
	"strings"
	"time"

	"gopkg.in/inf.v0"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/arohcp-cosmos-client/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/kuberesources"
)

const (
	cpuReserveThresholdPercent      int64 = 10
	memoryReserveThresholdPercent   int64 = 10
	swiftNICReserveThresholdPercent int64 = 10
	scaleCeilingMaxAge                    = 5 * time.Minute

	capacityReportMaxAge = 5 * time.Minute
)

type pressureSettings struct {
	cpuReservePercent      int64
	memoryReservePercent   int64
	swiftNICReservePercent int64
	scaleCeilingMaxAge     time.Duration
}

func defaultPressureSettings() pressureSettings {
	return pressureSettings{cpuReserveThresholdPercent, memoryReserveThresholdPercent, swiftNICReserveThresholdPercent, scaleCeilingMaxAge}
}

func unknownPressure(reason, message string) metav1.Condition {
	return metav1.Condition{Type: fleetapi.ConditionTypePressure, Status: metav1.ConditionUnknown, Reason: reason, Message: message}
}

// evaluatePressure uses only persisted scheduling evidence. Validate every
// resource before deciding True or False: invalid memory must not be hidden by
// an otherwise valid CPU pressure result.
func evaluatePressure(status fleetapi.ManagementClusterSchedulingStatus, now time.Time, settings pressureSettings) metav1.Condition {
	for _, check := range []struct{ condition, reason string }{
		{fleetapi.ConditionTypeCapacityDataCurrent, "CapacityCollectionNotCurrent"},
		{fleetapi.ConditionTypeScalingDataCurrent, "ScalingCollectionNotCurrent"},
	} {
		condition := meta.FindStatusCondition(status.Conditions, check.condition)
		if condition == nil {
			return unknownPressure(check.reason, check.condition+" is missing; check the reporting controller")
		}
		if condition.Status != metav1.ConditionTrue {
			return unknownPressure(check.reason, fmt.Sprintf("%s=%s: %s: %s", condition.Type, condition.Status, condition.Reason, condition.Message))
		}
	}
	for _, check := range []struct {
		timestamp *metav1.Time
		maxAge    time.Duration
		name      string
		reason    string
	}{
		{status.ObservedResources.LastReportedAt, capacityReportMaxAge, "ObservedResources.LastReportedAt", "CapacityReport"},
		{status.ScaleCeiling.LastReportedAt, settings.scaleCeilingMaxAge, "ScaleCeiling.LastReportedAt", "ScaleCeiling"},
	} {
		if check.timestamp == nil || check.timestamp.IsZero() {
			return unknownPressure("Missing"+check.reason+"Timestamp", check.name+" is missing; await a successful collection")
		}
		if check.timestamp.After(now) {
			return unknownPressure("Invalid"+check.reason+"Timestamp", check.name+" is in the future; check collector clocks")
		}
		if now.Sub(check.timestamp.Time) > check.maxAge {
			return unknownPressure("Stale"+check.reason, fmt.Sprintf("%s=%s exceeds maximum age %s; check the reporting pipeline", check.name, check.timestamp.UTC().Format(time.RFC3339), check.maxAge))
		}
	}

	pendingNICs, err := unreflectedSwiftNICReservations(status)
	if err != nil {
		return unknownPressure("InvalidPendingReservation", err.Error())
	}
	var low []string
	for _, check := range []struct {
		name      corev1.ResourceName
		threshold int64
	}{
		{corev1.ResourceCPU, settings.cpuReservePercent},
		{corev1.ResourceMemory, settings.memoryReservePercent},
		{kuberesources.SwiftNICResourceName, settings.swiftNICReservePercent},
	} {
		capacity, hasCapacity := status.ScaleCeiling.Capacity[check.name]
		usage, hasUsage := status.ObservedResources.Usage[check.name]
		requests, hasRequests := status.ObservedResources.Requests[check.name]
		if !hasCapacity || !hasUsage || !hasRequests {
			return unknownPressure("MissingResourceData", fmt.Sprintf("%s requires ScaleCeiling.Capacity, ObservedResources.Usage and ObservedResources.Requests entries", check.name))
		}
		if capacity.Sign() <= 0 || usage.Sign() < 0 || requests.Sign() < 0 {
			return unknownPressure("InvalidResourceData", fmt.Sprintf("%s requires positive scale-ceiling capacity and nonnegative usage/requests", check.name))
		}
		if check.threshold < 0 || check.threshold > 100 {
			return unknownPressure("InvalidReserveThreshold", fmt.Sprintf("%s reserve threshold must be between 0 and 100", check.name))
		}
		occupancy := usage
		if requests.Cmp(usage) > 0 {
			occupancy = requests
		}
		remaining := capacity.DeepCopy()
		remaining.Sub(occupancy)
		if check.name == kuberesources.SwiftNICResourceName {
			remaining.Sub(*resource.NewQuantity(pendingNICs, resource.DecimalSI))
		}
		// Compare exactly, including equality and fractional CPU quantities.
		reserve := new(inf.Dec).Mul(remaining.AsDec(), inf.NewDec(100, 0))
		minimum := new(inf.Dec).Mul(capacity.AsDec(), inf.NewDec(check.threshold, 0))
		if reserve.Cmp(minimum) < 0 {
			low = append(low, fmt.Sprintf("%s reserve %.2f%% is below %d%%", check.name, 100*remaining.AsApproximateFloat64()/capacity.AsApproximateFloat64(), check.threshold))
		}
	}
	if len(low) > 0 {
		return metav1.Condition{Type: fleetapi.ConditionTypePressure, Status: metav1.ConditionTrue, Reason: "InsufficientReserve", Message: strings.Join(low, "; ")}
	}
	return metav1.Condition{Type: fleetapi.ConditionTypePressure, Status: metav1.ConditionFalse, Reason: "AdequateReserve", Message: "CPU, memory and Swift NIC reserves meet their respective thresholds"}
}

// ObservedResources includes all observed HCPs, including not-ready ones. Only
// distinct pending IDs absent from both observation sets add reservations.
// Missing reservation metadata during a rolling upgrade is unknown, not zero.
func unreflectedSwiftNICReservations(status fleetapi.ManagementClusterSchedulingStatus) (int64, error) {
	reflected := sets.New[string]()
	for _, ids := range [][]*azcorearm.ResourceID{status.ReadyResourceIDs, status.NotReadyResourceIDs} {
		for _, id := range ids {
			if id != nil {
				reflected.Insert(strings.ToLower(id.String()))
			}
		}
	}
	var total int64
	for _, id := range status.PendingAssignedClusters {
		if id == nil {
			return 0, fmt.Errorf("pending assignment has no resource ID; check placement and pending cleanup")
		}
		key := strings.ToLower(id.String())
		if reflected.Has(key) {
			continue
		}
		count, found := status.PendingSwiftNICReservations[key]
		if !found || (count != 0 && count != 1 && count != 3) {
			return 0, fmt.Errorf("pending assignment %s has missing or invalid Swift NIC reservation metadata; check backend placement/pending cleanup", key)
		}
		total += count
		reflected.Insert(key)
	}
	return total, nil
}
