# ARO HCP CI Incident Mode

## Purpose

During a CI or platform incident, stop automatic second-stage scheduling and
automated retries while inexpensive first-wave validation continues. Approved
fixes can still be tested explicitly. This procedure uses existing controller
configuration; it does not change E2E job code or require a PR label.

## Prerequisites

Use an ARO-HCP checkout containing `hack/ci-incident-mode`, the Go toolchain
specified by `go.work`, Git, and the GitHub CLI (`gh`). Configure your Git commit
name and email, and authenticate `gh` to `github.com` with permission to create
or use your personal fork of `openshift/release`, push branches, and open PRs.

No local release checkout or particular folder layout is required. The helper
clones current release `main` into a temporary directory, validates and applies
the paired change, commits it on a unique branch, pushes to your fork, and opens
a **draft** PR against `openshift/release` `main`. The temporary checkout is
removed on completion or handled failure. Existing local checkouts are untouched.

The configuration files remain owned by DPTP in `openshift/release`.
Activation and recovery each require a release PR approved by its owners;
hosting the helper in ARO-HCP does not change that approval requirement.

## Configuration

From the ARO-HCP repository root:

```text
make enable-arohcp-ci-incident-mode
make disable-arohcp-ci-incident-mode
```

Or invoke the helper directly:

```text
go run ./hack/ci-incident-mode enable
go run ./hack/ci-incident-mode disable
```

Each command opens a draft PR with the following paired configuration changes:

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

Enable proposes manual mode and disabled automated retests. Disable proposes
automatic mode and enabled automated retests. Each command validates both
starting states before changing either file and rejects unexpected configuration.
No ci-operator test, generated Prow job, or E2E workflow is modified. Review any
existing incident PR before running a command again; each successful invocation
creates a new branch and draft PR, not an update to a previous one.

The helper deliberately rejects ambiguous entries and unsupported YAML layout
changes. Layout checks and edits are bound to the parsed ARO-HCP settings, not
matching text elsewhere in the files. If validation fails, review the actual
configuration rather than bypassing the check.
Run its focused regression suite from ARO-HCP:

```text
go test ./hack/ci-incident-mode -v
```

Workflow tests use disposable local Git repositories and a simulated GitHub
boundary; they never publish to GitHub. They cover draft creation, failures
before push, recovery after a pushed branch, and temporary-checkout cleanup.
Maintainers can optionally set
`RELEASE_REPO` to a release checkout to exercise a round-trip against temporary
copies of its real config files; without it, that test is explicitly skipped.
This variable is only for tests, not for running the helper. No shared CI job
is modified to run the suite.

Opening a draft PR does not change live CI. Each transition requires
a reviewed release PR, configuration deployment, and confirmation that both
services use the new settings. Retester reads its configuration at startup:
a ConfigMap update alone is insufficient. Coordinate a retester restart with
the Test Platform team after the updated ConfigMap is available, on both
activation and recovery. Pipeline-controller reloads its configuration
periodically; confirm the new mode rather than assuming merge is immediate
activation.

### Recover from a failure

Both configs are validated before writing, and both writes must finish before
committing or pushing. A local validation, write, or commit failure stops the
workflow and discards the temporary checkout. It cannot publish a partial
two-setting change. Fix the reported configuration, filesystem, credentials,
or Git identity problem before retrying. A newly created fork may also need
time to become available before Git accepts a push.

If a push or PR request reports a network failure, inspect your fork and open
release PRs before retrying: the server may have accepted the request. Once a
push succeeds, the helper prints the fork and branch. If PR creation fails, it
also prints a `gh pr create` recovery command with that head. Use that existing
branch to complete the draft instead of rerunning the toggle and creating
another branch. No command automatically merges a PR or restarts a service.

## Activate incident mode

1. Link the incident ticket and assign an owner for activation, manually
   approved runs, and recovery. Record the activation PR in the incident.
2. From the ARO-HCP repository root, run:

   ```text
   make enable-arohcp-ci-incident-mode
   ```

3. Open the printed draft PR URL and link the incident. Review its diff: it
   must change only the ARO-HCP `main` pipeline from `auto` to `manual` and
   ARO-HCP retester enablement from `true` to `false`.
   [Request PR review](#request-pr-review) through the Slack workflow, obtain
   owner approval, and merge the release PR; an open, draft, or held PR does not
   activate it.
4. Confirm pipeline-controller loaded manual mode. Coordinate and verify the
   retester restart after its ConfigMap contains the disabled repository
   setting. Do not consider the incident controls active until both are done.
5. Check an eligible PR at its current SHA: first-wave checks still run, but
   their completion no longer causes an automatic E2E scheduling comment.
   Confirm retester excludes ARO-HCP PRs instead of issuing automatic retests.
   Previously scheduled jobs and commands may still complete.

## Request PR review

After running the enable or disable command:

1. In Slack's `#forum-ocp-testplatform`, click **+** → **Workflows** →
   **Request PR Review**.
2. Paste the full `openshift/release` PR URL printed by the command.
3. Add the incident link and say whether you are pausing or restoring CI, then
   submit. Owner approval and merge are still required.

## Run E2E for an approved fix

1. Have the incident owner approve the proposed fix. There is no label gate.
2. Verify all required first-wave checks passed at the PR's **current HEAD
   SHA**, and that `e2e-parallel` is not already running or complete for it.
3. Comment `/test e2e-parallel` once and observe that exact run. A direct
   `/test` does not wait for first-wave success, so step 2 is a human check.
4. If the run fails, investigate before requesting another run. Retester will
   not automatically retry ARO-HCP jobs while incident mode is active.

## Restore normal operation

1. Record incident resolution. Review the activation PR and all subsequent
   changes to both settings. Confirm no other active incident or owner still
   needs either restriction, and obtain authorization to restore **both**
   baseline values. If either restriction is still needed, stop and coordinate;
   do not run the disable target. Script state checks cannot determine why a
   setting was disabled or who owns it. From the ARO-HCP repository root, run:

   ```text
   make disable-arohcp-ci-incident-mode
   ```

2. Link the incident in the printed draft PR and review the reverse two-setting
   diff. [Request PR review](#request-pr-review) through the Slack workflow and
   obtain owner approval before merging it.
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
