# Manually Trigger an E2E Gate Run

This SOP describes how to manually trigger the same Prow E2E job that EV2 uses for regional gating. See [When To Use This Procedure](#when-to-use-this-procedure).

## Background

Every EV2 rollout gates regional promotion on the `regionalGating` validation step defined in [`test/e2e-pipeline.yaml`](../../test/e2e-pipeline.yaml). The step triggers an environment-specific Prow job through the Gangway API, pinned via `--base-sha` to the exact ARO-HCP commit being rolled out. See [CI EV2 Integration](../ci/ev2-integration.md) for the full wiring.

The `make -C test/ {int,stage,prod}-e2e` targets invoke the same `prow-job-executor` with the same arguments, which is what makes them a faithful reproduction of the gate.

## When To Use This Procedure

| Situation | Environment | What to do |
|---|---|---|
| Failed rollout gate | PROD | Use the retry button in the [EV2 portal](https://ra.ev2portal.azure.net). Do **not** use this SOP. |
| Failed rollout gate | INT, STG | No per-step retry exists. Use this SOP — steps 1, 2 (retry variant), 3. See the alternatives below. |
| Validating E2E test-code changes, no PR open | INT, STG, PROD | Use this SOP — step 2 (branch variant) and step 3. |
| Validating E2E test-code changes, PR already open | INT, STG, PROD | Prefer the `/test` comment triggers in [Running E2E Tests In CI](../ci/e2e-testing.md); they need no local Azure credentials. |

The prohibition on manual testing in INT/STG/PROD described in that guide concerns manually creating clusters via `az` or the portal, not triggering the gate job.

### Alternatives For A Failed INT Or STG Gate

The EV2 portal offers no retry control on the gating step in these environments, so a flaky failure leaves three choices:

1. **Retry the whole rollout.** Correct but slow — it re-executes everything, not just the gate.
2. **Trigger another incremental rollout.** Also slow, and only makes sense if there is a subsequent revision to roll out anyway.
3. **Run the gate job manually with the rollout version associated** — this SOP. Fastest way to clear a flake, and the reason the `zz_injected_EV2RolloutVersion` association in [step 2](#retrying-a-failed-int-or-stg-gate) is mandatory rather than cosmetic: it is what ties the run back to the rollout being unblocked.

The `automatedRetry` block on the `regionalGating` step currently only takes effect in PROD. STG and INT set `useExclusiveLocks` in `stages.yaml`, and the sdp-pipelines generator ignores an explicit `automatedRetry` whenever the stage fails fast, so those stages fall back to the default MSI/connection-failure-only retry. Tracked as a generator fix in AROSLSRE-1764.

> [!IMPORTANT]
> This does not re-execute the EV2 step itself — it produces a passing gate run associated with the rollout. If the rollout still does not progress, fall back to option 1 or 2.

### Constraints When Testing Your Own Changes

When `BASE_SHA` is not supplied it defaults to `git rev-parse HEAD`, and Prow builds the `aro-hcp-tests` image from that commit, so your test code is what runs. Two constraints follow from how Prow resolves that commit:

- The commit must be pushed to **`origin`** (`Azure/ARO-HCP`). A personal fork is not enough — a temporary branch on origin is fine.
- Only **test code** is validated. RP and infrastructure changes are not deployed to INT/STG/PROD until after merge, so those cannot be exercised this way. Use the DEV `e2e-parallel` job for that, which provisions the DEV footprint on demand. See [CI Execution](../ci/execution.md).

To run a single test rather than the whole suite, apply a `MustFilter` in `test/cmd/aro-hcp-tests/main.go` as described in [Running Only Specific Tests](../ci/e2e-testing.md#running-only-specific-tests), then push and run the target.

> [!WARNING]
> Always revert the filter before merging. Leaving it in place silently skips tests in CI.

## Environment Reference

Every command below needs the values on one row of this table.

| Environment | Make target | `AZURE_TENANT_ID` (tenant) | Prow job |
|---|---|---|---|
| INT | `int-e2e` | `64dc69e4-d083-49fc-9569-ebece1dd1408` (Red Hat) | `branch-ci-Azure-ARO-HCP-main-e2e-integration-e2e-parallel` |
| STG | `stage-e2e` | `72f988bf-86f1-41af-91ab-2d7cd011db47` (Microsoft) | `branch-ci-Azure-ARO-HCP-main-e2e-stage-e2e-parallel` |
| PROD | `prod-e2e` | `72f988bf-86f1-41af-91ab-2d7cd011db47` (Microsoft) | `branch-ci-Azure-ARO-HCP-main-e2e-prod-e2e-parallel` |

`int-e2e` and `stage-e2e` pin `REGION=uksouth`; `prod-e2e` requires `REGION` to be supplied. All three set `CLOUD=public` and `GATE_PROMOTION=true`.

## Prerequisites

- `az login` completed for the tenant in the table above, with access to the `arohcpdev-global` Key Vault holding the `prow-token` secret. Verify the active account with `az account show`.
- `AZURE_TOKEN_CREDENTIALS=dev` exported. The make target does **not** set it, and without it the Key Vault lookup fails.
- `yq` installed (used by the make targets) and `jq` installed (used in step 1).
- `HEAD` pushed to a remote — the `e2e` target hard-fails otherwise.
- The rollout's ARO-HCP commit present on `origin` (Prow clones `Azure/ARO-HCP`).

## Procedure

### 1. Identify The Failed Run And Its Rollout Metadata

Only needed when retrying a rollout gate. Find the Prow run EV2 triggered. EV2-triggered runs carry `ev2.rollout/*` annotations; manual runs do not:

```bash
curl -s "https://prow.ci.openshift.org/prowjobs.js?omit=decoration_config,pod_spec" \
  | jq -r '.items[] | select(.spec.job=="branch-ci-Azure-ARO-HCP-main-e2e-integration-e2e-parallel")
           | {state:          .status.state,
              base_sha:       .spec.refs.base_sha,
              region:         .metadata.annotations["ev2.rollout/region"],
              build:          .metadata.annotations["ev2.rollout/build"],
              "sdp-pipelines": .metadata.annotations["ev2.rollout/sdp-pipelines"],
              "ARO-HCP":       .metadata.annotations["ev2.rollout/ARO-HCP"],
              url:            .status.url}'
```

Substitute the Prow job name for the environment being gated from the [Environment Reference](#environment-reference) table.

`prowjobs.js` only lists jobs still present in the Prow cluster. For older runs use the history page, which lists finished builds only:

```
https://prow.ci.openshift.org/job-history/gs/test-platform-results/logs/<jobName>
```

Record all four values — `base_sha`, `build`, `sdp-pipelines`, and `ARO-HCP`. Step 2 needs every one of them.

When testing your own changes rather than retrying a gate, skip this step: push your branch to `origin` and let `BASE_SHA` default to `HEAD`.

### 2. Trigger The Run

#### Retrying A Failed INT Or STG Gate

Check out the rollout's commit and pass the reconstructed rollout version. Both are required:

```bash
git checkout <full-sha-from-step-1>

AZURE_TOKEN_CREDENTIALS=dev \
AZURE_TENANT_ID=64dc69e4-d083-49fc-9569-ebece1dd1408 \
zz_injected_EV2RolloutVersion=build.<build>.sdp-pipelines.<sdp>.ARO-HCP.<12-char-sha> \
make -C test/ int-e2e 2>&1 | tee /tmp/int-e2e.log
```

Build the rollout version from the annotations captured in step 1 — `build.<ev2.rollout/build>.sdp-pipelines.<ev2.rollout/sdp-pipelines>.ARO-HCP.<ev2.rollout/ARO-HCP>`. For example:

```
zz_injected_EV2RolloutVersion=build.175911391.sdp-pipelines.0ee05788b.ARO-HCP.06acec5302a0
```

Why both are required:

- **The checkout.** `BASE_SHA` defaults to `HEAD`, so checking out the rollout's commit pins the run to it. [`test/execute-prow-job.sh`](../../test/execute-prow-job.sh) also aborts with `ARO-HCP commit mismatch` unless the trailing segment of the rollout version matches `git rev-parse --short=12 HEAD`.
- **The rollout version.** It is normally injected by EV2. Without it the run carries only `ev2.rollout/{cloud,environment,region}`, so it cannot be correlated with the rollout it was investigating and will not appear against that rollout. The value is split on dots into `tag.value` pairs and emitted as `ev2.rollout/<tag>` annotations.

#### Testing Your Own Pushed Branch

No commit pinning and no rollout version — `BASE_SHA` defaults to `HEAD`:

```bash
AZURE_TOKEN_CREDENTIALS=dev \
AZURE_TENANT_ID=64dc69e4-d083-49fc-9569-ebece1dd1408 \
make -C test/ int-e2e 2>&1 | tee /tmp/int-e2e.log
```

#### Common To Both

The examples above target INT. For STG or PROD, swap the make target and `AZURE_TENANT_ID` for the matching row of the [Environment Reference](#environment-reference) table, and supply `REGION` for `prod-e2e`.

> [!WARNING]
> Do not pipe this through `tail`. It buffers until the process exits, hiding the Prow job URL for the entire multi-hour run. Use `tee`.

To target a region other than `uksouth`, the wrapper targets will not help — they set `REGION` in the sub-make environment and override yours. Call the underlying `e2e` target directly:

```bash
PROW_JOB_NAME="$(yq .clouds.public.environments.int.defaults.e2e.regionTest.prowJobName \
  < config/config.msft.clouds-overlay.yaml)" \
REGION=<region> ENVIRONMENT=int AZURE_TOKEN_CREDENTIALS=dev \
make -C test/ e2e
```

### 3. Monitor And Act On The Result

The executor submits once and polls every 5 minutes until the job reaches a terminal state, printing the Prow URL on each update. With `GATE_PROMOTION=true` it exits non-zero if the job fails.

When retrying a rollout gate:

| Outcome | Action |
|---|---|
| Success | The failure was transient and the rollout should be able to proceed. |
| Same failure again | A real regression. Investigate the run's `finished.json` and JUnit output rather than retrying further. |

When testing your own changes, treat the run as ordinary CI feedback. Remember to revert any `MustFilter` before merging.

## Troubleshooting

| Symptom | Cause | Fix |
|---|---|---|
| `AADSTS50020: User account ... does not exist in tenant ...` repeating every few minutes with no other output | The active `az` login is in the wrong tenant for the target environment. `AZURE_TENANT_ID` does not re-scope an already-cached CLI login. The executor treats this as transient and backs off 1→2→4→8→16→32 min. | `az login --tenant <tenant-from-Environment-Reference> --scope "https://vault.azure.net/.default"` |
| `AZURE_TOKEN_CREDENTIALS must be set when RequireAzureTokenCredentials is true` | Missing `AZURE_TOKEN_CREDENTIALS=dev`. | Export it. |
| HTTP 403 with an HTML login page instead of JSON | Expired or invalidated `prow-token`. | Follow [Renew the Prow Token](renew-prow-token.md). |
| HTTP 429 from Gangway | Rate limit is roughly 9 requests/minute per client IP. | Wait; the executor retries with backoff over ~63 minutes. Do not launch a second run. |
| `... is explicitly required in go.mod, but not marked as explicit in vendor/modules.txt` | Stale vendor directory after a pull. | `go work vendor`, then discard the resulting `vendor/` changes before committing. |
| `ERROR: ARO-HCP commit mismatch` | `zz_injected_EV2RolloutVersion` set while on a different commit. | Check out the commit named in the rollout version, or unset the variable. |
| No job appears in Prow and there is no output | Output is buffered by `tail`, or the executor is asleep in a retry backoff. | Re-run with `tee`. Inspect with `pgrep -af prow-job-executor`. |

## Key Locations

| What | Where |
|------|-------|
| Make targets | [`test/Makefile`](../../test/Makefile) → `int-e2e`, `stage-e2e`, `prod-e2e`, `e2e` |
| Trigger script | [`test/execute-prow-job.sh`](../../test/execute-prow-job.sh) (used only by the make targets) |
| EV2 gate definition | [`test/e2e-pipeline.yaml`](../../test/e2e-pipeline.yaml) → `regionalGating` step |
| Job name per environment | [`config/config.msft.clouds-overlay.yaml`](../../config/config.msft.clouds-overlay.yaml) → `e2e.regionTest.prowJobName` |
| Token vault and secret | [`config/rendered/dev/dev/westus3.yaml`](../../config/rendered/dev/dev/westus3.yaml) → `global.keyVault.name`, `e2e.prow.globalKeyVaultTokenSecret` |
| Gangway API | `https://gangway-ci.apps.ci.l2s4.p1.openshiftapps.com/v1/executions` |
| Prow job status API | `https://prow.ci.openshift.org/prowjob?prowjob=<execution-id>` |
| EV2 portal | [ra.ev2portal.azure.net](https://ra.ev2portal.azure.net) |

## Related Documentation

- [CI EV2 Integration](../ci/ev2-integration.md)
- [EV2 Retry Catcher](../ci/ev2-retry-catcher.md)
- [Running E2E Tests In CI](../ci/e2e-testing.md)
- [Renew the Prow Token](renew-prow-token.md)
- [Test Tenant Access](test-test-tenant-access.md)
