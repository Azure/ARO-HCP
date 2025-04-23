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

package generate

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"

	options "github.com/Azure/ARO-HCP/tooling/templatize/cmd"
	"github.com/Azure/ARO-HCP/tooling/templatize/internal/testutil"
)

func TestRawOptions(t *testing.T) {
	tmpdir := t.TempDir()
	opts := &RawGenerationOptions{
		RolloutOptions: &options.RawRolloutOptions{
			Region:      "uksouth",
			RegionShort: "abcde",
			Stamp:       "fghij",
			BaseOptions: &options.RawOptions{
				ConfigFile: "../../testdata/config.yaml",
				Cloud:      "public",
				DeployEnv:  "dev",
			},
		},
		Input:  "../../testdata/pipeline.yaml",
		Output: fmt.Sprintf("%s/pipeline.yaml", tmpdir),
	}
	assert.NoError(t, generate(context.Background(), opts))
	testutil.CompareFileWithFixture(t, filepath.Join(tmpdir, "pipeline.yaml"))
}
