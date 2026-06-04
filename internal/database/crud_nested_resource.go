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

package database

import (
	"context"
	"fmt"
	"path"
	"strings"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/api/fleet"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplier"
)

type ResourceCRUD[InternalAPIType any, InternalAPITypePointer arm.CosmosMetadataAccessorPtr[InternalAPIType]] interface {
	GetByID(ctx context.Context, cosmosID string) (*InternalAPIType, error)
	Get(ctx context.Context, resourceID string) (*InternalAPIType, error)
	List(ctx context.Context, opts *DBClientListResourceDocsOptions) (DBClientIterator[InternalAPIType], error)
	Create(ctx context.Context, newObj *InternalAPIType, options *azcosmos.ItemOptions) (*InternalAPIType, error)
	Replace(ctx context.Context, newObj *InternalAPIType, options *azcosmos.ItemOptions) (*InternalAPIType, error)
	Delete(ctx context.Context, resourceID string) error

	AddCreateToTransaction(ctx context.Context, transaction DBTransaction, newObj *InternalAPIType, opts *azcosmos.TransactionalBatchItemOptions) (string, error)
	AddReplaceToTransaction(ctx context.Context, transaction DBTransaction, newObj *InternalAPIType, opts *azcosmos.TransactionalBatchItemOptions) (string, error)
}

type ValidatingResourceCRUD[InternalAPIType any, InternalAPITypePointer arm.CosmosMetadataAccessorPtr[InternalAPIType]] interface {
	GetByID(ctx context.Context, cosmosID string) (*InternalAPIType, error)
	Get(ctx context.Context, resourceID string) (*InternalAPIType, error)
	List(ctx context.Context, opts *DBClientListResourceDocsOptions) (DBClientIterator[InternalAPIType], error)
	Create(ctx context.Context, newObj *InternalAPIType, options *azcosmos.ItemOptions) (*InternalAPIType, error)
	Replace(ctx context.Context, newObj *InternalAPIType, oldObj *InternalAPIType, options *azcosmos.ItemOptions) (*InternalAPIType, error)
	Delete(ctx context.Context, resourceID string) error
}

// PartitionKeyDeriver computes the Cosmos partition key for documents accessed
// through a CRUD scope. Implementations capture the partitioning policy of a
// container: ARO resource containers partition by subscription ID, the fleet
// container partitions by top-level resource name, the kube-applier container
// partitions by management cluster name. Injecting the deriver lets a single
// CRUD type serve all three schemes.
//
// PartitionKey is the parent-scoped form used by CRUD operations: the two
// arguments cover both list/GetByID (resourceName is "") and per-item
// operations (resourceName is the leaf name). Implementations decide which
// arguments are load-bearing.
//
// PartitionKeyFromObject derives the partition key directly from a fully-formed
// object. It is used by the read-path migration fallback and by the in-memory
// mock CRUD's generic helpers. Implementations return an error when the
// object's shape isn't appropriate for the deriver's policy (so a registry of
// derivers can be tried in order to find the one that applies).
type PartitionKeyDeriver interface {
	PartitionKey(parentResourceID *azcorearm.ResourceID, resourceName string) (string, error)
	PartitionKeyFromObject(obj any) (string, error)
}

// SubscriptionPartitionKeyDeriver derives the partition key from the
// subscription ID embedded in the resource hierarchy. This is the policy
// used by every ARO resource container. For subscription-scoped resources
// the parent is nil and the resource name *is* the subscription ID.
type SubscriptionPartitionKeyDeriver struct{}

func (SubscriptionPartitionKeyDeriver) PartitionKey(parentResourceID *azcorearm.ResourceID, resourceName string) (string, error) {
	if parentResourceID == nil {
		return strings.ToLower(resourceName), nil
	}
	if len(parentResourceID.SubscriptionID) == 0 {
		return "", fmt.Errorf("subscriptionID is required")
	}
	return strings.ToLower(parentResourceID.SubscriptionID), nil
}

