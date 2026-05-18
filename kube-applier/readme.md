
`kube-applier` is a binary that runs in a management cluster and uses the in-cluster kubeconfig and
connection information for a cosmos container.

It reads content from cosmos that use the APIs from `internal/api/kubeapplier` to decide what actions to take.
At a high level:
1. `ApplyDesire` indicates a kube manifest in .spec.kubeContent to issues a server-side-apply for.
   Success/failure to be written to the `.status.conditions["Successful"]` condition..
2. `DeleteDesire` indicates a kube item in .spec.targetItem to issues a delete for.
   Success/failure to be written to the `.status.conditions["Successful"]` condition..
3. `ReadDesire` indicates a kube item in .spec.targetItem to issue a list/watch+informer for.
   The actual list/watch result to be written to `.status.kubeContent`.
   Success/failure to be written to the `.status.conditions["Successful"]` condition..

## Scale
The scale of the kube-applier is tiny: it covers a single management cluster.
A single management cluster will have a low hundreds of HostedClusters and if we have about 100 *Desires, we end up
with about 10k *Desires.
Ten thousand is such a small number that simple poll and iterate with 50 qps, we can scan every three minutes.
We'll probably actually use a larger burst and smaller QPS, but it's an easy scale to manage.
The scale of a region is larger, but is handled by cosmos so it will scale far beyond our needs.

## API structure
The API types for this will live in `internal/api/kubeapplier`.

Every `*Desire` API will interact with a single kubernetes resource instance.
We will not support lists, we will not support label selection, and we will not support list all.
This is for simplicity in reasoning about the status.
We may eventually add support for `ReadManyDesire`, but only if we find a need for it.

### ManagementCluster
Every `*Desire` API has a `.spec.managementCluster` field.
This is the name of the management cluster that the `kube-applier` is running in.
It is reasonably likely that will someday before an `*azcorearm.ResourceID`, but if that happens we'll adjust the string format first,
rewrite everything, then change the type.
No need to do so now since the type is a string.

### Conditions
Each `*Desire` API a list of conditions.
One of those conditions is the "Successful" condition.
Successful is true if the operation succeeded.
1. For ApplyDesire, this means a successful server-side-apply.
2. For DeleteDesire, this means the item is no longer present in the cluster.
   This is NOT the same as the delete call succeeded, remember that kubernetes has finalizers.
3. For ReadDesire, this means the list/watch succeeded and the informer synced.

When the kube-apiserver call fails,
1. `.status.conditions["Successful"].status` is false
2. `.status.conditions["Successful"].reason` is "KubeAPIError"
3. `.status.conditions["Successful"].message` is the error message from the kube-apiserver call.

When the kube-apiserver call cannot be executed,
1. `.status.conditions["Successful"].status` is false
2. `.status.conditions["Successful"].reason` is "PreCheckFailed"
3. `.status.conditions["Successful"].message` is whatever prevented us from calling the kube-apiserver.

## Database structure
The database is a new cosmos container called "kube-applier".
The cosmos container is partitioned by the name of the management cluster.
The `.cosmosMetadata.resourceID` field will be formatted like:
`subscriptions/{subscriptionID}/resourceGroups/{resourceGroupName}/providers/microsoft.redhatopenshift/hcpopenshiftclusters/{clusterName}/*desires/{resourceName}`
or
`subscriptions/{subscriptionID}/resourceGroups/{resourceGroupName}/providers/microsoft.redhatopenshift/hcpopenshiftclusters/{clusterName}/nodepools/{nodepoolName}/*desires/{resourceName}`
This allows us to have each management cluster with credentials only to its own management cluster partition so that
escapes from one management cluster don't compromise another.
The ARO-HCP backend running in the service cluster will have access to the "kube-applier" container across all partitions.
And the individual item IDs will nest nicely into our existing structures if we query all the data for a particular resourceID.

### Golang type details for Database
The golang types will be in `internal/database`.
A new `KubeApplierDBClient` will be created with `ResourceCRUD` style accessors for each `*Desire` API.
The input will require the management cluster name, subscriptionName, resourceGroupName, clusterName, and nodePoolName if applicable.
There will be a separate interface for listing across all partitions: this is needed for the ARO-HCP backend located in `backend` which will
have access to all partitions and will create the various `*Desire` instances.

The `internal/database/informers`, `internal/database/listers`, `internal/database/listertesting` packages will be populated with the informers and listers for the various `*Desire` APIs.
Look at the similar code in `backend/pkg/[informers,listers,listertesting]` packages for examples.

