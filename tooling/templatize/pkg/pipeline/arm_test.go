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

package pipeline

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
)

func TestComputeResourceGroupTags(t *testing.T) {
	tests := []struct {
		name         string
		existingTags map[string]*string
		persist      bool
		expectedTags map[string]*string
		description  string
	}{
		// Empty existing tags
		{
			name:         "empty_tags_persist_true",
			existingTags: map[string]*string{},
			persist:      true,
			expectedTags: map[string]*string{
				"persist": to.Ptr("true"),
			},
			description: "Empty tags with persist=true should add persist tag",
		},
		{
			name:         "empty_tags_persist_false",
			existingTags: map[string]*string{},
			persist:      false,
			expectedTags: map[string]*string{},
			description:  "Empty tags with persist=false should remain empty",
		},

		// Nil existing tags (edge case)
		{
			name:         "nil_tags_persist_true",
			existingTags: nil,
			persist:      true,
			expectedTags: map[string]*string{
				"persist": to.Ptr("true"),
			},
			description: "Nil tags with persist=true should add persist tag",
		},
		{
			name:         "nil_tags_persist_false",
			existingTags: nil,
			persist:      false,
			expectedTags: map[string]*string{},
			description:  "Nil tags with persist=false should result in empty map",
		},

		// Existing tags with persist="true"
		{
			name: "existing_persist_true_persist_true",
			existingTags: map[string]*string{
				"persist": to.Ptr("true"),
				"env":     to.Ptr("dev"),
			},
			persist: true,
			expectedTags: map[string]*string{
				"persist": to.Ptr("true"),
				"env":     to.Ptr("dev"),
			},
			description: "Existing persist=true with persist=true should preserve persist tag",
		},
		{
			name: "existing_persist_true_persist_false",
			existingTags: map[string]*string{
				"persist": to.Ptr("true"),
				"env":     to.Ptr("dev"),
			},
			persist: false,
			expectedTags: map[string]*string{
				"persist": to.Ptr("true"), // Should be preserved (safety rule)
				"env":     to.Ptr("dev"),
			},
			description: "Existing persist=true with persist=false should preserve persist tag (safety rule)",
		},

		// Existing tags with persist="false"
		{
			name: "existing_persist_false_persist_true",
			existingTags: map[string]*string{
				"persist": to.Ptr("false"),
				"env":     to.Ptr("dev"),
			},
			persist: true,
			expectedTags: map[string]*string{
				"persist": to.Ptr("true"),
				"env":     to.Ptr("dev"),
			},
			description: "Existing persist=false with persist=true should set persist to true",
		},
		{
			name: "existing_persist_false_persist_false",
			existingTags: map[string]*string{
				"persist": to.Ptr("false"),
				"env":     to.Ptr("dev"),
			},
			persist: false,
			expectedTags: map[string]*string{
				"env": to.Ptr("dev"),
			},
			description: "Existing persist=false with persist=false should not set persist tag",
		},

		// Existing tags with persist="something_else"
		{
			name: "existing_persist_invalid_persist_true",
			existingTags: map[string]*string{
				"persist": to.Ptr("maybe"),
				"env":     to.Ptr("dev"),
			},
			persist: true,
			expectedTags: map[string]*string{
				"persist": to.Ptr("true"),
				"env":     to.Ptr("dev"),
			},
			description: "Existing persist=invalid with persist=true should set persist to true",
		},
		{
			name: "existing_persist_invalid_persist_false",
			existingTags: map[string]*string{
				"persist": to.Ptr("maybe"),
				"env":     to.Ptr("dev"),
			},
			persist: false,
			expectedTags: map[string]*string{
				"env": to.Ptr("dev"),
			},
			description: "Existing persist=invalid with persist=false should not set persist tag",
		},

		// Existing tags without persist tag
		{
			name: "no_persist_tag_persist_true",
			existingTags: map[string]*string{
				"env":     to.Ptr("dev"),
				"project": to.Ptr("aro-hcp"),
			},
			persist: true,
			expectedTags: map[string]*string{
				"env":     to.Ptr("dev"),
				"project": to.Ptr("aro-hcp"),
				"persist": to.Ptr("true"),
			},
			description: "No persist tag with persist=true should add persist tag",
		},
		{
			name: "no_persist_tag_persist_false",
			existingTags: map[string]*string{
				"env":     to.Ptr("dev"),
				"project": to.Ptr("aro-hcp"),
			},
			persist: false,
			expectedTags: map[string]*string{
				"env":     to.Ptr("dev"),
				"project": to.Ptr("aro-hcp"),
			},
			description: "No persist tag with persist=false should not add persist tag",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := computeResourceGroupTags(tt.existingTags, tt.persist)
			assert.Equal(t, tt.expectedTags, result, tt.description)
		})
	}
}
