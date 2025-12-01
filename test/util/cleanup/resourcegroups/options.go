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

package resourcegroups

import (
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/Azure/ARO-HCP/test/util/framework"
)

type RawOptions struct {
	ResourceGroups   []string
	DeleteExpired    bool
	EvaluationTime   string
	DryRun           bool
	CleanupWorkflow  string
	Timeout          time.Duration
	IncludeLocations []string
	ExcludeLocations []string
}

type validatedOptions struct {
	*RawOptions
	includeLocations sets.Set[string]
	excludeLocations sets.Set[string]
	cleanupWorkflow  framework.CleanupWorkflow
}

type Options struct {
	*validatedOptions
}

func NewOptions() *RawOptions {
	return &RawOptions{
		ResourceGroups:   []string{},
		DeleteExpired:    true,
		EvaluationTime:   time.Now().Format(time.RFC3339),
		DryRun:           false,
		CleanupWorkflow:  string(framework.CleanupWorkflowStandard),
		Timeout:          60 * time.Minute,
		IncludeLocations: []string{},
		ExcludeLocations: []string{},
	}
}

func (o *RawOptions) Validate() (*Options, error) {

	includeLocations := sets.New(o.IncludeLocations...)
	excludeLocations := sets.New(o.ExcludeLocations...)
	if includeLocations.Len() > 0 && excludeLocations.Len() > 0 {
		return nil, fmt.Errorf("include-location and exclude-location flags are mutually exclusive")
	}

	var mode framework.CleanupWorkflow
	if o.CleanupWorkflow != string(framework.CleanupWorkflowStandard) && o.CleanupWorkflow != string(framework.CleanupWorkflowNoRP) {
		return nil, fmt.Errorf("invalid cleanup workflow: %s", o.CleanupWorkflow)
	} else {
		mode = framework.CleanupWorkflow(o.CleanupWorkflow)
	}
	return &Options{
		validatedOptions: &validatedOptions{
			RawOptions:       o,
			includeLocations: includeLocations,
			excludeLocations: excludeLocations,
			cleanupWorkflow:  mode,
		},
	}, nil
}
