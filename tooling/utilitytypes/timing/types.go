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

type SpecTimingMetadata struct {
	Identifier []string `json:"identifier"`
	StartedAt  string   `json:"startedAt"`
	FinishedAt string   `json:"finishedAt"`

	Steps []StepTimingMetadata `json:"steps,omitempty"`

	// Deployments holds deployment operation metadata by resource group and deployment name
	Deployments map[string]map[string][]Operation `json:"deployments,omitempty"`
}

type StepTimingMetadata struct {
	Name string `json:"name"`
	// StartedAt is the time at which the step started, formatted as RFC3339 date+time: 2025-11-05T13:16:20.624264+00:00
	StartedAt string `json:"startedAt"`
	// FinishedAt is the time at which the step finished, formatted as RFC3339 date+time: 2025-11-05T13:16:20.624264+00:00
	FinishedAt string `json:"finishedAt"`
}

// Operation describes an ARM deployment rollout operation taken to realize a template.
type Operation struct {
	// OperationType is what the operation did - known values: "Create", "Read", "EvaluateDeploymentOutput"
	OperationType string `json:"operationType"`

	// StartTimestamp is the time at which the operation started, formatted as RFC3339 date+time: 2025-11-05T13:16:20.624264+00:00
	StartTimestamp string `json:"startTimestamp"`
	// Duration is the time taken to run the operation, formatted as RFC3339 duration: PT3M12.9884364S
	Duration string `json:"duration"`

	// Resource defines the object of this operation.
	Resource *Resource `json:"resource,omitempty"`

	// Children holds the child operations when the resource is another deployment.
	Children []Operation `json:"children,omitempty"`
}

type Resource struct {
	// ResourceType is the resource provider and resource name, like "Microsoft.KeyVault/vaults".
	ResourceType string `json:"resourceType"`
	// ResourceGroup is the Azure resource group in which the resource exists.
	ResourceGroup string `json:"resourceGroup"`
	// Name is the name of the resource.
	Name string `json:"name"`
}
