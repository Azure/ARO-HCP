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
	"github.com/stretchr/testify/require"

	et "github.com/openshift-eng/openshift-tests-extension/pkg/extension/extensiontests"
	"github.com/openshift-eng/openshift-tests-extension/pkg/util/sets"
)

func specWithStartPriority(name string, labelStrings ...string) *et.ExtensionTestSpec {
	return &et.ExtensionTestSpec{Name: name, Labels: sets.New(labelStrings...)}
}

func specNames(specs et.ExtensionTestSpecs) []string {
	names := make([]string, 0, len(specs))
	for _, spec := range specs {
		names = append(names, spec.Name)
	}
	return names
}

func TestOrderSpecsForStartPlacesHigherPrioritiesFirst(t *testing.T) {
	specs := et.ExtensionTestSpecs{
		specWithStartPriority("default-a"),
		specWithStartPriority("first-a", "StartPriority:50"),
		specWithStartPriority("second-a", "StartPriority:20"),
		specWithStartPriority("first-b", "StartPriority:50"),
		specWithStartPriority("default-b"),
	}

	require.NoError(t, orderSpecsForStart(specs))

	assert.Equal(t, []string{"first-a", "first-b", "second-a", "default-a", "default-b"}, specNames(specs))
}

func TestOrderSpecsForStartLeavesSuitesWithoutPrioritiesUnchanged(t *testing.T) {
	specs := et.ExtensionTestSpecs{
		specWithStartPriority("a"),
		specWithStartPriority("b"),
		specWithStartPriority("c"),
	}

	require.NoError(t, orderSpecsForStart(specs))

	assert.Equal(t, []string{"a", "b", "c"}, specNames(specs))
}

func TestOrderSpecsForStartIgnoresUnrelatedLabels(t *testing.T) {
	specs := et.ExtensionTestSpecs{
		specWithStartPriority("slow", "Speed:Slow"),
		specWithStartPriority("prioritized", "StartPriority:20"),
	}

	require.NoError(t, orderSpecsForStart(specs))

	assert.Equal(t, []string{"prioritized", "slow"}, specNames(specs))
}

func TestParseStartPriorityRejectsInvalidLabels(t *testing.T) {
	testCases := []struct {
		name   string
		labels []string
		err    string
	}{
		{name: "no label", labels: nil},
		{name: "zero", labels: []string{"StartPriority:0"}},
		{name: "maximum", labels: []string{"StartPriority:1000"}},
		{name: "not an integer", labels: []string{"StartPriority:high"}, err: "not an integer"},
		{name: "negative", labels: []string{"StartPriority:-1"}, err: "outside the allowed range"},
		{name: "above maximum", labels: []string{"StartPriority:1001"}, err: "outside the allowed range"},
		{name: "duplicate keys", labels: []string{"StartPriority:20", "StartPriority:50"}, err: "multiple start priority labels"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			spec := specWithStartPriority("spec", tc.labels...)
			_, err := parseStartPriority(spec)
			if tc.err == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.err)
		})
	}
}
