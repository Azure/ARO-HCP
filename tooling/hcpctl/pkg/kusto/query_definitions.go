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

package kusto

// QueryDefinition declaratively describes a group of related KQL queries.
// For single-template queries, set TemplatePath directly.
// For multi-template queries, use Children with specific names and template paths.
type QueryDefinition struct {
	Name                string       `yaml:"name"`
	QueryType           QueryType    `yaml:"queryType"`
	Database            string       `yaml:"database"`
	TemplatePath        string       `yaml:"templatePath,omitempty"`
	Children            []QueryChild `yaml:"children,omitempty"`
	IncludeInMustGather bool         `yaml:"includeInMustGather,omitempty"`
}

// QueryChild describes a named sub-query within a multi-template QueryDefinition.
type QueryChild struct {
	Name         string `yaml:"name"`
	TemplatePath string `yaml:"templatePath"`
}

type QueryType string

const (
	QueryTypeServices           QueryType = "services"
	QueryTypeHostedControlPlane QueryType = "hosted-control-plane"
	QueryTypeInternal           QueryType = "must-gather-internal"
	QueryTypeKubernetesEvents   QueryType = "kubernetes-events"
	QueryTypeSystemdLogs        QueryType = "systemd-logs"
	QueryTypeCustomLogs         QueryType = "custom-logs"
)
