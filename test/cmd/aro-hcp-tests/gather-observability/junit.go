// Copyright 2025 Microsoft Corporation
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
	"bytes"
	"fmt"
	"maps"
	"slices"
	"strings"
	"text/template"
	"time"

	_ "embed"

	"github.com/go-logr/logr"

	"github.com/Azure/ARO-HCP/test/util/junit"
	"github.com/Azure/ARO-HCP/test/util/timing"
)

//go:embed artifacts/failure-output.tmpl
var failureOutputTmplData string

func buildTestName(workspace, ruleName string) string {
	return fmt.Sprintf("[aro-hcp-observability] [%s] alert %s does not fire", workspace, ruleName)
}

func alertsToJUnit(logger logr.Logger, workspaces map[string]*workspaceData, timeWindow timing.TimeWindow) *junit.TestSuites {
	testSuite := junit.TestSuite{
		Name: "aro-hcp-tests",
	}
	for _, wsType := range slices.Sorted(maps.Keys(workspaces)) {
		ws := workspaces[wsType]
		workspaceTestSuite := workspaceDataToJUnit(logger, ws, timeWindow)
		testSuite.TestCases = append(testSuite.TestCases, workspaceTestSuite.TestCases...)
		testSuite.NumTests += workspaceTestSuite.NumTests
		testSuite.NumFailed += workspaceTestSuite.NumFailed
		testSuite.NumSkipped += workspaceTestSuite.NumSkipped
		testSuite.Duration += workspaceTestSuite.Duration
	}
	return &junit.TestSuites{
		Suites: []*junit.TestSuite{&testSuite},
	}
}

func workspaceDataToJUnit(logger logr.Logger, ws *workspaceData, timeWindow timing.TimeWindow) *junit.TestSuite {
	logger = logger.WithValues("workspace", ws.Type)
	ruleNames := make(map[string]bool, len(ws.AlertRules))
	for _, r := range ws.AlertRules {
		ruleNames[r] = true
	}

	groups := make(map[string][]alert)
	for _, a := range ws.FiredAlerts {
		if !ruleNames[a.Alert.Name] {
			logger.Info("ignoring fired alert with no matching rule definition", "alert", a.Alert.Name)
			continue
		}
		groups[a.Alert.Name] = append(groups[a.Alert.Name], a)
	}

	var testCases []*junit.TestCase
	var totalDuration float64
	var numFailed, numSkipped uint

	for _, rule := range ws.AlertRules {
		tc := &junit.TestCase{
			Name: buildTestName(ws.Type, rule),
		}

		firings, hasFirings := groups[rule]
		if !hasFirings {
			testCases = append(testCases, tc)
			continue
		}

		duration := computeGroupDuration(firings, timeWindow)
		totalDuration += duration
		tc.Duration = duration

		// Evaluate each blast-radius category present among this rule's
		// firings independently (a single rule, e.g. KubePodNotReady, can
		// span multiple categories within one workspace -- for example
		// firings in both a controlplane namespace (tier 4, fail) and a
		// hostedcluster namespace (tier 5, warn)). The test case fails iff
		// any category's policy evaluation fails; see ARO-28187.
		catGroups := groupByCategory(firings)
		var failing, nonFailing []categoryFiringGroup
		for _, g := range catGroups {
			if evaluateCategoryPolicy(g, timeWindow) {
				failing = append(failing, g)
			} else {
				nonFailing = append(nonFailing, g)
			}
		}

		if len(failing) == 0 {
			// No category's policy calls for failure: mark the test case
			// skipped, same as the old "all known issues" behavior, but now
			// covering ignore/warn/under-threshold categories alike.
			numSkipped++
			tc.SkipMessage = &junit.SkipMessage{
				Message: buildSkipMessage(nonFailing),
			}
		} else {
			numFailed++
			tc.FailureOutput = &junit.FailureOutput{
				Message: buildFailureMessage(catGroups, failing),
				Output:  renderFirings("", flattenGroups(failing)),
			}
			if len(nonFailing) > 0 {
				tc.SystemOut = renderFirings("Non-failing firings (not counted as failures):", flattenGroups(nonFailing))
			}
		}

		testCases = append(testCases, tc)
	}

	return &junit.TestSuite{
		Name:       "aro-hcp-tests",
		NumTests:   uint(len(testCases)),
		NumFailed:  numFailed,
		NumSkipped: numSkipped,
		Duration:   totalDuration,
		TestCases:  testCases,
	}
}

type timeInterval struct {
	start, end time.Time
}

func mergeIntervals(intervals []timeInterval) []timeInterval {
	if len(intervals) == 0 {
		return nil
	}
	slices.SortFunc(intervals, func(a, b timeInterval) int {
		return a.start.Compare(b.start)
	})
	merged := []timeInterval{intervals[0]}
	for _, next := range intervals[1:] {
		last := &merged[len(merged)-1]
		if next.start.After(last.end) {
			merged = append(merged, next)
			continue
		}
		if next.end.After(last.end) {
			last.end = next.end
		}
	}
	return merged
}

