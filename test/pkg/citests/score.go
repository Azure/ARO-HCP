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

package citests

import (
	"fmt"
	"math"
	"sort"
	"time"
)

// priorityBands maps a rounded score in minutes to its proposed start priority.
// The order is significant: the first band whose minimum the score reaches wins.
var priorityBands = []struct {
	minMinutes float64
	priority   int
}{
	{50, 90},
	{45, 80},
	{40, 70},
	{35, 60},
	{30, 50},
	{25, 40},
	{20, 30},
	{15, 20},
}

// PriorityForScore maps a rounded score in minutes to a start priority.
func PriorityForScore(minutes float64) int {
	for _, band := range priorityBands {
		if minutes >= band.minMinutes {
			return band.priority
		}
	}
	return 0
}

// ScorePriorities derives priority proposals from a validated bundle. It makes no
// service calls: every normalization input is filtered for eligibility first, so
// skipped, failed, unprocessed and zero-timing records never act as zero durations.
func ScorePriorities(bundle Bundle, options PriorityOptions) (PriorityReport, error) {
	if err := bundle.Validate(); err != nil {
		return PriorityReport{}, err
	}
	minSamples := options.MinSamples
	if minSamples == 0 {
		minSamples = DefaultMinSamples
	}
	if minSamples < 1 {
		return PriorityReport{}, fmt.Errorf("min_samples must be at least 1")
	}

	report := PriorityReport{
		SchemaVersion: ReportSchemaVersion,
		Algorithm:     AlgorithmEnvironmentNormalizedMean,
		Selection:     bundle.Selection,
		MinSamples:    minSamples,
		Environments:  []EnvironmentSummary{},
		Quality:       newQuality(),
		Tests:         []TestReport{},
	}

	runs := make(map[string]Run, len(bundle.Runs))
	for _, run := range bundle.Runs {
		runs[run.BuildID] = run
	}
	report.Quality.SelectedBuildCount = len(bundle.Runs)
	for _, run := range bundle.Runs {
		if run.Processed {
			report.Quality.ProcessedBuildCount++
		}
		if eligibleRun(run) {
			report.Quality.GreenBuildCount++
		}
	}

	// A test ID may appear with several executions in one build; those retries
	// and re-ingestions are never independent samples.
	executions := make(map[string]map[string]*execution, len(bundle.Observations))
	testOrder := []string{}
	testSeen := map[string]struct{}{}
	for _, observation := range bundle.Observations {
		if _, ok := testSeen[observation.TestID]; !ok {
			testSeen[observation.TestID] = struct{}{}
			testOrder = append(testOrder, observation.TestID)
		}
		if executions[observation.BuildID] == nil {
			executions[observation.BuildID] = map[string]*execution{}
		}
		current, ok := executions[observation.BuildID][observation.TestID]
		if !ok {
			current = &execution{}
			executions[observation.BuildID][observation.TestID] = current
		}
		current.rows = append(current.rows, observation)
	}

	names := testNames(testOrder, executions)

	environmentSamples := map[string][]float64{}
	environmentBuilds := map[string]map[string]struct{}{}
	testEnvironmentSamples := map[string]map[string][]float64{}
	testEnvironmentBuilds := map[string]map[string]map[string]struct{}{}
	eligibleSamples := map[string]int{}
	for _, test := range testOrder {
		testEnvironmentSamples[test] = map[string][]float64{}
		testEnvironmentBuilds[test] = map[string]map[string]struct{}{}
	}

	for _, buildID := range sortedKeys(executions) {
		for _, test := range sortedKeys(executions[buildID]) {
			current := executions[buildID][test]
			if len(current.rows) > 1 {
				report.Quality.Exclusions[ExclusionAmbiguousExecution] += len(current.rows)
				continue
			}
			observation := current.rows[0]
			run := runs[observation.BuildID]
			if reason := exclusionReason(run, observation); reason != "" {
				report.Quality.Exclusions[reason]++
				continue
			}
			minutes := observation.FinishedAt.Sub(observation.StartedAt).Minutes()
			environment := run.Environment
			environmentSamples[environment] = append(environmentSamples[environment], minutes)
			if environmentBuilds[environment] == nil {
				environmentBuilds[environment] = map[string]struct{}{}
			}
			environmentBuilds[environment][buildID] = struct{}{}
			if testEnvironmentBuilds[test][environment] == nil {
				testEnvironmentBuilds[test][environment] = map[string]struct{}{}
			}
			testEnvironmentSamples[test][environment] = append(testEnvironmentSamples[test][environment], minutes)
			testEnvironmentBuilds[test][environment][buildID] = struct{}{}
			eligibleSamples[test]++
		}
	}

	if len(environmentSamples) == 0 || totalSamples(environmentSamples) == 0 {
		report.Quality.Reason = QualityReasonNoEligibleSamples
		report.Tests = insufficientRows(testOrder, names)
		return report, nil
	}

	environmentMedians := map[string]float64{}
	pooled := []float64{}
	for _, environment := range ScoringEnvironments {
		samples := environmentSamples[environment]
		if len(samples) == 0 {
			continue
		}
		median := exactMedian(samples)
		if !positiveFinite(median) {
			return PriorityReport{}, fmt.Errorf("environment %s median must be a positive finite number, got %v", environment, median)
		}
		environmentMedians[environment] = median
		pooled = append(pooled, samples...)
		report.Environments = append(report.Environments, EnvironmentSummary{
			Environment:              environment,
			EligibleBuildCount:       len(environmentBuilds[environment]),
			EligibleSampleCount:      len(samples),
			MedianMinutes:            median,
			LatestEligibleCompletion: latestCompletion(runs, environmentBuilds[environment]),
		})
	}
	if len(pooled) == 0 {
		report.Quality.Reason = QualityReasonNoEligibleSamples
		report.Tests = insufficientRows(testOrder, names)
		return report, nil
	}
	globalMedian := exactMedian(pooled)
	if !positiveFinite(globalMedian) {
		return PriorityReport{}, fmt.Errorf("global median must be a positive finite number, got %v", globalMedian)
	}
	report.GlobalMedianMinutes = &globalMedian

	for _, test := range testOrder {
		row := TestReport{
			TestID:       test,
			Name:         names[test].name,
			Status:       StatusInsufficientData,
			Environments: []TestEnvironmentStats{},
		}
		if !names[test].usable {
			// A proposal needs a label the source can be matched against, so an
			// unnamed test keeps its current priority even when it has samples.
			report.Quality.Exclusions[ExclusionUnusableName] += eligibleSamples[test]
		}
		bestRatio := math.Inf(-1)
		for _, environment := range ScoringEnvironments {
			samples := testEnvironmentSamples[test][environment]
			if len(samples) == 0 {
				continue
			}
			stats := TestEnvironmentStats{
				Environment:         environment,
				EligibleBuildCount:  len(testEnvironmentBuilds[test][environment]),
				EligibleSampleCount: len(samples),
				MeanMinutes:         arithmeticMean(samples),
				MedianMinutes:       environmentMedians[environment],
			}
			if stats.EligibleBuildCount >= minSamples {
				ratio := stats.MeanMinutes / stats.MedianMinutes
				if !positiveFinite(ratio) {
					return PriorityReport{}, fmt.Errorf("test %q environment %s ratio must be a positive finite number, got %v", row.Name, environment, ratio)
				}
				stats.Ratio = &ratio
				stats.Sufficient = true
				if ratio > bestRatio {
					bestRatio = ratio
				}
			}
			row.Environments = append(row.Environments, stats)
		}
		if names[test].usable && !math.IsInf(bestRatio, -1) {
			score := quantize(bestRatio * globalMedian)
			priority := PriorityForScore(score)
			row.Status = StatusScored
			row.ScoreMinutes = &score
			row.ProposedPriority = &priority
		}
		report.Tests = append(report.Tests, row)
	}
	sortTests(report.Tests)
	return report, nil
}

