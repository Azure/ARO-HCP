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

package timing

import (
	"compress/gzip"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/go-logr/logr/testr"

	clocktesting "k8s.io/utils/clock/testing"

	"sigs.k8s.io/yaml"

	"github.com/Azure/ARO-HCP/tooling/templatize/pkg/pipeline"
)

func ctxWithLogger(t *testing.T) context.Context {
	t.Helper()
	return logr.NewContext(context.Background(), testr.New(t))
}

func writeYAML(t *testing.T, path string, v any) {
	t.Helper()
	data, err := yaml.Marshal(v)
	if err != nil {
		t.Fatalf("marshal yaml: %v", err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatalf("write file %s: %v", path, err)
	}
}

func writeGzipYAML(t *testing.T, path string, v any) {
	t.Helper()
	data, err := yaml.Marshal(v)
	if err != nil {
		t.Fatalf("marshal yaml: %v", err)
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create file %s: %v", path, err)
	}
	defer f.Close()
	gw := gzip.NewWriter(f)
	if _, err := gw.Write(data); err != nil {
		t.Fatalf("gzip write: %v", err)
	}
	if err := gw.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
}

func mustParseTime(t *testing.T, s string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("parse time %q: %v", s, err)
	}
	return parsed
}

func TestLoadSteps(t *testing.T) {
	t.Parallel()
	sampleSteps := []pipeline.NodeInfo{
		{
			Info: pipeline.ExecutionInfo{
				StartedAt:  "2025-06-01T10:00:00Z",
				FinishedAt: "2025-06-01T10:30:00Z",
				State:      "completed",
			},
		},
		{
			Info: pipeline.ExecutionInfo{
				StartedAt:  "2025-06-01T10:30:00Z",
				FinishedAt: "2025-06-01T11:00:00Z",
				State:      "completed",
			},
		},
	}

	tests := []struct {
		name      string
		setup     func(t *testing.T, dir string)
		wantNil   bool
		wantErr   bool
		wantCount int
	}{
		{
			name:    "empty dir returns nil, nil",
			setup:   func(t *testing.T, dir string) {},
			wantNil: true,
		},
		{
			name: "dir with steps.yaml returns parsed steps",
			setup: func(t *testing.T, dir string) {
				writeYAML(t, filepath.Join(dir, "steps.yaml"), sampleSteps)
			},
			wantCount: 2,
		},
		{
			name: "dir with steps.yaml.gz returns parsed steps",
			setup: func(t *testing.T, dir string) {
				writeGzipYAML(t, filepath.Join(dir, "steps.yaml.gz"), sampleSteps)
			},
			wantCount: 2,
		},
		{
			name: "dir with neither file returns nil, nil",
			setup: func(t *testing.T, dir string) {
				// Write an unrelated file so the dir is not empty
				if err := os.WriteFile(filepath.Join(dir, "other.txt"), []byte("hello"), 0644); err != nil {
					t.Fatal(err)
				}
			},
			wantNil: true,
		},
		{
			name: "invalid YAML returns error",
			setup: func(t *testing.T, dir string) {
				if err := os.WriteFile(filepath.Join(dir, "steps.yaml"), []byte("{{invalid yaml"), 0644); err != nil {
					t.Fatal(err)
				}
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			tt.setup(t, dir)

			ctx := ctxWithLogger(t)
			got, err := LoadSteps(ctx, dir)

			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.wantNil {
				if got != nil {
					t.Fatalf("expected nil, got %v", got)
				}
				return
			}
			if len(got) != tt.wantCount {
				t.Fatalf("expected %d steps, got %d", tt.wantCount, len(got))
			}
		})
	}
}

func TestLoadSteps_EmptyDir(t *testing.T) {
	ctx := ctxWithLogger(t)
	got, err := LoadSteps(ctx, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil for empty dir string, got %v", got)
	}
}

