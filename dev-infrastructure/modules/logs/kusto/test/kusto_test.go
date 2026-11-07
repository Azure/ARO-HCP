// Copyright 2026 Microsoft Corporation
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

package kustotest

import (
	"io/fs"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestKustoFunctions(t *testing.T) {
	entries, err := fs.ReadDir(artifacts, "artifacts")
	require.NoError(t, err, "failed to read artifacts directory")

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		t.Run(name, func(t *testing.T) {
			suiteDir, err := fs.Sub(artifacts, "artifacts/"+name)
			require.NoError(t, err, "failed to sub into %s suite", name)
			RunFunctionTests(t, suiteDir)
		})
	}
}
