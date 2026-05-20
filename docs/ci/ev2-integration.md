# CI EV2 Integration

This document is the deep-dive companion to the shorter `EV2 Commit Pinning` section in [CI Execution](execution.md). It explains how EV2 rollouts invoke Prow, how the exact job name is selected, how commit pinning works, and how the Gangway authentication path is wired.

## Why EV2 Uses Prow Gating

Public-cloud rollouts need validation against the exact ARO-HCP revision being promoted. A periodic job cannot provide that signal because periodic jobs always test `HEAD`.

That is why EV2 uses Prow gating:

- EV2 selects an environment-specific Prow job
- the Prow run is started programmatically rather than by a GitHub event
- the run is pinned to the exact ARO-HCP commit being deployed
- the rollout can use the result as a promotion signal

In short, EV2 gating is not "just another scheduled E2E." It is rollout-coupled validation.

## Current Environment Mapping

The public-cloud environment mapping lives in `config/config.msft.clouds-overlay.yaml`.

Current `prowJobName` mappings are:

- **INT** -> `branch-ci-Azure-ARO-HCP-main-e2e-integration-e2e-parallel`
- **STG** -> `branch-ci-Azure-ARO-HCP-main-e2e-stage-e2e-parallel`
- **PROD** -> `branch-ci-Azure-ARO-HCP-main-e2e-prod-e2e-parallel`

This is the first place to check if a rollout is invoking the wrong Prow job for a given environment.

## How EV2 Maps To Prow Jobs

The direct contract between EV2 and Prow is defined in `test/e2e-pipeline.yaml`.

The key `regionalGating` validation step looks like this:

```yaml
validationSteps:
  - name: regionalGating
    action: ProwJob
    tokenKeyvault: "{{ .global.keyVault.name }}"
    tokenSecret: "{{ .e2e.prow.globalKeyVaultTokenSecret }}"
    jobName: "{{ .e2e.regionTest.prowJobName }}"
    gatePromotion: "{{ .e2e.regionTest.gatePromotion }}"
```

That wiring means:

- the selected `prowJobName` comes from the environment config
- the Gangway token comes from the global Key Vault secret configured for Prow
- the pipeline contract also supports `gatePromotion` as an environment-specific behavior flag

The current configuration snapshot above shows `prowJobName` directly in `config/config.msft.clouds-overlay.yaml`. Whether `gatePromotion` is set for a particular environment should always be verified from the active config rather than assumed from older docs.

## Programmatic Triggering And The `__e2e` Variant

The EV2-triggered jobs are defined in:

- `openshift/release: ci-operator/config/Azure/ARO-HCP/Azure-ARO-HCP-main__e2e.yaml`

That file is important for two reasons:

- the tests are marked `postsubmit: true`
- each job uses `run_if_changed: ^$`

That combination means these jobs are not automatically triggered on merge. Instead, they exist to be started programmatically through Gangway by the EV2 pipeline path.

This is why EV2-triggered runs show up in Prow as postsubmit-like jobs even though the trigger originated from EV2 rather than from a normal GitHub merge event.

## Commit Pinning And Test Image Fidelity

The commit-pinning path is what makes EV2 gating materially different from scheduled tests.

At a high level:

1. EV2 decides which ARO-HCP revision is being rolled out
2. `prow-job-executor` extracts that revision from `EV2_ROLLOUT_VERSION`
3. the Prow run is triggered with `--base-sha`
4. the E2E job runs against that pinned revision instead of `HEAD`

The `__e2e` variant also includes the `aro-hcp-e2e-tests` image build in the job configuration, so the test binary is built from the same source revision that EV2 requested. This keeps the test image and the rolled-out source aligned.

That alignment is the key guarantee EV2 gating provides:

- **EV2-triggered run:** validates the exact revision being promoted
- **Periodic run:** validates whatever is at `HEAD` at the time the job starts

For the broader CI image model around shared CI images, job-local builds, and promoted runner images, see [CI Image Lifecycle](image-lifecycle.md).

## Gangway Authentication And `prow-token`

The EV2 path authenticates to Gangway using the `prow-token` secret stored in Azure Key Vault.

Conceptually, the path is:

1. EV2 reaches the `regionalGating` ProwJob validation step
2. the step fetches the configured token secret from the global Key Vault
3. the token is used to authenticate against the Gangway API
4. Gangway starts the requested Prow job

The operational procedure for renewal lives in:

- [Renew the Prow Token](../sops/renew-prow-token.md)

Symptoms that usually point to token problems:

- the EV2 rollout gets stuck or fails at `regionalGating`
- the step log shows HTTP 403 with an HTML login page instead of a JSON Gangway response

If that happens, treat the token path as part of the rollout plumbing, not as a generic test failure.

## Identifying Rollouts From Prow Metadata

When an EV2-triggered Prow E2E job is running, you can map it back to the originating rollout from the job metadata in the Prow dashboard.

Look for annotations prefixed with:

- `ev2.rollout/ARO-HCP`
- `ev2.rollout/build`
- `ev2.rollout/region`
- `ev2.rollout/sdp-pipelines`

Those annotations give you the rollout identifier, build number, region, and the SDP pipelines revision associated with that run.

This is the fastest path when you need to answer:

- "Which rollout triggered this job?"
- "Which region was this gate evaluating?"
- "Which SDP pipeline revision was involved?"

## Promotion Gating

The pipeline contract includes `e2e.regionTest.gatePromotion`.

Conceptually, when that field is enabled for an environment:

- EV2 waits for the Prow result
- rollout progress depends on the job outcome
- failed validation blocks promotion to the next stage

The important operational point is that promotion gating is config-driven. The docs should explain the mechanism, but the active behavior for a specific environment should always be verified from the current environment config and pipeline inputs.

## Where To Look

When you need to change or debug EV2-to-Prow integration, start here:

- environment-to-job mapping: `config/config.msft.clouds-overlay.yaml`
- EV2 validation step contract: `test/e2e-pipeline.yaml`
- token-renewal procedure: `docs/sops/renew-prow-token.md`
- EV2-triggered ci-operator variant: `openshift/release: ci-operator/config/Azure/ARO-HCP/Azure-ARO-HCP-main__e2e.yaml`
- release-side job definitions: `openshift/release: ci-operator/jobs/Azure/ARO-HCP/`
- Prow job history: [Prow dashboard](https://prow.ci.openshift.org/?repo=Azure%2FARO-HCP)

## See Also

- [CI Overview](README.md)
- [CI Execution](execution.md)
- [CI Operations](operations.md)
- [Renew the Prow Token](../sops/renew-prow-token.md)
