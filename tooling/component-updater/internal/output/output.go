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
			sb.WriteString("\n| Location | Current | Compatible versions | Advertised upgrades | Candidate | Detail |\n")
			sb.WriteString("|----------|---------|---------------------|--------------------|-----------|--------|\n")
			ready, blocked, failed, invalid := 0, 0, 0, 0
			for _, region := range r.Regions {
				fmt.Fprintf(&sb, "| %s | %s | %s | %s | %s | %s |\n",
					escape(region.Location), escape(region.Current), escape(strings.Join(region.Compatible, ", ")),
					escape(strings.Join(region.Upgrades, ", ")), escape(region.Candidate), escape(region.Detail))
				switch region.Candidate {
				case "READY":
					ready++
				case "BLOCKED":
					blocked++
				}
				if region.Current == "ERROR" {
					failed++
				}
				if region.Current == "INVALID" {
					invalid++
				}
			}
			fmt.Fprintf(&sb, "\nRegions: %d ready, %d blocked, %d discovery errors, %d current invalid\n", ready, blocked, failed, invalid)
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

func escape(value string) string {
	return strings.NewReplacer("|", "\\|", "\r", " ", "\n", " ").Replace(value)
}
