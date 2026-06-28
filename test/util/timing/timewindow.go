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
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-logr/logr"

	"k8s.io/utils/clock"

	"sigs.k8s.io/yaml"

	"github.com/Azure/ARO-HCP/tooling/templatize/pkg/pipeline"
)

// endGracePeriodDuration is appended to step/test finish times when computing
// the time window so that trailing metric and alert data is captured.
const endGracePeriodDuration = 45 * time.Minute

// TimeWindow represents a start/end time range.
type TimeWindow struct {
	Start time.Time
	End   time.Time
}

// TimingInfo holds parsed start/end times and optional resource group names
// extracted from a timing metadata file.
type TimingInfo struct {
	StartTime          time.Time
	EndTime            time.Time
	SetupFinishTime    time.Time
	TestStartTime      time.Time
	CleanupStartTime   time.Time
	ResourceGroupNames []string
	Steps              []StepTimingMetadata
}

// LoadSteps reads a steps.yaml(.gz) file from the given directory.
// It returns nil (no error) when the file is not found.
func LoadSteps(ctx context.Context, dir string) ([]pipeline.NodeInfo, error) {
	if dir == "" {
		return nil, nil
	}
	logger, err := logr.FromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("logger not found in context: %w", err)
	}

	compressedPath := filepath.Join(dir, "steps.yaml.gz")
	uncompressedPath := filepath.Join(dir, "steps.yaml")

	var steps []pipeline.NodeInfo

	compressedData, err := os.ReadFile(compressedPath)
	if err == nil {
		gzipReader, err := gzip.NewReader(bytes.NewReader(compressedData))
		if err != nil {
			return nil, fmt.Errorf("failed to create gzip reader for %s: %w", compressedPath, err)
		}
		defer gzipReader.Close()

		stepsYamlBytes, err := io.ReadAll(gzipReader)
		if err != nil {
			return nil, fmt.Errorf("failed to decompress %s: %w", compressedPath, err)
		}
		if err := yaml.Unmarshal(stepsYamlBytes, &steps); err != nil {
			return nil, fmt.Errorf("failed to unmarshal steps file: %w", err)
		}
		return steps, nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("failed to read %s: %w", compressedPath, err)
	}

	plainData, err := os.ReadFile(uncompressedPath)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("failed to read %s: %w", uncompressedPath, err)
		}
		logger.Info("steps.yaml not found, will use fallback start time",
			"compressed", compressedPath, "uncompressed", uncompressedPath)
		return nil, nil
	}
	if err := yaml.Unmarshal(plainData, &steps); err != nil {
		return nil, fmt.Errorf("failed to unmarshal steps file: %w", err)
	}
	return steps, nil
}

// LoadTestTimingInfo reads all timing-metadata-*.yaml(.gz) files from a
// directory and returns parsed timing info keyed by test identifier.
func LoadTestTimingInfo(ctx context.Context, dir string) (map[string]TimingInfo, error) {
	if dir == "" {
		return nil, nil
	}

	result := make(map[string]TimingInfo)
	err := filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		fileName := filepath.Base(p)
		if !strings.HasPrefix(fileName, "timing-metadata-") {
			return nil
		}
		if !strings.HasSuffix(fileName, ".yaml") && !strings.HasSuffix(fileName, ".yaml.gz") {
			return nil
		}

		fileData, err := os.ReadFile(p)
		if err != nil {
			return fmt.Errorf("failed to read %s: %w", p, err)
		}

		var timingFileBytes []byte
		if strings.HasSuffix(p, ".gz") {
			gzipReader, err := gzip.NewReader(bytes.NewReader(fileData))
			if err != nil {
				return fmt.Errorf("failed to create gzip reader for %s: %w", p, err)
			}
			defer gzipReader.Close()
			timingFileBytes, err = io.ReadAll(gzipReader)
			if err != nil {
				return fmt.Errorf("failed to decompress %s: %w", p, err)
			}
		} else {
			timingFileBytes = fileData
		}

		var tm SpecTimingMetadata
		if err := yaml.Unmarshal(timingFileBytes, &tm); err != nil {
			return fmt.Errorf("failed to unmarshal %s: %w", p, err)
		}

		if tm.StartedAt == "" || tm.FinishedAt == "" {
			return nil
		}

		startedAt, err := time.Parse(time.RFC3339, tm.StartedAt)
		if err != nil {
			return fmt.Errorf("failed to parse startedAt in %s: %w", p, err)
		}
		finishedAt, err := time.Parse(time.RFC3339, tm.FinishedAt)
		if err != nil {
			return fmt.Errorf("failed to parse finishedAt in %s: %w", p, err)
		}

		rgNames := make([]string, 0)
		for rg := range tm.Deployments {
			if rg != "" {
				rgNames = append(rgNames, rg)
			}
		}

		setupFinishTime, testStartTime := deriveSetupTestBoundary(tm.Steps)

		key := strings.Join(tm.Identifier, " ")
		result[key] = TimingInfo{
			StartTime:          startedAt,
			EndTime:            finishedAt.Add(endGracePeriodDuration),
			SetupFinishTime:    setupFinishTime,
			TestStartTime:      testStartTime,
			CleanupStartTime:   finishedAt,
			ResourceGroupNames: rgNames,
			Steps:              tm.Steps,
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to walk timing input dir %s: %w", dir, err)
	}
	return result, nil
}

