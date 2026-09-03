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

package cosmosstorageutils

import (
	"fmt"
	"strings"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
)

const operationTimeToLive = 604800 // 7 days

// TimeToLiveForInternal returns the Cosmos DB TTL (in seconds) that a given
// internal object should be stored with, or 0 for "no TTL". This is the single
// source of truth for which document types are TTL-governed: it is used both
// when serializing documents (to stamp the ttl field) and by the Cosmos
// migration controller (to avoid re-writing ephemeral, TTL-governed documents,
// which would reset their _ts and therefore their TTL clock).
func TimeToLiveForInternal(internalObj any) int {
	switch v := internalObj.(type) {
	case *coreapi.Operation:
		// A typed nil pointer still matches this case; honor the documented
		// "nil => no TTL" contract rather than returning a TTL for it.
		if v == nil {
			return 0
		}
		return operationTimeToLive
	}
	return 0
}

func InternalToCosmosGeneric[InternalAPIType any](internalObj *InternalAPIType) (*GenericDocument[InternalAPIType], error) {
	if internalObj == nil {
		return nil, nil
	}

	metadata, ok := any(internalObj).(coreapi.CosmosMetadataAccessor)
	if !ok {
		return nil, fmt.Errorf("internalObj must be an coreapi.CosmosMetadataAccessor: %T", internalObj)
	}

	// PartitionKey must be populated on metadata by the CRUD layer (via
	// coreapi.EnsurePartitionKey) before serialization, and must already be
	// lowercased — Cosmos partition keys are case-sensitive, so writing a
	// mixed-case value here would silently fragment a partition across two
	// "equal but unequal" keys. Refuse to serialize otherwise.
	partitionKey := metadata.GetPartitionKey()
	if len(partitionKey) == 0 {
		return nil, fmt.Errorf("internalObj %T has no PartitionKey on its CosmosMetadata; the CRUD layer must call coreapi.EnsurePartitionKey before serializing", internalObj)
	}
	if partitionKey != strings.ToLower(partitionKey) {
		return nil, fmt.Errorf("internalObj %T PartitionKey %q is not lowercased; CosmosMetadata.SetPartitionKey normalizes case but the field was bypassed", internalObj, partitionKey)
	}

	cosmosObj := &GenericDocument[InternalAPIType]{
		TypedDocument: TypedDocument{
			BaseDocument: BaseDocument{
				ID: metadata.GetCosmosUID(),
			},
			PartitionKey: partitionKey,
			ResourceID:   metadata.GetResourceID(),
			ResourceType: metadata.GetResourceID().ResourceType.String(),
		},
		Content: *internalObj,
	}

	// this isn't pretty, but on balance it's a better choice so that we can share all the rest.
	// TimeToLiveForInternal is the single source of truth for which document
	// types are TTL-governed (see also the Cosmos migration controller).
	// TODO Add TTL to cosmosMetadata
	cosmosObj.TimeToLive = TimeToLiveForInternal(any(internalObj))

	return cosmosObj, nil
}

func CosmosGenericToInternal[InternalAPIType any](cosmosObj *GenericDocument[InternalAPIType]) (*InternalAPIType, error) {
	if cosmosObj == nil {
		return nil, nil
	}

	ret, ok := any(&cosmosObj.Content).(coreapi.CosmosMetadataAccessor)
	if !ok {
		return nil, fmt.Errorf("internalObj must be an coreapi.CosmosMetadataAccessor: %T", cosmosObj)
	}
	cosmosData := ret.(coreapi.CosmosPersistable).GetCosmosData()
	cosmosData.ExistingCosmosUID = cosmosObj.ID
	ret.SetEtag(cosmosObj.CosmosETag)
	// Legacy documents predating the InstanceVersion field land here with
	// the zero value. Treat that as "version 1" so a subsequent Get → modify
	// → Replace path round-trips without tripping PrepareForReplace, which
	// only allows InstanceVersion==0 to flag fresh-built docs the caller
	// forgot to deep-copy from the existing record.
	if cosmosData.InstanceVersion == 0 {
		cosmosData.InstanceVersion = 1
	}

	// Restore the partition key from the typed document. Documents written
	// after PR #5094 always carry one; for legacy documents written under the
	// old envelope (where the typed-doc PartitionKey was either absent or held
	// the subscription ID regardless of type), fall back to the type-aware
	// derivation so reads keep working during the migration.
	if pk := cosmosObj.PartitionKey; len(pk) != 0 {
		ret.SetPartitionKey(pk)
	} else if pk := DerivePartitionKey(&cosmosObj.Content); len(pk) != 0 {
		ret.SetPartitionKey(pk)
	}

	if defaulter, ok := ret.(Defaulter); ok {
		defaulter.EnsureDefaults()
	}

	// this isn't pretty, but on balance it's a better choice so that we can share all the rest.
	switch castObj := any(ret).(type) {
	case *coreapi.Subscription:
		if castObj.CosmosMetadata.ResourceID == nil && castObj.ResourceID != nil {
			castObj.CosmosMetadata.ResourceID = castObj.ResourceID
		}
		if castObj.CosmosMetadata.ResourceID == nil && cosmosObj.ResourceID != nil {
			castObj.CosmosMetadata.ResourceID = cosmosObj.ResourceID
		}
		castObj.LastUpdated = cosmosObj.CosmosTimestamp
	case coreapi.Subscription:
		castObj.LastUpdated = cosmosObj.CosmosTimestamp
	}

	if ret.GetResourceID() == nil {
		if cosmosObj.ResourceID != nil {
			ret.SetResourceID(cosmosObj.ResourceID)
		} else {
			return nil, fmt.Errorf("internalObj is missing a resourceID: %T: %q", cosmosObj, cosmosObj.ID)
		}
	}

	return &cosmosObj.Content, nil
}

type Defaulter interface {
	EnsureDefaults()
}

// objectPartitionKeyDerivers is the ordered registry that DerivePartitionKey
// walks. Most specific shape first (the kube-applier deriver matches only
// documents that expose a ManagementCluster on their spec), then the fleet
// types, with the catch-all subscription scheme last. Each deriver's
// PartitionKeyFromObject returns an error when its policy doesn't apply, so
// the iteration falls through to the next candidate.
var objectPartitionKeyDerivers = []PartitionKeyDeriver{
	KubeApplierPartitionKeyDeriver{},
	ProviderNamespacePartitionKeyDeriver{},
	FleetPartitionKeyDeriver{},
	SubscriptionPartitionKeyDeriver{},
}

// DerivePartitionKey computes the lowercased partition key for an internal
// object from its type. The CRUD layer is the canonical source of partition
// keys (the container knows its own rule), so this exists for two callers:
//
//   - The read path's migration fallback, when a stored document predates the
//     PartitionKey field on the envelope.
//   - The in-memory mock CRUD, which is generic over every container type and
//     would otherwise have to plumb a per-container PK through every helper.
//
// Returns "" when no derivation is possible — the caller should treat that
// as a programming error.
func DerivePartitionKey[InternalAPIType any](internalObj *InternalAPIType) string {
	for _, d := range objectPartitionKeyDerivers {
		if key, err := d.PartitionKeyFromObject(internalObj); err == nil && len(key) > 0 {
			return key
		}
	}
	return ""
}
