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

package arm

import "testing"

func TestNewDeletionStep_PanicsWhenSelectorIsInvalid(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name     string
		selector ResourceSelector
	}{
		{
			name:     "neither included nor excluded",
			selector: ResourceSelector{},
		},
		{
			name: "both included and excluded",
			selector: ResourceSelector{
				IncludedResourceTypes: []string{"typeA"},
				ExcludedResourceTypes: []string{"typeB"},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			defer func() {
				if recover() == nil {
					t.Fatalf("expected panic for invalid selector")
				}
			}()
			_ = NewDeletionStep(DeletionStepConfig{Selector: tc.selector})
		})
	}
}

func TestNewDeletionStep_DefaultNameForSingleIncludedType(t *testing.T) {
	t.Parallel()

	step := NewDeletionStep(DeletionStepConfig{
		Selector: ResourceSelector{
			IncludedResourceTypes: []string{"Microsoft.Network/privateEndpoints"},
		},
	})

	if got, want := step.Name(), "Delete Microsoft.Network/privateEndpoints"; got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestNewDeletionStep_DefaultNameForMultipleIncludedTypes(t *testing.T) {
	t.Parallel()

	step := NewDeletionStep(DeletionStepConfig{
		Selector: ResourceSelector{
			IncludedResourceTypes: []string{"typeA", "typeB"},
		},
	})

	if got, want := step.Name(), "Delete selected resources"; got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestNewDeletionStep_DefaultNameForExcludedTypes(t *testing.T) {
	t.Parallel()

	step := NewDeletionStep(DeletionStepConfig{
		Selector: ResourceSelector{
			ExcludedResourceTypes: []string{"typeA"},
		},
	})

	if got, want := step.Name(), "Delete resources excluding selected types"; got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}
