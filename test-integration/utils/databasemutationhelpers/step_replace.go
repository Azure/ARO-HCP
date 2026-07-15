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
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Azure/ARO-HCP/internal/api/arm"
)

type replaceStep[InternalAPIType any, InternalAPITypePointer arm.CosmosMetadataAccessorPtr[InternalAPIType]] struct {
	stepID StepID
	key    CosmosItemKey

	resources     []*InternalAPIType
	expectedError string
}

func newReplaceStep[InternalAPIType any, InternalAPITypePointer arm.CosmosMetadataAccessorPtr[InternalAPIType]](stepID StepID, stepDir fs.FS) (*replaceStep[InternalAPIType, InternalAPITypePointer], error) {
	keyBytes, err := fs.ReadFile(stepDir, "00-key.json")
	if err != nil {
		return nil, fmt.Errorf("failed to read key.json: %w", err)
	}
	var key CosmosItemKey
	if err := json.Unmarshal(keyBytes, &key); err != nil {
		return nil, fmt.Errorf("failed to unmarshal key.json: %w", err)
	}

	expectedErrorBytes, err := fs.ReadFile(stepDir, "expected-error.txt")
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("failed to read expected-error.txt: %w", err)
	}
	expectedError := strings.TrimSpace(string(expectedErrorBytes))

	resources, err := readResourcesInDir[InternalAPIType](stepDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read resource in dir: %w", err)
	}

	return &replaceStep[InternalAPIType, InternalAPITypePointer]{
		stepID:        stepID,
		key:           key,
		resources:     resources,
		expectedError: expectedError,
	}, nil
}

func (l *replaceStep[InternalAPIType, InternalAPITypePointer]) StepID() StepID {
	return l.stepID
}

func (l *replaceStep[InternalAPIType, InternalAPITypePointer]) RunTest(ctx context.Context, t *testing.T, stepInput StepInput) {
	resourceCRUDClient := NewCosmosCRUD[InternalAPIType, InternalAPITypePointer](t, stepInput.ResourcesDBClient, l.key.ResourceID.Parent, l.key.ResourceID.ResourceType)

	// Fetch the live document once so we can carry forward fields that the
	// storage layer requires every Replace caller to start from the existing
	// record: CosmosETag (for the conditional write) and InstanceVersion (so
	// PrepareForReplace can auto-increment it). Tests that exercise the
	// negative path supply their own etag/instanceVersion in the fixture and
	// we leave those values untouched.
	currentResource, err := resourceCRUDClient.Get(ctx, l.key.ResourceID.Name)
	if err != nil && !strings.Contains(err.Error(), "ERROR CODE: 404") {
		require.NoError(t, err, "failed to read existing resource before replace")
	}
	currentMD := cosmosDataFor(currentResource)

	for _, resource := range l.resources {
		if currentMD != nil {
			if md := cosmosDataFor(resource); md != nil {
				if len(md.CosmosETag) == 0 {
					md.CosmosETag = currentMD.CosmosETag
				}
				if md.InstanceVersion == 0 {
					md.InstanceVersion = currentMD.InstanceVersion
				}
			}
		}

		_, err := resourceCRUDClient.Replace(ctx, resource, nil)
		if len(l.expectedError) > 0 {
			require.ErrorContains(t, err, l.expectedError, "expected error during replace")
			return
		}
		require.NoError(t, err, "failed to replace resource")
	}
}
