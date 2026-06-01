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

	"github.com/google/uuid"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
)

// CosmosMetadata contains the information that persisted resources must have for us to support CRUD against them.
// These are not (currently) all stored in the same place in our various types.
type CosmosMetadata struct {
	ResourceID *azcorearm.ResourceID `json:"resourceID"`

	// ExistingCosmosUID exists to allow for a migration path from where we are today to a uuid based cosmosID
	// and this will be deleted afterwards.
	ExistingCosmosUID string `json:"-"`

	CosmosETag azcore.ETag `json:"etag,omitempty"`

	// InstanceVersion is a field that auto-increments every time the resource is updated.  This gives us the ability to
	// compare two instances of stored resources and determine which one is newer.  We will use this field to integrate
	// changefeeds for controllers and decide which changes are newer than the level we have already observed so that we
	// can periodically re-list all items.
	// The auto-incrementing happens automatically in the storage layer for conditional updates.
	InstanceVersion int64 `json:"instanceVersion"`

	// PartitionKey is the partition key for the CosmosDB document, it must be set before creation and must be all lowercase.
	// On the read-path, during our migration we will fill in an empty value based on the type we're reading.
	// Every type that embeds this struct must comment about what the PartitionKey is. For instance, subscriptionID, managementClusterID, etc.
	PartitionKey string `json:"partitionKey"`
}

var (
	_ CosmosPersistable      = &CosmosMetadata{}
	_ CosmosMetadataAccessor = &CosmosMetadata{}

	// cosmosDocIDUUIDNamespace was randomly created once.
	cosmosDocIDUUIDNamespace uuid.UUID
)

func init() {
	cosmosDocIDUUIDNamespace = Must(uuid.Parse("bf1ee0a1-0147-41ed-a083-d3cbbf7bea99"))
}

type CosmosPersistable interface {
	GetCosmosData() *CosmosMetadata
}

func (o *CosmosMetadata) GetCosmosUID() string {
	return Must(ResourceIDToCosmosID(o.ResourceID))
}

// GetPartitionKey returns the lowercased partition key stored on the
// metadata. The CosmosDB CRUD layer is responsible for populating this field
// on the write path (see EnsurePartitionKey) and on the read path (see the
// conversion layer's migration fallback); callers may rely on it being set
// after a successful Create/Get round-trip. The value is lowercased on the
// way out so callers do not have to do it themselves.
func (o *CosmosMetadata) GetPartitionKey() string {
	return strings.ToLower(o.PartitionKey)
}

// SetPartitionKey stores the partition key on the metadata, lowercasing the
// supplied value. Cosmos partition keys are case-sensitive; lowercasing here
// matches the convention every CRUD already uses and removes a class of
// "the value differs only in case" bugs at the store/query boundary.
func (o *CosmosMetadata) SetPartitionKey(partitionKey string) {
	o.PartitionKey = strings.ToLower(partitionKey)
}

func (o *CosmosMetadata) GetResourceID() *azcorearm.ResourceID {
	return o.ResourceID
}

func (o *CosmosMetadata) SetResourceID(resourceID *azcorearm.ResourceID) {
	o.ResourceID = resourceID
}

func (o *CosmosMetadata) GetEtag() azcore.ETag {
	return o.CosmosETag
}

func (o *CosmosMetadata) SetEtag(cosmosETag azcore.ETag) {
	o.CosmosETag = cosmosETag
}

// GetInstanceVersion returns the monotonically-increasing version counter
// stored on the document. The CRUD layer auto-increments it via SetInstanceVersion
// on every Replace (see PrepareForReplace).
func (o *CosmosMetadata) GetInstanceVersion() int64 {
	return o.InstanceVersion
}

// SetInstanceVersion overwrites the version counter. The CRUD layer is the
// only legitimate caller; tests can read it via GetInstanceVersion to assert
// the increment happened.
func (o *CosmosMetadata) SetInstanceVersion(v int64) {
	o.InstanceVersion = v
}

func (o *CosmosMetadata) GetCosmosData() *CosmosMetadata {
	return o
}

type CosmosMetadataAccessor interface {
	CosmosPersistable
	GetCosmosUID() string
	GetResourceID() *azcorearm.ResourceID
	SetResourceID(*azcorearm.ResourceID)
	GetEtag() azcore.ETag
	SetEtag(cosmosETag azcore.ETag)
	GetPartitionKey() string
	SetPartitionKey(string)
	GetInstanceVersion() int64
	SetInstanceVersion(int64)
}

// CosmosMetadataAccessorPtr constrains a type parameter to be a pointer to T
// that also implements CosmosMetadataAccessor. Generic CRUD code uses this so
// that a `*T` newObj argument is guaranteed to expose the metadata accessors
// at compile time, without runtime type assertions.
type CosmosMetadataAccessorPtr[T any] interface {
	*T
	CosmosMetadataAccessor
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

	// we predictably hash the values because there are length limitations on Azure.
	return uuid.NewSHA1(cosmosDocIDUUIDNamespace, []byte(strings.ToLower(resourceID))).String(), nil
}

// DeepCopyResourceID creates a true deep copy of an azcorearm.ResourceID by
// round-tripping through its string representation. This is necessary because
// ResourceID contains unexported fields (including parent pointers) that cannot
// be copied by simple struct assignment.
func DeepCopyResourceID(id *azcorearm.ResourceID) *azcorearm.ResourceID {
	if id == nil {
		return nil
	}
	resourceIDString := id.String()
	if len(resourceIDString) == 0 { // weird edge case.
		return &azcorearm.ResourceID{}
	}

	copied, err := azcorearm.ParseResourceID(resourceIDString)
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
