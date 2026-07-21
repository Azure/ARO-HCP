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

package internal

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestReplaceImageDigest(t *testing.T) {
	cfg := map[string]any{
		"hypershift": map[string]any{
			"image": map[string]any{
				"digest": "sha256:aaa",
			},
			"sharedIngressImage": map[string]any{
				"digest": "sha256:bbb",
			},
		},
		"other": map[string]any{
			"digest": "sha256:ccc",
		},
	}

	result := ReplaceImageDigest(cfg)

	assert.Equal(t, "sha256:1234567890", result["hypershift"].(map[string]any)["image"].(map[string]any)["digest"])
	assert.Equal(t, "sha256:1234567890", result["hypershift"].(map[string]any)["sharedIngressImage"].(map[string]any)["digest"])
	assert.Equal(t, "sha256:1234567890", result["other"].(map[string]any)["digest"])
}

func TestReplaceImageDigestExcluding(t *testing.T) {
	cfg := map[string]any{
		"hypershift": map[string]any{
			"image": map[string]any{
				"digest": "sha256:aaa",
			},
			"sharedIngressImage": map[string]any{
				"digest": "sha256:bbb",
			},
		},
		"other": map[string]any{
			"digest": "sha256:ccc",
		},
	}

	result := ReplaceImageDigestExcluding(cfg, []string{
		"hypershift.sharedIngressImage.digest",
	})

	assert.Equal(t, "sha256:1234567890", result["hypershift"].(map[string]any)["image"].(map[string]any)["digest"])
	assert.Equal(t, "sha256:bbb", result["hypershift"].(map[string]any)["sharedIngressImage"].(map[string]any)["digest"])
	assert.Equal(t, "sha256:1234567890", result["other"].(map[string]any)["digest"])
}