// quantize applies the one-decimal policy with an explicit half-step tie rule.
func quantize(minutes float64) float64 {
	return math.RoundToEven(minutes*10) / 10
}

// newQuality initializes every exclusion reason so consumers never distinguish
// "absent" from "zero".
func newQuality() Quality {
	return Quality{Exclusions: map[string]int{
		ExclusionAmbiguousExecution: 0,
		ExclusionRunNotEligible:     0,
		ExclusionUnknownEnvironment: 0,
		ExclusionNotPassed:          0,
		ExclusionNonE2ESource:       0,
		ExclusionUnusableName:       0,
		ExclusionInvalidTiming:      0,
	}}
}

// eligibleRun reports whether a run is a successful, processed CI job run.
func eligibleRun(run Run) bool {
	return run.OverallResult == "SUCCESS" && !run.Failed && run.Processed
}

// exclusionReason returns the first reason an observation cannot be scored, in
// the documented evaluation order, or the empty string when it is eligible.
func exclusionReason(run Run, observation Observation) string {
	switch {
	case !eligibleRun(run):
		return ExclusionRunNotEligible
	case run.Environment == "" || run.Environment == EnvironmentUnknown:
		return ExclusionUnknownEnvironment
	case observation.Result != "passed" || observation.Failed:
		return ExclusionNotPassed
	case observation.Source != "e2e":
		return ExclusionNonE2ESource
	case observation.StartedAt.IsZero() || observation.FinishedAt.IsZero() ||
		!observation.FinishedAt.After(observation.StartedAt):
		return ExclusionInvalidTiming
	}
	return ""
}

