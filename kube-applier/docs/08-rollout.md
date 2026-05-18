# 08 &mdash; Phased rollout plan

The goal of this phasing is that **every PR in the sequence is independently
shippable, reviewable, and produces no regressions in main**. Most early
PRs touch only `internal/` and have no runtime effect.

## Phase 0 &mdash; Resolve blocking design questions

Before any implementation:

- [ ] Confirm partition-key isolation strategy with platform: a single
      Cosmos role assignment scoped to one partition value per management
      cluster.
- [ ] Confirm the workload identity / service-account model the kube-applier
      pod will use.
- [ ] Confirm whether the kube-applier container should live in the same
      Cosmos *account* as `Resources` or a separate account. (Same account
      is simpler and matches readme; separate account is stronger
      isolation.)
- [ ] Decide field-manager string (recommend `kube-applier`).

## Phase 1 &mdash; API package (Doc 02)

Single PR.

- [ ] Add deepcopy markers + `doc.go` to `internal/api/kubeapplier`.
- [ ] Run generators; commit `zz_generated.deepcopy.go`.
- [ ] Add `ResourceType` constants and resource-ID helpers.
- [ ] Add condition-name and reason-string constants.
- [ ] Unit tests for resource-ID parse/format and JSON round-trips.

Exit criteria: `go build ./...` is green; nothing else depends on the new
package yet.

## Phase 2 &mdash; Database wiring (Doc 03)

Single PR.

- [ ] Add `kube-applier` container constant + client field.
- [ ] Add `NewKubeApplierPartitionKey` helper.
- [ ] Add `KubeApplier(...)` accessor + `KubeApplierCRUD` and the per-type
      `ResourceCRUD[T]` instances.
- [ ] Extend `GlobalListers` with `ApplyDesires`/`DeleteDesires`/`ReadDesires`.
- [ ] Add `database.Kube{Apply,Delete,Read}Desire` envelope types.
- [ ] Update `internal/databasetesting` mocks.

Exit criteria: existing tests stay green; new tests for the partition-key
helper and CRUD round-trip pass; `MockDBClient` can store and list each
`*Desire`.

## Phase 3 &mdash; Listers / Informers / Listertesting (Doc 04)

Single PR.

- [ ] Create `internal/database/listers/` with three listers + helpers.
- [ ] Create `internal/database/informers/` with the
      `KubeApplierInformers` factory.
- [ ] Create `internal/database/listertesting/` with slice and DB-backed
      fakes.
- [ ] Tests for each new package.

Exit criteria: `go test ./internal/database/...` passes; nothing else
depends on the new packages yet.

## Phase 4 &mdash; IaC for the new container

Single small PR, ideally landed in parallel with Phase 2/3 reviews.

- [ ] Add the `kube-applier` Cosmos container to bicep templates in
      `dev-infrastructure/`.
- [ ] Add managed-identity + role-assignment scoped to the new container,
      partitioned per management cluster.
- [ ] Verify in `cspr` first; deploy to `dev` after merge.

Exit criteria: the container exists in `cspr`; partition-scoped writes
work end-to-end via a one-off script.

## Phase 5 &mdash; Backend writer + cross-partition reads

Out of scope for these docs &mdash; this is whatever logic in `backend/`
will create `*Desire` documents in response to user-facing operations. Note
it for sequencing only: it cannot ship before Phase 2.

## Phase 6 &mdash; kube-applier binary skeleton (Doc 06.1-6.3)

Single PR.

- [ ] Create the `kube-applier/` Go module; add to `go.work`.
- [ ] `cmd/main.go`, `cmd/root.go`, `pkg/app/kubeapplier.go`.
- [ ] In-cluster client wiring; Cosmos wiring; leader election.
- [ ] Health (`:8083`) + metrics (`:8081`) servers.
- [ ] An empty `Run` loop that just blocks on context &mdash; no controllers
      yet.
- [ ] Dockerfile + Helm chart skeleton (Doc 06.4) without RBAC for desires.

Exit criteria: `make build` produces a binary; in a smoke test it acquires
the lease, exposes `/healthz` and `/metrics`, then exits cleanly on signal.

## Phase 7 &mdash; ApplyDesireController (Doc 05.1)

Single PR.

- [ ] Implement the controller with statuswriter + condition helpers.
- [ ] Wire it up in `Run`.
- [ ] Manifestclient-based unit tests covering the matrix in Doc 07.2.

Exit criteria: in cspr, a hand-inserted ApplyDesire produces the expected
kube object and `.status.conditions["Successful"]=True`.

## Phase 8 &mdash; DeleteDesireController (Doc 05.2)

Single PR. Same shape as Phase 7.

## Phase 9 &mdash; ReadDesire controllers (Doc 05.3 + 5.4)

Recommended split:

- 9a: `ReadDesireKubernetesController` plus a degenerate manager that hard-codes
       a single ReadDesire (so the per-instance controller can be tested
       end-to-end without the full lifecycle logic).
- 9b: `ReadDesireInformerManagingController` lifecycle (start/stop/recreate
       on TargetItem change).

Each is its own PR.

## Phase 10 &mdash; Integration tests (Doc 07.3)

Single PR after the controllers are in place.

- [ ] KIND-based integration tests under `test-integration/kube-applier/`.
- [ ] Add to CI.

## Phase 11 &mdash; Production deployment

- [ ] Tighten the kube-applier ClusterRole (off `cluster-admin`) once we
      know the GVR allowlist from real workloads.
- [ ] Author the `sdp-pipelines` PR for Microsoft int/stage/prod
      deployment per `CLAUDE.md`'s instructions.
- [ ] Set scale targets and alerting rules in `observability/`.

## Cross-cutting checklist (every PR)

- [ ] Lint: `make lint`.
- [ ] Generators: `make generate` (deepcopy, mocks).
- [ ] Tests: `make test` plus the package-specific test targets.
- [ ] License headers via `addlicense` (per `CLAUDE.md`).
- [ ] No new emojis; no new docstring bloat (per repo style).

## Risk register

| Risk | Mitigation |
| --- | --- |
| Per-partition Cosmos role doesn't exist / isn't fine-grained enough | Phase 0 confirmation; if blocked, fall back to a less-isolated single role and document the gap. |
| `manifestclient` doesn't support SSA replay accurately | Validate in Phase 1 with a throwaway test; fall back to envtest if needed. |
| Per-instance dynamic informers leak goroutines | Integration test specifically asserts goroutine count after rapid create/delete churn. |
| Cluster-admin RBAC blocks production approval | Tighten in Phase 11 before SRE-level deployment; do not ship cluster-admin to `int+`. |
| RESTMapper cache staleness when CRDs install | Use `DeferredDiscoveryRESTMapper` and reset on `NoKindMatch`. |
