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

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestProposeLimits(t *testing.T) {
	const (
		maxLimit  = int64(20_000_000)
		threshold = 85.0
	)

	tests := []struct {
		name        string
		current     AMWLimits
		utilization AMWUtilization
		threshold   float64
		maxLimit    int64
		expected    *AMWLimits
	}{
		{
			// Both below 2M — bump both to 2M regardless of utilization.
			name:        "below auto-approve limit bumps to 2M unconditionally",
			current:     AMWLimits{MaxActiveTimeSeries: 1_000_000, MaxEventsPerMinute: 1_000_000},
			utilization: AMWUtilization{ActiveTimeSeriesPercent: 10, EventsPerMinutePercent: 10},
			threshold:   threshold,
			maxLimit:    maxLimit,
			expected:    &AMWLimits{MaxActiveTimeSeries: 2_000_000, MaxEventsPerMinute: 2_000_000},
		},
		{
			// One below 2M, one at 2M with low utilization — only the sub-2M one changes.
			name:        "only sub-2M dimension bumped when other is at 2M with low usage",
			current:     AMWLimits{MaxActiveTimeSeries: 1_000_000, MaxEventsPerMinute: 2_000_000},
			utilization: AMWUtilization{ActiveTimeSeriesPercent: 50, EventsPerMinutePercent: 50},
			threshold:   threshold,
			maxLimit:    maxLimit,
			expected:    &AMWLimits{MaxActiveTimeSeries: 2_000_000, MaxEventsPerMinute: 2_000_000},
		},
		{
			// One below 2M, one at 2M with high utilization — both change.
			// ATS: bumped to 2M. EPM: 90% of 2M = 1.8M, 175% = 3,150,000.
			name:        "sub-2M dimension bumped and high-usage dimension scaled",
			current:     AMWLimits{MaxActiveTimeSeries: 1_000_000, MaxEventsPerMinute: 2_000_000},
			utilization: AMWUtilization{ActiveTimeSeriesPercent: 50, EventsPerMinutePercent: 90},
			threshold:   threshold,
			maxLimit:    maxLimit,
			expected:    &AMWLimits{MaxActiveTimeSeries: 2_000_000, MaxEventsPerMinute: 3_150_000},
		},
		{
			name:        "no scaling when at 2M with both below threshold",
			current:     AMWLimits{MaxActiveTimeSeries: 2_000_000, MaxEventsPerMinute: 2_000_000},
			utilization: AMWUtilization{ActiveTimeSeriesPercent: 30, EventsPerMinutePercent: 40},
			threshold:   threshold,
			maxLimit:    maxLimit,
			expected:    nil,
		},
		{
			name:        "no scaling when exactly at threshold",
			current:     AMWLimits{MaxActiveTimeSeries: 2_000_000, MaxEventsPerMinute: 2_000_000},
			utilization: AMWUtilization{ActiveTimeSeriesPercent: 85, EventsPerMinutePercent: 85},
			threshold:   threshold,
			maxLimit:    maxLimit,
			expected:    nil,
		},
		{
			// 90% of 2M = 1.8M usage. 175% of 1.8M = 3,150,000.
			name:        "scales active time series to 175% of usage",
			current:     AMWLimits{MaxActiveTimeSeries: 2_000_000, MaxEventsPerMinute: 2_000_000},
			utilization: AMWUtilization{ActiveTimeSeriesPercent: 90, EventsPerMinutePercent: 50},
			threshold:   threshold,
			maxLimit:    maxLimit,
			expected:    &AMWLimits{MaxActiveTimeSeries: 3_150_000, MaxEventsPerMinute: 2_000_000},
		},
		{
			// 95% of 2M = 1.9M. 175% of 1.9M = 3,325,000. Rounded = 3,320,000.
			name:        "scales events per minute to 175% of usage",
			current:     AMWLimits{MaxActiveTimeSeries: 2_000_000, MaxEventsPerMinute: 2_000_000},
			utilization: AMWUtilization{ActiveTimeSeriesPercent: 50, EventsPerMinutePercent: 95},
			threshold:   threshold,
			maxLimit:    maxLimit,
			expected:    &AMWLimits{MaxActiveTimeSeries: 2_000_000, MaxEventsPerMinute: 3_320_000},
		},
		{
			// 90% of 2M → 3,150,000. 95% of 2M → 3,320,000.
			name:        "scales both dimensions",
			current:     AMWLimits{MaxActiveTimeSeries: 2_000_000, MaxEventsPerMinute: 2_000_000},
			utilization: AMWUtilization{ActiveTimeSeriesPercent: 90, EventsPerMinutePercent: 95},
			threshold:   threshold,
			maxLimit:    maxLimit,
			expected:    &AMWLimits{MaxActiveTimeSeries: 3_150_000, MaxEventsPerMinute: 3_320_000},
		},
		{
			// 95% of 12M = 11.4M. 175% = 19,950,000.
			name:        "caps at max limit when 175% of usage exceeds it",
			current:     AMWLimits{MaxActiveTimeSeries: 12_000_000, MaxEventsPerMinute: 12_000_000},
			utilization: AMWUtilization{ActiveTimeSeriesPercent: 95, EventsPerMinutePercent: 95},
			threshold:   threshold,
			maxLimit:    maxLimit,
			expected:    &AMWLimits{MaxActiveTimeSeries: 19_950_000, MaxEventsPerMinute: 19_950_000},
		},
		{
			name:        "no scaling when already at max limit",
			current:     AMWLimits{MaxActiveTimeSeries: maxLimit, MaxEventsPerMinute: maxLimit},
			utilization: AMWUtilization{ActiveTimeSeriesPercent: 90, EventsPerMinutePercent: 90},
			threshold:   threshold,
			maxLimit:    maxLimit,
			expected:    nil,
		},
		{
			// Active at max stays. Events: 90% of 5M = 4.5M. 175% = 7,875,000. Rounded = 7,870,000.
			name:        "only scales dimension not at max",
			current:     AMWLimits{MaxActiveTimeSeries: maxLimit, MaxEventsPerMinute: 5_000_000},
			utilization: AMWUtilization{ActiveTimeSeriesPercent: 90, EventsPerMinutePercent: 90},
			threshold:   threshold,
			maxLimit:    maxLimit,
			expected:    &AMWLimits{MaxActiveTimeSeries: maxLimit, MaxEventsPerMinute: 7_870_000},
		},
		{
			// 100% of 4M = 4M. 175% = 7,000,000. 100% of 8M = 8M. 175% = 14,000,000.
			name:        "at 100% utilization requests 175% of usage",
			current:     AMWLimits{MaxActiveTimeSeries: 4_000_000, MaxEventsPerMinute: 8_000_000},
			utilization: AMWUtilization{ActiveTimeSeriesPercent: 100, EventsPerMinutePercent: 100},
			threshold:   threshold,
			maxLimit:    maxLimit,
			expected:    &AMWLimits{MaxActiveTimeSeries: 7_000_000, MaxEventsPerMinute: 14_000_000},
		},
		{
			// 86% of 2M = 1,720,000. 175% = 3,010,000.
			name:        "just above threshold produces increase rounded to 10k",
			current:     AMWLimits{MaxActiveTimeSeries: 2_000_000, MaxEventsPerMinute: 2_000_000},
			utilization: AMWUtilization{ActiveTimeSeriesPercent: 86, EventsPerMinutePercent: 86},
			threshold:   threshold,
			maxLimit:    maxLimit,
			expected:    &AMWLimits{MaxActiveTimeSeries: 3_010_000, MaxEventsPerMinute: 3_010_000},
		},
		{
			// Over 100% utilization (being throttled):
			// 95% of 2M = 1,900,000. 175% = 3,325,000. Rounded = 3,320,000.
			// 163% of 2M = 3,260,000. 175% = 5,705,000. Rounded = 5,700,000.
			name:        "over 100% utilization requests 175% of actual usage",
			current:     AMWLimits{MaxActiveTimeSeries: 2_000_000, MaxEventsPerMinute: 2_000_000},
			utilization: AMWUtilization{ActiveTimeSeriesPercent: 95, EventsPerMinutePercent: 163},
			threshold:   threshold,
			maxLimit:    maxLimit,
			expected:    &AMWLimits{MaxActiveTimeSeries: 3_320_000, MaxEventsPerMinute: 5_700_000},
		},
		{
			// Below 2M with high utilization — bumps to 2M (auto-approve), not 175% of usage.
			// 126% of 1M = 1,260,000. 175% = 2,205,000. But auto-approve gives us 2M safely.
			name:        "below auto-approve with high usage still proposes 2M not usage-based",
			current:     AMWLimits{MaxActiveTimeSeries: 1_000_000, MaxEventsPerMinute: 1_000_000},
			utilization: AMWUtilization{ActiveTimeSeriesPercent: 126, EventsPerMinutePercent: 400},
			threshold:   threshold,
			maxLimit:    maxLimit,
			expected:    &AMWLimits{MaxActiveTimeSeries: 2_000_000, MaxEventsPerMinute: 2_000_000},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ProposeLimits(tt.current, tt.utilization, tt.threshold, tt.maxLimit)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestProposeDimension(t *testing.T) {
	tests := []struct {
		name               string
		currentLimit       int64
		utilizationPercent float64
		maxLimit           int64
		expected           int64
	}{
		{
			name:               "rounds down to nearest 10k",
			currentLimit:       2_000_000,
			utilizationPercent: 86,
			maxLimit:           20_000_000,
			// 86% of 2M = 1,720,000. 175% = 3,010,000.
			expected: 3_010_000,
		},
		{
			name:               "exact multiple of 10k unchanged",
			currentLimit:       5_000_000,
			utilizationPercent: 100,
			maxLimit:           20_000_000,
			// 100% of 5M = 5M. 175% = 8,750,000.
			expected: 8_750_000,
		},
		{
			name:               "caps at maxLimit",
			currentLimit:       15_000_000,
			utilizationPercent: 90,
			maxLimit:           20_000_000,
			// 90% of 15M = 13.5M. 175% = 23,625,000 → capped at 20M.
			expected: 20_000_000,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := proposeDimension(tt.currentLimit, tt.utilizationPercent, tt.maxLimit)
			assert.Equal(t, tt.expected, result)
		})
	}
}
