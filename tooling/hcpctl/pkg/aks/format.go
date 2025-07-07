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

package aks

import (
	"fmt"

	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/common"
)

// getAKSFormatter returns a formatter configured for AKS clusters
func getAKSFormatter() *common.Formatter[AKSCluster] {
	tableOptions := common.TableOptions[AKSCluster]{
		Title:        "Found %d management cluster(s)",
		EmptyMessage: "No management clusters found",
		Columns: []common.TableColumn[AKSCluster]{
			{Header: "CLUSTER NAME", Field: func(item AKSCluster) string { return item.Name }},
			{Header: "SUBSCRIPTION ID", Field: func(item AKSCluster) string { return item.SubscriptionID }},
			{Header: "RESOURCE GROUP", Field: func(item AKSCluster) string { return item.ResourceGroup }},
			{Header: "LOCATION", Field: func(item AKSCluster) string { return item.Location }},
		},
	}
	return common.NewFormatter("ManagementClusters", tableOptions)
}

// FormatAKSTable formats clusters as a table string
func FormatAKSTable(clusters []AKSCluster) string {
	formatter := getAKSFormatter()
	return formatter.FormatTable(clusters)
}

// DisplayAKSTable prints clusters in table format to stdout
func DisplayAKSTable(clusters []AKSCluster) {
	fmt.Print(FormatAKSTable(clusters))
}

// FormatAKSYAML formats clusters as YAML string with metadata and items
func FormatAKSYAML(clusters []AKSCluster) (string, error) {
	formatter := getAKSFormatter()
	return formatter.FormatYAML(clusters)
}

// DisplayAKSYAML prints clusters in YAML format to stdout
func DisplayAKSYAML(clusters []AKSCluster) error {
	output, err := FormatAKSYAML(clusters)
	if err != nil {
		return err
	}
	fmt.Print(output)
	return nil
}

// FormatAKSJSON formats clusters as JSON string with metadata and items
func FormatAKSJSON(clusters []AKSCluster) (string, error) {
	formatter := getAKSFormatter()
	return formatter.FormatJSON(clusters)
}

// DisplayAKSJSON prints clusters in JSON format to stdout
func DisplayAKSJSON(clusters []AKSCluster) error {
	output, err := FormatAKSJSON(clusters)
	if err != nil {
		return err
	}
	fmt.Println(output)
	return nil
}

// OutputFormat represents the supported output formats
type OutputFormat = common.OutputFormat

const (
	OutputFormatTable = common.OutputFormatTable
	OutputFormatYAML  = common.OutputFormatYAML
	OutputFormatJSON  = common.OutputFormatJSON
)

func ValidateOutputFormat(format string) (OutputFormat, error) {
	return common.ValidateOutputFormat(format)
}

// FormatAKSClusters formats clusters in the specified format and returns the string
func FormatAKSClusters(clusters []AKSCluster, format OutputFormat) (string, error) {
	formatter := getAKSFormatter()
	return formatter.Format(clusters, format)
}

// DisplayAKSClusters displays clusters in the specified format
func DisplayAKSClusters(clusters []AKSCluster, format OutputFormat) error {
	formatter := getAKSFormatter()
	return formatter.Display(clusters, format)
}
