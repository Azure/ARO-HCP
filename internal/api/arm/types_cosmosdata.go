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

package arm

import (
	"errors"
	"fmt"
	"strings"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
)

// CosmosMetadata contains the information that persisted resources must have for us to support CRUD against them.
// These are not (currently) all stored in the same place in our various types.
type CosmosMetadata struct {
	ResourceID *azcorearm.ResourceID `json:"resourceID"`
}

var (
	_ CosmosPersistable      = &CosmosMetadata{}
	_ CosmosMetadataAccessor = &CosmosMetadata{}
)

type CosmosPersistable interface {
	GetCosmosData() *CosmosMetadata
}

func (o *CosmosMetadata) GetCosmosUID() string {
	return Must(ResourceIDToCosmosID(o.ResourceID))
}

func (o *CosmosMetadata) GetPartitionKey() string {
	return strings.ToLower(o.ResourceID.SubscriptionID)
}

func (o *CosmosMetadata) GetResourceID() *azcorearm.ResourceID {
	return o.ResourceID
}

func (o *CosmosMetadata) SetResourceID(resourceID *azcorearm.ResourceID) {
	o.ResourceID = resourceID
}

func (o *CosmosMetadata) GetCosmosData() *CosmosMetadata {
	return &CosmosMetadata{
		ResourceID: o.ResourceID,
	}
}

type CosmosMetadataAccessor interface {
	GetResourceID() *azcorearm.ResourceID
	SetResourceID(*azcorearm.ResourceID)
}

func ResourceIDToCosmosID(resourceID *azcorearm.ResourceID) (string, error) {
	if resourceID == nil {
		return "", errors.New("resource ID is nil")
	}
	return ResourceIDStringToCosmosID(resourceID.String())
}

func ResourceIDStringToCosmosID(resourceID string) (string, error) {
	if len(resourceID) == 0 {
		return "", errors.New("resource ID is empty")
	}
	// cosmos uses a REST API, which means that IDs that contain slashes cause problems with URL handling.
	// We chose | because that is a delimiter that is not allowed inside of an ARM resource ID because it is a separator
	// for multiple resource IDs.
	return strings.ReplaceAll(strings.ToLower(resourceID), "/", "|"), nil
}

// DeepCopyResourceID creates a true deep copy of an azcorearm.ResourceID by
// round-tripping through its string representation. This is necessary because
// ResourceID contains unexported fields (including parent pointers) that cannot
// be copied by simple struct assignment.
func DeepCopyResourceID(id *azcorearm.ResourceID) *azcorearm.ResourceID {
	if id == nil {
		return nil
	}
	copied, err := azcorearm.ParseResourceID(id.String())
	if err != nil {
		panic(fmt.Sprintf("failed to deep copy ResourceID %q: %v", id.String(), err))
	}
	return copied
}

// Must is a helper function that takes a value and error, returns the value if no error occurred,
// or panics if an error occurred. This is useful for test setup where we don't expect errors.
func Must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}