func computeGroupDuration(firings []alert, tw timing.TimeWindow) float64 {
	var intervals []timeInterval
	for _, f := range firings {
		if f.Alert.StartsAt == nil {
			continue
		}
		end := tw.End
		if f.Alert.EndsAt != nil {
			end = *f.Alert.EndsAt
		}
		if end.After(*f.Alert.StartsAt) {
			intervals = append(intervals, timeInterval{start: *f.Alert.StartsAt, end: end})
		}
	}
	var total float64
	for _, interval := range mergeIntervals(intervals) {
		total += interval.end.Sub(interval.start).Seconds()
	}
	return total
}

// categoryFiringGroup buckets a rule's firings by the blast-radius category
// assigned by categorizeAlerts (see categories.go), along with that
// category's tier/policy/reason and threshold, denormalized from the first
// firing in the group (all firings in a group share the same category, so
// its config is consistent across them).
type categoryFiringGroup struct {
	category           string // "" means no category matched (uncategorized)
	tier               int
	policy             string
	reason             string
	minFirings         int
	minDurationSeconds float64
	firings            []alert
}

// groupByCategory buckets firings by their assigned category, preserving the
// order categories first appear in.
func groupByCategory(firings []alert) []categoryFiringGroup {
	index := make(map[string]int, len(firings))
	var groups []categoryFiringGroup
	for _, f := range firings {
		key := f.Metadata.Category
		i, ok := index[key]
		if !ok {
			i = len(groups)
			index[key] = i
			groups = append(groups, categoryFiringGroup{
				category:           f.Metadata.Category,
				tier:               f.Metadata.CategoryTier,
				policy:             f.Metadata.CategoryPolicy,
				reason:             f.Metadata.CategoryReason,
				minFirings:         f.Metadata.CategoryMinFirings,
				minDurationSeconds: f.Metadata.CategoryMinDurationSeconds,
			})
		}
		groups[i].firings = append(groups[i].firings, f)
	}
	return groups
}

// evaluateCategoryPolicy returns true if the group's category policy calls
// for failing the owning test case. An uncategorized group (no category
// config matched) fails closed, same as policyFail: per David Eads' review
// of ARO-28187, an unclassified firing is "treated as [tier] 1a until
// reassigned", not silently passed.
func evaluateCategoryPolicy(g categoryFiringGroup, tw timing.TimeWindow) bool {
	switch g.policy {
	case policyFail:
		return true
	case policyFailOverThreshold:
		if g.minFirings > 0 && len(g.firings) >= g.minFirings {
			return true
		}
		if g.minDurationSeconds > 0 && computeGroupDuration(g.firings, tw) >= g.minDurationSeconds {
			return true
		}
		return false
	case policyWarn, policyIgnore:
		return false
	default:
		return true
	}
}

func flattenGroups(groups []categoryFiringGroup) []alert {
	var firings []alert
	for _, g := range groups {
		firings = append(firings, g.firings...)
	}
	return firings
}

func categoryLabel(g categoryFiringGroup) string {
	name := g.category
	if name == "" {
		name = "uncategorized"
	}
	policy := g.policy
	if policy == "" {
		policy = policyFail // matches evaluateCategoryPolicy's fail-closed default
	}
	return fmt.Sprintf("%s (tier %d, %s)", name, g.tier, policy)
}

func buildSkipMessage(nonFailing []categoryFiringGroup) string {
	parts := make([]string, 0, len(nonFailing))
	for _, g := range nonFailing {
		label := categoryLabel(g)
		if g.reason != "" {
			label = fmt.Sprintf("%s: %s", label, g.reason)
		}
		parts = append(parts, label)
	}
	return "non-blocking: " + strings.Join(parts, "; ")
}

func buildFailureMessage(all, failing []categoryFiringGroup) string {
	var total int
	for _, g := range all {
		total += len(g.firings)
	}
	names := make([]string, len(failing))
	for i, g := range failing {
		names[i] = categoryLabel(g)
	}
	return fmt.Sprintf("alert fired %d time(s); failing categories: %s", total, strings.Join(names, ", "))
}

var tmplFuncs = template.FuncMap{
	"state": func(condition string) string {
		if condition == "Fired" {
			return "Fired (not resolved)"
		}
		return condition
	},
	"formatTime": func(t any) string {
		if t == nil {
			return ""
		}
		if v, ok := t.(*time.Time); ok && v != nil {
			return v.UTC().Format("2006-01-02T15:04:05Z")
		}
		return ""
	},
	"formatLabels": func(labels map[string]string) string {
		keys := slices.Sorted(maps.Keys(labels))
		parts := make([]string, len(keys))
		for i, k := range keys {
			parts[i] = fmt.Sprintf("%s=%q", k, labels[k])
		}
		return strings.Join(parts, ", ")
	},
	"inc": func(i int) int { return i + 1 },
}

var firingsTemplate = template.Must(template.New("failure-output.tmpl").Funcs(tmplFuncs).Parse(failureOutputTmplData))

func renderFirings(header string, firings []alert) string {
	var buf bytes.Buffer
	if err := firingsTemplate.Execute(&buf, struct {
		Header  string
		Firings []alert
	}{Header: header, Firings: firings}); err != nil {
		return fmt.Sprintf("(failed to render output: %v)", err)
	}
	return buf.String()
}
