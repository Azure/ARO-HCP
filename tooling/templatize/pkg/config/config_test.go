package config

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
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
			result := mergeVariables(tc.base, tc.override)
			assert.Equal(t, tc.expected, result)
		})
	}

}
