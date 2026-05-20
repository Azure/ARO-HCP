# Renew the Prow Token

This SOP describes how to renew the `prow-token` used by EV2 pipelines to trigger Prow E2E gating jobs via the Gangway API.

## Background

The `prow-token` is a Kubernetes ServiceAccount token for the `periodic-job-bot` SA in the `aro-hcp-prow-ci` namespace on the OpenShift CI cluster. EV2 pipelines use it during the `regionalGating` step (defined in [`test/e2e-pipeline.yaml`](../../test/e2e-pipeline.yaml)) to authenticate against the [Gangway API](https://gangway-ci.apps.ci.l2s4.p1.openshiftapps.com/v1/executions) and trigger postsubmit E2E jobs.

For more on how secrets are stored and deployed, see [Secret Synchronization](../secret-sync.md). For details on the Prow jobs themselves, see [Prow Jobs / EV2 Gating E2E Tests](../prow.md#ev2-gating-e2e-tests).

### Symptoms of an expired or invalid token

- EV2 rollouts get stuck at the `regionalGating` step
- The step log shows HTTP 403 with an HTML login page instead of a JSON response
- The [EV2 portal](https://ra.ev2portal.azure.net) shows the gating step as failed

### When does the token need renewal?

The current token is a legacy ServiceAccount token **without an `exp` claim**, so it does not expire by time. However, it can be invalidated by:

- OpenShift CI cluster upgrades that recreate the `aro-hcp-prow-ci` namespace
- Deletion/recreation of the `periodic-job-bot` ServiceAccount or its `api-token-secret`
- Changes to the RBAC or authentication configuration on the CI cluster

## Prerequisites

- Membership in the [`aro-hcp-prow-ci` Rover group](https://rover.redhat.com/groups/edit/members/aro-hcp-prow-ci)
- `oc` CLI installed
- `secret-sync` binary built (`cd tooling/secret-sync && make`)
- Logged into Azure CLI (`az login`) — only needed if you want to verify against Key Vault

## Procedure

### 1. Log into OpenShift CI

1. Go to the [OpenShift CI console](https://console-openshift-console.apps.ci.l2s4.p1.openshiftapps.com/)
2. Click your name in the top right, then **Copy login command**
3. Run the `oc login` command from the page

Verify access:

```bash
oc get secret api-token-secret -n aro-hcp-prow-ci
```

If this fails with a permissions error, confirm you are in the [Rover group](https://rover.redhat.com/groups/edit/members/aro-hcp-prow-ci).

### 2. Run the renewal script

The [`renew-prow-token.sh`](../../dev-infrastructure/openshift-ci/renew-prow-token.sh) script automates extracting the token and registering it in all 4 global Key Vaults.

**Extract from cluster and register in all vaults:**

```bash
./dev-infrastructure/openshift-ci/renew-prow-token.sh --extract
```

**Or provide a token file if you already extracted it:**

```bash
./dev-infrastructure/openshift-ci/renew-prow-token.sh --token-file /path/to/token.txt
```

**Or update only a single vault (for testing):**

```bash
./dev-infrastructure/openshift-ci/renew-prow-token.sh --extract --vault arohcpdev-global
```

### 3. Commit and create a PR

The script modifies `dev-infrastructure/data/encryptedsecrets.yaml` with the new encrypted token. This change must be committed and merged via PR:

```bash
git diff dev-infrastructure/data/encryptedsecrets.yaml
git add dev-infrastructure/data/encryptedsecrets.yaml
git commit -m "Renew prow-token in all global Key Vaults"
```

### 4. Wait for the token to reach Key Vault

The token only reaches Key Vault when the `decrypt-and-ingest-secrets` step in [`global-pipeline.yaml`](../../dev-infrastructure/global-pipeline.yaml) runs (action: `SecretSync`). This step is part of the `Microsoft.Azure.ARO.HCP.Global` service group, which is the first step of every EV2 rollout (see [Pipeline Documentation](../pipelines.md)).

- **Dev**: The `global-pipeline-postsubmit` Prow job runs automatically after PR merge.
- **Int / Stg / Prod**: EV2 rollouts consume a pinned ARO-HCP commit via [`sdp-pipelines/hcp/Revision.mk`](https://dev.azure.com/msazure/AzureRedHatOpenShift/_git/sdp-pipelines?path=/hcp/Revision.mk). After your PR merges, bump `Revision.mk` to a commit that includes the updated `encryptedsecrets.yaml`. The next EV2 rollout using that revision will populate the token during its Global step. See [Prepare a rollout](../ev2-deployment.md#prepare-a-rollout) for details.

## Key Locations

| What | Where |
|------|-------|
| Token source (K8s) | `aro-hcp-prow-ci` namespace, `api-token-secret` secret, on OpenShift CI cluster |
| Token storage (Azure) | 4 global Key Vaults: `arohcpdev-global`, `arohcpint-global`, `arohcpstg-global`, `arohcpprod-global` |
| Encrypted at rest | [`dev-infrastructure/data/encryptedsecrets.yaml`](../../dev-infrastructure/data/encryptedsecrets.yaml) |
| Renewal script | [`dev-infrastructure/openshift-ci/renew-prow-token.sh`](../../dev-infrastructure/openshift-ci/renew-prow-token.sh) |
| Config reference | [`config/config.yaml`](../../config/config.yaml) → `e2e.prow.globalKeyVaultTokenSecret: "prow-token"` |
| Pipeline using it | [`test/e2e-pipeline.yaml`](../../test/e2e-pipeline.yaml) → `regionalGating` step |
| Rover group | [aro-hcp-prow-ci](https://rover.redhat.com/groups/edit/members/aro-hcp-prow-ci) |
