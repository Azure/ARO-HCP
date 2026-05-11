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

		allKnown := true
		for _, f := range firings {
			if !f.Metadata.KnownIssue {
				allKnown = false
				break
			}
		}

		if allKnown {
			// if all alert firings for this alert rule are known issues
			// we mark the test case as skipped
			numSkipped++
			tc.SkipMessage = &junit.SkipMessage{
				Message: buildSkipMessage(firings),
			}
		} else {
			// if not all alert firings for this alert rule are known issues
			// we mark the test case as failed and
			// * report the unknown firings as failures
			// * report the known firings as system output
			numFailed++
			var unknown, known []alert
			for _, f := range firings {
				if f.Metadata.KnownIssue {
					known = append(known, f)
				} else {
					unknown = append(unknown, f)
				}
			}
			tc.FailureOutput = &junit.FailureOutput{
				Message: buildFailureMessage(firings),
				Output:  renderFirings("", unknown),
			}
			if len(known) > 0 {
				tc.SystemOut = renderFirings("Known firings (not counted as failures):", known)
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

func computeGroupDuration(firings []alert, tw timing.TimeWindow) float64 {
	var total float64
	for _, f := range firings {
		if f.Alert.StartsAt == nil {
			continue
		}
		end := tw.End
		if f.Alert.EndsAt != nil {
			end = *f.Alert.EndsAt
		}
		d := end.Sub(*f.Alert.StartsAt).Seconds()
		if d > 0 {
			total += d
		}
	}
	return total
}

func buildSkipMessage(firings []alert) string {
	reasons := make(map[string]bool)
	var ordered []string
	for _, f := range firings {
		r := f.Metadata.KnownIssueReason
		if r != "" && !reasons[r] {
			reasons[r] = true
			ordered = append(ordered, r)
		}
	}
	return "known issue: " + strings.Join(ordered, "; ")
}

func buildFailureMessage(firings []alert) string {
	var unknown, known int
	for _, f := range firings {
		if f.Metadata.KnownIssue {
			known++
		} else {
			unknown++
		}
	}
	total := len(firings)
	if known == 0 {
		return fmt.Sprintf("alert fired %d time(s)", total)
	}
	return fmt.Sprintf("alert fired %d time(s) (%d unknown, %d known)", total, unknown, known)
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
