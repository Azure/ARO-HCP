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

package monitor

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"
)

// alertingBicep is the Prometheus rule group consumed by the dev-CI alerting
// step. Its expressions are interpolated strings, so nothing else in the repo
// fails when they drift from the collector's metric names, the schedule math
// or the scrape-replica dedup. These tests pin all three.
const alertingBicep = "../../alerting.bicep"

func mustReadAlertingBicep(t *testing.T) string {
	t.Helper()
	content, err := os.ReadFile(alertingBicep)
	if err != nil {
		t.Fatalf("failed to read %s: %v", alertingBicep, err)
	}
	return string(content)
}

// Every metric name the rule expressions select must be a metric the
// collector actually emits; a rename in either place otherwise pages on a
// series that never exists (or stops paging silently).
func TestAlertRulesReferenceExportedMetrics(t *testing.T) {
	bicep := mustReadAlertingBicep(t)

	exported := map[string]bool{
		metricLastRun:         true,
		metricLastRunDuration: true,
		metricLastSuccess:     true,
		metricConsecutive:     true,
		metricScrapeSuccess:   true,
		metricLastScrape:      true,
		metricJobsMatched:     true,
	}

	// Metric selectors look like "(image_updater_foo)" inside expressions.
	selectors := regexp.MustCompile(`\((image_updater_[a-z_]+)\)`).
		FindAllStringSubmatch(bicep, -1)
	if len(selectors) == 0 {
		t.Fatal("no metric selectors found in alerting.bicep; parsing drifted")
	}
	for _, match := range selectors {
		if !exported[match[1]] {
			t.Errorf("alert rule selects %q, which the collector does not emit", match[1])
		}
	}

	// And every emitted metric that a rule should watch is referenced.
	for metric := range exported {
		if metric == metricLastSuccess {
			// last_success is exported for dashboards, not alerted on.
			continue
		}
		if !strings.Contains(bicep, metric) {
			t.Errorf("collector emits %q but no alert rule references it", metric)
		}
	}
}

// The managed Prometheus agent scrapes each target from two replicas and the
// Azure Monitor Workspace stores both series; every instant selector must be
// governed by the prometheus_replica aggregation or each firing notifies the
// action group twice. Range selectors (absent_over_time) have no labels to
// aggregate and are exempt.
func TestAlertRulesDedupeScrapeReplicas(t *testing.T) {
	bicep := mustReadAlertingBicep(t)

	expressions := regexp.MustCompile(`expression: '(.+)'`).
		FindAllStringSubmatch(bicep, -1)
	if len(expressions) != 5 {
		t.Fatalf("expected 5 rule expressions, found %d; parsing drifted", len(expressions))
	}

	// In bicep source the aggregation is the unexpanded ${replicaDedup}
	// interpolation, whose value is pinned separately below. The selector
	// pattern consumes the open paren, so the prefix ends at the
	// interpolation itself.
	const dedup = "${replicaDedup}"
	selectorPattern := regexp.MustCompile(`\((image_updater_[a-z_]+)(\[[^\]]*\])?\)`)
	for _, match := range expressions {
		expression := match[1]
		for _, selector := range selectorPattern.FindAllStringSubmatch(expression, -1) {
			if selector[2] != "" {
				continue // range selector, exempt
			}
			i := strings.Index(expression, selector[0])
			prefix := strings.TrimRight(expression[:i], " ")
			if !strings.HasSuffix(prefix, dedup) {
				t.Errorf("selector %s in expression %q is not preceded by the prometheus_replica dedup", selector[0], expression)
			}
		}
	}

	// And the dedup variable itself must aggregate exactly the replica label.
	if !strings.Contains(bicep, "var replicaDedup = 'max without (prometheus_replica)'") {
		t.Error("replicaDedup variable changed; it must aggregate prometheus_replica away")
	}
}

// The stall budget is schedule math: the job runs 06:00/14:00/22:00 UTC on
// weekdays only. Pin the thresholds and the invariants that keep the Monday
// rule from false-paging: the weekday budget covers the 8h slot gap plus run
// time, and the Monday budget crosses before the 09:00 weekday gate takes
// over so a Friday-night stall never falls through both rules.
func TestStallThresholdMath(t *testing.T) {
	bicep := mustReadAlertingBicep(t)

	weekday := intParam(t, bicep, "weekdayStallSeconds")
	monday := intParam(t, bicep, "mondayStallSeconds")

	// Weekday budget: 9h, covering the 8h gap between slots plus run time.
	if want := 9 * time.Hour; time.Duration(weekday)*time.Second != want {
		t.Errorf("weekdayStallSeconds = %d, want %d (9h)", weekday, int(want/time.Second))
	}

	// Monday budget: 58h. From a Friday ~22:25 completion the threshold is
	// crossed Monday ~08:25, before the 09:00 weekday gate replaces this rule
	// (58h - 56h weekend gap = 2h grace for the Monday 06:00 run).
	if want := 58 * time.Hour; time.Duration(monday)*time.Second != want {
		t.Errorf("mondayStallSeconds = %d, want %d (58h)", monday, int(want/time.Second))
	}

	if weekday >= monday {
		t.Errorf("weekdayStallSeconds (%d) must be smaller than mondayStallSeconds (%d)", weekday, monday)
	}

	// Gates: day_of_week() is Sunday=0..Saturday=6, hour() is UTC. The
	// weekday gate must cover Tue-Fri fully plus Monday from 09:00, and the
	// Monday gate only before 09:00, so every hour of the week is governed by
	// exactly one budget.
	if !strings.Contains(bicep, "var weekdayStallGate = '(day_of_week() >= 2 and day_of_week() <= 5) or (day_of_week() == 1 and hour() >= 9)'") {
		t.Error("weekdayStallGate changed; day-numbering or hour gate drifted")
	}
	if !strings.Contains(bicep, "var mondayStallGate = 'day_of_week() == 1 and hour() < 9'") {
		t.Error("mondayStallGate changed; day-numbering or hour gate drifted")
	}
}

func intParam(t *testing.T, bicep, name string) int {
	t.Helper()
	match := regexp.MustCompile(name + ` int = (\d+)`).FindStringSubmatch(bicep)
	if match == nil {
		t.Fatalf("param %s not found in alerting.bicep; parsing drifted", name)
	}
	var value int
	if _, err := fmt.Sscanf(match[1], "%d", &value); err != nil {
		t.Fatalf("param %s value %q is not an integer: %v", name, match[1], err)
	}
	return value
}
