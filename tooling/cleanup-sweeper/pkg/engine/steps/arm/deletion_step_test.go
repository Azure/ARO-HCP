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
