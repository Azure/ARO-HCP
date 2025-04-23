package database


import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

const testResourceType = "test"
const testPropertiesValue = "foo"

type testProperties struct {
	Value string
}

func (p testProperties) GetValidTypes() []string {
	return []string{testResourceType}
}

func TestTypedDocumentMarshal(t *testing.T) {
	tests := []struct {
		name     string
		typedDoc *typedDocument
		err      string
	}{
		{
			name: "sucessful marshal",
			typedDoc: &typedDocument{
				ResourceType: testResourceType,
			},
			err: "",
		},
		{
			name:     "missing resource type",
			typedDoc: &typedDocument{},
			err:      "missing type",
		},
		{
			name: "invalid resource type",
			typedDoc: &typedDocument{
				ResourceType: "invalid",
			},
			err: "invalid type 'invalid' for testProperties",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			innerDoc := &testProperties{testPropertiesValue}
			data, err := typedDocumentMarshal[testProperties](tt.typedDoc, innerDoc)

			if tt.err != "" {
				assert.EqualError(t, err, tt.err)
			} else if assert.NoError(t, err) {
				assert.NotEmpty(t, data)
			}
		})
	}
}

func TestTypedDocumentUnmarshal(t *testing.T) {
	tests := []struct {
		name string
		data string
		err  string
	}{
		{
			name: "successful unmarshal",
			data: fmt.Sprintf("{\"resourceType\": \"%s\", \"properties\": {\"value\": \"%s\"}}", testResourceType, testPropertiesValue),
			err:  "",
		},
		{
			name: "missing resource type",
			data: fmt.Sprintf("{\"properties\": {\"value\": \"%s\"}}", testPropertiesValue),
			err:  "missing type",
		},
		{
			name: "invalid resource type",
			data: fmt.Sprintf("{\"resourceType\": \"invalid\", \"properties\": {\"value\": \"%s\"}}", testPropertiesValue),
			err:  "invalid type 'invalid' for testProperties",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			typedDoc, innerDoc, err := typedDocumentUnmarshal[testProperties]([]byte(tt.data))

			if tt.err != "" {
				assert.EqualError(t, err, tt.err)
			} else if assert.NoError(t, err) {
				assert.Equal(t, testResourceType, typedDoc.ResourceType)
				assert.Equal(t, testPropertiesValue, innerDoc.Value)
			}
		})
	}
}
