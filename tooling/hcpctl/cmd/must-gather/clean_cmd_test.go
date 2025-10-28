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

package mustgather

import (
	"regexp"
	"testing"

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/assert"

	"github.com/Azure/ARO-HCP/tooling/hcpctl/cmd/must-gather/schema"

	"github.com/Azure/ARO-HCP/tooling/hcpctl/internal/testutil"
)

func TestWalkAndMatchRegexPatterns(t *testing.T) {
	patterns := []*replacement{
		{
			Regex:              regexp.MustCompile("([0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12})"),
			ReplacementPattern: "x-uid-%010d",
		},
	}

	allMatches, err := walkAndMatchRegexPatterns(testr.New(t), "../../testdata/test-config", patterns)
	assert.NoError(t, err)
	testutil.CompareWithFixture(t, allMatches, testutil.WithExtension(".txt"))
}

func TestDefaultGenerateMustGatherCleanConfig(t *testing.T) {
	opts := &CleanOptions{}
	opts.ValidatedCleanOptions = &ValidatedCleanOptions{
		RawCleanOptions: &RawCleanOptions{},
	}
	loadConfig, err := loadMustGatherCleanConfig(t.Context(), opts)
	assert.NoError(t, err)
	assert.NotNil(t, loadConfig)
}

func TestExtendConfigWithPatterns(t *testing.T) {
	loadConfig := &schema.SchemaJson{}
	allMatches := map[string]string{
		"12345678-1234-1234-1234-123456789012": "x-uid-%010d",
	}
	err := extendConfigWithPatterns(loadConfig, allMatches)
	assert.NoError(t, err)
	assert.NotNil(t, loadConfig)
	assert.Equal(t, loadConfig.Config.Obfuscate, []schema.Obfuscate{
		{
			Type: schema.ObfuscateTypeExact,
			ExactReplacements: []schema.ObfuscateExactReplacementsElem{
				{
					Original:    "12345678-1234-1234-1234-123456789012",
					Replacement: "x-uid-%010d",
				},
			},
		},
	})
}
