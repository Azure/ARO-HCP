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
	"time"

	"k8s.io/apimachinery/pkg/api/meta"

	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
)

// IsCapacityCurrent reports whether capacity should be treated as up to date.
//
// Both checks matter and catch different failure modes: the condition alone
// only reflects the outcome of the most recent controller run — if the
// controller stops running, the condition simply stops updating and stays
// whatever it last was. Checking capacity.LastReportedAt against now/maxAge
// independently catches that stalled-pipeline case.
func IsCapacityCurrent(status fleetapi.ManagementClusterSchedulingStatus, now time.Time, maxAge time.Duration) bool {
	if !meta.IsStatusConditionTrue(status.Conditions, fleetapi.ConditionTypeCapacityDataCurrent) {
		return false
	}
	if status.ObservedResources.LastReportedAt == nil {
		return false
	}
	return now.Sub(status.ObservedResources.LastReportedAt.Time) <= maxAge
}
