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
	"testing"

	"github.com/stretchr/testify/assert"

	et "github.com/openshift-eng/openshift-tests-extension/pkg/extension/extensiontests"
	"github.com/openshift-eng/openshift-tests-extension/pkg/util/sets"

	"github.com/Azure/ARO-HCP/test/util/labels"
)

func TestSlowSpecsFirstMovesSlowSpecsAheadWithoutReorderingGroups(t *testing.T) {
	spec := func(name string, slow bool) *et.ExtensionTestSpec {
		labelSet := sets.New[string]()
		if slow {
			labelSet.Insert(labels.Slow[0])
		}
		return &et.ExtensionTestSpec{Name: name, Labels: labelSet}
	}

	specs := et.ExtensionTestSpecs{
		spec("fast-a", false),
		spec("slow-a", true),
		spec("fast-b", false),
		spec("slow-b", true),
	}

	slowSpecsFirst(specs)

	names := make([]string, 0, len(specs))
	for _, s := range specs {
		names = append(names, s.Name)
	}
	assert.Equal(t, []string{"slow-a", "slow-b", "fast-a", "fast-b"}, names)
}

func TestSlowSpecsFirstLeavesAnAllFastSuiteUnchanged(t *testing.T) {
	spec := func(name string) *et.ExtensionTestSpec {
		return &et.ExtensionTestSpec{Name: name, Labels: sets.New[string]()}
	}
	specs := et.ExtensionTestSpecs{spec("a"), spec("b"), spec("c")}

	slowSpecsFirst(specs)

	names := make([]string, 0, len(specs))
	for _, s := range specs {
		names = append(names, s.Name)
	}
	assert.Equal(t, []string{"a", "b", "c"}, names)
}
