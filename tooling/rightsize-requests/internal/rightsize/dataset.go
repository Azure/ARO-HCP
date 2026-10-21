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

package rightsize

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type inputDataset struct {
	Reports         []datasetReport
	Recommendations []inputRecommendation
	Warnings        []string
	Start, End      time.Time
	CPUWindow       string
}

type datasetReport struct {
	Path   string
	SHA256 string
	Report inputReport
}

// readDataset validates every input before returning usable evidence. Only whole
// reports are deduplicated; counts and savings from distinct runs stay separate.
func readDataset(paths []string) (inputDataset, error) {
	if len(paths) == 0 {
		return inputDataset{}, fmt.Errorf("input dataset requires at least one report path")
	}
	absPaths := make([]string, 0, len(paths))
	for _, path := range paths {
		if strings.TrimSpace(path) == "" {
			return inputDataset{}, fmt.Errorf("input dataset report path must not be blank")
		}
		absPath, err := filepath.Abs(path)
		if err != nil {
			return inputDataset{}, fmt.Errorf("input %q: %w", path, err)
		}
		absPaths = append(absPaths, absPath)
	}
	sort.Strings(absPaths)

	var dataset inputDataset
	seen := map[[sha256.Size]byte]bool{}
	for _, path := range absPaths {
		report, err := readInput(path)
		if err != nil {
			return inputDataset{}, fmt.Errorf("input %q: %w", path, err)
		}
		if dataset.CPUWindow != "" && report.CPUWindow != dataset.CPUWindow {
			return inputDataset{}, fmt.Errorf("input %q: cpuWindow=%s cannot be mixed with cpuWindow=%s", path, report.CPUWindow, dataset.CPUWindow)
		}
		// Canonical encoding ignores whitespace and object key order, but not
		// recommendation order. Copies and symlink aliases keep the first path.
		data, err := json.Marshal(report)
		if err != nil {
			return inputDataset{}, fmt.Errorf("input %q: encode report: %w", path, err)
		}
		hash := sha256.Sum256(data)
		if seen[hash] {
			continue
		}
		seen[hash] = true
		dataset.Reports = append(dataset.Reports, datasetReport{Path: path, SHA256: fmt.Sprintf("%x", hash), Report: report})
		dataset.CPUWindow = report.CPUWindow
		if dataset.Start.IsZero() || report.Start.Before(dataset.Start) {
			dataset.Start = report.Start
		}
		if dataset.End.IsZero() || report.End.After(dataset.End) {
			dataset.End = report.End
		}
		for _, warning := range report.Warnings {
			dataset.Warnings = append(dataset.Warnings, path+": "+warning)
		}
		for _, row := range report.Recommendations {
			row.source = path
			dataset.Recommendations = append(dataset.Recommendations, row)
		}
	}
	return dataset, nil
}
