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
	"os"
	"strings"
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
	IsDevelopment    bool
	Timeout          time.Duration
	IncludeLocations []string
	ExcludeLocations []string
	Tracked          bool
	SharedDir        string
}

// validatedOptions is a private wrapper that enforces a call of Validate() before Complete() can be invoked.
type validatedOptions struct {
	*RawOptions
}

type ValidatedOptions struct {
	// Embed a private pointer that cannot be instantiated outside of this package.
	*validatedOptions
}

type completedOptions struct {
	ResourceGroups   []string
	DryRun           bool
	IsDevelopment    bool
	Timeout          time.Duration
	IncludeLocations sets.Set[string]
	ExcludeLocations sets.Set[string]
	CleanupWorkflow  framework.CleanupWorkflow
	DeleteExpired    bool
	EvaluationTime   time.Time
}

type Options struct {
	*completedOptions
}

func DefaultOptions() *RawOptions {
	return &RawOptions{
		ResourceGroups:   []string{},
		DeleteExpired:    false,
		EvaluationTime:   time.Now().Format(time.RFC3339),
		DryRun:           false,
		CleanupWorkflow:  string(framework.CleanupWorkflowStandard),
		IsDevelopment:    false,
		Timeout:          60 * time.Minute,
		IncludeLocations: []string{},
		ExcludeLocations: []string{},
		Tracked:          false,
		SharedDir:        framework.SharedDir(),
	}
}

func (o *RawOptions) Validate() (*ValidatedOptions, error) {

	if o.CleanupWorkflow != string(framework.CleanupWorkflowStandard) && o.CleanupWorkflow != string(framework.CleanupWorkflowNoRP) {
		return nil, fmt.Errorf("invalid cleanup workflow: %s", o.CleanupWorkflow)
	}

	optionGroups := []struct {
		memberFlags      sets.Set[string]
		seenFlags        sets.Set[string]
		exclusivityGroup bool
		oneOfGroup       bool
	}{
		{
			memberFlags:      sets.New("--resource-group", "--expired", "--tracked"),
			exclusivityGroup: true,
			oneOfGroup:       true,
			seenFlags:        sets.New[string](),
		},
		{
			memberFlags:      sets.New("--include-location", "--exclude-location"),
			exclusivityGroup: true,
			oneOfGroup:       false,
			seenFlags:        sets.New[string](),
		},
		{
			memberFlags:      sets.New("--mode=no-rp", "--is-development"),
			exclusivityGroup: true,
			oneOfGroup:       false,
			seenFlags:        sets.New[string](),
		},
	}

	for _, item := range []struct {
		flag  string
		isSet func() bool
	}{
		{flag: "--resource-group", isSet: func() bool { return len(o.ResourceGroups) > 0 }},
		{flag: "--expired", isSet: func() bool { return o.DeleteExpired }},
		{flag: "--tracked", isSet: func() bool { return o.Tracked }},
		{flag: "--include-location", isSet: func() bool { return len(o.IncludeLocations) > 0 }},
		{flag: "--exclude-location", isSet: func() bool { return len(o.ExcludeLocations) > 0 }},
		{flag: "--is-development", isSet: func() bool { return o.IsDevelopment }},
		{flag: "--mode=no-rp", isSet: func() bool { return o.CleanupWorkflow == string(framework.CleanupWorkflowNoRP) }},
	} {
		if item.isSet() {
			for _, group := range optionGroups {
				if group.memberFlags.Has(item.flag) {
					group.seenFlags.Insert(item.flag)
				}
			}
		}
	}

	for _, group := range optionGroups {
		if group.exclusivityGroup && group.seenFlags.Len() > 1 {
			return nil, fmt.Errorf("%s are mutually exclusive", group.memberFlags.UnsortedList())
		}
		if group.oneOfGroup && group.seenFlags.Len() == 0 {
			return nil, fmt.Errorf("one of %s is required", group.memberFlags.UnsortedList())
		}
	}

	return &ValidatedOptions{
		validatedOptions: &validatedOptions{
			RawOptions: o,
		},
	}, nil
}

func (o *ValidatedOptions) Complete() (*Options, error) {

	if o.IsDevelopment {
		if err := os.Setenv("AROHCP_ENV", "development"); err != nil {
			return nil, fmt.Errorf("failed to set AROHCP_ENV environment variable: %w", err)
		}
	}

	// When a tracked is provided, populate ResourceGroups from files named:
	//   tracked-resource-group_<resource-group-name>
	// in the shared directory.
	var resourceGroups []string
	if o.Tracked {
		entries, err := os.ReadDir(o.SharedDir)
		if err != nil {
			return nil, fmt.Errorf("reading tracked path %q: %w", o.SharedDir, err)
		}

		const prefix = "tracked-resource-group_"
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			name := entry.Name()
			if !strings.HasPrefix(name, prefix) {
				continue
			}
			rg := strings.TrimPrefix(name, prefix)
			if rg == "" {
				continue
			}
			resourceGroups = append(resourceGroups, rg)
		}

		if len(resourceGroups) == 0 {
			return nil, fmt.Errorf("no %s* files found in %q", prefix, o.SharedDir)
		}
	} else {
		resourceGroups = o.ResourceGroups
	}

	var evalTime time.Time
	var err error
	if o.DeleteExpired {
		evalTime, err = time.Parse(time.RFC3339, o.EvaluationTime)
		if err != nil {
			return nil, fmt.Errorf("failed to parse --evaluation-time value: %w", err)
		}
	}

	return &Options{
		completedOptions: &completedOptions{
			ResourceGroups:   resourceGroups,
			DryRun:           o.DryRun,
			CleanupWorkflow:  framework.CleanupWorkflow(o.CleanupWorkflow),
			IsDevelopment:    o.IsDevelopment,
			Timeout:          o.Timeout,
			IncludeLocations: sets.New(o.IncludeLocations...),
			ExcludeLocations: sets.New(o.ExcludeLocations...),
			DeleteExpired:    o.DeleteExpired,
			EvaluationTime:   evalTime,
		},
	}, nil
}
