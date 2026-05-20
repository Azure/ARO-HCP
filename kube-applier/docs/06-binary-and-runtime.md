# 06 &mdash; Binary, runtime, deployment scaffolding

## Layout

```
kube-applier/
  cmd/
    main.go              // thin: cobra root.Execute()
    root.go              // flags, ToOptions(), Run()
  pkg/
    app/
      kubeapplier.go     // KubeApplier struct, Run loop, leader election
      cosmos_wiring.go   // mirror backend/pkg/app/cosmos_wiring.go
      kube_wiring.go     // in-cluster client, dynamic, RESTMapper
    controllers/
      conditions/
      statuswriter/
      apply_desire/
      delete_desire/
      read_desire_manager/
      read_desire_kubernetes/
  deploy/                 // helm chart (Doc 06.4)
  pipeline.yaml           // ARO-HCP pipeline definition
  Makefile
  go.mod / go.sum
  readme.md               // (already exists)
  docs/                   // (this directory)
```

`go.mod` is added to the workspace via `go.work` &mdash; the kube-applier is
its own module like `backend/`, `frontend/`, etc.

Reference the backend layout 1:1 wherever possible:
`backend/cmd/`, `backend/pkg/app/backend.go`, `backend/main.go`.

## 6.1 Flags & options

Mirror `backend/cmd/root.go:286-415`. Required flags:

| Flag | Purpose |
| --- | --- |
| `--cosmos-name` / `--cosmos-url` | Cosmos endpoint (paired) |
| `--management-cluster` | Partition key value (from a downward-API env var or pod label preferred over a CLI flag) |
| `--metrics-listen-address` | default `:8081` |
| `--healthz-listen-address` | default `:8083` |
| `--leader-election-namespace` | namespace for the leader-election lease |
| `--leader-election-id` | typically `kube-applier` |
| `--threads-apply` / `--threads-delete` | optional, default 4 |
| `--field-manager` | default `kube-applier` |

`--management-cluster` should default from an env var (e.g.
`MANAGEMENT_CLUSTER`) populated by the deployment's downward API so it
cannot be misconfigured per-replica.

## 6.2 Wiring

Adapt `backend/pkg/app/backend.go` `Run()`:

```go
func (k *KubeApplier) Run(ctx context.Context) error {
    // 1. kubeconfig (in-cluster) for both leader-election and dynamic client.
    cfg, err := rest.InClusterConfig()

    // 2. dynamic client + RESTMapper.
    dyn, err := dynamic.NewForConfig(cfg)
    disco, err := discovery.NewDiscoveryClientForConfig(cfg)
    rm := restmapper.NewDeferredDiscoveryRESTMapper(memory.NewMemCacheClient(disco))

    // 3. Cosmos.
    cosmos, err := newCosmosDBClient(ctx, k.options)

    // 4. ScopedListers (single-partition view, see Doc 04.3).
    scoped := scopedlisters.New(cosmos, k.options.ManagementCluster)

    // 5. Informers + listers.
    informers := informers.NewKubeApplierInformers(ctx, scoped)

    // 6. Controllers.
    applyCtl := apply_desire.New(informers, dyn, rm, cosmos, k.options.ManagementCluster)
    deleteCtl := delete_desire.New(informers, dyn, rm, cosmos, k.options.ManagementCluster)
    readMgr := read_desire_manager.New(informers, dyn, rm, cosmos, k.options.ManagementCluster)

    // 7. Health/metrics servers (Doc 6.3).
    go startHealthAndMetrics(ctx, k.options)

    // 8. Leader election.
    return runWithLeaderElection(ctx, cfg, k.options, func(leCtx context.Context) {
        go informers.RunWithContext(leCtx)
        go applyCtl.Run(leCtx, k.options.ThreadsApply)
        go deleteCtl.Run(leCtx, k.options.ThreadsDelete)
        go readMgr.Run(leCtx, 1)
        <-leCtx.Done()
    })
}
```

The `runWithLeaderElection` helper is a copy of the backend's pattern
(`backend/pkg/app/backend.go:531-604`).

## 6.3 Health + metrics

Two HTTP servers on different ports (matches backend exactly):

- `:8083` &mdash; `/healthz` returns 503 if the leader-election checker
  reports the lease as not renewed; 200 otherwise.
- `:8081` &mdash; Prometheus `promhttp.Handler()` over the
  `component-base/metrics/legacyregistry`.

Workqueue metrics: register `workqueue.SetProvider(...)` at startup so per
controller queue metrics flow into Prometheus &mdash; this is already the
pattern in the backend; copy it.

## 6.4 Helm chart

Mirror an existing service helm chart (e.g. `backend/deploy/helm/`):

- `Chart.yaml`
- `values.yaml` &mdash; image, replicaCount=2, leader-election-id, cosmos
  config secret name, mgmt-cluster value injected via downward API.
- `templates/deployment.yaml` &mdash; pod with downward API env:

  ```yaml
  env:
    - name: MANAGEMENT_CLUSTER
      valueFrom:
        configMapKeyRef:
          name: management-cluster-info
          key: name
  ```

- `templates/serviceaccount.yaml` &mdash; bound to a workload identity for
  cosmos access.
- `templates/role.yaml` + `templates/rolebinding.yaml` &mdash; namespace-scoped
  permissions for the leader-election lease.
- `templates/clusterrole.yaml` &mdash; **wide** privileges. The kube-applier
  needs to apply/delete/read arbitrary resources. Start with the equivalent of
  `cluster-admin` and tighten in a follow-up; document the explicit scope
  decision in this template.

## 6.5 ARO-HCP pipeline.yaml

Add `kube-applier/pipeline.yaml` modeled on
`backend/pipeline.yaml`. Verify it is referenced from
`Makefile:services_mgmt_pipelines` &mdash; the kube-applier deploys to
**management** clusters, not service clusters.

## 6.6 IaC additions

In `dev-infrastructure/`:

- Add the `kube-applier` cosmos container to whichever bicep template
  defines `Resources` / `Billing` / `Locks`. Set the partition key path to
  `/_partitionKey` (matching existing containers) and the indexing policy
  to mirror `Resources`.
- Add a managed identity / role assignment scoped to the new container that
  the kube-applier pods use via workload identity. Restrict the role to the
  `_partitionKey` value of the local management cluster (Cosmos role
  scoping supports `partitionKey` predicates &mdash; confirm with platform).

These IaC changes are intentionally separated from the binary/helm work in
[Doc 08](08-rollout.md) so the binary can be built and tested against
`dev` first.

## 6.7 Workspace registration

- Add `./kube-applier` to `go.work`.
- Add `./kube-applier` to top-level `Makefile` targets that fan out across
  services (lint, build, test).
- Add the helm chart path to `svc-deploy.sh` if it is a registry of charts.

## 6.8 Open questions for this layer

1. **Leader-election lease namespace**: same as the kube-applier's own
   namespace? Confirm.
2. **Pod identity**: workload identity vs. CSI-mounted client secret? Match
   what backend does in management clusters today (likely workload identity).
3. **ClusterRole scope**: cluster-admin is the safe-but-broad starting point.
   The right long-term answer is a curated allowlist driven by the GVRs that
   `*Desire`s reference; out of scope for the initial implementation.
