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

package databasemutationhelpers

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Azure/ARO-HCP/internal/utils"
)

type loadCosmosStep struct {
	stepID StepID

	// contents and filenames are index-aligned so UPDATE mode can rewrite the
	// original on-disk file with the roundtripped bytes.
	contents  [][]byte
	filenames []string
}

func NewLoadCosmosStep(stepID StepID, stepDir fs.FS) (*loadCosmosStep, error) {
	contents, filenames, err := readRawBytesAndFilenamesInDir(stepDir)
	if err != nil {
		return nil, utils.TrackError(err)
	}

	return &loadCosmosStep{
		stepID:    stepID,
		contents:  contents,
		filenames: filenames,
	}, nil
}

var _ IntegrationTestStep = &loadCosmosStep{}

func (l *loadCosmosStep) StepID() StepID {
	return l.stepID
}

func (l *loadCosmosStep) RunTest(ctx context.Context, t *testing.T, stepInput StepInput) {
	if updateMode() {
		l.applyUpdate(t)
	}

	for _, content := range l.contents {
		// Use the ContentLoader interface (works with both Cosmos and mock)
		err := stepInput.ContentLoader.LoadContent(ctx, content)
		require.NoError(t, err, "failed to load cosmos content: %v", string(content))
	}
}

// applyUpdate roundtrips every fixture through CosmosToInternal +
// InternalToCosmos for its resourceType and writes the result back to disk,
// then updates the in-memory l.contents so the subsequent LoadContent calls
// load the refreshed bytes.
func (l *loadCosmosStep) applyUpdate(t *testing.T) {
	updateDir := resolveStepUpdateDir(t, l.stepID)
	if updateDir == "" {
		t.Fatalf("UPDATE=true set but loadCosmos step %s has no resolvable on-disk fixture dir", l.stepID)
		return
	}
	for i, content := range l.contents {
		filename := l.filenames[i]
		if filename == "" {
			continue
		}
		out, err := roundtripCosmosBytes(content)
		require.NoError(t, err, "UPDATE roundtrip failed for %s", filename)
		fullPath := filepath.Join(updateDir, filename)
		require.NoError(t, os.WriteFile(fullPath, append(out, '\n'), 0644), "write %s", fullPath)
		l.contents[i] = out
	}
}