// identityContainerStep reports whether a step name refers to identity container setup.
func identityContainerStep(name string) bool {
	return strings.Contains(strings.ToLower(name), "identity container")
}

// deriveSetupTestBoundary inspects the steps in the timing metadata and returns:
//   - setupFinishTime: the latest FinishedAt among steps whose name contains
//     "identity container" (setup steps).
//   - testStartTime: the earliest StartedAt among steps whose name does NOT
//     contain "identity container" (test steps).
//
// Either or both may be zero when the relevant steps are not present or their
// timestamps cannot be parsed.
func deriveSetupTestBoundary(steps []StepTimingMetadata) (setupFinishTime, testStartTime time.Time) {
	for _, step := range steps {
		if identityContainerStep(step.Name) {
			if step.FinishedAt != "" {
				if t, err := time.Parse(time.RFC3339, step.FinishedAt); err == nil {
					if setupFinishTime.IsZero() || t.After(setupFinishTime) {
						setupFinishTime = t
					}
				}
			}
		} else {
			if step.StartedAt != "" {
				if t, err := time.Parse(time.RFC3339, step.StartedAt); err == nil {
					if testStartTime.IsZero() || t.Before(testStartTime) {
						testStartTime = t
					}
				}
			}
		}
	}
	return setupFinishTime, testStartTime
}

// ComputeTimeWindow derives a start/end time range from steps, test timing
// info, and an optional CLI-provided fallback. Priority for the start time
// is: steps > test timing > startFallback. At least one source of start
// time data must be available; otherwise an error is returned.
func ComputeTimeWindow(ctx context.Context, clk clock.PassiveClock, steps []pipeline.NodeInfo, testTimingInfo map[string]TimingInfo, startFallback *time.Time) (TimeWindow, error) {
	logger, err := logr.FromContext(ctx)
	if err != nil {
		return TimeWindow{}, fmt.Errorf("logger not found in context: %w", err)
	}

	start := time.Time{}
	startSource := ""
	end := time.Time{}
	endSource := ""

	// Steps provide start and end times
	for _, step := range steps {
		if step.Info.StartedAt != "" {
			t, err := time.Parse(time.RFC3339, step.Info.StartedAt)
			if err != nil {
				return TimeWindow{}, fmt.Errorf("failed to parse step startedAt %q: %w", step.Info.StartedAt, err)
			}
			if start.IsZero() || t.Before(start) {
				start = t
				startSource = "steps"
			}
		}
		if step.Info.FinishedAt != "" {
			t, err := time.Parse(time.RFC3339, step.Info.FinishedAt)
			if err != nil {
				return TimeWindow{}, fmt.Errorf("failed to parse step finishedAt %q: %w", step.Info.FinishedAt, err)
			}
			t = t.Add(endGracePeriodDuration)
			if end.IsZero() || t.After(end) {
				end = t
				endSource = "steps (+grace)"
			}
		}
	}

	// Test timing info as fallback for start and end — only fills in
	// values that were not already derived from steps.
	if start.IsZero() {
		for _, ti := range testTimingInfo {
			if start.IsZero() || ti.StartTime.Before(start) {
				start = ti.StartTime
				startSource = "test timing"
			}
		}
	}
	if end.IsZero() {
		for _, ti := range testTimingInfo {
			if end.IsZero() || ti.EndTime.After(end) {
				end = ti.EndTime
				endSource = "test timing"
			}
		}
	}

	// provided start time as lowest-priority fallback
	if start.IsZero() && startFallback != nil {
		start = *startFallback
		startSource = "CLI fallback"
	}

	if start.IsZero() {
		return TimeWindow{}, fmt.Errorf("no start time available: provide --timing-input with steps/test timing or --start-time-fallback")
	}
	// When no end time from any source, use now + grace
	if end.IsZero() {
		end = clk.Now().Add(endGracePeriodDuration)
		endSource = "clock (now + grace)"
	}

	logger.Info("query time window",
		"start", start.Format(time.RFC3339), "startSource", startSource,
		"end", end.Format(time.RFC3339), "endSource", endSource)

	return TimeWindow{Start: start, End: end}, nil
}
