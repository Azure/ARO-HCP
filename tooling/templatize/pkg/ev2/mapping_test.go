package ev2

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/Azure/ARO-HCP/tooling/templatize/pkg/config"
)

func TestMapping(t *testing.T) {
	testData := config.Variables{
		"key1": "value1",
		"key2": 42,
		"key3": true,
		"parent": map[string]interface{}{
			"nested":    "nestedvalue",
			"nestedInt": 42,
			"deeper": map[string]interface{}{
				"deepest": "deepestvalue",
			},
		},
	}
	tests := []struct {
		name              string
		generator         PlaceholderGenerator
		expectedFlattened map[string]string
		expectedReplace   map[string]interface{}
	}{
		{
			name:      "dunder",
			generator: NewDunderPlaceholders(),
			expectedFlattened: map[string]string{
				"__key1__":                  "key1",
				"__key2__":                  "key2",
				"__key3__":                  "key3",
				"__parent_nested__":         "parent.nested",
				"__parent_nestedInt__":      "parent.nestedInt",
				"__parent_deeper_deepest__": "parent.deeper.deepest",
			},
			expectedReplace: map[string]interface{}{
				"key1": "__key1__",
				"key2": "__key2__",
				"key3": "__key3__",
				"parent": map[string]interface{}{
					"nested":    "__parent_nested__",
					"nestedInt": "__parent_nestedInt__",
					"deeper":    map[string]interface{}{"deepest": "__parent_deeper_deepest__"},
				},
			},
		},
		{
			name:      "bicep",
			generator: NewBicepParamPlaceholders(),
			expectedFlattened: map[string]string{
				"__key1__":                  "key1",
				"__key2__":                  "key2",
				"__key3__":                  "key3",
				"__parent_nested__":         "parent.nested",
				"__parent_nestedInt__":      "parent.nestedInt",
				"__parent_deeper_deepest__": "parent.deeper.deepest",
			},
			expectedReplace: map[string]interface{}{
				"key1": "__key1__",
				"key2": "any('__key2__')",
				"key3": "any('__key3__')",
				"parent": map[string]interface{}{
					"nested":    "__parent_nested__",
					"nestedInt": "any('__parent_nestedInt__')",
					"deeper":    map[string]interface{}{"deepest": "__parent_deeper_deepest__"},
				},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			flattened, replace := EV2Mapping(testData, tc.generator, []string{})
			if diff := cmp.Diff(tc.expectedFlattened, flattened); diff != "" {
				t.Errorf("got incorrect flattened: %v", diff)
			}
			if diff := cmp.Diff(tc.expectedReplace, replace); diff != "" {
				t.Errorf("got incorrect replace: %v", diff)
			}
		})
	}
}