func (SubscriptionPartitionKeyDeriver) PartitionKeyFromObject(obj any) (string, error) {
	md, ok := obj.(arm.CosmosMetadataAccessor)
	if !ok {
		return "", fmt.Errorf("subscription partitioning requires an arm.CosmosMetadataAccessor, got %T", obj)
	}
	rid := md.GetResourceID()
	if rid == nil {
		return "", fmt.Errorf("subscription partitioning requires a non-nil resourceID")
	}
	if len(rid.SubscriptionID) == 0 {
		return "", fmt.Errorf("subscription partitioning requires a non-empty subscriptionID on the resource")
	}
	return strings.ToLower(rid.SubscriptionID), nil
}

// ResourceIDBuilder constructs a full Azure resource ID from a parent scope, a
// resource type, and an optional leaf name. Implementations capture the
// path-construction policy of a container: ARO resource containers honor the
// Microsoft.Resources → provider-namespace transition; fleet and kube-applier
// containers use a simpler parent/type/name concatenation. resourceName is
// empty when the caller wants the *parent path* of all items (used by List).
type ResourceIDBuilder interface {
	BuildResourceID(parentResourceID *azcorearm.ResourceID, resourceType azcorearm.ResourceType, resourceName string) (*azcorearm.ResourceID, error)
}

// FleetPartitionKeyDeriver partitions by the lowercased name of the root
// ancestor of the resource hierarchy — i.e. by stamp name. For top-level
// resources (nil parent) the resource name itself IS the partition key.
// List/GetByID with neither a parent nor a resource name to fall back on
// returns an error. This is the policy used by the fleet container.
type FleetPartitionKeyDeriver struct{}

func (FleetPartitionKeyDeriver) PartitionKey(parentResourceID *azcorearm.ResourceID, resourceName string) (string, error) {
	if pk := topLevelResourceName(parentResourceID); len(pk) > 0 {
		return pk, nil
	}
	if len(resourceName) == 0 {
		return "", fmt.Errorf("partition key cannot be derived: no parent ancestor and no resource name")
	}
	return strings.ToLower(resourceName), nil
}

func (FleetPartitionKeyDeriver) PartitionKeyFromObject(obj any) (string, error) {
	switch obj.(type) {
	case *fleet.Stamp, *fleet.ManagementCluster:
		// only the fleet types live in the fleet container
	default:
		return "", fmt.Errorf("fleet partitioning does not apply to %T", obj)
	}
	md, ok := obj.(arm.CosmosMetadataAccessor)
	if !ok {
		return "", fmt.Errorf("fleet partitioning requires an arm.CosmosMetadataAccessor, got %T", obj)
	}
	if pk := topLevelResourceName(md.GetResourceID()); len(pk) > 0 {
		return pk, nil
	}
	return "", fmt.Errorf("fleet partitioning could not derive a key from %T's resourceID", obj)
}

// topLevelResourceName walks a resource ID to its root ancestor and returns
// its lowercased name. Returns "" if rid is nil or carries no name.
func topLevelResourceName(rid *azcorearm.ResourceID) string {
	if rid == nil {
		return ""
	}
	curr := rid
	for curr.Parent != nil && len(curr.Parent.Name) > 0 {
		curr = curr.Parent
	}
	return strings.ToLower(curr.Name)
}

// KubeApplierPartitionKeyDeriver derives the partition key used by the
// kube-applier container, which is partitioned by the lowercased management
// cluster resourceID. For CRUD-scope operations the per-container instance
// carries the management cluster's resourceID directly; for per-object
// derivation (read-path migration fallback, mock CRUD) we read the cluster
// off the *Desire's spec.managementCluster.
type KubeApplierPartitionKeyDeriver struct {
	ManagementClusterResourceID *azcorearm.ResourceID
}

func (d KubeApplierPartitionKeyDeriver) PartitionKey(_ *azcorearm.ResourceID, _ string) (string, error) {
	if d.ManagementClusterResourceID == nil {
		return "", fmt.Errorf("kube-applier partitioning requires a non-nil ManagementClusterResourceID")
	}
	return strings.ToLower(d.ManagementClusterResourceID.String()), nil
}

