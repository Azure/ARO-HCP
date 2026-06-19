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
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type cosmosCompare struct {
	stepID StepID

	// expectedContent and expectedFilenames stay index-aligned so UPDATE mode
	// can map each parsed fixture back to its on-disk source file.
	expectedContent   []*database.TypedDocument
	expectedFilenames []string
}

func NewCosmosCompareStep(stepID StepID, stepDir fs.FS) (*cosmosCompare, error) {
	expectedContent, expectedFilenames, err := readResourcesAndFilenamesInDir[database.TypedDocument](stepDir)
	if err != nil {
		return nil, utils.TrackError(err)
	}

	return &cosmosCompare{
		stepID:            stepID,
		expectedContent:   expectedContent,
		expectedFilenames: expectedFilenames,
	}, nil
}

var _ IntegrationTestStep = &cosmosCompare{}

func (l *cosmosCompare) StepID() StepID {
	return l.stepID
}

func (l *cosmosCompare) RunTest(ctx context.Context, t *testing.T, stepInput StepInput) {
	// Use the DocumentLister interface (works with both Cosmos and mock)
	allActual, err := stepInput.DocumentLister.ListAllDocuments(ctx)
	require.NoError(t, err)

	if updateMode() {
		l.runUpdate(t, allActual)
		return
	}

	// Index actuals by typedDocumentKey so each expected document matches at
	// most one actual. Diffs are only computed for the matched pair, which keeps
	// failures readable instead of dumping an N*M diff of every actual.
	actualByKey := map[string]*database.TypedDocument{}
	for _, currActual := range allActual {
		key := typedDocumentKey(currActual)
		if key == "" {
			continue
		}
		actualByKey[strings.ToLower(key)] = currActual
	}

	for _, currExpected := range l.expectedContent {
		key := typedDocumentKey(currExpected)
		if key == "" {
			t.Errorf("expected document has no usable key (resourceID or operation externalId/request); cannot match:\n%v", stringifyResource(currExpected))
			continue
		}
		currActual, ok := actualByKey[strings.ToLower(key)]
		if !ok {
			t.Errorf("did not find document with key %s:\n%v", key, stringifyResource(currExpected))
			continue
		}
		diff, equals := ResourceInstanceEquals(t, currExpected, currActual)
		if !equals {
			t.Log(diff)
			t.Errorf("document with key %s did not match expected", key)
		}
	}
}

// runUpdate rewrites the fixture directory to match allActual: existing files
// whose resourceID is still in actuals are overwritten, files whose resourceID
// disappeared are deleted, and actuals with no existing file get a new one
// named after a sanitized resourceID.
func (l *cosmosCompare) runUpdate(t *testing.T, allActual []*database.TypedDocument) {
	updateDir := resolveStepUpdateDir(t, l.stepID)
	if updateDir == "" {
		t.Fatalf("UPDATE=true set but cosmosCompare step %s has no resolvable on-disk fixture dir", l.stepID)
		return
	}

	actualByKey := map[string]*database.TypedDocument{}
	for _, currActual := range allActual {
		key := typedDocumentKey(currActual)
		if key == "" {
			continue
		}
		actualByKey[strings.ToLower(key)] = currActual
	}

	// Map each existing fixture file to its typedDocumentKey, then either
	// overwrite the file (key still in actuals) or delete it.
	usedActualKeys := map[string]struct{}{}
	for i, currExpected := range l.expectedContent {
		filename := l.expectedFilenames[i]
		if filename == "" {
			continue
		}
		fullPath := filepath.Join(updateDir, filename)
		key := typedDocumentKey(currExpected)
		if key == "" {
			t.Logf("UPDATE: removing fixture without a usable key: %s", fullPath)
			require.NoError(t, os.Remove(fullPath))
			continue
		}
		lcKey := strings.ToLower(key)
		currActual, ok := actualByKey[lcKey]
		if !ok {
			t.Logf("UPDATE: removing fixture not present in actual: %s", fullPath)
			require.NoError(t, os.Remove(fullPath))
			continue
		}
		usedActualKeys[lcKey] = struct{}{}
		// Skip the rewrite when the comparator already considers them equal
		// — only stripped fields differ, so the on-disk fixture is fine.
		if _, equals := ResourceInstanceEquals(t, currExpected, currActual); equals {
			continue
		}
		out, err := marshalCanonical(currActual)
		require.NoError(t, err, "marshal actual for %s", fullPath)
		require.NoError(t, os.WriteFile(fullPath, append(out, '\n'), 0644), "write %s", fullPath)
	}

	// Anything left in actuals had no matching fixture file. Create one.
	for lcKey, currActual := range actualByKey {
		if _, used := usedActualKeys[lcKey]; used {
			continue
		}
		filename := sanitizeForFilename(currActual.ResourceID.String()) + ".json"
		fullPath := filepath.Join(updateDir, filename)
		out, err := marshalCanonical(currActual)
		require.NoError(t, err, "marshal new actual for %s", fullPath)
		t.Logf("UPDATE: creating fixture for %s: %s", currActual.ResourceID, fullPath)
		require.NoError(t, os.WriteFile(fullPath, append(out, '\n'), 0644), "write %s", fullPath)
	}
}
