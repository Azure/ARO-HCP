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
	"fmt"
	"io/fs"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Azure/ARO-HCP/internal/api/arm"
)

// replaceWithETagStep reads the current resource to get its etag, then performs a replace
// with the updated resource data and the current etag. This tests the positive case
// for etag-based conditional replace.
type replaceWithETagStep[InternalAPIType any] struct {
	stepID StepID
	key    CosmosItemKey

	resources []*InternalAPIType
}

func newReplaceWithETagStep[InternalAPIType any](stepID StepID, stepDir fs.FS) (*replaceWithETagStep[InternalAPIType], error) {
	keyBytes, err := fs.ReadFile(stepDir, "00-key.json")
	if err != nil {
		return nil, fmt.Errorf("failed to read key.json: %w", err)
	}
	var key CosmosItemKey
	if err := json.Unmarshal(keyBytes, &key); err != nil {
		return nil, fmt.Errorf("failed to unmarshal key.json: %w", err)
	}

	resources, err := readResourcesInDir[InternalAPIType](stepDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read resource in dir: %w", err)
	}

	return &replaceWithETagStep[InternalAPIType]{
		stepID:    stepID,
		key:       key,
		resources: resources,
	}, nil
}

var _ IntegrationTestStep = &replaceWithETagStep[any]{}

func (l *replaceWithETagStep[InternalAPIType]) StepID() StepID {
	return l.stepID
}

func (l *replaceWithETagStep[InternalAPIType]) RunTest(ctx context.Context, t *testing.T, stepInput StepInput) {
	resourceCRUDClient := NewCosmosCRUD[InternalAPIType](t, stepInput.ResourcesDBClient, l.key.ResourceID.Parent, l.key.ResourceID.ResourceType)

	// First, read the current resource to get its CosmosMetadata (etag plus
	// instance version). The Replace path requires both: a matching etag for
	// the conditional write, and a non-zero instance version so PrepareForReplace
	// can auto-increment it.
	currentResource, err := resourceCRUDClient.Get(ctx, l.key.ResourceID.Name)
	require.NoError(t, err, "failed to get current resource")
	currentMD := cosmosDataFor(currentResource)
	require.NotNil(t, currentMD, "current resource should expose CosmosMetadata")
	require.NotEmpty(t, currentMD.CosmosETag, "current resource should have an etag")
	require.NotZero(t, currentMD.InstanceVersion, "current resource should have a non-zero InstanceVersion")

	for _, resource := range l.resources {
		// Carry the live etag and instance version onto the resource we're
		// about to replace so the storage-layer prechecks pass.
		if md := cosmosDataFor(resource); md != nil {
			md.CosmosETag = currentMD.CosmosETag
			md.InstanceVersion = currentMD.InstanceVersion
		}

		_, err := resourceCRUDClient.Replace(ctx, resource, nil)
		require.NoError(t, err, "replace with current etag should succeed")
	}
}

// cosmosDataFor returns the embedded *arm.CosmosMetadata from a resource that
// implements arm.CosmosPersistable. Returns nil if the value doesn't satisfy
// the interface (no resource in the suite is expected to fall in that case;
// the nil return preserves the previous getETagFromResource behavior).
func cosmosDataFor(resource any) *arm.CosmosMetadata {
	if p, ok := resource.(arm.CosmosPersistable); ok {
		return p.GetCosmosData()
	}
	return nil
}