func (KubeApplierPartitionKeyDeriver) PartitionKeyFromObject(obj any) (string, error) {
	mc, ok := obj.(kubeapplier.ManagementClusterAccessor)
	if !ok {
		return "", fmt.Errorf("kube-applier partitioning requires a kubeapplier.ManagementClusterAccessor, got %T", obj)
	}
	rid := mc.GetManagementCluster()
	if rid == nil {
		return "", fmt.Errorf("kube-applier partitioning requires a non-nil ManagementCluster on the object")
	}
	return strings.ToLower(rid.String()), nil
}

// ClusterNestedResourceIDBuilder is the default ARO-resource path policy: a
// nil parent yields a /subscriptions/<name> path; a parent in the
// Microsoft.Resources namespace transitions via /providers/<namespace>; deeper
// parents (already in the provider namespace, like an HCP cluster or node
// pool) must include a resourceGroup. Use this for any container whose layout
// is rooted in the standard ARM subscriptions/resourceGroups scheme.
type ClusterNestedResourceIDBuilder struct{}

func (ClusterNestedResourceIDBuilder) BuildResourceID(parentResourceID *azcorearm.ResourceID, resourceType azcorearm.ResourceType, resourceName string) (*azcorearm.ResourceID, error) {
	if parentResourceID == nil {
		return arm.ToSubscriptionResourceID(resourceName)
	}

	if len(parentResourceID.SubscriptionID) == 0 {
		return nil, fmt.Errorf("subscriptionID is required")
	}
	parts := []string{parentResourceID.String()}

	if !strings.EqualFold(parentResourceID.ResourceType.Namespace, api.ProviderNamespace) {
		if len(resourceName) == 0 {
			// in this case, adding the actual provider type results in an illegal resourceID
			// for instance /subscriptions/0465bc32-c654-41b8-8d87-9815d7abe8f6/resourceGroups/some-resource-group/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters does not parse
			resourcePathString := path.Join(parts...)
			return azcorearm.ParseResourceID(resourcePathString)
		}

		parts = append(parts,
			"providers",
			resourceType.Namespace,
		)

	} else {
		// for non-top level resources, we must have a resourceGroup
		if len(parentResourceID.ResourceGroupName) == 0 {
			return nil, fmt.Errorf("resourceGroup is required")
		}
	}
	parts = append(parts, resourceType.Types[len(resourceType.Types)-1])

	if len(resourceName) > 0 {
		parts = append(parts, resourceName)
	}

	resourcePathString := path.Join(parts...)
	return azcorearm.ParseResourceID(resourcePathString)
}

// FleetResourceIDBuilder builds resource IDs for the fleet container, which
// lives outside the ARM subscriptions/resourceGroups hierarchy. A nil parent
// yields a top-level path "/providers/<full-type>/<name>" (e.g. a stamp); a
// non-nil parent extends with "/<leaf-type>/<name>". The whole path is
// lowercased.
type FleetResourceIDBuilder struct{}

func (FleetResourceIDBuilder) BuildResourceID(parentResourceID *azcorearm.ResourceID, resourceType azcorearm.ResourceType, resourceName string) (*azcorearm.ResourceID, error) {
	var base string
	if parentResourceID != nil {
		base = parentResourceID.String() + "/" + resourceType.Types[len(resourceType.Types)-1]
	} else {
		base = "/providers/" + resourceType.String()
	}
	if len(resourceName) > 0 {
		base += "/" + resourceName
	}
	return azcorearm.ParseResourceID(strings.ToLower(base))
}

type nestedCosmosResourceCRUD[InternalAPIType any, InternalAPITypePointer arm.CosmosMetadataAccessorPtr[InternalAPIType], CosmosAPIType any] struct {
	containerClient *azcosmos.ContainerClient

	// parentResourceID is relative to the storage we're using.  it can be as high as a subscription and as low as we go.
	// resources directly under a subscription or resourcegroup are handled a little specially when computing a resourceIDPath.
	parentResourceID    *azcorearm.ResourceID
	resourceType        azcorearm.ResourceType
	partitionKeyDeriver PartitionKeyDeriver
	resourceIDBuilder   ResourceIDBuilder
}

