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

package database

import (
	"context"
	"fmt"
	"path"
	"strings"
	"sync"

	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/fleet"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// ManagementClusterLister is the narrow lister shape KubeApplierDBClients depends
// on to resolve a management-cluster resourceID into its Cosmos container name and
// partition-key value. The listers package's full ManagementClusterLister satisfies
// it; declaring the slimmer interface here keeps the import direction clean
// (database can stay below database/listers in the import graph).
type ManagementClusterLister interface {
	List(ctx context.Context) ([]*fleet.ManagementCluster, error)
}

// NewDBBackedManagementClusterLister adapts a FleetDBClient's cross-partition
// ManagementClusters global lister into the slim ManagementClusterLister
// interface. Each List() call hits Cosmos directly — no informer caching —
// which is fine for low-cadence callers like the orphan-cleanup controller
// (60-minute jitter). Backend startup that doesn't yet have informers wired
// can use this without taking on the informer lifecycle.
func NewDBBackedManagementClusterLister(fleetClient FleetDBClient) ManagementClusterLister {
	return &dbBackedManagementClusterLister{fleetClient: fleetClient}
}

type dbBackedManagementClusterLister struct {
	fleetClient FleetDBClient
}

func (l *dbBackedManagementClusterLister) List(ctx context.Context) ([]*fleet.ManagementCluster, error) {
	iter, err := l.fleetClient.GlobalListers().ManagementClusters().List(ctx, nil)
	if err != nil {
		return nil, err
	}
	var out []*fleet.ManagementCluster
	for _, mc := range iter.Items(ctx) {
		out = append(out, mc)
	}
	if err := iter.GetError(); err != nil {
		return nil, err
	}
	return out, nil
}

// KubeApplierDBClient is the database surface for a single management cluster's
// kube-applier container. In the per-management-cluster container model, every
// container holds exactly one management cluster's *Desire documents, so callers
// never need to pass a management-cluster name into a method on this interface.
// Callers that span multiple management clusters (the backend) hold a
// KubeApplierDBClients (plural) and obtain a per-MC client via For().
type KubeApplierDBClient interface {
	// ApplyDesiresForCluster returns a CRUD scoped to a cluster parent.
	ApplyDesiresForCluster(subscriptionID, resourceGroupName, clusterName string) (ResourceCRUD[kubeapplier.ApplyDesire, *kubeapplier.ApplyDesire], error)
	// ApplyDesiresForNodePool returns a CRUD scoped to a nodepool parent.
	ApplyDesiresForNodePool(subscriptionID, resourceGroupName, clusterName, nodePoolName string) (ResourceCRUD[kubeapplier.ApplyDesire, *kubeapplier.ApplyDesire], error)
	// DeleteDesiresForCluster returns a CRUD scoped to a cluster parent.
	DeleteDesiresForCluster(subscriptionID, resourceGroupName, clusterName string) (ResourceCRUD[kubeapplier.DeleteDesire, *kubeapplier.DeleteDesire], error)
	// DeleteDesiresForNodePool returns a CRUD scoped to a nodepool parent.
	DeleteDesiresForNodePool(subscriptionID, resourceGroupName, clusterName, nodePoolName string) (ResourceCRUD[kubeapplier.DeleteDesire, *kubeapplier.DeleteDesire], error)
	// ReadDesiresForCluster returns a CRUD scoped to a cluster parent.
	ReadDesiresForCluster(subscriptionID, resourceGroupName, clusterName string) (ResourceCRUD[kubeapplier.ReadDesire, *kubeapplier.ReadDesire], error)
	// ReadDesiresForNodePool returns a CRUD scoped to a nodepool parent.
	ReadDesiresForNodePool(subscriptionID, resourceGroupName, clusterName, nodePoolName string) (ResourceCRUD[kubeapplier.ReadDesire, *kubeapplier.ReadDesire], error)

	// Listers lists every *Desire of each kind in this container — i.e. across the
	// one management cluster's worth of data. Replaces the old GlobalListers /
	// PartitionListers split, which existed only because all management clusters
	// previously shared one container.
	Listers() KubeApplierListers

	// UntypedCRUD walks this container by resourceID prefix, returning
	// TypedDocument rows for cross-cutting cleanup. Deletion goes through
	// DeleteByCosmosID using the partitionKey from the listed row.
	UntypedCRUD(parentResourceID azcorearm.ResourceID) (UntypedResourceCRUD, error)
}

// KubeApplierListers exposes per-container listers for each *Desire kind. The
// underlying container holds one management cluster's documents, so a List call
// returns every desire of that kind for that management cluster.
type KubeApplierListers interface {
	ApplyDesires() GlobalLister[kubeapplier.ApplyDesire]
	DeleteDesires() GlobalLister[kubeapplier.DeleteDesire]
	ReadDesires() GlobalLister[kubeapplier.ReadDesire]
}

// KubeApplierApplyDesireCRUD is the narrow per-type peer interface that the
// apply_desire controller takes as its database dependency. KubeApplierDBClient
// satisfies it; tests can also provide a one-method fake.
type KubeApplierApplyDesireCRUD interface {
	ApplyDesiresForCluster(subscriptionID, resourceGroupName, clusterName string) (ResourceCRUD[kubeapplier.ApplyDesire, *kubeapplier.ApplyDesire], error)
	ApplyDesiresForNodePool(subscriptionID, resourceGroupName, clusterName, nodePoolName string) (ResourceCRUD[kubeapplier.ApplyDesire, *kubeapplier.ApplyDesire], error)
}

// KubeApplierDeleteDesireCRUD is the DeleteDesire peer of KubeApplierApplyDesireCRUD.
type KubeApplierDeleteDesireCRUD interface {
	DeleteDesiresForCluster(subscriptionID, resourceGroupName, clusterName string) (ResourceCRUD[kubeapplier.DeleteDesire, *kubeapplier.DeleteDesire], error)
	DeleteDesiresForNodePool(subscriptionID, resourceGroupName, clusterName, nodePoolName string) (ResourceCRUD[kubeapplier.DeleteDesire, *kubeapplier.DeleteDesire], error)
}

// KubeApplierReadDesireCRUD is the ReadDesire peer of KubeApplierApplyDesireCRUD.
type KubeApplierReadDesireCRUD interface {
	ReadDesiresForCluster(subscriptionID, resourceGroupName, clusterName string) (ResourceCRUD[kubeapplier.ReadDesire, *kubeapplier.ReadDesire], error)
	ReadDesiresForNodePool(subscriptionID, resourceGroupName, clusterName, nodePoolName string) (ResourceCRUD[kubeapplier.ReadDesire, *kubeapplier.ReadDesire], error)
}

// clusterParentResourceID returns the parent resource ID for a cluster-scoped
// *Desire (everything under /providers/.../hcpOpenShiftClusters/<clusterName>).
func clusterParentResourceID(subscriptionID, resourceGroupName, clusterName string) (*azcorearm.ResourceID, error) {
	if len(subscriptionID) == 0 {
		return nil, fmt.Errorf("subscriptionID is required")
	}
	if len(resourceGroupName) == 0 {
		return nil, fmt.Errorf("resourceGroupName is required")
	}
	if len(clusterName) == 0 {
		return nil, fmt.Errorf("clusterName is required")
	}
	return azcorearm.ParseResourceID(strings.ToLower(path.Join(
		"/subscriptions", strings.ToLower(subscriptionID),
		"resourceGroups", resourceGroupName,
		"providers", api.ClusterResourceType.String(), clusterName,
	)))
}

// nodePoolParentResourceID returns the parent resource ID for a nodepool-scoped
// *Desire (under .../hcpOpenShiftClusters/<clusterName>/nodePools/<nodePoolName>).
func nodePoolParentResourceID(subscriptionID, resourceGroupName, clusterName, nodePoolName string) (*azcorearm.ResourceID, error) {
	if len(nodePoolName) == 0 {
		return nil, fmt.Errorf("nodePoolName is required")
	}
	clusterID, err := clusterParentResourceID(subscriptionID, resourceGroupName, clusterName)
	if err != nil {
		return nil, err
	}
	return azcorearm.ParseResourceID(strings.ToLower(path.Join(
		clusterID.String(), api.NodePoolResourceTypeName, nodePoolName,
	)))
}

// kubeApplierCosmosDBClient implements KubeApplierDBClient against a Cosmos
// container that holds one management cluster's data. managementClusterPartitionKey
// is the lowercased partition-key value used for every write/query against the
// container; documents must carry a matching Spec.ManagementCluster.
type kubeApplierCosmosDBClient struct {
	kubeApplier                   *azcosmos.ContainerClient
	managementClusterPartitionKey string
}

var _ KubeApplierDBClient = &kubeApplierCosmosDBClient{}

// NewKubeApplierDBClient wraps a pre-opened Cosmos container client for a single
// management cluster. managementClusterPartitionKey is the value used as the
// partition key for every CRUD call; it is lowercased on entry to match the
// existing kube-applier write helpers, which lowercase Spec.ManagementCluster
// before comparing.
func NewKubeApplierDBClient(container *azcosmos.ContainerClient, managementClusterPartitionKey *azcorearm.ResourceID) KubeApplierDBClient {
	return &kubeApplierCosmosDBClient{
		kubeApplier:                   container,
		managementClusterPartitionKey: strings.ToLower(managementClusterPartitionKey.String()),
	}
}

// NewKubeApplierDBClientFromDatabase opens the named container under the given
// Cosmos database and wraps it for the named management cluster. Convenience
// for callers like the kube-applier sidecar that have a DatabaseClient in hand.
func NewKubeApplierDBClientFromDatabase(database *azcosmos.DatabaseClient, containerName string, managementClusterPartitionKey *azcorearm.ResourceID) (KubeApplierDBClient, error) {
	container, err := database.NewContainer(containerName)
	if err != nil {
		return nil, utils.TrackError(err)
	}
	return NewKubeApplierDBClient(container, managementClusterPartitionKey), nil
}

func (c *kubeApplierCosmosDBClient) ApplyDesiresForCluster(subscriptionID, resourceGroupName, clusterName string) (ResourceCRUD[kubeapplier.ApplyDesire, *kubeapplier.ApplyDesire], error) {
	parentID, err := clusterParentResourceID(subscriptionID, resourceGroupName, clusterName)
	if err != nil {
		return nil, err
	}
	return NewCosmosResourceCRUDWithStrategies[kubeapplier.ApplyDesire, *kubeapplier.ApplyDesire, GenericDocument[kubeapplier.ApplyDesire]](
		c.kubeApplier, parentID, kubeapplier.ClusterScopedApplyDesireResourceType,
		StaticPartitionKeyDeriver{Key: c.managementClusterPartitionKey}, ClusterNestedResourceIDBuilder{},
	), nil
}

func (c *kubeApplierCosmosDBClient) ApplyDesiresForNodePool(subscriptionID, resourceGroupName, clusterName, nodePoolName string) (ResourceCRUD[kubeapplier.ApplyDesire, *kubeapplier.ApplyDesire], error) {
	parentID, err := nodePoolParentResourceID(subscriptionID, resourceGroupName, clusterName, nodePoolName)
	if err != nil {
		return nil, err
	}
	return NewCosmosResourceCRUDWithStrategies[kubeapplier.ApplyDesire, *kubeapplier.ApplyDesire, GenericDocument[kubeapplier.ApplyDesire]](
		c.kubeApplier, parentID, kubeapplier.NodePoolScopedApplyDesireResourceType,
		StaticPartitionKeyDeriver{Key: c.managementClusterPartitionKey}, ClusterNestedResourceIDBuilder{},
	), nil
}

func (c *kubeApplierCosmosDBClient) DeleteDesiresForCluster(subscriptionID, resourceGroupName, clusterName string) (ResourceCRUD[kubeapplier.DeleteDesire, *kubeapplier.DeleteDesire], error) {
	parentID, err := clusterParentResourceID(subscriptionID, resourceGroupName, clusterName)
	if err != nil {
		return nil, err
	}
	return NewCosmosResourceCRUDWithStrategies[kubeapplier.DeleteDesire, *kubeapplier.DeleteDesire, GenericDocument[kubeapplier.DeleteDesire]](
		c.kubeApplier, parentID, kubeapplier.ClusterScopedDeleteDesireResourceType,
		StaticPartitionKeyDeriver{Key: c.managementClusterPartitionKey}, ClusterNestedResourceIDBuilder{},
	), nil
}

func (c *kubeApplierCosmosDBClient) DeleteDesiresForNodePool(subscriptionID, resourceGroupName, clusterName, nodePoolName string) (ResourceCRUD[kubeapplier.DeleteDesire, *kubeapplier.DeleteDesire], error) {
	parentID, err := nodePoolParentResourceID(subscriptionID, resourceGroupName, clusterName, nodePoolName)
	if err != nil {
		return nil, err
	}
	return NewCosmosResourceCRUDWithStrategies[kubeapplier.DeleteDesire, *kubeapplier.DeleteDesire, GenericDocument[kubeapplier.DeleteDesire]](
		c.kubeApplier, parentID, kubeapplier.NodePoolScopedDeleteDesireResourceType,
		StaticPartitionKeyDeriver{Key: c.managementClusterPartitionKey}, ClusterNestedResourceIDBuilder{},
	), nil
}

func (c *kubeApplierCosmosDBClient) ReadDesiresForCluster(subscriptionID, resourceGroupName, clusterName string) (ResourceCRUD[kubeapplier.ReadDesire, *kubeapplier.ReadDesire], error) {
	parentID, err := clusterParentResourceID(subscriptionID, resourceGroupName, clusterName)
	if err != nil {
		return nil, err
	}
	return NewCosmosResourceCRUDWithStrategies[kubeapplier.ReadDesire, *kubeapplier.ReadDesire, GenericDocument[kubeapplier.ReadDesire]](
		c.kubeApplier, parentID, kubeapplier.ClusterScopedReadDesireResourceType,
		StaticPartitionKeyDeriver{Key: c.managementClusterPartitionKey}, ClusterNestedResourceIDBuilder{},
	), nil
}

func (c *kubeApplierCosmosDBClient) ReadDesiresForNodePool(subscriptionID, resourceGroupName, clusterName, nodePoolName string) (ResourceCRUD[kubeapplier.ReadDesire, *kubeapplier.ReadDesire], error) {
	parentID, err := nodePoolParentResourceID(subscriptionID, resourceGroupName, clusterName, nodePoolName)
	if err != nil {
		return nil, err
	}
	return NewCosmosResourceCRUDWithStrategies[kubeapplier.ReadDesire, *kubeapplier.ReadDesire, GenericDocument[kubeapplier.ReadDesire]](
		c.kubeApplier, parentID, kubeapplier.NodePoolScopedReadDesireResourceType,
		StaticPartitionKeyDeriver{Key: c.managementClusterPartitionKey}, ClusterNestedResourceIDBuilder{},
	), nil
}

func (c *kubeApplierCosmosDBClient) Listers() KubeApplierListers {
	return &cosmosKubeApplierListers{
		kubeApplier:  c.kubeApplier,
		partitionKey: c.managementClusterPartitionKey,
	}
}

func (c *kubeApplierCosmosDBClient) UntypedCRUD(parentResourceID azcorearm.ResourceID) (UntypedResourceCRUD, error) {
	return newKubeApplierUntypedCRUD(c.kubeApplier, parentResourceID), nil
}

// cosmosKubeApplierListers implements KubeApplierListers against a single per-MC
// Cosmos container. partitionKey scopes every query to this container's one
// partition (there is only one in the per-MC model, but Cosmos still requires a key).
type cosmosKubeApplierListers struct {
	kubeApplier  *azcosmos.ContainerClient
	partitionKey string
}

var _ KubeApplierListers = &cosmosKubeApplierListers{}

func (g *cosmosKubeApplierListers) ApplyDesires() GlobalLister[kubeapplier.ApplyDesire] {
	return &cosmosKubeApplierDesireLister[kubeapplier.ApplyDesire, GenericDocument[kubeapplier.ApplyDesire]]{
		kubeApplier:  g.kubeApplier,
		partitionKey: g.partitionKey,
		resourceTypes: []azcorearm.ResourceType{
			kubeapplier.ClusterScopedApplyDesireResourceType,
			kubeapplier.NodePoolScopedApplyDesireResourceType,
		},
	}
}

func (g *cosmosKubeApplierListers) DeleteDesires() GlobalLister[kubeapplier.DeleteDesire] {
	return &cosmosKubeApplierDesireLister[kubeapplier.DeleteDesire, GenericDocument[kubeapplier.DeleteDesire]]{
		kubeApplier:  g.kubeApplier,
		partitionKey: g.partitionKey,
		resourceTypes: []azcorearm.ResourceType{
			kubeapplier.ClusterScopedDeleteDesireResourceType,
			kubeapplier.NodePoolScopedDeleteDesireResourceType,
		},
	}
}

func (g *cosmosKubeApplierListers) ReadDesires() GlobalLister[kubeapplier.ReadDesire] {
	return &cosmosKubeApplierDesireLister[kubeapplier.ReadDesire, GenericDocument[kubeapplier.ReadDesire]]{
		kubeApplier:  g.kubeApplier,
		partitionKey: g.partitionKey,
		resourceTypes: []azcorearm.ResourceType{
			kubeapplier.ClusterScopedReadDesireResourceType,
			kubeapplier.NodePoolScopedReadDesireResourceType,
		},
	}
}

// cosmosKubeApplierDesireLister lists *Desire documents (one kind per instance)
// from a kube-applier container, unioning the cluster-scoped and node-pool-scoped
// resource types in a single query against one partition.
type cosmosKubeApplierDesireLister[InternalAPIType, CosmosAPIType any] struct {
	kubeApplier   *azcosmos.ContainerClient
	resourceTypes []azcorearm.ResourceType
	partitionKey  string
}

func (l *cosmosKubeApplierDesireLister[InternalAPIType, CosmosAPIType]) List(
	ctx context.Context, options *DBClientListResourceDocsOptions,
) (DBClientIterator[InternalAPIType], error) {
	var resourceTypeConditions []string
	for _, rt := range l.resourceTypes {
		resourceTypeConditions = append(
			resourceTypeConditions,
			fmt.Sprintf("STRINGEQUALS(c.resourceType, %q, true)", rt.String()),
		)
	}
	query := fmt.Sprintf("SELECT * FROM c WHERE %s", strings.Join(resourceTypeConditions, " OR "))

	queryOptions := azcosmos.QueryOptions{PageSizeHint: -1}
	if options != nil {
		if options.PageSizeHint != nil {
			queryOptions.PageSizeHint = max(*options.PageSizeHint, -1)
		}
		queryOptions.ContinuationToken = options.ContinuationToken
	}

	pager := l.kubeApplier.NewQueryItemsPager(query, azcosmos.NewPartitionKeyString(l.partitionKey), &queryOptions)

	if options != nil && ptr.Deref(options.PageSizeHint, -1) > 0 {
		return newQueryResourcesSinglePageIterator[InternalAPIType, CosmosAPIType](pager), nil
	}
	return newQueryResourcesIterator[InternalAPIType, CosmosAPIType](pager), nil
}

// KubeApplierDBClients is a thread-safe registry of KubeApplierDBClient keyed by
// management-cluster resourceID. Each entry corresponds to one Cosmos container,
// resolved at lookup time from the configured ManagementClusterLister. Per-MC
// clients are constructed lazily on first For() access and cached.
type KubeApplierDBClients interface {
	// For returns the client for the given management cluster, constructing it
	// on demand. It walks the configured ManagementClusterLister to find the
	// management cluster whose ResourceID matches; the container name comes
	// from the management cluster's Status.KubeApplierCosmosContainerName.
	// Returns nil if no management cluster matches, if the lister errors, or
	// if the matched management cluster has no container name configured.
	For(ctx context.Context, managementClusterResourceID *azcorearm.ResourceID) KubeApplierDBClient
}

// kubeApplierDBClients is the cosmos-backed implementation of KubeApplierDBClients.
type kubeApplierDBClients struct {
	database *azcosmos.DatabaseClient

	// mcLister is the source of truth for which management clusters exist and
	// what their per-container configuration looks like. It is queried fresh
	// on each For() call so additions and removals at the lister level are
	// picked up without restarting the backend; only the per-MC azcosmos
	// client construction is cached.
	mcLister ManagementClusterLister

	mu      sync.Mutex
	clients map[string]KubeApplierDBClient // key = lowercased(rid.String())
}

var _ KubeApplierDBClients = &kubeApplierDBClients{}

// NewKubeApplierDBClients constructs a thread-safe registry whose contents are
// resolved against the provided ManagementClusterLister. Each ManagementCluster's
// Status.KubeApplierCosmosContainerName names the Cosmos container; the
// management cluster's Status.MaestroConsumerName is used as the per-container
// partition key. Per-MC KubeApplierDBClient instances are built lazily and
// cached on first access via For().
func NewKubeApplierDBClients(database *azcosmos.DatabaseClient, mcLister ManagementClusterLister) KubeApplierDBClients {
	return &kubeApplierDBClients{
		database: database,
		mcLister: mcLister,
		clients:  map[string]KubeApplierDBClient{},
	}
}

func (c *kubeApplierDBClients) For(ctx context.Context, managementClusterResourceID *azcorearm.ResourceID) KubeApplierDBClient {
	if managementClusterResourceID == nil {
		return nil
	}
	key := strings.ToLower(managementClusterResourceID.String())

	c.mu.Lock()
	defer c.mu.Unlock()

	if existing, ok := c.clients[key]; ok {
		return existing
	}

	mc := c.findManagementClusterLocked(ctx, managementClusterResourceID)
	if mc == nil {
		return nil
	}
	containerName := mc.Status.KubeApplierCosmosContainerName
	if len(containerName) == 0 {
		return nil
	}
	container, err := c.database.NewContainer(containerName)
	if err != nil {
		// NewContainer only errors on malformed inputs at construction time —
		// treat as misconfiguration and surface as nil. The caller already
		// has to handle nil for "not found" anyway.
		return nil
	}
	// Partition key per container is the lowercased MaestroConsumerName; *Desire
	// documents written into this container must carry a matching
	// Spec.ManagementCluster. The kube-applier binary is started with the same
	// string via --management-cluster.
	client := NewKubeApplierDBClient(container, managementClusterResourceID)
	c.clients[key] = client
	return client
}

// findManagementClusterLocked walks the lister looking for the management cluster
// whose ResourceID matches the caller's. Linear iteration is intentional: the
// fleet is small (low hundreds of MCs at the worst case) and walking the list
// keeps us tolerant of any resourceID-format mismatch between the caller's input
// and the canonical form from the lister.
func (c *kubeApplierDBClients) findManagementClusterLocked(ctx context.Context, rid *azcorearm.ResourceID) *fleet.ManagementCluster {
	mcs, err := c.mcLister.List(ctx)
	if err != nil {
		return nil
	}
	target := strings.ToLower(rid.String())
	for _, mc := range mcs {
		mcRID := managementClusterResourceID(mc)
		if mcRID == nil {
			continue
		}
		if strings.ToLower(mcRID.String()) == target {
			return mc
		}
	}
	return nil
}

// managementClusterResourceID prefers the explicit ResourceID field (kept on the
// type during the migration off cosmosMetadata-only resourceIDs), falling back
// to CosmosMetadata.ResourceID. Returns nil if neither is set.
func managementClusterResourceID(mc *fleet.ManagementCluster) *azcorearm.ResourceID {
	if mc == nil {
		return nil
	}
	if mc.ResourceID != nil {
		return mc.ResourceID
	}
	return mc.CosmosMetadata.ResourceID
}
