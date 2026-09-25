# ARO HCP CI Incident Mode

## Purpose

During a CI or platform incident, stop automatic second-stage scheduling and
automated retries while inexpensive first-wave validation continues. Approved
fixes can still be tested explicitly. This procedure uses existing controller
configuration; it does not change E2E job code or require a PR label.

## Prerequisites

The incident-mode Make targets and `hack/toggle_arohcp_ci_incident_mode.py`
must be merged into `openshift/release` `main` and present in your checkout
before using this procedure. An open release PR does not make them available.
Create the incident branch from an up-to-date `main`, then check availability
from the release repository root without editing configuration:

```text
make -n enable-arohcp-ci-incident-mode disable-arohcp-ci-incident-mode
```

Confirm the output invokes the helper for `enable` and `disable`, and that the
helper file exists. If either target or the helper is missing, stop; wait for
the release support to merge and update your checkout before proceeding.

## Configuration

With the prerequisites satisfied, use these two release targets:

```text
make enable-arohcp-ci-incident-mode
make disable-arohcp-ci-incident-mode
```

They prepare the following paired configuration changes:

| Configuration | Normal operation | Incident mode |
| --- | --- | --- |
| `core-services/pipeline-controller/config.yaml`, Azure/ARO-HCP `main`, `mode.trigger` | `auto` | `manual` |
| `core-services/retester/_config.yaml`, `retester.orgs.Azure.repos.ARO-HCP.enabled` | `true` | `false` |

These scopes differ: pipeline manual mode applies to `main`, while retester
disablement affects **all branches and jobs** in Azure/ARO-HCP, including
retries of inexpensive checks. The incident owner and reviewers must explicitly
accept this repository-wide retry suppression before merging an activation PR.
This is an automatic-path pause, not a hard kill switch: authorized manual
`/test`, `/retest`, and `/pipeline` commands remain available.

Enable selects manual mode and disables automated retests. Disable restores
automatic mode and automated retests. Each command validates both starting
states before changing either file and rejects unexpected configuration.
No ci-operator test, generated Prow job, or E2E workflow is modified.

The helper requires Python 3 and PyYAML. It deliberately rejects ambiguous
entries and unsupported YAML layout changes. If it fails, review the current
configuration rather than bypassing validation. Run its focused regression
suite with `python3 -m unittest discover -s hack -p 'test_toggle_arohcp_ci_incident_mode.py'`
from the release repository. This local suite includes a round-trip against
copies of the checked-in config files. Run it before proposing a toggle or
changing either config's layout; it is not run by the shared CI jobs.

The commands edit a local working tree, not live CI. Each transition requires
a reviewed release PR, configuration deployment, and confirmation that both
services use the new settings. Retester reads its configuration at startup:
a ConfigMap update alone is insufficient. Coordinate a retester restart with
the Test Platform team after the updated ConfigMap is available, on both
activation and recovery. Pipeline-controller reloads its configuration
periodically; confirm the new mode rather than assuming merge is immediate
activation.

### Recover from a local write failure

Validation errors leave both files untouched, but writes are sequential, not
atomic across files. An I/O failure or interrupted process can leave one file
updated or partially written. Start with both config files clean, or save any
existing edits before toggling. If a write fails:

1. Do not stage or commit the incomplete transition. Inspect both files with
   `git diff -- core-services/pipeline-controller/config.yaml core-services/retester/_config.yaml`.
2. Fix the filesystem or permission problem. Restore only the toggle's partial
   changes from the pre-command versions of both files, preserving any unrelated
   work. Do not try the opposite target to repair a mixed state; it will reject
   that state.
3. Confirm both files match their original starting state, rerun the intended
   target, and review the complete two-setting diff before committing.

## Activate incident mode

1. Link the incident ticket and assign an owner for activation, manually
   approved runs, and recovery. Record the activation PR in the incident.
2. On a fresh release branch with both config files clean, run:

   ```text
   make enable-arohcp-ci-incident-mode
   ```

3. Review the diff. It must change only the ARO-HCP `main` pipeline from
   `auto` to `manual` and ARO-HCP retester enablement from `true` to `false`.
   Merge the release PR; a draft or held PR does not activate the switch.
4. Confirm pipeline-controller loaded manual mode. Coordinate and verify the
   retester restart after its ConfigMap contains the disabled repository
   setting. Do not consider the incident controls active until both are done.
5. Check an eligible PR at its current SHA: first-wave checks still run, but
   their completion no longer causes an automatic E2E scheduling comment.
   Confirm retester excludes ARO-HCP PRs instead of issuing automatic retests.
   Previously scheduled jobs and commands may still complete.

## Run E2E for an approved fix

1. Have the incident owner approve the proposed fix. There is no label gate.
2. Verify all required first-wave checks passed at the PR's **current HEAD
   SHA**, and that `e2e-parallel` is not already running or complete for it.
3. Comment `/test e2e-parallel` once and observe that exact run. A direct
   `/test` does not wait for first-wave success, so step 2 is a human check.
4. If the run fails, investigate before requesting another run. Retester will
   not automatically retry ARO-HCP jobs while incident mode is active.

Avoid `/pipeline required`, which can re-run the entire required second stage.
`/pipeline remaining` can also schedule second-stage jobs before the first
wave passes. Prefer the exact E2E command for an approved fix.

## Restore normal operation

1. Record incident resolution. Review the activation PR and all subsequent
   changes to both settings. Confirm no other active incident or owner still
   needs either restriction, and obtain authorization to restore **both**
   baseline values. If either restriction is still needed, stop and coordinate;
   do not run the disable target. Script state checks cannot determine why a
   setting was disabled or who owns it. On a fresh release branch, run:

   ```text
   make disable-arohcp-ci-incident-mode
   ```

2. Review the reverse two-setting diff and merge the release PR.
3. Confirm pipeline-controller loaded `auto`, and coordinate a retester
   restart after its ConfigMap again enables ARO-HCP. Verify its effective
   repository policy; restored retests can include failures from the incident.
4. Verify a new eligible PR automatically schedules E2E after first-wave
   success. Inventory PRs that waited during the incident: do not assume a
   mode change retroactively schedules their missing runs.
5. Before manually filling a missing E2E run, verify the current SHA's
   first-wave results and whether automatic scheduling or retester has already
   requested it. Avoid duplicate runs.

## Scope and limits

- Manual mode pauses **all** automatic second-stage pipeline-controller jobs
  for Azure/ARO-HCP `main`, not just `e2e-parallel`.
- Retester disablement is **repository-wide**, not per-test or branch-scoped;
  it also pauses automated retries of inexpensive checks. Their initial
  first-wave runs continue.
- Existing E2E jobs are unchanged. No label is enforced, and authorized manual
  commands can still start or retry tests. Manual `/test` and `/retest` are
  not disabled by the retester setting.
- Running or already-queued jobs are not cancelled. Periodic, postsubmit,
  EV2, and other independent retry mechanisms are outside these controls.
