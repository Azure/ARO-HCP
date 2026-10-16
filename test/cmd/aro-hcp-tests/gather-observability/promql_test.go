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

package gatherobservability

import (
	"testing"
	"time"
)

func TestResolveReportRange(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		name  string
		query string
		end   time.Time
		want  string
	}{
		{
			// Regression case: rounding up to a coarser unit (e.g. the nearest
			// minute) would push the effective lookback earlier than start,
			// letting a pre-window spike enter a ranking that claims to cover
			// only [start,end]. Millisecond precision keeps the window exact.
			name:  "non-round duration is preserved exactly, not rounded up to a coarser unit",
			query: "increase(foo[__REPORT_RANGE__] @ end())",
			end:   base.Add(1*time.Hour + 30*time.Minute + time.Second),
			want:  "increase(foo[5401000ms] @ end())",
		},
		{
			name:  "whole-minute windows convert exactly",
			query: "increase(foo[__REPORT_RANGE__] @ end())",
			end:   base.Add(45 * time.Minute),
			want:  "increase(foo[2700000ms] @ end())",
		},
		{
			name:  "zero-length window floors to one millisecond rather than zero",
			query: "increase(foo[__REPORT_RANGE__] @ end())",
			end:   base,
			want:  "increase(foo[1ms] @ end())",
		},
		{
			name:  "every occurrence is substituted",
			query: "increase(foo[__REPORT_RANGE__] @ end()) / increase(bar[__REPORT_RANGE__] @ end())",
			end:   base.Add(2 * time.Hour),
			want:  "increase(foo[7200000ms] @ end()) / increase(bar[7200000ms] @ end())",
		},
		{
			name:  "queries without the placeholder are returned unchanged",
			query: "rate(foo[5m])",
			end:   base.Add(time.Hour),
			want:  "rate(foo[5m])",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := resolveReportRange(tt.query, base, tt.end)
			if got != tt.want {
				t.Errorf("resolveReportRange() = %q, want %q", got, tt.want)
			}
		})
	}
}
