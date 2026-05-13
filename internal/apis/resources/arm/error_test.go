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

package arm

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewCloudErrorBodyFromSlice(t *testing.T) {
	const multipleErrorsMessage = "Multiple errors occurred"

	tests := []struct {
		name     string
		errors   []CloudErrorBody
		expected *CloudErrorBody
	}{
		{
			name:     "No errors",
			errors:   []CloudErrorBody{},
			expected: nil,
		},
		{
			name: "Single error",
			errors: []CloudErrorBody{
				{
					Code:    "code",
					Message: "message",
					Target:  "target",
				},
			},
			expected: &CloudErrorBody{
				Code:    "code",
				Message: "message",
				Target:  "target",
			},
		},
		{
			name: "Multiple errors",
			errors: []CloudErrorBody{
				{
					Code:    "code1",
					Message: "message1",
					Target:  "target1",
				},
				{
					Code:    "code2",
					Message: "message2",
					Target:  "target2",
				},
			},
			expected: &CloudErrorBody{
				Code:    CloudErrorCodeMultipleErrorsOccurred,
				Message: multipleErrorsMessage,
				Target:  "",
				Details: []CloudErrorBody{
					{
						Code:    "code1",
						Message: "message1",
						Target:  "target1",
					},
					{
						Code:    "code2",
						Message: "message2",
						Target:  "target2",
					},
				},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.expected, NewCloudErrorBodyFromSlice(test.errors, multipleErrorsMessage))
		})
	}
}

func TestCloudErrorBody_String(t *testing.T) {
	tests := []struct {
		name     string
		body     *CloudErrorBody
		expected string
	}{
		{
			name: "One detail",
			body: &CloudErrorBody{
				Code:    "code",
				Message: "message",
				Target:  "target",
				Details: []CloudErrorBody{
					{
						Code:    "innercode",
						Message: "innermessage",
						Target:  "innertarget",
						Details: []CloudErrorBody{},
					},
				},
			},
			expected: "code: target: message Details: innercode: innertarget: innermessage",
		},
		{
			name: "Two details",
			body: &CloudErrorBody{
				Code:    "code",
				Message: "message",
				Target:  "target",
				Details: []CloudErrorBody{
					{
						Code:    "innercode",
						Message: "innermessage",
						Target:  "innertarget",
						Details: []CloudErrorBody{},
					},
					{
						Code:    "innercode2",
						Message: "innermessage2",
						Target:  "innertarget2",
						Details: []CloudErrorBody{},
					},
				},
			},
			expected: "code: target: message Details: innercode: innertarget: innermessage, innercode2: innertarget2: innermessage2",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.expected, test.body.String())
		})
	}
}