var _ ResourceCRUD[api.HCPOpenShiftClusterNodePool, *api.HCPOpenShiftClusterNodePool] = &nestedCosmosResourceCRUD[api.HCPOpenShiftClusterNodePool, *api.HCPOpenShiftClusterNodePool, GenericDocument[api.HCPOpenShiftClusterNodePool]]{}

// NewCosmosResourceCRUD constructs a CRUD using the subscription-ID partition
// key policy and the standard ARM-style path builder. For containers that
// partition or build paths differently use NewCosmosResourceCRUDWithPartitionKey
// or NewCosmosResourceCRUDWithStrategies.
func NewCosmosResourceCRUD[InternalAPIType any, InternalAPITypePointer arm.CosmosMetadataAccessorPtr[InternalAPIType], CosmosAPIType any](
	containerClient *azcosmos.ContainerClient, parentResourceID *azcorearm.ResourceID, resourceType azcorearm.ResourceType) *nestedCosmosResourceCRUD[InternalAPIType, InternalAPITypePointer, CosmosAPIType] {

	return NewCosmosResourceCRUDWithStrategies[InternalAPIType, InternalAPITypePointer, CosmosAPIType](
		containerClient, parentResourceID, resourceType, SubscriptionPartitionKeyDeriver{}, ClusterNestedResourceIDBuilder{})
}

// NewCosmosResourceCRUDWithPartitionKey constructs a CRUD with a caller-chosen
// partition key policy and the standard ARM-style path builder.
func NewCosmosResourceCRUDWithPartitionKey[InternalAPIType any, InternalAPITypePointer arm.CosmosMetadataAccessorPtr[InternalAPIType], CosmosAPIType any](
	containerClient *azcosmos.ContainerClient, parentResourceID *azcorearm.ResourceID, resourceType azcorearm.ResourceType, partitionKeyDeriver PartitionKeyDeriver) *nestedCosmosResourceCRUD[InternalAPIType, InternalAPITypePointer, CosmosAPIType] {

	return NewCosmosResourceCRUDWithStrategies[InternalAPIType, InternalAPITypePointer, CosmosAPIType](
		containerClient, parentResourceID, resourceType, partitionKeyDeriver, ClusterNestedResourceIDBuilder{})
}

// NewCosmosResourceCRUDWithStrategies constructs a CRUD with caller-chosen
// partition-key and resource-ID-path policies. Use this to back containers
// whose layout deviates from the standard ARO scheme (fleet, kube-applier).
func NewCosmosResourceCRUDWithStrategies[InternalAPIType any, InternalAPITypePointer arm.CosmosMetadataAccessorPtr[InternalAPIType], CosmosAPIType any](
	containerClient *azcosmos.ContainerClient, parentResourceID *azcorearm.ResourceID, resourceType azcorearm.ResourceType, partitionKeyDeriver PartitionKeyDeriver, resourceIDBuilder ResourceIDBuilder) *nestedCosmosResourceCRUD[InternalAPIType, InternalAPITypePointer, CosmosAPIType] {

	return &nestedCosmosResourceCRUD[InternalAPIType, InternalAPITypePointer, CosmosAPIType]{
		containerClient:     containerClient,
		parentResourceID:    parentResourceID,
		resourceType:        resourceType,
		partitionKeyDeriver: partitionKeyDeriver,
		resourceIDBuilder:   resourceIDBuilder,
	}
}

func (d *nestedCosmosResourceCRUD[InternalAPIType, InternalAPITypePointer, CosmosAPIType]) makeResourceIDPath(resourceName string) (*azcorearm.ResourceID, error) {
	return d.resourceIDBuilder.BuildResourceID(d.parentResourceID, d.resourceType, resourceName)
}

func (d *nestedCosmosResourceCRUD[InternalAPIType, InternalAPITypePointer, CosmosAPIType]) GetByID(ctx context.Context, cosmosID string) (*InternalAPIType, error) {
	if strings.ToLower(cosmosID) != cosmosID {
		return nil, fmt.Errorf("cosmosID must be lowercase, not: %q", cosmosID)
	}
	partitionKey, err := d.partitionKeyDeriver.PartitionKey(d.parentResourceID, "")
	if err != nil {
		return nil, err
	}

	return getByItemID[InternalAPIType, CosmosAPIType](ctx, d.containerClient, partitionKey, cosmosID)
}

