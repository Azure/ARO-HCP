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
			"nested": "nestedvalue",
			"deeper": map[string]interface{}{
				"deepest": "deepestvalue",
			},
		},
	}
	expectedFlattened := map[string]string{
		"__key1__":                  "key1",
		"__key2__":                  "key2",
		"__key3__":                  "key3",
		"__parent_nested__":         "parent.nested",
		"__parent_deeper_deepest__": "parent.deeper.deepest",
	}
	expectedReplace := map[string]interface{}{
		"key1": "__key1__",
		"key2": "__key2__",
		"key3": "__key3__",
		"parent": map[string]interface{}{
			"nested": "__parent_nested__",
			"deeper": map[string]interface{}{"deepest": "__parent_deeper_deepest__"},
		},
	}
	flattened, replace := EV2Mapping(testData, []string{})
	if diff := cmp.Diff(expectedFlattened, flattened); diff != "" {
		t.Errorf("got incorrect flattened: %v", diff)
	}
	if diff := cmp.Diff(expectedReplace, replace); diff != "" {
		t.Errorf("got incorrect replace: %v", diff)
	}
}
