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

package fleetapihelpers

import (
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
)

func TestIsCapacityCurrent(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	maxAge := 5 * time.Minute

	cond := func(status metav1.ConditionStatus) metav1.Condition {
		return metav1.Condition{Type: fleetapi.ConditionTypeCapacityDataCurrent, Status: status}
	}
	reportedAt := func(d time.Duration) *metav1.Time {
		t := metav1.NewTime(now.Add(-d))
		return &t
	}

	for _, tc := range []struct {
		name   string
		status fleetapi.ManagementClusterSchedulingStatus
		want   bool
	}{
		{
			name: "condition true and recently reported is current",
			status: fleetapi.ManagementClusterSchedulingStatus{
				Conditions: []metav1.Condition{cond(metav1.ConditionTrue)},
				ObservedResources: fleetapi.ObservedResources{
					LastReportedAt: reportedAt(1 * time.Minute),
				},
			},
			want: true,
		},
		{
			name: "condition true but reported longer ago than maxAge is not current",
			status: fleetapi.ManagementClusterSchedulingStatus{
				Conditions: []metav1.Condition{cond(metav1.ConditionTrue)},
				ObservedResources: fleetapi.ObservedResources{
					LastReportedAt: reportedAt(10 * time.Minute),
				},
			},
			want: false,
		},
		{
			name: "condition false is not current even if recently reported",
			status: fleetapi.ManagementClusterSchedulingStatus{
				Conditions: []metav1.Condition{cond(metav1.ConditionFalse)},
				ObservedResources: fleetapi.ObservedResources{
					LastReportedAt: reportedAt(1 * time.Minute),
				},
			},
			want: false,
		},
		{
			name: "condition absent is not current",
			status: fleetapi.ManagementClusterSchedulingStatus{
				ObservedResources: fleetapi.ObservedResources{
					LastReportedAt: reportedAt(1 * time.Minute),
				},
			},
			want: false,
		},
		{
			name: "condition true but no LastReportedAt is not current",
			status: fleetapi.ManagementClusterSchedulingStatus{
				Conditions: []metav1.Condition{cond(metav1.ConditionTrue)},
			},
			want: false,
		},
		{
			name: "reported exactly at maxAge is current",
			status: fleetapi.ManagementClusterSchedulingStatus{
				Conditions: []metav1.Condition{cond(metav1.ConditionTrue)},
				ObservedResources: fleetapi.ObservedResources{
					LastReportedAt: reportedAt(maxAge),
				},
			},
			want: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsCapacityCurrent(tc.status, now, maxAge); got != tc.want {
				t.Errorf("IsCapacityCurrent = %v, want %v", got, tc.want)
			}
		})
	}
}
