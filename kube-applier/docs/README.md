# `kube-applier` Implementation Plan

This directory contains the staged implementation plan for the `kube-applier`
component described in [../readme.md](../readme.md).

## Reading order

| Doc | Topic |
| --- | --- |
| [01-overview.md](01-overview.md) | Goals, components, key design decisions, open questions |
| [02-api-types.md](02-api-types.md) | Work needed in `internal/api/kubeapplier` (deepcopy, ResourceType registration, helpers) |
| [03-database.md](03-database.md) | New cosmos container, partition-key strategy, CRUD wiring, GlobalListers |
| [04-informers-listers.md](04-informers-listers.md) | New `internal/database/{informers,listers,listertesting}` packages |
| [05-controllers.md](05-controllers.md) | Controller designs (ApplyDesire, DeleteDesire, ReadDesireInformerManaging, ReadDesireKubernetes) |
| [06-binary-and-runtime.md](06-binary-and-runtime.md) | The `kube-applier` binary: kubeconfig, dynamic client, leader election, health/metrics, deployment scaffolding |
| [07-testing.md](07-testing.md) | Unit testing with `manifestclient`; integration testing with KIND |
| [08-rollout.md](08-rollout.md) | Phased PR plan with checkpoints |

## What already exists on this branch

- `internal/api/kubeapplier/types_apply_desire.go`
- `internal/api/kubeapplier/types_delete_desire.go`
- `internal/api/kubeapplier/types_read_desire.go`
- `kube-applier/readme.md` (design intent)

Everything else described in these docs is net-new work.

## Executive summary

The `kube-applier` is a per-management-cluster controller binary that brokers
between Cosmos DB and the local Kubernetes apiserver. It reconciles three
small CRDs stored in Cosmos:

- `ApplyDesire` &rarr; server-side-apply of `.spec.kubeContent` to the cluster
- `DeleteDesire` &rarr; delete `.spec.targetItem` and wait for finalizers
- `ReadDesire` &rarr; informer-backed read of `.spec.targetItem`, mirrored to `.status.kubeContent`

A separate ARO-HCP backend service (running in service clusters) creates and
owns these `*Desire` documents. The kube-applier only writes status.

The work breaks into four largely-independent layers:

1. **API plumbing** &mdash; deepcopy, ResourceType registration, condition helpers.
2. **Storage** &mdash; new cosmos container, custom partition key, CRUD per type, cross-partition lister for the backend.
3. **Cache layer** &mdash; informers/listers/listertesting under `internal/database/` so both the backend (creator) and kube-applier (consumer) can share them.
4. **Runtime** &mdash; the binary itself: four controllers, in-cluster kubeconfig, dynamic+SSA client, leader election, observability.

See [08-rollout.md](08-rollout.md) for the suggested PR sequence.
