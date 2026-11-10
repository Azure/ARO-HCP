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
	"slices"

	et "github.com/openshift-eng/openshift-tests-extension/pkg/extension/extensiontests"

	"github.com/Azure/ARO-HCP/test/util/labels"
)

// Orders specs so Speed:Slow specs enter the scheduler queue first.
//
// The suite scheduler is FIFO: it hands out the first spec whose conflict group, taints
// and resource pools allow it to run. Enqueuing the longest specs first keeps them off
// the suite's critical path, because the makespan is set by the last spec to finish, not
// by the order in which short specs complete. Order within each group is preserved.
func slowSpecsFirst(specs et.ExtensionTestSpecs) {
	slices.SortStableFunc(specs, func(a, b *et.ExtensionTestSpec) int {
		aSlow := a.Labels.Has(labels.Slow[0])
		bSlow := b.Labels.Has(labels.Slow[0])
		switch {
		case aSlow == bSlow:
			return 0
		case aSlow:
			return -1
		default:
			return 1
		}
	})
}
