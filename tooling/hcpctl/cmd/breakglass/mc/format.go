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

package mc

import (
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/cluster"
)

// DisplayClustersTable displays management clusters in a formatted table
func DisplayClustersTable(clusters []cluster.AKSCluster) {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)

	// Print header
	fmt.Fprintln(w, "CLUSTER NAME\tSUBSCRIPTION\tRESOURCE GROUP\tLOCATION\tSTATE")
	fmt.Fprintln(w, strings.Repeat("-", 80))

	// Print clusters
	for _, cluster := range clusters {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
			cluster.Name,
			truncate(cluster.Subscription, 30),
			cluster.ResourceGroup,
			cluster.Location,
			cluster.State,
		)
	}

	w.Flush()
}

// truncate truncates a string to the specified length
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

// ErrorFormatter formats errors for display
type ErrorFormatter struct{}

// FormatError formats an error for CLI output
func (f *ErrorFormatter) FormatError(err error) string {
	if err == nil {
		return ""
	}
	return fmt.Sprintf("Error: %v\n", err)
}
