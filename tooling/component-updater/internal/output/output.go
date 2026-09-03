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

package output

import (
	"fmt"
	"sort"
	"strings"

	"github.com/Azure/ARO-HCP/tooling/component-updater/internal/updater"
)

func FormatMarkdownTable(results []updater.ComponentResult) string {
	sort.Slice(results, func(i, j int) bool {
		return results[i].Name < results[j].Name
	})

	var sb strings.Builder
	sb.WriteString("| Component | Current | Available | Next |\n")
	sb.WriteString("|-----------|---------|-----------|------|\n")

	for _, r := range results {
		available := strings.Join(r.Available, ", ")
		next := r.Next
		if next == "" {
			next = "(up to date)"
		}
		if r.Error != nil {
			available = fmt.Sprintf("error: %v", r.Error)
			next = "-"
		}
		fmt.Fprintf(&sb, "| %s | %s | %s | %s |\n", r.Name, r.Current, available, next)
	}

	return sb.String()
}
