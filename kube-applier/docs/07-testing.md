# 07 &mdash; Testing strategy

The readme commits to two testing technologies:

- **Unit tests** &mdash;
  [`github.com/openshift/library-go/pkg/manifestclient`](https://github.com/openshift/library-go/tree/master/pkg/manifestclient)
  for fake kubernetes clients, plus the existing `internal/databasetesting`
  and the new `internal/database/listertesting` for fake `KubeApplier`
  clients.
- **Integration tests** &mdash; KIND for a real kube-apiserver, plus the
  same fake `KubeApplier` clients (we are *not* spinning up Cosmos in
  integration).

This document covers what each test layer must cover and how to scaffold it.

## 7.1 Adding `manifestclient` to the workspace

Search shows it is **not yet** imported. To add:

1. `cd kube-applier && go get github.com/openshift/library-go@latest`.
2. Run `go mod tidy` and commit `go.sum`.
3. The library only needs to be a dependency of `kube-applier/pkg/...` test
   files; we do not want it in `internal/database/` test code.

`manifestclient` records and replays kube API interactions from a
filesystem of YAML manifests. The standard pattern:

```go
testdata := os.DirFS("testdata/apply_basic")
roundTripper, _ := manifestclient.NewRoundTripper(testdata)
cfg := &rest.Config{Transport: roundTripper}
dyn, _ := dynamic.NewForConfig(cfg)
```

## 7.2 Unit-test matrix per controller

### ApplyDesireController

| Case | Expected condition |
| --- | --- |
| valid Deployment payload, apply succeeds | `Successful=True` |
| invalid YAML in `kubeContent` | `Successful=False`, reason `PreCheckFailed` |
| GVK has no RESTMapper match | `Successful=False`, reason `PreCheckFailed` |
| kube-apiserver returns 403 | `Successful=False`, reason `KubeAPIError`, msg contains the upstream error |
| existing object owned by another field manager (force=true) | `Successful=True`; verify the diff was applied |
| no-op resync (status already correct) | no Cosmos write (verify via mock) |

### DeleteDesireController

| Case | Expected condition |
| --- | --- |
| target absent | `Successful=True` |
| target present, no deletionTimestamp, delete returns 404 (race) | `Successful=True` |
| target present, no deletionTimestamp, delete succeeds | `Successful=False`, reason `WaitingForDeletion`, message includes UID + DT |
| target present, deletionTimestamp already set | `Successful=False`, reason `WaitingForDeletion` |
| delete returns 500 | `Successful=False`, reason `KubeAPIError` |

### ReadDesireInformerManagingController

| Case | Expected behaviour |
| --- | --- |
| ReadDesire created | one running child controller; no per-launch status condition |
| ReadDesire.spec.targetItem changed | old child stopped (verify cancel called), new child started |
| ReadDesire deleted | child stopped, removed from manager map |
| invalid GVR in targetItem | no child started; `Successful=False`, reason `PreCheckFailed` |

### ReadDesireKubernetesController

| Case | Expected behaviour |
| --- | --- |
| target exists at startup | `Status.KubeContent` populated, `Successful=True` |
| target absent at startup | `Status.KubeContent` empty/nil sentinel, `Successful=True` |
| target appears after startup (informer event) | status updated within the test's deadline |
| target disappears | status updated to empty within the 60s tick (use a fake clock) |
| informer ListWatch errors | `Successful=False`, reason `KubeAPIError` |
| no-op resync | no Cosmos write |

Use a fake clock (`k8s.io/utils/clock/testing.FakeClock`) so the 60-second
ticks are deterministic.

## 7.3 Integration tests (KIND)

Location: `test-integration/kube-applier/`.

Pattern to follow: `test-integration/backend/controllers/do_nothing/`.
Use `integrationutils.WithAndWithoutCosmos(t, fn)` if/when we want to also
exercise real cosmos &mdash; for the initial pass, fake `KubeApplier` clients
backed by `MockDBClient` are sufficient.

Test setup:

1. `sigs.k8s.io/kind` to create a one-node cluster (or reuse an existing
   shared KIND harness if one exists in the repo).
2. Build `*rest.Config` against the KIND kubeconfig.
3. Build `dynamic.Interface` and `RESTMapper`.
4. Start an in-process `KubeApplier` `Run()` using:
   - real `dyn` + `rm` against KIND
   - `MockDBClient` as the Cosmos
   - `MANAGEMENT_CLUSTER=test-mgmt-1`
5. Drive the test by inserting `*Desire` documents via the mock and asserting:
   - the live KIND state changes as expected (kubectl-equivalent reads)
   - the mock observes the expected status writes

Smoke scenarios at minimum:

- Apply a Namespace + ConfigMap, verify they exist; mutate the ApplyDesire,
  verify the ConfigMap is updated; delete the ApplyDesire (mock-side),
  verify the ConfigMap is left in place (we are not cascading deletes).
- Create a DeleteDesire targeting the ConfigMap, verify it is deleted and
  the desire reports `Successful=True`.
- Create a ReadDesire on the ConfigMap; verify `.status.kubeContent`
  becomes the live ConfigMap; mutate the live ConfigMap directly via the
  KIND client; verify `.status.kubeContent` updates within one resync.

## 7.4 What we are explicitly *not* testing (yet)

- Multi-replica leader election &mdash; relies on plumbing already exercised
  by the backend; covered by smoke during deployment.
- Cosmos credential isolation &mdash; an IaC-level guarantee, not something
  this binary can self-test.
- Performance / scale &mdash; the readme has an empty "Scale" section. Once
  we have load expectations from the backend's design we can add a
  separate benchmark suite.

## 7.5 CI integration

- Unit tests run in the existing `make test` target after the new module is
  added to `go.work`.
- KIND integration tests piggyback on whatever harness `test-integration/`
  uses today &mdash; do not invent a new test runner.
- Add coverage reporting to the same dashboards that the backend uses.
