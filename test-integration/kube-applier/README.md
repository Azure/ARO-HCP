# kube-applier integration tests

Artifact-driven integration tests for the kube-applier. Each test is a
directory under `artifacts/` containing numbered step subdirectories; the
framework discovers them automatically and runs each one as a `t.Run` subtest.

The controllers run in-process against:

- a real `kube-apiserver` + `etcd` provided by `sigs.k8s.io/controller-runtime`'s
  envtest (no Docker), and
- a `databasetesting.MockKubeApplierDBClient` standing in for Cosmos. The
  framework holds the client through the `database.KubeApplierDBClient`
  interface, so a future joint backend+kube-applier test can swap in an
  implementation that shares storage with the backend's MockDBClient.

### What is envtest?

[envtest](https://book.kubebuilder.io/reference/envtest.html) is the
[`sigs.k8s.io/controller-runtime`](https://pkg.go.dev/sigs.k8s.io/controller-runtime/pkg/envtest)
package that boots a real `kube-apiserver` + `etcd` in-process against a
local copy of the kubebuilder binaries. It is the controller-runtime project
maintainer's preferred way to test against a real apiserver without the
overhead of a full cluster (e.g. `kind` or `minikube`); the apiserver speaks
the actual API including admission, defaulting, and SSA conflict handling
that a fake client cannot reproduce. Despite the name, envtest is shipped by
controller-runtime, not directly by kubebuilder — it is just the binaries
themselves that come from kubebuilder's release artifacts.

## Layout

```
artifacts/<TestName>/
  NN-<stepType>-<description>/
    *.json
```

Each test gets a per-test Kubernetes namespace named after `<TestName>` (with
underscores converted to hyphens), so artifact JSONs that reference that
namespace will not collide with sibling tests.

## Step types

| stepType | Purpose | Files |
| --- | --- | --- |
| `loadApplyDesire` | Insert ApplyDesire docs into the mock Cosmos. | `*.json` (one per desire) |
| `loadDeleteDesire` | Insert DeleteDesire docs. | `*.json` |
| `loadReadDesire` | Insert ReadDesire docs. | `*.json` |
| `kubernetesLoad` | Create unstructured Kubernetes objects via the dynamic client. | `*.json` |
| `kubernetesApply` | Get-then-Update existing Kubernetes objects. Preserves resourceVersion / uid. | `*.json` |
| `kubernetesDelete` | Delete a Kubernetes object identified by `00-key.json`. | `00-key.json` |
| `desireEventually` | Poll Cosmos via the matching CRUD until the document matches `expected.json` (subset match). The kind of *Desire is inferred from the resource ID. | `00-key.json` (`{"resourceID":"..."}`) + `expected.json` |
| `kubernetesEventually` | Poll the cluster via the dynamic client until the live object matches `expected.json`. Set `"absent": true` on the key to instead wait for `IsNotFound`. | `00-key.json` (`{apiVersion, kind, namespace, name, [resource], [absent]}`) + optional `expected.json` |

`expected.json` uses **subset match**:

- map keys: every key in expected must be present in actual; extra keys in
  actual are ignored;
- slices: every element in expected must match (recursively) at least one
  element in actual; order is ignored, extra actual elements are ignored;
- scalars: must be deeply equal after JSON normalization.

## Running

The simplest path is the repo's `make test-unit` target. It downloads the
envtest binaries (etcd + kube-apiserver) into `./bin/envtest/` on first run
and exports `KUBEBUILDER_ASSETS` for every test invocation in the workspace,
including this package.

```bash
make test-unit
```

To run only this package once the binaries are downloaded:

```bash
export KUBEBUILDER_ASSETS=$(make -s envtest-setup)
go test ./test-integration/kube-applier/...
```

Running the tests directly with `go test` and no `KUBEBUILDER_ASSETS` is a
hard error: TestMain prints setup instructions and exits non-zero.