func (d *nestedCosmosResourceCRUD[InternalAPIType, InternalAPITypePointer, CosmosAPIType]) Get(ctx context.Context, resourceID string) (*InternalAPIType, error) {
	completeResourceID, err := d.makeResourceIDPath(resourceID)
	if err != nil {
		return nil, fmt.Errorf("failed to make ResourceID path for '%s': %w", resourceID, err)
	}
	partitionKey, err := d.partitionKeyDeriver.PartitionKey(d.parentResourceID, resourceID)
	if err != nil {
		return nil, err
	}

	return get[InternalAPIType, CosmosAPIType](ctx, d.containerClient, partitionKey, completeResourceID)
}

func (d *nestedCosmosResourceCRUD[InternalAPIType, InternalAPITypePointer, CosmosAPIType]) List(ctx context.Context, options *DBClientListResourceDocsOptions) (DBClientIterator[InternalAPIType], error) {
	partitionKey, err := d.partitionKeyDeriver.PartitionKey(d.parentResourceID, "")
	if err != nil {
		return nil, err
	}

	if d.parentResourceID == nil {
		// Top-level list: no path prefix to scope by, only the resource type
		// filter. The deriver returned whatever partition key (typically "")
		// makes this a cross-partition query on its container.
		return list[InternalAPIType, CosmosAPIType](ctx, d.containerClient, partitionKey, &d.resourceType, nil, options, false)
	}

	prefix, err := d.makeResourceIDPath("")
	if err != nil {
		return nil, fmt.Errorf("failed to make ResourceID path for '%s': %w", d.parentResourceID.ResourceGroupName, err)
	}

	return list[InternalAPIType, CosmosAPIType](ctx, d.containerClient, partitionKey, &d.resourceType, prefix, options, false)
}

func (d *nestedCosmosResourceCRUD[InternalAPIType, InternalAPITypePointer, CosmosAPIType]) AddCreateToTransaction(ctx context.Context, transaction DBTransaction, newObj *InternalAPIType, opts *azcosmos.TransactionalBatchItemOptions) (string, error) {
	return addCreateToTransaction[InternalAPIType, CosmosAPIType, InternalAPITypePointer](ctx, transaction, newObj, opts)
}

func (d *nestedCosmosResourceCRUD[InternalAPIType, InternalAPITypePointer, CosmosAPIType]) AddReplaceToTransaction(ctx context.Context, transaction DBTransaction, newObj *InternalAPIType, opts *azcosmos.TransactionalBatchItemOptions) (string, error) {
	return addReplaceToTransaction[InternalAPIType, CosmosAPIType, InternalAPITypePointer](ctx, transaction, newObj, opts)
}

func (d *nestedCosmosResourceCRUD[InternalAPIType, InternalAPITypePointer, CosmosAPIType]) Create(ctx context.Context, newObj *InternalAPIType, options *azcosmos.ItemOptions) (*InternalAPIType, error) {
	return create[InternalAPIType, CosmosAPIType, InternalAPITypePointer](ctx, d.containerClient, newObj, options)
}

func (d *nestedCosmosResourceCRUD[InternalAPIType, InternalAPITypePointer, CosmosAPIType]) Replace(ctx context.Context, newObj *InternalAPIType, options *azcosmos.ItemOptions) (*InternalAPIType, error) {
	return replace[InternalAPIType, CosmosAPIType, InternalAPITypePointer](ctx, d.containerClient, newObj, options)
}

func (d *nestedCosmosResourceCRUD[InternalAPIType, InternalAPITypePointer, CosmosAPIType]) Delete(ctx context.Context, resourceName string) error {
	completeResourceID, err := d.makeResourceIDPath(resourceName)
	if err != nil {
		return fmt.Errorf("failed to make ResourceID path for '%s': %w", resourceName, err)
	}
	partitionKey, err := d.partitionKeyDeriver.PartitionKey(d.parentResourceID, resourceName)
	if err != nil {
		return err
	}

	return deleteResource(ctx, d.containerClient, partitionKey, completeResourceID)
}
