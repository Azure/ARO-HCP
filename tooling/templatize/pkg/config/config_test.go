package config

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/Azure/ARO-HCP/tooling/templatize/internal/testutil"
)

func TestConfigProvider(t *testing.T) {
	region := "uksouth"
	regionShort := "uks"
	stamp := "1"

	configProvider := NewConfigProvider("../../testdata/config.yaml")

	variables, err := configProvider.GetVariables("public", "int", region, NewConfigReplacements(region, regionShort, stamp))
	assert.NoError(t, err)
	assert.NotNil(t, variables)

	// key is not in the config file
	assert.Nil(t, variables["svc_resourcegroup"])

	// key is in the config file, region constant value
	assert.Equal(t, "uksouth", variables["test"])

	// key is in the config file, default in INT, constant value
	assert.Equal(t, "aro-hcp-int.azurecr.io/maestro-server:the-stable-one", variables["maestro_image"])

	// key is in the config file, default, varaible value
	assert.Equal(t, fmt.Sprintf("hcp-underlay-%s", regionShort), variables["regionRG"])
}

func TestInterfaceToVariable(t *testing.T) {
	testCases := []struct {
		name               string
		i                  interface{}
		ok                 bool
		expecetedVariables Variables
	}{
		{
			name:               "empty interface",
			ok:                 false,
			expecetedVariables: Variables{},
		},
		{
			name:               "empty map",
			i:                  map[string]interface{}{},
			ok:                 true,
			expecetedVariables: Variables{},
		},
		{
			name: "map",
			i: map[string]interface{}{
				"key1": "value1",
				"key2": "value2",
			},
			ok: true,
			expecetedVariables: Variables{
				"key1": "value1",
				"key2": "value2",
			},
		},
		{
			name: "nested map",
			i: map[string]interface{}{
				"key1": map[string]interface{}{
					"key2": "value2",
				},
			},
			ok: true,
			expecetedVariables: Variables{
				"key1": Variables{
					"key2": "value2",
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			vars, ok := InterfaceToVariables(tc.i)
			assert.Equal(t, tc.ok, ok)
			assert.Equal(t, tc.expecetedVariables, vars)
		})
	}
}

func TestMergeVariable(t *testing.T) {
	testCases := []struct {
		name     string
		base     Variables
		override Variables
		expected Variables
	}{
		{
			name:     "nil base",
			expected: nil,
		},
		{
			name:     "empty base and override",
			base:     Variables{},
			expected: Variables{},
		},
		{
			name:     "merge into empty base",
			base:     Variables{},
			override: Variables{"key1": "value1"},
			expected: Variables{"key1": "value1"},
		},
		{
			name:     "merge into base",
			base:     Variables{"key1": "value1"},
			override: Variables{"key2": "value2"},
			expected: Variables{"key1": "value1", "key2": "value2"},
		},
		{
			name:     "override base, change schema",
			base:     Variables{"key1": Variables{"key2": "value2"}},
			override: Variables{"key1": "value1"},
			expected: Variables{"key1": "value1"},
		},
		{
			name:     "merge into sub map",
			base:     Variables{"key1": Variables{"key2": "value2"}},
			override: Variables{"key1": Variables{"key3": "value3"}},
			expected: Variables{"key1": Variables{"key2": "value2", "key3": "value3"}},
		},
		{
			name:     "override sub map value",
			base:     Variables{"key1": Variables{"key2": "value2"}},
			override: Variables{"key1": Variables{"key2": "value3"}},
			expected: Variables{"key1": Variables{"key2": "value3"}},
		},
		{
			name:     "override nested sub map",
			base:     Variables{"key1": Variables{"key2": Variables{"key3": "value3"}}},
			override: Variables{"key1": Variables{"key2": Variables{"key3": "value4"}}},
			expected: Variables{"key1": Variables{"key2": Variables{"key3": "value4"}}},
		},
		{
			name:     "override nested sub map multiple levels",
			base:     Variables{"key1": Variables{"key2": Variables{"key3": "value3"}}},
			override: Variables{"key1": Variables{"key2": Variables{"key4": "value4"}}, "key5": "value5"},
			expected: Variables{"key1": Variables{"key2": Variables{"key3": "value3", "key4": "value4"}}, "key5": "value5"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := MergeVariables(tc.base, tc.override)
			assert.Equal(t, tc.expected, result)
		})
	}

}

func TestLoadSchemaURL(t *testing.T) {
	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "{\"type\": \"object\"}")
	}))
	defer testServer.Close()

	configProvider := configProviderImpl{}
	configProvider.schema = testServer.URL

	schema, err := configProvider.loadSchema()
	assert.Nil(t, err)
	assert.NotNil(t, schema)
	assert.Equal(t, map[string]any{"type": "object"}, schema)
}

