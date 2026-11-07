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

package output

import (
	"fmt"
	"sort"
	"strings"

	"github.com/Azure/ARO-HCP/tooling/component-updater/internal/providers"
	"github.com/Azure/ARO-HCP/tooling/component-updater/internal/updater"
)

func FormatMarkdownTable(results []updater.ComponentResult) string {
	results = append([]updater.ComponentResult(nil), results...)
	sort.Slice(results, func(i, j int) bool {
		return results[i].Name < results[j].Name
	})

	var sb strings.Builder
	for _, r := range results {
		if r.Paused && r.Error == nil {
			fmt.Fprintf(&sb, "Component: %s\nResult: PAUSED\nDetail: %s\n\n", escape(r.Name), escape(r.Detail))
			continue
		}
		candidate := r.Candidate
		if candidate == "" {
			candidate = "not identified"
		}
		fmt.Fprintf(&sb, "Component: %s\nConfigured version: %s\nNext candidate: %s\n",
			escape(r.Name), escape(r.Current), escape(candidate))
		if r.Compatibility != "" {
			fmt.Fprintf(&sb, "Compatibility constraint: %s\n", escape(r.Compatibility))
		}
		if len(r.Regions) > 0 {
			writeRegions(&sb, r.Regions)
		}
		common := strings.Join(r.Available, ", ")
		if r.Error != nil {
			common = "unconfirmed (operation failed)"
		}
		fmt.Fprintf(&sb, "Available versions: %s\n", escape(common))
		outcome := "NO CHANGE"
		if r.Error != nil {
			outcome = "FAILED - " + r.Error.Error()
		} else if r.Updated {
			outcome = "UPDATED: " + r.Current + " -> " + r.Next
		} else if r.Next != "" {
			outcome = "UPDATE AVAILABLE: " + r.Current + " -> " + r.Next + " (check only; no files written)"
		}
		fmt.Fprintf(&sb, "Result: %s\nDetail: %s\n\n", escape(outcome), escape(r.Detail))
	}

	return sb.String()
}

// writeRegions renders Location | Current | <provider columns> | Candidate | Detail.
// Provider columns appear in first-seen order; missing values render empty.
func writeRegions(sb *strings.Builder, regions []providers.RegionResult) {
	var extra []string
	seen := map[string]bool{}
	for _, region := range regions {
		for _, column := range region.Columns {
			if !seen[column.Name] {
				seen[column.Name] = true
				extra = append(extra, column.Name)
			}
		}
	}
	header := append(append([]string{"Location", "Current"}, extra...), "Candidate", "Detail")
	for i := range header {
		header[i] = escape(header[i])
	}
	fmt.Fprintf(sb, "\n| %s |\n|%s\n", strings.Join(header, " | "), strings.Repeat("---|", len(header)))

	counts := map[string]int{}
	for _, region := range regions {
		values := map[string]string{}
		for _, column := range region.Columns {
			values[column.Name] = column.Value
		}
		row := []string{region.Location, string(region.CurrentStatus)}
		for _, name := range extra {
			row = append(row, values[name])
		}
		row = append(row, string(region.CandidateStatus), region.Detail)
		for i := range row {
			row[i] = escape(row[i])
		}
		fmt.Fprintf(sb, "| %s |\n", strings.Join(row, " | "))
		counts[string(region.CandidateStatus)]++
		counts[string(region.CurrentStatus)]++
	}
	fmt.Fprintf(sb, "\nRegions: %d ready, %d blocked, %d discovery errors, %d current invalid\n",
		counts[string(providers.CandidateReady)], counts[string(providers.CandidateBlocked)],
		counts[string(providers.CurrentError)], counts[string(providers.CurrentInvalid)])
}

func escape(value string) string {
	return strings.NewReplacer("|", "\\|", "\r", " ", "\n", " ").Replace(value)
}
