package database

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

import (
	"fmt"
	"testing"
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
			if err != nil {
				if err.Error() != tt.err {
					t.Errorf("unexpected error: %v", err)
				}
			} else if tt.err != "" {
				t.Error("expected error but got none")
			} else if len(data) == 0 {
				t.Error("marshalled data is empty")
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
			if err != nil {
				if err.Error() != tt.err {
					t.Errorf("unexpected error: %v", err)
				}
			} else if tt.err != "" {
				t.Error("expected error but got none")
			} else {
				if typedDoc.ResourceType != testResourceType {
					t.Errorf("expected resourceType '%s' but got '%s'", testResourceType, typedDoc.ResourceType)
				}
				if innerDoc.Value != testPropertiesValue {
					t.Errorf("expected value '%s' but got '%s'", testPropertiesValue, innerDoc.Value)
				}
			}
		})
	}
}