func TestLoadSchema(t *testing.T) {
	testDirs := t.TempDir()

	err := os.WriteFile(testDirs+"/schema.json", []byte(`{"type": "object"}`), 0644)
	assert.Nil(t, err)

	configProvider := configProviderImpl{}
	configProvider.schema = testDirs + "/schema.json"

	schema, err := configProvider.loadSchema()
	assert.Nil(t, err)
	assert.NotNil(t, schema)
	assert.Equal(t, map[string]any{"type": "object"}, schema)
}

func TestLoadSchemaError(t *testing.T) {
	testDirs := t.TempDir()

	err := os.WriteFile(testDirs+"/schma.json", []byte(`{"type": "object"}`), 0644)
	assert.Nil(t, err)

	configProvider := configProviderImpl{}
	configProvider.schema = testDirs + "/schema.json"
	_, err = configProvider.loadSchema()
	assert.NotNil(t, err)
}

func TestValidateSchema(t *testing.T) {
	testSchema := `{
	"type": "object",
	"properties": {
		"key1": {
			"type": "string"
		}
	},
	"additionalProperties": false
}`

	testDirs := t.TempDir()

	err := os.WriteFile(testDirs+"/schema.json", []byte(testSchema), 0644)
	assert.Nil(t, err)

	configProvider := configProviderImpl{}
	configProvider.schema = "schema.json"
	configProvider.config = testDirs + "/config.yaml"

	err = configProvider.validateSchema(map[string]any{"foo": "bar"})
	assert.NotNil(t, err)
	assert.ErrorContains(t, err, "additional properties 'foo' not allowed")

	err = configProvider.validateSchema(map[string]any{"key1": "bar"})
	assert.Nil(t, err)
}

func TestConvertToInterface(t *testing.T) {
	vars := Variables{
		"key1": "value1",
		"key2": Variables{
			"key3": "value3",
		},
	}

	expected := map[string]any{
		"key1": "value1",
		"key2": map[string]any{
			"key3": "value3",
		},
	}

	result := convertToInterface(vars)
	assert.Equal(t, expected, result)
	assert.IsType(t, expected, map[string]any{})
	assert.IsType(t, expected["key2"], map[string]any{})
}

func TestPreprocessContent(t *testing.T) {
	fileContent, err := os.ReadFile("../../testdata/test.bicepparam")
	assert.Nil(t, err)

	processed, err := PreprocessContent(
		fileContent,
		map[string]any{
			"regionRG": "bahamas",
			"clusterService": map[string]any{
				"imageTag": "cs-image",
			},
		},
	)
	assert.Nil(t, err)
	testutil.CompareWithFixture(t, processed, testutil.WithExtension(".bicepparam"))
}

func TestPreprocessContentMissingKey(t *testing.T) {
	testCases := []struct {
		name       string
		content    string
		vars       map[string]any
		shouldFail bool
	}{
		{
			name:    "missing key",
			content: "foo: {{ .bar }}",
			vars: map[string]any{
				"baz": "bar",
			},
			shouldFail: true,
		},
		{
			name:    "missing nested key",
			content: "foo: {{ .bar.baz }}",
			vars: map[string]any{
				"baz": "bar",
			},
			shouldFail: true,
		},
		{
			name:    "no missing key",
			content: "foo: {{ .bar }}",
			vars: map[string]any{
				"bar": "bar",
			},
			shouldFail: false,
		},
		{
			name:    "no missing nested key",
			content: "foo: {{ .bar.baz }}",
			vars: map[string]any{
				"bar": map[string]any{
					"baz": "baz",
				},
			},
			shouldFail: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := PreprocessContent(
				[]byte(tc.content),
				tc.vars,
			)
			if tc.shouldFail {
				assert.NotNil(t, err)
			} else {
				assert.Nil(t, err)
			}
		})
	}
}
