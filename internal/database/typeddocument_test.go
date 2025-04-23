// Copyright 2025 Microsoft Corporation
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
