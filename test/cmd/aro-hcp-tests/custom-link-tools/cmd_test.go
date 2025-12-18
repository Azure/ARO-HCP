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

package customlinktools

import (
	"path/filepath"
	"testing"

	"github.com/go-logr/logr"
	"github.com/go-logr/logr/testr"

	"github.com/Azure/ARO-HCP/test/util/testutil"
)

func TestGeneratedHTML(t *testing.T) {
	ctx := logr.NewContext(t.Context(), testr.New(t))
	tmpdir := t.TempDir()
	opts := Options{
		completedOptions: &completedOptions{
			TimingInputDir: "../testdata/output",
			OutputDir:      tmpdir,
		},
	}
	err := opts.Run(ctx)
	if err != nil {
		t.Fatalf("failed to run custom link tools: %v", err)
	}
	testutil.CompareFileWithFixture(t, filepath.Join(tmpdir, "custom-link-tools.html"))
}
