# Kubernetes JSON decoding pilot: internal module

## Behavior and scope

- Branch: `chai/sigs-json-strict-internal`, based on upstream `main` at
  `4a04076b8`. The branch name is retained, but the implementation is NON-strict.
- `internal/jsonutil/json.go` exports `func Unmarshal(data []byte, v any) error`
  and directly returns `kjson.UnmarshalCaseSensitivePreserveInts(data, v)`.
  This mirrors apimachinery `serializer/json` non-strict decoding: preserve
  integers and match struct fields case-sensitively, without rejecting unknown
  or duplicate fields. Syntax and type errors still propagate.
- All 475 original `json.Unmarshal` sites across 56 files use the helper.
  Both original `json.NewDecoder` sites in `internal/errorutils/error_test.go`
  use `kjson.NewDecoderCaseSensitivePreserveInts(resp.Body).Decode(&cloudErr)`;
  the interim `io.ReadAll` conversion was removed. No original decoder sites
  remain, and existing error handling is preserved.
- Encoding stays on stdlib `encoding/json`. All existing `Marshal`,
  `MarshalIndent`, and `NewEncoder` call lines are unchanged.
- Only the `internal/` module and this requested root NOTES.md are changed;
  no YAML, other modules, or vendor directories are changed.
- `sigs.k8s.io/json v0.0.0-20250730193827-2d320260d730` is promoted from an
  existing indirect dependency to a direct dependency. There is no
  `internal/vendor` directory; `go mod vendor` is not applicable.

## Test changes and compatibility

- Helper tests cover valid objects, ignored unknown fields, last-duplicate-wins
  behavior, exact-case matching, ignored mismatched-case fields, syntax/type
  errors, and preservation of an integer larger than the exact float64 range.
- `internal/api/coreapi/defaults_test.go` now asserts `int64`, not `float64`,
  for OpenAPI integer defaults. It compares against `int64(expected)` instead
  of narrowing the decoded value to int32.
- The first non-strict full test run exposed a real integer-consumer assumption
  in `SetSoftDeleteFields`: `NestedFloat64` did not accept the new int64 values,
  so a soft delete reset instanceVersion 8 to 2 and informers ignored it as
  stale. Both content/directory cases of `TestFixtureLoadsReachSyncedInformers`
  timed out waiting for deletion (48 packages passed, one failed).
- Fixed that production consumer, rather than weakening the informer tests:
  read the nested value and accept int64 without a float conversion, while
  retaining support for legacy float64 callers and the existing fallback for
  missing/invalid values. Added regression tests for integers, integers beyond
  float64's exact range, legacy floats, zero, negative, invalid, and null values.
  Neither the JSON fixture content nor the soft-delete encoding call changed.
- No fixtures, field spellings, partial structs, or logging producers were
  changed to accommodate decoding. No real key-casing problem was identified
  in the validation performed.
- The earlier strict-decoding attempt failed in six packages because of
  intentional partial decoding, duplicate log keys, and continuation-token
  replay/shutdown cascades. The requested non-strict behavior intentionally
  accepts the omitted fields and duplicate keys rather than altering those
  contracts. Those earlier failures are historical, not the desired behavior.

## Separate issue: pre-existing duplicate-key logging

`CosmosSnapshotLogger` and `AddResourceTypes` remain unchanged. They expose a
pre-existing latent logging bug, not malformed handwritten fixture data:

- `CosmosSnapshotLogger` in `internal/database/informers/informerutils/snapshot.go`
  adds resource identity, then appends cluster/resource-group keys again.
  Its change-feed caller also attaches resource identity before calling it.
- `AddResourceTypes` in `internal/utils/context.go` appends the same
  `resourceType` key for each resource type.
- Captured first-party logs consequently contain duplicate `hcp_cluster_name`,
  `cluster_id`, `resource_group`, or `resourceType` keys. Non-strict decoding
  tolerates these, but duplicate-key consumers can interpret them differently.

Track deduplication/representation of these logging fields in a separate issue.
No issue was opened by this task, and this pilot does not fix the logging bug.

## Validation and repository conventions

Module commands run from `internal/` with `GOWORK=off` to validate its own
module dependencies without synchronizing or modifying other modules.

| Command | Result |
| --- | --- |
| `GOWORK=off go get sigs.k8s.io/json@v0.0.0-20250730193827-2d320260d730` (initial conversion) | Exit 0 |
| `GOWORK=off go mod tidy` (rerun after non-strict conversion) | Exit 0; go.sum unchanged |
| `GOWORK=off go test ./jsonutil ./api/coreapi ./errorutils` | Exit 0; all three packages pass |
| `GOWORK=off go build ./...` | Exit 0 |
| `GOWORK=off go vet ./...` | Exit 0; no diagnostics |
| `GOWORK=off go test ./database/cosmosstorage/cosmosstorageutils ./database/informers/coreinformers` | Exit 0 after integer-consumer correction; both packages pass |
| `GOWORK=off go test ./...` | Final rerun: exit 0; 49 packages pass, zero fail, 33 have no test files |
| `gofmt -l internal` (repository root) | Exit 0; no files listed |
| `/workspace/bin/goimports -l -local github.com/Azure/ARO-HCP internal` (repository root) | Exit 0; no files listed |
| `xargs /workspace/bin/addlicense -check -c 'Microsoft Corporation' -l apache < /workspace/changed-go-files.txt` (repository root) | Exit 0 |
| `git diff --check` (repository root) | Exit 0 |

Formatting used `xargs /workspace/bin/goimports -w -local github.com/Azure/ARO-HCP < /workspace/changed-go-files.txt`.
The file list contains all 57 modified existing Go files and the two new helper
files. Tools are repository-pinned goimports v0.26.0 and addlicense v1.1.1;
installation commands were `GOWORK=off GOBIN=/workspace/bin go install golang.org/x/tools/cmd/goimports@v0.26.0`
and `GOWORK=off GOBIN=/workspace/bin go install github.com/google/addlicense@v1.1.1`,
both exit 0. Content checks confirm 475 helper call sites, two Kubernetes decoder
sites, zero original stdlib decode sites, unchanged encoding call lines, and
unchanged implementations of both logging helpers.

Root AGENTS.md, CLAUDE.md, CONTRIBUTING.md, scoped guidance, and Makefile targets
were reviewed. `make fmt` supplies the goimports convention used here;
`make licenses` supplies the license convention checked on changed files.
No struct definitions, mock interfaces, or generation inputs changed, so
deepcopy/mock regeneration is not applicable. Whole-workspace `make generate`,
`make tidy`, and unrelated YAML/config/E2E verification targets would exceed
this pilot's scope and were not run.

The 378 generated Go decoding sites are included to cover every internal site.
`api/Makefile`'s `models` target regenerates the latest TypeSpec API; older
versions require historical sources. No generator inputs outside internal were
changed. Future SDK regeneration must preserve/reapply this JSON substitution;
a durable cross-module generator customization is outside this pilot.

Final validation logs are `/workspace/non-strict-final-{build,vet,test}.log`,
`/workspace/non-strict-focused-tests.log`, and
`/workspace/non-strict-regression-tests.log`. The initial failed non-strict run
is retained in `/workspace/non-strict-test.log`; its integer-consumer regression
is resolved in the final run. Tidy, formatting, license checks, build, and vet
were also rerun successfully after that correction.
