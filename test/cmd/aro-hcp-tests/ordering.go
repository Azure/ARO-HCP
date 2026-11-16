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

package main

import (
	"cmp"
	"fmt"
	"slices"
	"strconv"
	"strings"

	et "github.com/openshift-eng/openshift-tests-extension/pkg/extension/extensiontests"

	"github.com/Azure/ARO-HCP/test/util/labels"
)

// Orders specs so the FIFO suite scheduler starts the highest priority work first.
//
// The suite scheduler hands out the first spec whose conflict group, taints and resource
// pools allow it to run, so the makespan is set by the last spec to finish, not by the
// order in which short specs complete. Specs declaring a StartPriority label enter the
// queue ahead of lower priority specs, and the sort is stable so specs in the same tier
// keep the order they were declared in. Ordering never changes suite membership.
func orderSpecsForStart(specs et.ExtensionTestSpecs) error {
	priorities := make(map[*et.ExtensionTestSpec]int, len(specs))
	for _, spec := range specs {
		priority, err := parseStartPriority(spec)
		if err != nil {
			return err
		}
		priorities[spec] = priority
	}

	slices.SortStableFunc(specs, func(a, b *et.ExtensionTestSpec) int {
		return cmp.Compare(priorities[b], priorities[a])
	})

	return nil
}

// Returns the spec's declared start priority, or 0 when the spec declares none.
func parseStartPriority(spec *et.ExtensionTestSpec) (int, error) {
	var declared []string
	for label := range spec.Labels {
		if strings.HasPrefix(label, labels.StartPriorityLabelPrefix) {
			declared = append(declared, label)
		}
	}

	switch len(declared) {
	case 0:
		return 0, nil
	case 1:
	default:
		slices.Sort(declared)
		return 0, fmt.Errorf("spec %q has multiple start priority labels (%s); exactly one is allowed", spec.Name, strings.Join(declared, ", "))
	}

	value := strings.TrimPrefix(declared[0], labels.StartPriorityLabelPrefix)
	priority, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("spec %q has start priority %q that is not an integer", spec.Name, value)
	}
	if priority < 0 || priority > labels.StartPriorityMax {
		return 0, fmt.Errorf("spec %q has start priority %d outside the allowed range 0..%d", spec.Name, priority, labels.StartPriorityMax)
	}

	return priority, nil
}
