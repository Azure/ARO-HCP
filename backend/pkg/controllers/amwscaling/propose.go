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

package amwscaling

// AMWLimits represents the current ingestion limits on an Azure Monitor Workspace.
type AMWLimits struct {
	MaxActiveTimeSeries int64
	MaxEventsPerMinute  int64
}

// AMWUtilization represents the current utilization as percentages (0-100).
type AMWUtilization struct {
	ActiveTimeSeriesPercent float64
	EventsPerMinutePercent  float64
}

// ProposeLimits returns new limits if scaling is needed, or nil if no change is required.
// The threshold is a percentage (0-100) above which limits should be increased.
// The maxLimit caps the maximum value any limit can reach (Azure caps at 20M).
//
// Azure's approval rules:
//   - The requested limit must be ≤ 200% of current absolute usage.
//   - The server also denies requests if peak usage over the last N days is less
//     than 80% of the current limit (i.e., the workspace must be consistently near
//     its limit before an increase is approved).
//
// We request 175% of current usage and round down to the nearest 10,000 to stay
// safely within Azure's 200% approval limit while providing meaningful headroom.
// See: https://learn.microsoft.com/en-us/azure/azure-monitor/metrics/azure-monitor-workspace-monitor-ingest-limits?tabs=portal#request-for-an-increase-in-ingestion-limits-preview
func ProposeLimits(current AMWLimits, utilization AMWUtilization, threshold float64, maxLimit int64) *AMWLimits {
	proposed := current
	changed := false

	if utilization.ActiveTimeSeriesPercent > threshold && current.MaxActiveTimeSeries < maxLimit {
		proposed.MaxActiveTimeSeries = proposeDimension(current.MaxActiveTimeSeries, utilization.ActiveTimeSeriesPercent, maxLimit)
		if proposed.MaxActiveTimeSeries > current.MaxActiveTimeSeries {
			changed = true
		} else {
			proposed.MaxActiveTimeSeries = current.MaxActiveTimeSeries
		}
	}
	if utilization.EventsPerMinutePercent > threshold && current.MaxEventsPerMinute < maxLimit {
		proposed.MaxEventsPerMinute = proposeDimension(current.MaxEventsPerMinute, utilization.EventsPerMinutePercent, maxLimit)
		if proposed.MaxEventsPerMinute > current.MaxEventsPerMinute {
			changed = true
		} else {
			proposed.MaxEventsPerMinute = current.MaxEventsPerMinute
		}
	}

	if !changed {
		return nil
	}
	return &proposed
}

// proposeDimension computes the new limit for a single dimension.
// It requests 175% of current absolute usage, rounded down to the nearest 10,000.
func proposeDimension(currentLimit int64, utilizationPercent float64, maxLimit int64) int64 {
	currentUsage := float64(currentLimit) * utilizationPercent / 100
	requested := int64(currentUsage * 1.75)
	// Round down to nearest 10,000.
	requested = (requested / 10_000) * 10_000
	return min(requested, maxLimit)
}
