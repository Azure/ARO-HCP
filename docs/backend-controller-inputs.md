# Backend controller input interests

Audited against `3b3a5248bd` plus the interest registrations in this change.
This inventory covers all **75 constructor sites** in `backend/pkg/controllers`
that wrap a watching controller, including the named validation factories.
Operation controllers and the custom orphan/mismatch sweep loops do not expose
`AddInterestingResource` and are outside this filter inventory; their behavior
remains in [the controller catalog](generated/service-provider-control-flow.md#2-complete-controller-catalog).

Each concrete constructor explicitly registers its Cosmos resource inputs.
The shared wrappers still impose no default interests. Inputs include existence
and deletion gates, `NeedsWork`, delegated helper reads, and existing desires
compared by `EnsureReadDesire`/`EnsureApplyDesire`. A controller's own reconcile
status writes are not domain inputs; status aggregators do read those documents.
The primary key resource also remains accepted to preserve initial reconciliation
and periodic resync, including controllers whose domain reads are child documents.

`Type(name)` below uses the API resource type constant without its `ResourceType`
suffix. `*` means an arbitrary name, not all resource types. Cluster, node-pool,
external-auth, credential, subscription, operation and rollout names come from
requests or runtime state. Service-provider and management-cluster singletons use
the existing `coreapi`/`fleetapi` `ResourceName` constants for `default`.
Fixed desire name identifiers below are declared in
[`kubeapplierapi/resource_names.go`](../internal/api/kubeapplierapi/resource_names.go)
and shared with producers/readers. Collection wildcards are justified in the
last column; cleanup intentionally includes legacy or unexpected names.

**An interest is a filter, not an informer subscription or fan-out.** These
registrations enumerate dependencies without changing informer wiring. The filter
runs on the original event before parent traversal. A matching event queues work
only if an existing informer delivers it and the event can reach the controller's
key within the configured depth. Dependencies outside that path remain covered by
primary-resource resync or existing explicit callbacks:

- Cluster wrappers receive Cluster, ServiceProviderCluster, direct cluster
  ManagementClusterContent, and optional direct cluster ApplyDesire/ReadDesire
  events. Node-pool wrappers receive the analogous node-pool events. Status
  aggregators explicitly subscribe to their direct Controller-document children.
- External-auth wrappers receive ExternalAuth events. Credential wrappers receive
  their request/revocation and optional direct ReadDesire and ApplyDesire events;
  concrete controllers select relevant desire types/names through interests.
- Subscription wrappers receive Subscription events; management-cluster wrappers
  receive ManagementCluster events. Their descendant collections are polled.
- Rollout wrappers receive ControlPlaneVersionRollout events. StatusCollector and
  ZStreamProgressiveDesiredVersionRollout retain their explicit cluster/SPC
  callbacks. Explicit enqueues and error retries bypass resource-event filtering.
- Parent/sibling inputs (such as a node pool's parent cluster, subscription or
  HostedCluster mirror), fleet placement inputs, external Azure/Cluster Service
  reads, billing documents, configuration and clocks retain their existing polling
  or retry behavior. Registering an interest does not provide child fan-out.

Lifecycle effects, fields written and completion gates are unchanged, so the
existing [lifecycle DOT/PNG diagrams](generated/service-provider-control-flow.md#3-resource-lifecycle-digraphs)
retain the same causal edges. Filtering removes unrelated wake-ups, not reads or
reconciliation steps.

## Cluster and credential controllers

### Direct child subscriptions

`QueueForChildInformers` subscribes to child events with a one-hop owner mapping
and the existing type/name interest filter. All generic watches handle add/update and
delete/tombstone events, and wait for source and handler cache synchronization.
Union consumers also retain their aggregate discovery barrier.
Supplementary subscriptions use zero resync: only the primary watch retains its
existing periodic reconciliation cadence. Source cache events still enqueue immediately.

- The three degraded aggregators watch their scoped Controller documents.
- Cluster/node-pool ReadDesire creators watch their named mirrors.
- Credential request/revocation desire creators and cleanup controllers watch
  their direct ApplyDesires.
- Cluster credential marking watches request/revocation children.
- Cluster child cleanup and final deletion watch node pools, external auths,
  requests/revocations, and direct cluster desires (ApplyDesire only for final
  deletion; ApplyDesire and ReadDesire for child cleanup).
- ClusterResources watches node pools only. Desire changes remain observed on
  its existing primary-resource cadence to avoid amplifying live Cluster Service
  resource fetches. Minor-upgrade reconciliation watches node pools and
  their provider-state documents with a two-hop limit. Keys come directly from
  event resource-ID ancestors, without live reads or cache scans.

These subscriptions promptly observe existing dependencies; parent/sibling and
Fleet dependencies still require reverse lookup/fan-out. Delete/tombstone handling
and source/handler readiness are standard across all generic watch APIs.

| Constructor source | Explicit resource inputs | Collection rationale and additional inputs |
|---|---|---|
| [ManagedResourceGroup](../backend/pkg/controllers/cluster/azureresources/managed_resource_group_controller.go) | `Cluster(*)`, `ServiceProviderCluster(default)`, `Subscription(*)` | Subscription tenant, managed-resource-group intent and prior state; polls Azure resource groups. |
| [KeyRotationBackup](../backend/pkg/controllers/cluster/backups/key_rotation_controller.go) | `Cluster(*)`, `ServiceProviderCluster(default)`, `ClusterScopedApplyDesire(*)`, `ClusterScopedReadDesire(*)` | Lists on-demand backup desires whose names include the namespace and KMS fingerprint; also reads the HostedCluster mirror. Backup configuration and time affect reconciliation. |
| [BackupSchedule](../backend/pkg/controllers/cluster/backups/schedule_controller.go) | `Cluster(*)`, `ServiceProviderCluster(default)`, `ClusterScopedApplyDesire(*)`, `ClusterScopedReadDesire(*)` | Lists backup desires whose names include the namespace and configured schedule; also reads the HostedCluster mirror. Backup configuration affects reconciliation. |
| [ClusterClusterServiceCreate](../backend/pkg/controllers/cluster/creation/cluster_cluster_service_create_controller.go) | `Cluster(*)`, `ServiceProviderCluster(default)`, `Subscription(*)`, `ManagementCluster(default)` | Creation/placement gates, subscription tenant and management-cluster shard; polls Cluster Service matches. |
| [ClusterPendingClusterServiceIDAssign](../backend/pkg/controllers/cluster/creation/cluster_pending_cluster_service_id_assign_controller.go) | `Cluster(*)` | Assigns the pending Cluster Service ID from cluster state. |
| [CreateServiceProviderCluster](../backend/pkg/controllers/cluster/creation/create_service_provider_cluster_controller.go) | `Cluster(*)`, `ServiceProviderCluster(default)` | Cluster deletion gate and existence of its service-provider singleton. |
| [DesiresCreator](../backend/pkg/controllers/cluster/credentialrequest/creation/desires_creator.go) | `SystemAdminCredentialRequest(*)`, `Cluster(*)`, `ServiceProviderCluster(default)`, `SystemAdminCredentialRequestScopedApplyDesire(SystemAdminCredentialCSRDesireName)`, `SystemAdminCredentialRequestScopedApplyDesire(SystemAdminCredentialCSRApprovalDesireName)`, `SystemAdminCredentialRequestScopedReadDesire(SystemAdminCredentialCSRDesireName)` | Reads parent cluster/placement and ensures the fixed CSR, approval and CSR mirror desires. |
| [SystemAdminCredentialIssuanceObserver](../backend/pkg/controllers/cluster/credentialrequest/creation/issuance_observer.go) | `SystemAdminCredentialRequest(*)`, `SystemAdminCredentialRequestScopedReadDesire(SystemAdminCredentialCSRDesireName)` | Reads the fixed credential CSR mirror and request state. |
| [SystemAdminCredentialClusterDeletionCleanup](../backend/pkg/controllers/cluster/credentialrequest/deletion/cluster_deletion_cleanup.go) | `SystemAdminCredentialRequest(*)`, `ServiceProviderCluster(default)`, `SystemAdminCredentialRequestScopedApplyDesire(*)`, `SystemAdminCredentialRequestScopedReadDesire(*)` | Lists every child desire for teardown, including legacy/unexpected names; parent placement selects the kube-applier DB. |
| [SystemAdminCredentialPostIssuanceCleanup](../backend/pkg/controllers/cluster/credentialrequest/deletion/post_issuance_cleanup.go) | `SystemAdminCredentialRequest(*)`, `ServiceProviderCluster(default)`, `SystemAdminCredentialRequestScopedApplyDesire(*)`, `SystemAdminCredentialRequestScopedReadDesire(*)` | Lists every child desire for teardown after issuance; parent placement selects the kube-applier DB. |
| [SystemAdminCredentialRevokedGC](../backend/pkg/controllers/cluster/credentialrequest/deletion/revoked_gc.go) | `SystemAdminCredentialRequest(*)` | Credential creation time and retention clock. |
| [RevocationDesires](../backend/pkg/controllers/cluster/credentialrevocation/creation/revocation_desires.go) | `SystemAdminCredentialRevocation(*)`, `Cluster(*)`, `ServiceProviderCluster(default)`, `SystemAdminCredentialRevocationScopedApplyDesire(SystemAdminCredentialRevocationDesireName)`, `SystemAdminCredentialRevocationScopedReadDesire(SystemAdminCredentialRevocationDesireName)` | Reads parent cluster/placement and ensures the fixed revocation apply/read desires. |
| [SystemAdminCredentialRevocationMarkRequests](../backend/pkg/controllers/cluster/credentialrevocation/creation/revocation_mark_requests.go) | `SystemAdminCredentialRevocation(*)`, `SystemAdminCredentialRequest(*)` | Lists every credential request, skipping those already marked for deletion; the clock supplies the deletion timestamp. |
| [SystemAdminCredentialRevocationCompletion](../backend/pkg/controllers/cluster/credentialrevocation/deletion/revocation_completion.go) | `SystemAdminCredentialRevocation(*)`, `SystemAdminCredentialRevocationScopedReadDesire(SystemAdminCredentialRevocationDesireName)` | Reads the fixed revocation mirror and revocation state. |
| [SystemAdminCredentialRevocationDeletion](../backend/pkg/controllers/cluster/credentialrevocation/deletion/revocation_deletion.go) | `SystemAdminCredentialRevocation(*)`, `ServiceProviderCluster(default)`, `SystemAdminCredentialRevocationScopedApplyDesire(*)`, `SystemAdminCredentialRevocationScopedReadDesire(*)` | Lists every child desire for teardown; parent placement selects the kube-applier DB. |
| [CleanOrphanedClusterManagedResourceGroup](../backend/pkg/controllers/cluster/deletion/clean_orphaned_cluster_managed_resource_group_controller.go) | `Subscription(*)`, `Cluster(*)` | Lists all clusters in the subscription and polls Azure managed resource groups. |
| [ClusterChildResourcesCleanupController](../backend/pkg/controllers/cluster/deletion/cluster_child_resources_cleanup_controller.go) | `Cluster(*)`, `ServiceProviderCluster(default)`, `NodePool(*)`, `ExternalAuth(*)`, `SystemAdminCredentialRequest(*)`, `SystemAdminCredentialRevocation(*)`, `ClusterScopedManagementClusterContent(*)`, `ClusterScopedApplyDesire(*)`, `ClusterScopedReadDesire(*)` | Waits for all node pools, external auths and credentials/revocations; sweeps arbitrary cluster children and desires, excluding controller status. |
| [ClusterClusterServiceDeleteDispatch](../backend/pkg/controllers/cluster/deletion/cluster_cluster_service_delete_dispatch_controller.go) | `Cluster(*)` | Cluster Service deletion and elapsed-time gate. |
| [ClusterDeletionClusterServiceIDClearer](../backend/pkg/controllers/cluster/deletion/cluster_cluster_service_id_clearer.go) | `Cluster(*)` | Polls Cluster Service cluster disappearance. |
| [ClusterCredentialDeletionMarkerController](../backend/pkg/controllers/cluster/deletion/cluster_credential_deletion_marker_controller.go) | `Cluster(*)`, `SystemAdminCredentialRequest(*)`, `SystemAdminCredentialRevocation(*)` | Lists every credential request and revocation to mark them for deletion. |
| [ClusterDeletionController](../backend/pkg/controllers/cluster/deletion/cluster_deletion_controller.go) | `Cluster(*)`, `ServiceProviderCluster(default)`, `NodePool(*)`, `ExternalAuth(*)`, `SystemAdminCredentialRequest(*)`, `SystemAdminCredentialRevocation(*)`, `ClusterScopedManagementClusterContent(*)`, `ClusterScopedApplyDesire(*)` | Checks child-resource lists, Maestro bundle references and all cluster ApplyDesires before final deletion. |
| [ClusterDenyAssignment](../backend/pkg/controllers/cluster/denyassignments/deny_assignment_controller.go) | `Cluster(*)`, `ServiceProviderCluster(default)`, `Subscription(*)` | Subscription tenant, managed-resource-group intent and prior state; polls Azure deny assignments. |
| [ActualHostedCluster](../backend/pkg/controllers/cluster/hostedcluster/actual_hosted_cluster_controller.go) | `Cluster(*)`, `ServiceProviderCluster(default)`, `ClusterScopedReadDesire(ReadDesireNameReadonlyHostedCluster)` | Mirrors the observed HostedCluster onto the service-provider singleton. |
| [ClusterIdentitySync](../backend/pkg/controllers/cluster/identity/cluster_identity_sync.go) | `Cluster(*)`, `ServiceProviderCluster(default)` | Mirrors resolved service-provider identity information onto the cluster. |
| [FetchDataPlaneOperatorsManagedIdentitiesInfo](../backend/pkg/controllers/cluster/identity/fetch_data_plane_operators_managed_identities_info.go) | `Cluster(*)`, `ServiceProviderCluster(default)` | Reads identity intent and prior observations; polls Azure managed identities. |
| [FetchMSIIdentitiesInfo](../backend/pkg/controllers/cluster/identity/fetch_msi_identities_info.go) | `Cluster(*)`, `ServiceProviderCluster(default)` | Reads identity intent and prior observations; polls the MSI data plane. |
| [ManagementClusterPlacementSync](../backend/pkg/controllers/cluster/placement/management_cluster_placement_sync.go) | `Cluster(*)`, `ServiceProviderCluster(default)`, `ManagementCluster(default)` | Resolves the Cluster Service provision shard through management-cluster records. |
| [PendingCleanup](../backend/pkg/controllers/cluster/placement/pending_cleanup_controller.go) | `ManagementCluster(default)`, `ManagementClusterScheduling(default)`, `Cluster(*)`, `ServiceProviderCluster(default)` | Uses management-cluster resync as the sweep trigger; reads scheduling reservations and each referenced cluster/service-provider singleton. |
| [Placement](../backend/pkg/controllers/cluster/placement/placement_controller.go) | `Cluster(*)`, `ServiceProviderCluster(default)`, `ManagementCluster(default)`, `ManagementClusterScheduling(default)` | Lists management clusters and their default scheduling documents; reads arbitrarily named clusters in pending reservations to calculate capacity. |
| [ClusterBaseDomainPrefixSync](../backend/pkg/controllers/cluster/properties/cluster_base_domain_prefix_sync.go) | `Cluster(*)` | Reads the live Cluster Service base-domain prefix. |
| [ClusterPropertiesSync](../backend/pkg/controllers/cluster/properties/cluster_properties_sync.go) | `Cluster(*)`, `ClusterScopedReadDesire(ReadDesireNameReadonlyHostedCluster)` | Mirrors HostedCluster API/console properties onto the cluster. |
| [DesiredControlPlaneSize](../backend/pkg/controllers/cluster/properties/desired_control_plane_size_sync.go) | `Cluster(*)`, `ServiceProviderCluster(default)` | Compares desired size with live Cluster Service properties. |
| [ServiceProviderClusterPropertiesSync](../backend/pkg/controllers/cluster/properties/serviceprovidercluster_properties_sync.go) | `Cluster(*)`, `ServiceProviderCluster(default)`, `ClusterScopedReadDesire(ReadDesireNameReadonlyHostedCluster)`, `ClusterScopedReadDesire(ReadDesireNameServingCA)` | Reads fixed HostedCluster and serving-CA mirrors. |
| [CreateClusterScopedReadDesires](../backend/pkg/controllers/cluster/readdesires/create_cluster_scoped_read_desires_controller.go) | `Cluster(*)`, `ServiceProviderCluster(default)`, `ClusterScopedReadDesire(ReadDesireNameReadonlyHostedCluster)`, `ClusterScopedReadDesire(ReadDesireNameReadonlyHypershiftControlPlaneComponentClusterAutoscaler)`, `ClusterScopedReadDesire(ReadDesireNameServingCA)` | Ensures the three fixed cluster mirrors; Cluster Service ID, namespace configuration and placement choose targets. |
| [RoleAssignments](../backend/pkg/controllers/cluster/roleassignments/role_assignments_controller.go) | `Cluster(*)`, `ServiceProviderCluster(default)`, `Subscription(*)` | Reads identity/role-assignment state and subscription tenant; polls Azure role assignments. |
| [ClusterDegradedAggregator](../backend/pkg/controllers/cluster/status/cluster_degraded_aggregator.go) | `Cluster(*)`, `ClusterController(*)`, `ClusterScopedApplyDesire(*)`, `ClusterScopedReadDesire(*)` | Lists every cluster controller and direct cluster-scoped desire condition; explicitly excludes node-pool desires. |
| [ClusterRequirementsValidAggregator](../backend/pkg/controllers/cluster/status/cluster_requirements_valid_aggregator.go) | `Cluster(*)`, `ServiceProviderCluster(default)` | Aggregates validations stored on the service-provider singleton. |
| [ClusterClusterServiceUpdateDispatch](../backend/pkg/controllers/cluster/update/cluster_cluster_service_update_dispatch_controller.go) | `Cluster(*)`, `ServiceProviderCluster(default)`, `Subscription(*)` | Subscription AFECs and service-provider desired state feed the Cluster Service update. |
| [Named cluster validations](../backend/pkg/controllers/cluster/validation/cluster_validation_controller.go) | `Cluster(*)`, `ServiceProviderCluster(default)`, `Subscription(*)` | All configured validators receive cluster and subscription; prior validation status lives on the service-provider singleton. Azure inputs are polled. |
| [ControlPlaneActiveVersions](../backend/pkg/controllers/cluster/version/control_plane_active_version_controller.go) | `Cluster(*)`, `ServiceProviderCluster(default)`, `ClusterScopedReadDesire(ReadDesireNameReadonlyHostedCluster)` | Reads the fixed HostedCluster version mirror. |
| [BestVersionSelection](../backend/pkg/controllers/cluster/version/rollout/best_version_controller.go) | `ControlPlaneVersionRollout(*)` | Dynamic y-stream/channel rollout and Cincinnati graph. |
| [ForcedClusterDesiredVersion](../backend/pkg/controllers/cluster/version/rollout/forced_desired_version_controller.go) | `Cluster(*)`, `ServiceProviderCluster(default)`, `ControlPlaneVersionRollout(*)` | Reads dynamically selected rollouts for forced desired versions. |
| [InitialNormalClusterDesiredVersion](../backend/pkg/controllers/cluster/version/rollout/initial_normal_desired_version_controller.go) | `Cluster(*)`, `ServiceProviderCluster(default)`, `ControlPlaneVersionRollout(*)` | Reads the dynamic channel rollout for the requested initial version. |
| [MinorUpgradeNormalClusterDesiredVersion](../backend/pkg/controllers/cluster/version/rollout/minor_upgrade_normal_desired_version_controller.go) | `Cluster(*)`, `ServiceProviderCluster(default)`, `ControlPlaneVersionRollout(*)`, `NodePool(*)`, `ServiceProviderNodePool(default)` | Reads dynamic channel rollout and lists all node pools with their service-provider singleton for version-skew checks. |
| [RolloutSeeding](../backend/pkg/controllers/cluster/version/rollout/rollout_seeding_controller.go) | `Cluster(*)`, `ServiceProviderCluster(default)`, `ControlPlaneVersionRollout(*)` | Seeds dynamically named rollouts for requested and effective cluster versions. |
| [StatusCollector](../backend/pkg/controllers/cluster/version/rollout/status_collector_controller.go) | `ControlPlaneVersionRollout(*)`, `Cluster(*)`, `ServiceProviderCluster(default)` | Lists membership across clusters/service-provider singletons; explicit callbacks fan out changes to affected rollouts. |
| [ZStreamProgressiveDesiredVersionRollout](../backend/pkg/controllers/cluster/version/rollout/zstream_progressive_desired_version_rollout_controller.go) | `ControlPlaneVersionRollout(*)`, `Cluster(*)`, `ServiceProviderCluster(default)` | Lists rollout members/candidates; explicit callbacks fan out input changes. Clock controls progressive rollout. |
| [TriggerControlPlaneUpgrade](../backend/pkg/controllers/cluster/version/trigger_control_plane_upgrade_controller.go) | `Cluster(*)`, `ServiceProviderCluster(default)`, `OperationStatus(*)` | Reads the dynamically referenced active operation; polls Cluster Service upgrade policies. |

## Node-pool controllers

| Constructor source | Explicit resource inputs | Collection rationale and additional inputs |
|---|---|---|
| [CreateServiceProviderNodePool](../backend/pkg/controllers/nodepool/creation/create_service_provider_nodepool_controller.go) | `NodePool(*)`, `ServiceProviderNodePool(default)` | Node-pool deletion gate and existence of its service-provider singleton. |
| [NodePoolClusterServiceCreate](../backend/pkg/controllers/nodepool/creation/node_pool_cluster_service_create_controller.go) | `NodePool(*)`, `Cluster(*)` | Parent cluster supplies the Cluster Service ID; creates the external node pool. |
| [NodePoolChildResourcesCleanupController](../backend/pkg/controllers/nodepool/deletion/node_pool_child_resources_cleanup_controller.go) | `NodePool(*)`, `ServiceProviderNodePool(default)`, `ServiceProviderCluster(default)`, `NodePoolScopedManagementClusterContent(*)`, `NodePoolScopedApplyDesire(*)`, `NodePoolScopedReadDesire(*)` | Lists arbitrary children and all node-pool desires; parent placement selects the kube-applier DB. Controller status documents are ignored. |
| [NodePoolClusterServiceDeleteDispatch](../backend/pkg/controllers/nodepool/deletion/node_pool_cluster_service_delete_dispatch_controller.go) | `NodePool(*)` | Cluster Service deletion and elapsed-time gate. |
| [NodePoolDeletionClusterServiceIDClearer](../backend/pkg/controllers/nodepool/deletion/node_pool_cluster_service_id_clearer.go) | `NodePool(*)` | Polls Cluster Service node-pool disappearance. |
| [NodePoolDeletionController](../backend/pkg/controllers/nodepool/deletion/node_pool_deletion_controller.go) | `NodePool(*)`, `ServiceProviderNodePool(default)`, `ServiceProviderCluster(default)`, `NodePoolScopedManagementClusterContent(*)`, `NodePoolScopedApplyDesire(*)` | Waits for remaining non-controller children, Maestro bundle references and all node-pool ApplyDesires. |
| [CreateNodePoolScopedReadDesires](../backend/pkg/controllers/nodepool/readdesires/create_nodepool_scoped_read_desires_controller.go) | `NodePool(*)`, `Cluster(*)`, `ServiceProviderCluster(default)`, `NodePoolScopedReadDesire(ReadDesireNameReadonlyNodePool)` | Reads parent cluster/placement and ensures the fixed NodePool mirror. |
| [NodePoolDegradedAggregator](../backend/pkg/controllers/nodepool/status/nodepool_degraded_aggregator.go) | `NodePool(*)`, `NodePoolController(*)`, `NodePoolScopedApplyDesire(*)`, `NodePoolScopedReadDesire(*)` | Lists every node-pool controller and desire condition; names are runtime-defined. |
| [NodePoolRequirementsValidAggregator](../backend/pkg/controllers/nodepool/status/nodepool_requirements_valid_aggregator.go) | `NodePool(*)`, `ServiceProviderNodePool(default)` | Aggregates validations stored on the service-provider singleton. |
| [NodePoolClusterServiceUpdateDispatch](../backend/pkg/controllers/nodepool/update/node_pool_cluster_service_update_dispatch_controller.go) | `NodePool(*)` | Dispatches pending node-pool intent to Cluster Service. |
| [Named node-pool validations](../backend/pkg/controllers/nodepool/validation/nodepool_validation_controller.go) | `NodePool(*)`, `ServiceProviderNodePool(default)`, `Cluster(*)`, `Subscription(*)` | All configured validators receive the node pool, parent cluster and subscription; prior validation status lives on the service-provider singleton. Azure inputs are polled. |
| [NodePoolActiveVersions](../backend/pkg/controllers/nodepool/version/nodepool_active_version_controller.go) | `NodePool(*)`, `ServiceProviderNodePool(default)`, `NodePoolScopedReadDesire(ReadDesireNameReadonlyNodePool)` | Reads the fixed NodePool mirror and persists observed versions. |
| [NodepoolVersion](../backend/pkg/controllers/nodepool/version/nodepool_version_controller.go) | `NodePool(*)`, `ServiceProviderNodePool(default)`, `ServiceProviderCluster(default)`, `Subscription(*)`, `ClusterScopedReadDesire(ReadDesireNameReadonlyHostedCluster)` | Parent control-plane versions, subscription AFECs and HostedCluster UUID feed Cincinnati validation. Parent/sibling changes require node-pool resync; generic parent traversal cannot fan out to node pools. |
| [TriggerNodePoolUpgrade](../backend/pkg/controllers/nodepool/version/trigger_node_pool_upgrade_controller.go) | `NodePool(*)`, `ServiceProviderNodePool(default)` | Desired version and observed node-pool state; polls Cluster Service upgrade policies. |

## External-auth controllers

| Constructor source | Explicit resource inputs | Collection rationale and additional inputs |
|---|---|---|
| [ExternalAuthClusterServiceCreate](../backend/pkg/controllers/externalauth/creation/external_auth_cluster_service_create_controller.go) | `ExternalAuth(*)`, `Cluster(*)` | Parent cluster supplies the Cluster Service ID; polls Cluster Service external auth. |
| [ExternalAuthChildResourcesCleanupController](../backend/pkg/controllers/externalauth/deletion/external_auth_child_resources_cleanup_controller.go) | `ExternalAuth(*)` | Lists untyped children, excluding controller status. No typed non-controller external-auth children exist; the resync polls this collection. |
| [ExternalAuthClusterServiceDeleteDispatch](../backend/pkg/controllers/externalauth/deletion/external_auth_cluster_service_delete_dispatch_controller.go) | `ExternalAuth(*)` | Cluster Service deletion and elapsed-time gate. |
| [ExternalAuthDeletionClusterServiceIDClearer](../backend/pkg/controllers/externalauth/deletion/external_auth_cluster_service_id_clearer.go) | `ExternalAuth(*)` | Polls Cluster Service external auth disappearance. |
| [ExternalAuthDeletionController](../backend/pkg/controllers/externalauth/deletion/external_auth_deletion_controller.go) | `ExternalAuth(*)` | Polls untyped remaining children, excluding controller status; no typed non-controller children exist. |
| [ExternalAuthDegradedAggregator](../backend/pkg/controllers/externalauth/status/externalauth_degraded_aggregator.go) | `ExternalAuth(*)`, `ExternalAuthController(*)` | Lists all external-auth controller conditions, including runtime-configured controllers. |
| [ExternalAuthClusterServiceUpdateDispatch](../backend/pkg/controllers/externalauth/update/external_auth_cluster_service_update_dispatch_controller.go) | `ExternalAuth(*)` | Polls Cluster Service external auth to dispatch pending intent. |

## Other watching controllers

| Constructor source | Explicit resource inputs | Collection rationale and additional inputs |
|---|---|---|
| [CreateBillingDoc](../backend/pkg/controllers/billing/registration.go) | `Cluster(*)`, `Subscription(*)` | Billing document by cluster UID (cache/live billing DB), clock and configured location. |
| [ClusterResources](../backend/pkg/controllers/clusterresources/cluster_resources_controller.go) | `Cluster(*)`, `ServiceProviderCluster(default)`, `NodePool(*)`, `ClusterScopedApplyDesire(*)`, `NodePoolScopedApplyDesire(*)`, `ClusterScopedReadDesire(*)`, `NodePoolScopedReadDesire(*)` | Polls Cluster Service resources; lists owned desires, including stale or orphaned names and node-pool children, for teardown. Desires are intentionally polled. |
| [CosmosMigration](../backend/pkg/controllers/cosmosmigration/cosmos_migration.go) | `Subscription(*)`, `OperationStatus(*)`, `Cluster(*)`, `ServiceProviderCluster(*)`, `NodePool(*)`, `ServiceProviderNodePool(*)`, `ExternalAuth(*)`, `ClusterController(*)`, `NodePoolController(*)`, `ExternalAuthController(*)`, `ClusterScopedManagementClusterContent(*)`, `NodePoolScopedManagementClusterContent(*)`, `ClusterScopedApplyDesire(*)`, `NodePoolScopedApplyDesire(*)`, `ClusterScopedReadDesire(*)`, `NodePoolScopedReadDesire(*)` | Subscription sweep lists all legacy operations, clusters, node pools, external auths, service-provider documents, controller documents, management-cluster contents and desires. Child collections are polled. |
| [CosmosRolloutVersionMigration](../backend/pkg/controllers/cosmosmigration/rollout_version_migration.go) | `ControlPlaneVersionRollout(*)` | Migrates each dynamically named channel rollout. |
| [CSStateDump](../backend/pkg/controllers/datadump/cs_state_dump.go) | `Cluster(*)`, `NodePool(*)` | Lists the cluster’s arbitrarily named node pools; polls Cluster Service state. |
| [BackfillClusterUID](../backend/pkg/controllers/mismatch/registration.go) | `Cluster(*)` | Active billing documents for this cluster supply its UID (live billing DB). |
