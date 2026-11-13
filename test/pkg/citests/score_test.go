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
	"context"
	"fmt"
	"math"
	"testing"
	"time"
)

const (
	slowTestID      = "c2e0ad79a6e827c16c93bd94be7622bf0915c3cb1308a17dc1e706a9204e1dce"
	middleTestID    = "0dfed7bb3a98610cebbcd455da3c95946988320c115a7696038e62b09c79868c"
	fastTestID      = "c8d689885defdce848030d59403fa5dd2313ccf6cfce52d7a37294b8e930e2b9"
	skippedTestID   = "e0a5ec2c2fbf0b77c7cda5769e2f71ce418a7858c9d7ed7d4115aeeb2b738af0"
	slowTestName    = "[sig-aro] should take the longest to finish"
	middleTestName  = "[sig-aro] should take a medium amount of time"
	fastTestName    = "[sig-aro] should take the shortest time"
	skippedTestName = "[sig-aro] should be skipped everywhere"
)

func TestScorePrioritiesSyntheticPopulation(t *testing.T) {
	report, err := ScorePriorities(loadFixture(t), PriorityOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if report.SchemaVersion != ReportSchemaVersion || report.Algorithm != AlgorithmEnvironmentNormalizedMean || report.MinSamples != DefaultMinSamples {
		t.Fatalf("unexpected report header: %+v", report)
	}
	if report.GlobalMedianMinutes == nil || *report.GlobalMedianMinutes != 30 {
		t.Fatalf("global median = %v, want 30", report.GlobalMedianMinutes)
	}
	if len(report.Environments) != 2 {
		t.Fatalf("environments = %+v", report.Environments)
	}
	for _, environment := range report.Environments {
		wantMedian := map[string]float64{EnvironmentDev: 20, EnvironmentInt: 40}[environment.Environment]
		if environment.MedianMinutes != wantMedian {
			t.Errorf("%s median = %v, want %v", environment.Environment, environment.MedianMinutes, wantMedian)
		}
		if environment.EligibleBuildCount != 3 || environment.EligibleSampleCount != 9 {
			t.Errorf("%s counts = %d builds/%d samples, want 3/9", environment.Environment, environment.EligibleBuildCount, environment.EligibleSampleCount)
		}
		if environment.LatestEligibleCompletion == nil || !environment.LatestEligibleCompletion.Equal(time.Date(2026, 1, 4, 3, 0, 0, 0, time.UTC)) {
			t.Errorf("%s latest completion = %v", environment.Environment, environment.LatestEligibleCompletion)
		}
	}
	if report.Quality.SelectedBuildCount != 8 || report.Quality.ProcessedBuildCount != 8 || report.Quality.GreenBuildCount != 8 {
		t.Fatalf("quality counts = %+v", report.Quality)
	}
	if report.Quality.Exclusions[ExclusionNotPassed] != 2 {
		t.Fatalf("skipped rows were not counted: %+v", report.Quality.Exclusions)
	}
	for _, reason := range []string{ExclusionAmbiguousExecution, ExclusionRunNotEligible, ExclusionUnknownEnvironment, ExclusionNonE2ESource, ExclusionUnusableName, ExclusionInvalidTiming} {
		if report.Quality.Exclusions[reason] != 0 {
			t.Errorf("unexpected %s exclusions: %+v", reason, report.Quality.Exclusions)
		}
	}

	want := []struct {
		name     string
		id       string
		status   string
		score    float64
		priority int
	}{
		{slowTestName, slowTestID, StatusScored, 60, 90},
		{middleTestName, middleTestID, StatusScored, 30, 50},
		{fastTestName, fastTestID, StatusScored, 15, 20},
		{skippedTestName, skippedTestID, StatusInsufficientData, 0, 0},
	}
	if len(report.Tests) != len(want) {
		t.Fatalf("tests = %+v", report.Tests)
	}
	for i, expected := range want {
		row := report.Tests[i]
		if row.Name != expected.name || row.TestID != expected.id || row.Status != expected.status {
			t.Fatalf("row %d = %+v, want %+v", i, row, expected)
		}
		if expected.status == StatusInsufficientData {
			if row.ScoreMinutes != nil || row.ProposedPriority != nil {
				t.Fatalf("skipped-only spec received a proposal: %+v", row)
			}
			if len(row.Environments) != 0 {
				t.Fatalf("skipped-only spec reported strata: %+v", row.Environments)
			}
			continue
		}
		if row.ScoreMinutes == nil || *row.ScoreMinutes != expected.score {
			t.Fatalf("%s score = %v, want %v", row.Name, row.ScoreMinutes, expected.score)
		}
		if row.ProposedPriority == nil || *row.ProposedPriority != expected.priority {
			t.Fatalf("%s priority = %v, want %v", row.Name, row.ProposedPriority, expected.priority)
		}
		if len(row.Environments) != 2 {
			t.Fatalf("%s strata = %+v", row.Name, row.Environments)
		}
		for _, stats := range row.Environments {
			if !stats.Sufficient || stats.Ratio == nil || stats.EligibleBuildCount != 3 || stats.EligibleSampleCount != 3 {
				t.Fatalf("%s %s strata = %+v", row.Name, stats.Environment, stats)
			}
			wantMean := map[string]float64{EnvironmentDev: expected.score * 20 / 30, EnvironmentInt: expected.score * 40 / 30}[stats.Environment]
			if math.Abs(stats.MeanMinutes-wantMean) > 1e-9 {
				t.Fatalf("%s %s mean = %v, want %v", row.Name, stats.Environment, stats.MeanMinutes, wantMean)
			}
		}
	}
}

func TestScorePrioritiesIgnoresSkippedRows(t *testing.T) {
	bundle := loadFixture(t)
	executed := bundle
	executed.Observations = nil
	for _, observation := range bundle.Observations {
		if observation.Result == "passed" {
			executed.Observations = append(executed.Observations, observation)
		}
	}
	if len(executed.Observations) == len(bundle.Observations) {
		t.Fatal("fixture lost its skipped rows")
	}
	withSkips, err := ScorePriorities(bundle, PriorityOptions{})
	if err != nil {
		t.Fatal(err)
	}
	withoutSkips, err := ScorePriorities(executed, PriorityOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if *withSkips.GlobalMedianMinutes != *withoutSkips.GlobalMedianMinutes {
		t.Fatalf("skips changed the global median: %v vs %v", *withSkips.GlobalMedianMinutes, *withoutSkips.GlobalMedianMinutes)
	}
	if len(withSkips.Environments) != len(withoutSkips.Environments) {
		t.Fatalf("skips changed the environment population: %+v vs %+v", withSkips.Environments, withoutSkips.Environments)
	}
	for i := range withSkips.Environments {
		got, want := withSkips.Environments[i], withoutSkips.Environments[i]
		if got.Environment != want.Environment || got.EligibleBuildCount != want.EligibleBuildCount ||
			got.EligibleSampleCount != want.EligibleSampleCount || got.MedianMinutes != want.MedianMinutes ||
			!got.LatestEligibleCompletion.Equal(*want.LatestEligibleCompletion) {
			t.Fatalf("skips changed %+v to %+v", got, want)
		}
	}
	for i := range withoutSkips.Tests {
		got, want := withSkips.Tests[i], withoutSkips.Tests[i]
		if got.TestID != want.TestID || got.Status != want.Status || *got.ScoreMinutes != *want.ScoreMinutes || *got.ProposedPriority != *want.ProposedPriority {
			t.Fatalf("skips changed a proposal: %+v vs %+v", got, want)
		}
	}
}

func TestScorePrioritiesSparseAndMissingStrata(t *testing.T) {
	bundle := syntheticBundle(t)
	report, err := ScorePriorities(bundle, PriorityOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if report.GlobalMedianMinutes == nil || *report.GlobalMedianMinutes != 20 {
		t.Fatalf("global median = %v, want 20", report.GlobalMedianMinutes)
	}
	rows := map[string]TestReport{}
	for _, row := range report.Tests {
		rows[row.Name] = row
	}
	// int alone qualifies, so a dev stratum of two builds cannot block a proposal.
	sparse := rows["sparse"]
	if sparse.Status != StatusScored || sparse.ScoreMinutes == nil || *sparse.ScoreMinutes != 26.7 || *sparse.ProposedPriority != 40 {
		t.Fatalf("sparse spec = %+v", sparse)
	}
	dev, intStats := sparse.Environments[0], sparse.Environments[1]
	if dev.Environment != EnvironmentDev || dev.Sufficient || dev.Ratio != nil || dev.EligibleBuildCount != 2 {
		t.Fatalf("sparse dev stratum = %+v", dev)
	}
	if intStats.Environment != EnvironmentInt || !intStats.Sufficient || intStats.Ratio == nil || math.Abs(*intStats.Ratio-40.0/30.0) > 1e-9 {
		t.Fatalf("sparse int stratum = %+v", intStats)
	}
	// A spec that never reaches the build threshold proposes nothing.
	thin := rows["thin"]
	if thin.Status != StatusInsufficientData || thin.ScoreMinutes != nil || thin.ProposedPriority != nil {
		t.Fatalf("thin spec = %+v", thin)
	}
	if len(thin.Environments) != 1 || thin.Environments[0].Sufficient || thin.Environments[0].Ratio != nil {
		t.Fatalf("thin strata = %+v", thin.Environments)
	}
	anchor := rows["anchor"]
	if anchor.Status != StatusScored || *anchor.ScoreMinutes != 20 || *anchor.ProposedPriority != 30 {
		t.Fatalf("anchor spec = %+v", anchor)
	}
}

func TestScorePrioritiesExclusionReasons(t *testing.T) {
	bundle := syntheticBundle(t)
	base := bundle.Runs[0]
	failure := base
	failure.BuildID = "failed"
	failure.OverallResult = "FAILURE"
	unprocessed := base
	unprocessed.BuildID = "unprocessed"
	unprocessed.Processed = false
	unknown := base
	unknown.BuildID = "unknown"
	unknown.JobName = "custom-e2e-parallel"
	unknown.Environment = EnvironmentUnknown
	bundle.Runs = append(bundle.Runs, failure, unprocessed, unknown)

	timed := func(buildID, testID, name string) Observation {
		start := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
		return Observation{BuildID: buildID, TestID: testID, Name: name, NameVariants: 1,
			Source: "e2e", Result: "passed", StartedAt: start, FinishedAt: start.Add(10 * time.Minute)}
	}
	failedObservation := timed("dev-1", "failed-result", "failed result")
	failedObservation.Result = "failed"
	failedObservation.Failed = true
	observability := timed("dev-1", "observability", "observability rows")
	observability.Source = "observability"
	ambiguous := timed("dev-1", "ambiguous", "ambiguous execution")
	ambiguousRetry := ambiguous
	ambiguousRetry.FinishedAt = ambiguousRetry.FinishedAt.Add(time.Minute)
	zeroTiming := timed("dev-1", "zero-timing", "zero timing")
	zeroTiming.StartedAt, zeroTiming.FinishedAt = time.Time{}, time.Time{}
	backwards := timed("dev-1", "backwards", "backwards timing")
	backwards.FinishedAt = backwards.StartedAt

	bundle.Observations = append(bundle.Observations,
		timed("failed", "on-failed-run", "passed on a failed run"),
		timed("unprocessed", "on-unprocessed-run", "passed on an unprocessed run"),
		timed("unknown", "on-unknown-run", "passed on an unknown environment"),
		failedObservation, observability, ambiguous, ambiguousRetry, zeroTiming, backwards,
	)
	report, err := ScorePriorities(bundle, PriorityOptions{})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]int{
		ExclusionAmbiguousExecution: 2,
		ExclusionRunNotEligible:     2,
		ExclusionUnknownEnvironment: 1,
		ExclusionNotPassed:          1,
		ExclusionNonE2ESource:       1,
		ExclusionUnusableName:       0,
		ExclusionInvalidTiming:      2,
	}
	for reason, count := range want {
		if report.Quality.Exclusions[reason] != count {
			t.Errorf("%s exclusions = %d, want %d", reason, report.Quality.Exclusions[reason], count)
		}
	}
	for _, row := range report.Tests {
		if row.Status == StatusScored && row.Name != "anchor" && row.Name != "sparse" {
			t.Fatalf("ineligible records produced a proposal: %+v", row)
		}
	}
}

func TestScorePrioritiesNoEligibleSamples(t *testing.T) {
	bundle := loadFixture(t)
	onlySkipped := bundle
	onlySkipped.Observations = nil
	for _, observation := range bundle.Observations {
		if observation.Result != "passed" {
			onlySkipped.Observations = append(onlySkipped.Observations, observation)
		}
	}
	report, err := ScorePriorities(onlySkipped, PriorityOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if report.GlobalMedianMinutes != nil || len(report.Environments) != 0 {
		t.Fatalf("empty population produced normalization input: %+v", report)
	}
	if report.Quality.Reason != QualityReasonNoEligibleSamples {
		t.Fatalf("missing quality reason: %+v", report.Quality)
	}
	if len(report.Tests) != 1 || report.Tests[0].Status != StatusInsufficientData || report.Tests[0].ProposedPriority != nil {
		t.Fatalf("observed tests are not reported as insufficient: %+v", report.Tests)
	}
}

func TestQuantizeAndPriorityBands(t *testing.T) {
	for _, tc := range []struct{ in, want float64 }{
		{26.65, 26.6}, // half rounds to the even tenth
		{26.75, 26.8},
		{14.94, 14.9},
		{49.96, 50},
	} {
		if got := quantize(tc.in); got != tc.want {
			t.Errorf("quantize(%v) = %v, want %v", tc.in, got, tc.want)
		}
	}
	for _, tc := range []struct {
		minutes  float64
		priority int
	}{
		{90, 90}, {50, 90}, {49.9, 80}, {45, 80}, {44.9, 70}, {40, 70}, {35, 60}, {30, 50},
		{25, 40}, {20, 30}, {15, 20}, {14.9, 0}, {0, 0},
	} {
		if got := PriorityForScore(tc.minutes); got != tc.priority {
			t.Errorf("PriorityForScore(%v) = %d, want %d", tc.minutes, got, tc.priority)
		}
	}
}

func TestExactMedianAveragesEvenCounts(t *testing.T) {
	for _, tc := range []struct {
		in   []float64
		want float64
	}{
		{[]float64{40, 10, 20}, 20},
		{[]float64{40, 10, 20, 50}, 30},
		{[]float64{5}, 5},
		{[]float64{30, 10}, 20},
	} {
		if got := exactMedian(tc.in); got != tc.want {
			t.Errorf("exactMedian(%v) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

// syntheticBundle builds a three-build, two-environment population with a spec
// that qualifies in int only, a spec below the threshold everywhere and an
// anchor spec that defines the medians.
func syntheticBundle(t *testing.T) Bundle {
	t.Helper()
	selection := Selection{
		Origin:    "https://example.invalid/ServiceLogs",
		From:      time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		To:        time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC),
		JobFilter: "e2e-parallel",
	}
	bundle := Bundle{SchemaVersion: BundleSchemaVersion, Selection: selection}
	finished := selection.From.Add(24 * time.Hour)
	for _, environment := range []struct{ name, job, prefix string }{
		{EnvironmentDev, "pull-ci-Azure-ARO-HCP-main-e2e-parallel", "dev"},
		{EnvironmentInt, "branch-ci-Azure-ARO-HCP-main-e2e-integration-e2e-parallel", "int"},
	} {
		for i := 1; i <= 3; i++ {
			bundle.Runs = append(bundle.Runs, Run{
				BuildID:       fmt.Sprintf("%s-%d", environment.prefix, i),
				JobName:       environment.job,
				Environment:   environment.name,
				OverallResult: "SUCCESS",
				Processed:     true,
				StartedAt:     finished.Add(-3 * time.Hour),
				FinishedAt:    finished,
			})
		}
	}
	add := func(buildID, testID, name string, minutes float64) {
		start := selection.From.Add(48 * time.Hour)
		bundle.Observations = append(bundle.Observations, Observation{
			BuildID: buildID, TestID: testID, Name: name, NameVariants: 1, Source: "e2e", Result: "passed",
			StartedAt: start, FinishedAt: start.Add(time.Duration(minutes * float64(time.Minute))),
		})
	}
	for i := 1; i <= 3; i++ {
		dev := fmt.Sprintf("dev-%d", i)
		intBuild := fmt.Sprintf("int-%d", i)
		add(dev, "anchor-id", "anchor", 10)
		add(intBuild, "anchor-id", "anchor", 20)
	}
	for i := 1; i <= 3; i++ {
		add(fmt.Sprintf("int-%d", i), "sparse-id", "sparse", 40)
	}
	for i := 1; i <= 2; i++ {
		add(fmt.Sprintf("dev-%d", i), "sparse-id", "sparse", 20)
		add(fmt.Sprintf("dev-%d", i), "thin-id", "thin", 10)
	}
	if err := bundle.Validate(); err != nil {
		t.Fatal(err)
	}
	return bundle
}

func TestCollectAndScoreLateArrivingNames(t *testing.T) {
	selection := loadFixture(t).Selection
	builds := make([]string, observationBatchSize+3)
	for index := range builds {
		builds[index] = fmt.Sprintf("%04d", index)
	}
	harness := newWireHarness(t, selection, builds, "late-names")
	bundle, err := Collect(context.Background(), harness.client, "ServiceLogs", selection, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if got := harness.batchSizes(); len(got) != 2 || got[0] != observationBatchSize || got[1] != 3 {
		t.Fatalf("observation batches = %v", got)
	}
	report, err := ScorePriorities(bundle, PriorityOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Tests) != 1 {
		t.Fatalf("tests = %+v", report.Tests)
	}
	row := report.Tests[0]
	if row.Name != slowTestName || row.Status != StatusScored {
		t.Fatalf("late name was lost: %+v", row)
	}
	if row.Environments[0].EligibleBuildCount != len(builds) || row.Environments[0].EligibleSampleCount != len(builds) {
		t.Fatalf("batches lost executions: %+v", row.Environments[0])
	}
	if report.Quality.Exclusions[ExclusionUnusableName] != 0 {
		t.Fatalf("an absent early name discarded executions: %+v", report.Quality.Exclusions)
	}
}

func TestScorePrioritiesRequiresAUsableName(t *testing.T) {
	bundle := syntheticBundle(t)
	// Two different names for one test ID make the label unusable.
	bundle.Observations[0].Name = "anchor renamed"
	report, err := ScorePriorities(bundle, PriorityOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var anchor *TestReport
	for index := range report.Tests {
		if report.Tests[index].TestID == "anchor-id" {
			anchor = &report.Tests[index]
		}
	}
	if anchor == nil {
		t.Fatalf("anchor test is missing: %+v", report.Tests)
	}
	if anchor.Name != "" || anchor.Status != StatusInsufficientData || anchor.ProposedPriority != nil {
		t.Fatalf("conflicting names produced a proposal: %+v", anchor)
	}
	if anchor.ScoreMinutes != nil {
		t.Fatalf("conflicting names produced a score: %+v", anchor)
	}
	if report.Quality.Exclusions[ExclusionUnusableName] != 6 {
		t.Fatalf("unusable-name exclusions = %d, want the six anchor executions", report.Quality.Exclusions[ExclusionUnusableName])
	}
	// The durations stay in the normalization population even though the label
	// cannot be proposed.
	if report.GlobalMedianMinutes == nil || *report.GlobalMedianMinutes != 20 {
		t.Fatalf("global median = %v, want 20", report.GlobalMedianMinutes)
	}
	for _, environment := range report.Environments {
		want := map[string]float64{EnvironmentDev: 10, EnvironmentInt: 30}[environment.Environment]
		if environment.MedianMinutes != want {
			t.Fatalf("%s median = %v, want %v", environment.Environment, environment.MedianMinutes, want)
		}
	}
	for _, row := range report.Tests {
		if row.TestID == "sparse-id" && row.Status != StatusScored {
			t.Fatalf("an unrelated test lost its proposal: %+v", row)
		}
	}
}