## Controller structure
The `kube-applier` binary will be controller-based with many controllers structured similarly to the `backend` binary today.
Instead of using the `Controller` type to communicate `Degraded` status, that will be communicated on the `*Desire` `.status.conditions["Degraded"]` field.
Several controllers will exist

### ReadDesireKubernetesController
An instance of this controller will be created and started for each `ReadDesire` instance.
Each instance will hold
1. the `.spec.targetItem`
2. the `ReadDesireLister`
3. a single-item kubernetes informer
4. single-item kubernetes lister
4. a `KubeApplierDBClient`
5. the resourceID of the `ReadDesire` instance

In addition to running when the informer triggers, the controller will unconditionally run every one minute.
We do this so that if the item doesn't exist, we can properly report that.

When the sync loop runs, we read the item from the kubernetes lister and from the `ReadDesireLister` and compare the
`.status.kubeContent` against the kubernetes lister result.
If they are different, then we update the `.status.kubeContent` and write it back to the database.

### ReadDesireInformerManagingController
This controller will use the `ReadDesire` informer to feed a sync function for `ReadDesire` instances.
Each time a particular `ReadDesire.spec.targetItem` changes — that is, the
GVR, namespace, or name identifying the kube object to watch (not changes to
the watched object's own content) — the old `ReadDesireKubernetesController`
instance will be stopped, discarded, and a new one will be created.

The manager does not publish a per-launch status condition. The
`ReadDesireKubernetesController` itself owns `Successful` and the
`.status.kubeContent` field, which together carry whether the watch is
working. A separate "watch was last (re)launched at" timestamp turned out
to be uninterpretable — consumers cannot distinguish a target-driven
relaunch from a process restart — so it is not surfaced.

When a `ReadDesire` is deleted, the `ReadDesireKubernetesController` instance will be stopped and discarded.

### DeleteDesireController
This controller will use the `DeleteDesire` informer to feed a sync function for `DeleteDesire` instances.
When the sync loop runs, it will
1. issue a get for the `.spec.targetItem`
   1. If it doesn't exist, write success and return
   2. If it does exist and has a deletion timestamp, indicate
      1. `.status.conditions["Successful"].status` is false
      2. `.status.conditions["Successful"].reason` is "WaitingForDeletion"
      3. `.status.conditions["Successful"].message` contains a message that includes the deletion timestamp and UID
      4. and return
   3. if it does exist and has no deletion timestamp,
      1. issue a delete for the `.spec.targetItem`.
         1. If unsuccessful, use the standard rule for `.status.conditions["Successful"]` and return
         2. If successful, issue a get for the deletion timestamp, indicate
            1. `.status.conditions["Successful"].status` is false
            2. `.status.conditions["Successful"].reason` is "WaitingForDeletion"
            3. `.status.conditions["Successful"].message` contains a message that includes the deletion timestamp and UID
            4. and return
This controller must resync every 60 seconds.

### ApplyDesireController
This controller will use the `ApplyDesire` informer to feed a sync function for `ApplyDesire` instances.
When the sync loop runs, it will
1. issue a server-side apply with force the `.spec.kubeContent`
2. it will use the standard rules for `.status.conditions["Successful"]`

#### Adopting existing resources
SSA's `force=true` claims field ownership over fields the kube-applier writes
even if a different field manager owned them previously, but it does **not**
delete fields the prior owner wrote that are no longer in our object — those
remain owned by the prior manager. Adopting resources that pre-date the
kube-applier (e.g. created by hand or by maestro) therefore needs a one-time
sweep to clear stale managedFields entries, or careful authoring of the
ApplyDesire's `.spec.kubeContent` to cover every field of interest. We will
solve this case-by-case rather than baking adoption logic into the kube-applier.

## Testing
Unit tests use the https://github.com/openshift/library-go/tree/master/pkg/manifestclient library to create fake kubernetes clients
and the `internal/databasetesting` and `internal/database/listertesting` packages to create fake `KubeApplier` clients.

Integration tests use [envtest](https://book.kubebuilder.io/reference/envtest.html)
(via `sigs.k8s.io/controller-runtime`) to bring up a real `kube-apiserver` +
`etcd` in-process, paired with the same fake `KubeApplier` clients from
`internal/databasetesting` and `internal/database/listertesting`. envtest
gives us the actual SSA conflict and admission semantics that a fake client
cannot reproduce, without the Docker dependency a `kind`-based suite would
need. See `test-integration/kube-applier/README.md` for setup.