func TestLoadTestTimingInfo(t *testing.T) {
	t.Parallel()
	makeTiming := func(id []string, start, finish string, rgs map[string]map[string][]Operation) SpecTimingMetadata {
		return SpecTimingMetadata{
			Identifier:  id,
			StartedAt:   start,
			FinishedAt:  finish,
			Deployments: rgs,
		}
	}

	tests := []struct {
		name      string
		setup     func(t *testing.T, dir string)
		wantNil   bool
		wantErr   bool
		wantKeys  []string
		wantCount int
	}{
		{
			name:    "empty dir returns empty map",
			setup:   func(t *testing.T, dir string) {},
			wantNil: false,
		},
		{
			name: "dir with timing-metadata yaml files returns parsed info",
			setup: func(t *testing.T, dir string) {
				tm := makeTiming(
					[]string{"suite", "test1"},
					"2025-06-01T10:00:00Z",
					"2025-06-01T11:00:00Z",
					map[string]map[string][]Operation{
						"rg-one": {},
					},
				)
				writeYAML(t, filepath.Join(dir, "timing-metadata-test1.yaml"), tm)
			},
			wantKeys:  []string{"suite test1"},
			wantCount: 1,
		},
		{
			name: "dir with gz files works",
			setup: func(t *testing.T, dir string) {
				tm := makeTiming(
					[]string{"suite", "test2"},
					"2025-06-01T12:00:00Z",
					"2025-06-01T13:00:00Z",
					nil,
				)
				writeGzipYAML(t, filepath.Join(dir, "timing-metadata-test2.yaml.gz"), tm)
			},
			wantKeys:  []string{"suite test2"},
			wantCount: 1,
		},
		{
			name: "files without timing-metadata prefix are skipped",
			setup: func(t *testing.T, dir string) {
				tm := makeTiming(
					[]string{"suite", "skipped"},
					"2025-06-01T10:00:00Z",
					"2025-06-01T11:00:00Z",
					nil,
				)
				// This file should be skipped
				writeYAML(t, filepath.Join(dir, "other-data.yaml"), tm)
				// This file should be picked up
				tm2 := makeTiming(
					[]string{"suite", "picked"},
					"2025-06-01T10:00:00Z",
					"2025-06-01T11:00:00Z",
					nil,
				)
				writeYAML(t, filepath.Join(dir, "timing-metadata-picked.yaml"), tm2)
			},
			wantKeys:  []string{"suite picked"},
			wantCount: 1,
		},
		{
			name: "invalid timestamp returns error",
			setup: func(t *testing.T, dir string) {
				tm := makeTiming(
					[]string{"bad"},
					"not-a-timestamp",
					"2025-06-01T11:00:00Z",
					nil,
				)
				writeYAML(t, filepath.Join(dir, "timing-metadata-bad.yaml"), tm)
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			tt.setup(t, dir)

			ctx := ctxWithLogger(t)
			got, err := LoadTestTimingInfo(ctx, dir)

			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.wantNil {
				if got != nil {
					t.Fatalf("expected nil, got %v", got)
				}
				return
			}
			if len(got) != tt.wantCount {
				t.Fatalf("expected %d entries, got %d", tt.wantCount, len(got))
			}
			for _, key := range tt.wantKeys {
				if _, ok := got[key]; !ok {
					t.Errorf("expected key %q not found in result", key)
				}
			}
		})
	}
}

func TestLoadTestTimingInfo_EmptyDir(t *testing.T) {
	ctx := ctxWithLogger(t)
	got, err := LoadTestTimingInfo(ctx, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil for empty dir string, got %v", got)
	}
}

