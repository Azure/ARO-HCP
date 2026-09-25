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
	"fmt"
	"strings"
	"testing"
)

func TestFrontendPhaseQueries(t *testing.T) {
	t.Parallel()
	cfg, err := loadQueriesConfig()
	if err != nil {
		t.Fatalf("load embedded queries config: %v", err)
	}
	queries := map[string]QuerySpec{}
	for _, panel := range cfg.Panels {
		if panel.Title == "Frontend Metrics" {
			for _, query := range panel.Queries {
				queries[query.Title] = query
			}
		}
	}
	for _, tc := range []struct {
		title    string
		quantile string
	}{
		{"Frontend Request Phase Latency (p50, diagnostic)", "0.5"},
		{"Frontend Request Phase Latency (p99, diagnostic)", "0.99"},
	} {
		t.Run(tc.title, func(t *testing.T) {
			query, ok := queries[tc.title]
			if !ok {
				t.Fatal("phase chart missing from Frontend Metrics")
			}
			want := fmt.Sprintf(`histogram_quantile(%s,
				sum by (le, route, method, phase, cluster) (
					rate(frontend_http_request_phase_duration_seconds_bucket{route!="/subscriptions/{subscriptionid}/providers/microsoft.redhatopenshift/locations/{location}/hcpoperationresults/{operationid}"}[30m])
				)
			)`, tc.quantile)
			if strings.Join(strings.Fields(query.Query), "") != strings.Join(strings.Fields(want), "") {
				t.Errorf("phase query = %s, want %s", query.Query, want)
			}
			if query.Source != sourcePrometheus || query.Workspace != workspaceSvc || query.Step != "60s" || query.Unit != "seconds" {
				t.Errorf("phase chart must query svc Prometheus at 60s steps in seconds: %+v", query)
			}
			if query.ChartType != chartTypeLine || query.MinPeakThreshold != 0 {
				t.Errorf("phase chart must use an unfiltered line graph: chartType=%q, minPeakThreshold=%v", query.ChartType, query.MinPeakThreshold)
			}
		})
	}
}