// testNames resolves one usable name per test ID. A name that only some rows
// carry is still the test's name: the dimension can become visible between
// batch queries, and an absent name never invalidates a recorded duration.
// Conflicting names, or a dimension that reports several names for one ID,
// leave the test nameless so it can never be proposed against a guessed label.
func testNames(testOrder []string, executions map[string]map[string]*execution) map[string]testName {
	names := make(map[string]testName, len(testOrder))
	for _, test := range testOrder {
		distinct := map[string]struct{}{}
		ambiguous := false
		for _, buildID := range sortedKeys(executions) {
			current, ok := executions[buildID][test]
			if !ok {
				continue
			}
			for _, row := range current.rows {
				if row.NameVariants > 1 {
					ambiguous = true
				}
				if row.Name != "" {
					distinct[row.Name] = struct{}{}
				}
			}
		}
		resolved := testName{}
		if len(distinct) == 1 && !ambiguous {
			for name := range distinct {
				resolved = testName{name: name, usable: true}
			}
		}
		names[test] = resolved
	}
	return names
}

// insufficientRows reports every observed test as insufficient, preserving the
// evidence that the population was observed but unusable.
func insufficientRows(testOrder []string, names map[string]testName) []TestReport {
	rows := make([]TestReport, 0, len(testOrder))
	for _, test := range testOrder {
		rows = append(rows, TestReport{
			TestID:       test,
			Name:         names[test].name,
			Status:       StatusInsufficientData,
			Environments: []TestEnvironmentStats{},
		})
	}
	sortTests(rows)
	return rows
}

// exactMedian computes the median over the complete sample set, averaging the
// two middle values for even counts.
func exactMedian(values []float64) float64 {
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	middle := len(sorted) / 2
	if len(sorted)%2 == 1 {
		return sorted[middle]
	}
	return (sorted[middle-1] + sorted[middle]) / 2
}

// arithmeticMean returns the plain mean of the complete sample set.
func arithmeticMean(values []float64) float64 {
	sum := 0.0
	for _, value := range values {
		sum += value
	}
	return sum / float64(len(values))
}

// positiveFinite rejects denominators and scores that cannot be normalized.
func positiveFinite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value > 0
}

// latestCompletion reports the latest qualified run completion of an environment.
func latestCompletion(runs map[string]Run, builds map[string]struct{}) *time.Time {
	var latest *time.Time
	for buildID := range builds {
		finished := runs[buildID].FinishedAt
		if latest == nil || finished.After(*latest) {
			value := finished
			latest = &value
		}
	}
	return latest
}

// totalSamples counts the pooled eligible sample set.
func totalSamples(environmentSamples map[string][]float64) int {
	total := 0
	for _, samples := range environmentSamples {
		total += len(samples)
	}
	return total
}

// sortTests orders scored rows by descending score then name, with insufficient
// rows last in name order.
func sortTests(rows []TestReport) {
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if (a.Status == StatusScored) != (b.Status == StatusScored) {
			return a.Status == StatusScored
		}
		if a.Status == StatusScored && b.Status == StatusScored && *a.ScoreMinutes != *b.ScoreMinutes {
			return *a.ScoreMinutes > *b.ScoreMinutes
		}
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		return a.TestID < b.TestID
	})
}

// sortedKeys returns map keys in sorted order.
func sortedKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// testName is the resolved label of one test ID and whether it may be proposed.
type testName struct {
	name   string
	usable bool
}

// execution groups every recorded row of one test in one build.
type execution struct {
	rows []Observation
}
