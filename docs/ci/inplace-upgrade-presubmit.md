# In-Place Upgrade Presubmit (`e2e-parallel-inplace-upgrade`)

`e2e-parallel-inplace-upgrade` is a required presubmit that checks a PR can be rolled out on top of infrastructure that is already running `main`. The standard `e2e-parallel` job always provisions a fresh environment, so it cannot see problems that only appear on an upgrade: a breaking Bicep parameter change, a missing migration step, an incompatible configuration transition, or customer clusters that do not survive the rollout. This job exists to catch those.

It is scheduled automatically alongside `e2e-parallel` once the first-wave checks pass, and can be re-run with `/test e2e-parallel-inplace-upgrade`. It runs in its own DEV environment (`ci02`) with a dedicated E2E subscription, so it does not compete with `e2e-parallel` for slots or for customer-subscription quota; the pool is defined in [`e2e-slots.yaml`](../../test/e2e-config/e2e-slots.yaml). The independence is not complete: both release workflows still declare the legacy MSI-mock and ARM-helper leases, which every run takes, so an upgrade run can wait behind `e2e-parallel` even when `ci02` slots are free. See [Slot Assets](slot-assets.md#mock-identities) for that remaining gate. Region selection for this job works the same way as for `e2e-parallel`, see [DEV CI Regional Load Management](dev-region-load-management.md).

## How It Works

The job uses the `aro-hcp-local-e2e-upgrade` workflow from `openshift/release` (`ci-operator/step-registry/aro-hcp/local-e2e-upgrade`). After acquiring a slot (see the [slot-manager design](../../test/cmd/aro-hcp-tests/slot-manager/DESIGN.md#a-jobs-path)), it runs two main phases.

**Baseline from `main`.** The `aro-hcp-provision-from-main` step checks out the commit the PR is based on and provisions a complete environment from it, using that commit's templates, charts and configuration together with the service images that the `images-push` postsubmit published to the shared ACR for the same commit. If those images are not published yet it waits for a while. If they never appear, it falls back to the images of the newest recent `main` commit that has them. Only the service images change: the templates, charts and configuration stay at the PR's base commit, so the baseline can mix revisions. Keep that in mind when an upgrade failure looks like a regression, and check which image revision the job actually resolved. The script is [`hack/ci/provision-from-main.sh`](../../hack/ci/provision-from-main.sh).

**Upgrade and validate.** The `aro-hcp-test-local-upgrade` step builds a configuration override from the images and HyperShift settings built for the PR, then runs the `upgrade/in-place` suite. Each test in that suite creates its own cluster and node pool on the baseline and records its state. Once every test has reported in, the suite runs the regional deployment pipeline once with the PR's code, and then each test validates that its own cluster is still healthy and was updated as expected. How the tests coordinate around the single upgrade is described in [Upgrade Barrier and Coordinator](../../test/AGENTS.md#upgrade-barrier-and-coordinator).

The usual post steps gather artifacts, delete tracked resource groups, tear down the environment, and release the slot.

## Interpreting Failures

Which part of the job failed tells you whose problem it is:

- **`aro-hcp-provision-from-main` fails.** The baseline could not be created from `main` before your PR was applied, so `main` itself, or its published images, is the problem. Check the health of `main`'s CI rather than the PR.
- **`aro-hcp-test-local-upgrade` fails during the upgrade.** The environment built from `main` could not be rolled forward to the PR's state, which is the regression this job is designed to find. Compare the Bicep, Helm and configuration differences between `main` and the PR, and look at the pipeline output in the step's artifacts.
- **`aro-hcp-test-local-upgrade` fails in post-upgrade validation.** The rollout itself succeeded but a customer cluster did not come out healthy. This can be an upgrade problem or an ordinary bug in the PR's service code.

## Limitations

The baseline is provisioned with the `templatize` binary baked into the PR's test image, running against `main`'s pipeline definitions. A PR that changes `templatize` in a way incompatible with `main`'s pipelines can therefore make the baseline fail spuriously. The baseline step also needs service images for a recent `main` commit in ACR, so it fails if the `images-push` postsubmit has been broken for a long stretch.
