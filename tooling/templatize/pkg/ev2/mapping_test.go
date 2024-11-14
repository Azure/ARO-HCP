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
		"__KEY1__":                  "key1",
		"__KEY2__":                  "key2",
		"__KEY3__":                  "key3",
		"__PARENT_NESTED__":         "parent.nested",
		"__PARENT_DEEPER_DEEPEST__": "parent.deeper.deepest",
	}
	expecetedReplace := map[string]interface{}{
		"key1": "__KEY1__",
		"key2": "__KEY2__",
		"key3": "__KEY3__",
		"parent": map[string]interface{}{
			"nested": "__PARENT_NESTED__",
			"deeper": map[string]interface{}{"deepest": "__PARENT_DEEPER_DEEPEST__"},
		},
	}
	flattened, replace := EV2Mapping(testData, []string{})
	if diff := cmp.Diff(expectedFlattened, flattened); diff != "" {
		t.Errorf("got incorrect flattened: %v", diff)
	}
	if diff := cmp.Diff(expecetedReplace, replace); diff != "" {
		t.Errorf("got incorrect replace: %v", diff)
	}
}