func TestLoadTestTimingInfo_ResourceGroupExtraction(t *testing.T) {
	dir := t.TempDir()
	tm := SpecTimingMetadata{
		Identifier: []string{"rg-test"},
		StartedAt:  "2025-06-01T10:00:00Z",
		FinishedAt: "2025-06-01T11:00:00Z",
		Deployments: map[string]map[string][]Operation{
			"rg-alpha": {},
			"rg-beta":  {},
			"":         {}, // empty key should be skipped
		},
	}
	writeYAML(t, filepath.Join(dir, "timing-metadata-rg.yaml"), tm)

	ctx := ctxWithLogger(t)
	got, err := LoadTestTimingInfo(ctx, dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	info := got["rg-test"]
	if len(info.ResourceGroupNames) != 2 {
		t.Fatalf("expected 2 resource group names, got %d: %v", len(info.ResourceGroupNames), info.ResourceGroupNames)
	}
	rgSet := map[string]bool{}
	for _, rg := range info.ResourceGroupNames {
		rgSet[rg] = true
	}
	if !rgSet["rg-alpha"] || !rgSet["rg-beta"] {
		t.Errorf("unexpected resource groups: %v", info.ResourceGroupNames)
	}
}

func TestLoadTestTimingInfo_EndTimeIncludesGracePeriod(t *testing.T) {
	dir := t.TempDir()
	finishedAt := "2025-06-01T11:00:00Z"
	tm := SpecTimingMetadata{
		Identifier: []string{"grace"},
		StartedAt:  "2025-06-01T10:00:00Z",
		FinishedAt: finishedAt,
	}
	writeYAML(t, filepath.Join(dir, "timing-metadata-grace.yaml"), tm)

	ctx := ctxWithLogger(t)
	got, err := LoadTestTimingInfo(ctx, dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	info := got["grace"]
	expectedEnd := mustParseTime(t, finishedAt).Add(endGracePeriodDuration)
	if !info.EndTime.Equal(expectedEnd) {
		t.Errorf("expected end time %v, got %v", expectedEnd, info.EndTime)
	}
}

func TestComputeTimeWindow(t *testing.T) {
	t.Parallel()
	refTime := mustParseTime(t, "2025-06-01T12:00:00Z")
	fakeClock := clocktesting.NewFakePassiveClock(refTime)

	step := func(start, finish string) pipeline.NodeInfo {
		return pipeline.NodeInfo{
			Info: pipeline.ExecutionInfo{
				StartedAt:  start,
				FinishedAt: finish,
			},
		}
	}

	timingInfoEntry := func(start, end time.Time) TimingInfo {
		return TimingInfo{
			StartTime: start,
			EndTime:   end,
		}
	}

	timePtr := func(t time.Time) *time.Time {
		return &t
	}

	tests := []struct {
		name           string
		steps          []pipeline.NodeInfo
		testTimingInfo map[string]TimingInfo
		startOverride  *time.Time
		wantStart      time.Time
		wantEnd        time.Time
		wantErr        bool
	}{
		{
			name: "steps only: earliest start, latest end + grace",
			steps: []pipeline.NodeInfo{
				step("2025-06-01T10:00:00Z", "2025-06-01T10:30:00Z"),
				step("2025-06-01T09:50:00Z", "2025-06-01T11:00:00Z"),
			},
			wantStart: mustParseTime(t, "2025-06-01T09:50:00Z"),
			wantEnd:   mustParseTime(t, "2025-06-01T11:00:00Z").Add(endGracePeriodDuration),
		},
		{
			name: "start fallback is ignored when steps provide start",
			steps: []pipeline.NodeInfo{
				step("2025-06-01T10:00:00Z", "2025-06-01T11:00:00Z"),
			},
			startOverride: timePtr(mustParseTime(t, "2025-06-01T08:00:00Z")),
			wantStart:     mustParseTime(t, "2025-06-01T10:00:00Z"),
			wantEnd:       mustParseTime(t, "2025-06-01T11:00:00Z").Add(endGracePeriodDuration),
		},
		{
			name: "test timing info as fallback when no steps",
			testTimingInfo: map[string]TimingInfo{
				"test-a": timingInfoEntry(
					mustParseTime(t, "2025-06-01T09:00:00Z"),
					mustParseTime(t, "2025-06-01T10:30:00Z"),
				),
			},
			wantStart: mustParseTime(t, "2025-06-01T09:00:00Z"),
			wantEnd:   mustParseTime(t, "2025-06-01T10:30:00Z"),
		},
		{
			name:    "no timing data at all returns error",
			wantErr: true,
		},
		{
			name: "start fallback ignored when steps exist, steps used for both start and end",
			steps: []pipeline.NodeInfo{
				step("2025-06-01T10:00:00Z", "2025-06-01T11:00:00Z"),
			},
			startOverride: timePtr(mustParseTime(t, "2025-06-01T07:00:00Z")),
			wantStart:     mustParseTime(t, "2025-06-01T10:00:00Z"),
			wantEnd:       mustParseTime(t, "2025-06-01T11:00:00Z").Add(endGracePeriodDuration),
		},
		{
			name: "multiple steps: finds true earliest and latest",
			steps: []pipeline.NodeInfo{
				step("2025-06-01T10:00:00Z", "2025-06-01T10:30:00Z"),
				step("2025-06-01T09:00:00Z", "2025-06-01T09:30:00Z"),
				step("2025-06-01T11:00:00Z", "2025-06-01T12:00:00Z"),
			},
			wantStart: mustParseTime(t, "2025-06-01T09:00:00Z"),
			wantEnd:   mustParseTime(t, "2025-06-01T12:00:00Z").Add(endGracePeriodDuration),
		},
		{
			name: "clock fallback for end when no finish times available",
			steps: []pipeline.NodeInfo{
				{
					Info: pipeline.ExecutionInfo{
						StartedAt:  "2025-06-01T10:00:00Z",
						FinishedAt: "",
					},
				},
			},
			wantStart: mustParseTime(t, "2025-06-01T10:00:00Z"),
			wantEnd:   refTime.Add(endGracePeriodDuration),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := ctxWithLogger(t)
			got, err := ComputeTimeWindow(ctx, fakeClock, tt.steps, tt.testTimingInfo, tt.startOverride)

			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !got.Start.Equal(tt.wantStart) {
				t.Errorf("start: got %v, want %v", got.Start, tt.wantStart)
			}
			if !got.End.Equal(tt.wantEnd) {
				t.Errorf("end: got %v, want %v", got.End, tt.wantEnd)
			}
		})
	}
}

func TestComputeTimeWindow_TestTimingNotUsedWhenStepsProvide(t *testing.T) {
	// When steps provide both start and end, test timing info should NOT
	// override them (it is only a fallback).
	ctx := ctxWithLogger(t)
	fakeClock := clocktesting.NewFakePassiveClock(time.Now())

	steps := []pipeline.NodeInfo{
		{
			Info: pipeline.ExecutionInfo{
				StartedAt:  "2025-06-01T10:00:00Z",
				FinishedAt: "2025-06-01T11:00:00Z",
			},
		},
	}
	testTimingInfo := map[string]TimingInfo{
		"should-not-use": {
			StartTime: mustParseTime(t, "2025-06-01T08:00:00Z"),
			EndTime:   mustParseTime(t, "2025-06-01T14:00:00Z"),
		},
	}

	got, err := ComputeTimeWindow(ctx, fakeClock, steps, testTimingInfo, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	wantStart := mustParseTime(t, "2025-06-01T10:00:00Z")
	wantEnd := mustParseTime(t, "2025-06-01T11:00:00Z").Add(endGracePeriodDuration)
	if !got.Start.Equal(wantStart) {
		t.Errorf("start: got %v, want %v (test timing should not override steps)", got.Start, wantStart)
	}
	if !got.End.Equal(wantEnd) {
		t.Errorf("end: got %v, want %v (test timing should not override steps)", got.End, wantEnd)
	}
}

func TestDeriveSetupTestBoundary(t *testing.T) {
	tests := []struct {
		name            string
		steps           []StepTimingMetadata
		wantSetupFinish string // RFC3339 or "" for zero
		wantTestStart   string
	}{
		{
			name:            "no steps",
			steps:           nil,
			wantSetupFinish: "",
			wantTestStart:   "",
		},
		{
			name: "only identity container steps",
			steps: []StepTimingMetadata{
				{Name: "Assign 2 identity containers", StartedAt: "2025-06-01T10:00:00Z", FinishedAt: "2025-06-01T10:01:00Z"},
				{Name: "Lease identity container", StartedAt: "2025-06-01T10:01:00Z", FinishedAt: "2025-06-01T10:02:00Z"},
			},
			wantSetupFinish: "2025-06-01T10:02:00Z",
			wantTestStart:   "",
		},
		{
			name: "only test steps",
			steps: []StepTimingMetadata{
				{Name: "Create cluster", StartedAt: "2025-06-01T10:05:00Z", FinishedAt: "2025-06-01T10:30:00Z"},
			},
			wantSetupFinish: "",
			wantTestStart:   "2025-06-01T10:05:00Z",
		},
		{
			name: "mixed setup and test steps",
			steps: []StepTimingMetadata{
				{Name: "Assign 2 identity containers", StartedAt: "2025-06-01T10:00:00Z", FinishedAt: "2025-06-01T10:01:00Z"},
				{Name: "Lease identity container", StartedAt: "2025-06-01T10:01:00Z", FinishedAt: "2025-06-01T10:03:00Z"},
				{Name: "Create cluster", StartedAt: "2025-06-01T10:03:00Z", FinishedAt: "2025-06-01T10:30:00Z"},
				{Name: "Verify cluster health", StartedAt: "2025-06-01T10:30:00Z", FinishedAt: "2025-06-01T10:35:00Z"},
			},
			wantSetupFinish: "2025-06-01T10:03:00Z",
			wantTestStart:   "2025-06-01T10:03:00Z",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotSetup, gotTest := deriveSetupTestBoundary(tt.steps)

			if tt.wantSetupFinish == "" {
				if !gotSetup.IsZero() {
					t.Errorf("setupFinishTime: got %v, want zero", gotSetup)
				}
			} else {
				want := mustParseTime(t, tt.wantSetupFinish)
				if !gotSetup.Equal(want) {
					t.Errorf("setupFinishTime: got %v, want %v", gotSetup, want)
				}
			}

			if tt.wantTestStart == "" {
				if !gotTest.IsZero() {
					t.Errorf("testStartTime: got %v, want zero", gotTest)
				}
			} else {
				want := mustParseTime(t, tt.wantTestStart)
				if !gotTest.Equal(want) {
					t.Errorf("testStartTime: got %v, want %v", gotTest, want)
				}
			}
		})
	}
}

func TestLoadTestTimingInfo_SetupTestBoundary(t *testing.T) {
	dir := t.TempDir()
	tm := SpecTimingMetadata{
		Identifier: []string{"boundary-test"},
		StartedAt:  "2025-06-01T10:00:00Z",
		FinishedAt: "2025-06-01T11:00:00Z",
		Steps: []StepTimingMetadata{
			{Name: "Assign 2 identity containers", StartedAt: "2025-06-01T10:00:00Z", FinishedAt: "2025-06-01T10:02:00Z"},
			{Name: "Create cluster", StartedAt: "2025-06-01T10:02:00Z", FinishedAt: "2025-06-01T10:50:00Z"},
		},
	}
	writeYAML(t, filepath.Join(dir, "timing-metadata-boundary.yaml"), tm)

	ctx := ctxWithLogger(t)
	got, err := LoadTestTimingInfo(ctx, dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	info := got["boundary-test"]

	wantSetupFinish := mustParseTime(t, "2025-06-01T10:02:00Z")
	if !info.SetupFinishTime.Equal(wantSetupFinish) {
		t.Errorf("SetupFinishTime: got %v, want %v", info.SetupFinishTime, wantSetupFinish)
	}

	wantTestStart := mustParseTime(t, "2025-06-01T10:02:00Z")
	if !info.TestStartTime.Equal(wantTestStart) {
		t.Errorf("TestStartTime: got %v, want %v", info.TestStartTime, wantTestStart)
	}

	wantCleanup := mustParseTime(t, "2025-06-01T11:00:00Z")
	if !info.CleanupStartTime.Equal(wantCleanup) {
		t.Errorf("CleanupStartTime: got %v, want %v", info.CleanupStartTime, wantCleanup)
	}
}
