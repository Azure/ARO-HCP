# Reusable Infrastructure Identity Deployment Contract

ARO HCP infrastructure identity reuse is disabled by default. Without an
explicit bundle input, service and management deployments create all managed
identities in their deployment resource groups exactly as before.

The future slot-manager infrastructure identity handler may opt a DEV
deployment into reuse by publishing one shell-escaped JSON value in
`LEASED_INFRASTRUCTURE_IDENTITY_BUNDLE` after acquisition and admission:

```json
{
  "serviceResourceGroup": "persistent-service-identities",
  "managementResourceGroups": {
    "1": "persistent-management-1-identities",
    "2": "persistent-management-2-identities"
  }
}
```

Resource-group names are resolved in the subscription of each deployment
scope. Service-cluster identities must exist in the service deployment
subscription, and each management stamp's identities must exist in that
management deployment's subscription. The contract intentionally does not
support cross-subscription identity references.

This is a proposed deployment input, not an export implemented by
Azure/ARO-HCP#7104. The lifecycle implementation introduced by that PR exports
`INFRA_SUBSCRIPTION_ID` only when infrastructure assets are demanded; it does
not publish the bundle mapping above or enable infrastructure identity leasing
in the live catalog.

`hack/ci/build-config-override.sh` validates the input and writes the three
deployment settings under `infrastructureIdentities`, setting `useLeased: true`.
Parameter templates pass this flag as `useLeasedInfrastructureIdentities`;
the shared managed-identities module receives `useLeasedIdentities`.
Missing or malformed fields fail config generation. Config validation also requires a mapping for
the management stamp being rendered, so enabling reuse cannot silently fall
back to identity creation.

Each resource group is one complete bundle. Bundle selection is explicit:
`serviceResourceGroup` is the service cluster bundle and every
`managementResourceGroups` key is the management stamp identifier. Resource
group array order and the primary Boskos slot name have no meaning.

The service bundle must contain these canonical roles using the identity names
already configured for the deployment:

- 12 workload identities: frontend, backend, billing, Maestro server, Clusters
  Service, logs, Prometheus, MSI credentials refresher, Admin API, SessionGate,
  custom exporter, and Fleet
- the AKS control-plane identity (`<service AKS name>-msi`)
- `image-puller`

Each management bundle must contain:

- six workload identities: Maestro consumer, management agent, logs,
  Prometheus, Velero, and kube-applier
- the AKS control-plane identity (`<management AKS name>-msi`)
- `image-puller`

The role list is authoritative for deployment wiring, while the configured
identity names remain authoritative for actual Azure names. The future handler
must provision and validate all roles before publication.

In reuse mode Bicep treats the UAMIs as `existing` resources in the current
deployment subscription and supplied resource groups. It still creates per-run federated identity
credentials for the new AKS OIDC issuer and applies the same RBAC grants at
their original target scopes. AKS-generated kubelet and Key Vault Secrets
Provider identities remain unchanged.

## Personal DEV testing

The standalone subscription-scoped
[`infrastructure-identities.bicep`](../../dev-infrastructure/modules/infrastructure-identities.bicep)
module provisions the identity bundles without FICs, RBAC, AKS clusters, or
deployment stacks. It is not part of environment deployment or cleanup.
The generated test parameters create **30 UAMIs: 14 service + 8 management
stamp 1 + 8 management stamp 2**. They require three RGs, not one: names such as
`image-puller`, `logs-mdsd`, and `prometheus` repeat across bundles.

The test helper renders names from your personal configuration, including each
stamp's AKS control-plane identity name. This single-subscription provisioner
is for personal DEV testing; it rejects a split-subscription configuration.
The deployment wiring itself still supports service/management subscriptions
being different. No slot-manager acquisition or admission is performed.
Reserve the bundles exclusively for this environment; do not share them with
another running environment.

Use a **fresh** personal environment. Do not switch an existing AKS cluster's
control-plane identity as part of this test. If necessary, remove your existing
personal environment using the normal cleanup procedure first. All commands
below run from the deployment-wiring repository root with the same `USER`.

### 1. Prepare inputs locally

Complete the [personal-dev prerequisites](../personal-dev.md#prerequisites),
then choose an identity RG prefix outside the environment RG naming scheme:

```bash
export LEASED_INFRA_IDENTITY_PREFIX="aro-hcp-infra-identities-${USER}"
export LEASED_INFRA_IDENTITY_DIR="$PWD/_artifacts/leased-infra-identities"

make prepare-leased-infra-identities

export SUBSCRIPTION="$(yq -r '.svc.subscription.key' "$LEASED_INFRA_IDENTITY_DIR/config-1.yaml")"
export LOCATION="$(jq -r '.parameters.location.value' "$LEASED_INFRA_IDENTITY_DIR/deployment-parameters.json")"
```

This does not access Azure. It writes the provisioning parameters, a personal
config override enabling `useLeased` and two management stamps, and the rendered
configuration for each stamp. `DEPLOY_ENV` must be `pers`. A custom `CONFIG_FILE`
can be supplied, but use the same file when preparing and deploying.
Your Azure principal must be allowed to create RGs and UAMIs in the subscription;
the environment deployer must also be allowed to assign those UAMIs from their
persistent RGs.

### 2. Provision the three identity RGs

Review the account and what-if output before deploying:

```bash
az account show --subscription "$SUBSCRIPTION" --query '{name:name,id:id,tenantId:tenantId}'
az deployment sub what-if \
  --subscription "$SUBSCRIPTION" --location "$LOCATION" \
  --template-file dev-infrastructure/modules/infrastructure-identities.bicep \
  --parameters @"$LEASED_INFRA_IDENTITY_DIR/deployment-parameters.json"

az deployment sub create \
  --subscription "$SUBSCRIPTION" --location "$LOCATION" \
  --name "$LEASED_INFRA_IDENTITY_PREFIX" \
  --template-file dev-infrastructure/modules/infrastructure-identities.bicep \
  --parameters @"$LEASED_INFRA_IDENTITY_DIR/deployment-parameters.json"
```

This creates `${LEASED_INFRA_IDENTITY_PREFIX}-svc`,
`${LEASED_INFRA_IDENTITY_PREFIX}-mgmt-1`, and
`${LEASED_INFRA_IDENTITY_PREFIX}-mgmt-2`. Use an ordinary deployment, **not a
deployment stack**. These RGs have `persist=true`, which gives them **15 days**
under the current DEV sweeper policy, not indefinite retention.

Capture the identity IDs and principals for subsequent comparisons:

```bash
snapshot_identities() {
  for suffix in svc mgmt-1 mgmt-2; do
    az identity list --subscription "$SUBSCRIPTION" \
      --resource-group "${LEASED_INFRA_IDENTITY_PREFIX}-${suffix}" \
      --query '[].{id:id,clientId:clientId,principalId:principalId}' -o json || return
  done | jq -s 'add | sort_by(.id)'
}
set -o pipefail
snapshot_identities > "$LEASED_INFRA_IDENTITY_DIR/identities-before.json"
jq -e 'length == 30' "$LEASED_INFRA_IDENTITY_DIR/identities-before.json"
```

### 3. Deploy personal DEV using those identities

```bash
make personal-dev-env-leased-identities
```

This uses the normal build/push/deploy workflow, merging the leased identity
override with the image overrides rather than replacing them. To use published
images instead of building locally:

```bash
make personal-dev-env-leased-identities USE_LATEST_IMAGES=1
```

The target prepares inputs again but **does not provision or recreate the
identity pool**. Missing identities fail deployment; there is no fallback to
creating new ones. Keep using this target, or explicitly pass its config
override to partial pipeline commands, for subsequent updates.
This target disables templatize's step cache on every run, so retries and
recreation do not reuse cached step results. The normal `personal-dev-env`
target retains its existing caching behavior.

### 4. Verify identity use

Check that the service cluster and both management clusters reference their
respective persistent RGs:

```bash
az aks show --subscription "$SUBSCRIPTION" \
  -g "$(yq -r '.svc.rg' "$LEASED_INFRA_IDENTITY_DIR/config-1.yaml")" \
  -n "$(yq -r '.svc.aks.name' "$LEASED_INFRA_IDENTITY_DIR/config-1.yaml")" \
  --query 'identity.userAssignedIdentities'
for stamp in 1 2; do
  az aks show --subscription "$SUBSCRIPTION" \
    -g "$(yq -r '.mgmt.rg' "$LEASED_INFRA_IDENTITY_DIR/config-${stamp}.yaml")" \
    -n "$(yq -r '.mgmt.aks.name' "$LEASED_INFRA_IDENTITY_DIR/config-${stamp}.yaml")" \
    --query 'identity.userAssignedIdentities'
done

az identity federated-credential list --subscription "$SUBSCRIPTION" \
  -g "${LEASED_INFRA_IDENTITY_PREFIX}-mgmt-1" \
  --identity-name "$(yq -r '.maestro.agent.managedIdentityName' "$LEASED_INFRA_IDENTITY_DIR/config-1.yaml")" \
  --query '[].{name:name,issuer:issuer,subject:subject}'

snapshot_identities > "$LEASED_INFRA_IDENTITY_DIR/identities-after.json"
diff -u "$LEASED_INFRA_IDENTITY_DIR/identities-before.json" \
  "$LEASED_INFRA_IDENTITY_DIR/identities-after.json"
```

The diff must be empty. Verify FIC issuers match their cluster's
`oidcIssuerProfile.issuerUrl`, and inspect workload pods/service accounts and ACR
pull permissions as in normal personal-dev testing. AKS-created kubelet and
Key Vault add-on identities are intentionally excluded from the 30.

### 5. Test teardown and reuse

Keep the override during cleanup so both management stamps are selected:

```bash
make cleanup-entrypoint/Region \
  OVERRIDE_CONFIG_FILE="$LEASED_INFRA_IDENTITY_DIR/config-override.yaml" \
  CLEANUP_DRY_RUN=true

# After reviewing the proposed environment RG deletions:
make cleanup-entrypoint/Region \
  OVERRIDE_CONFIG_FILE="$LEASED_INFRA_IDENTITY_DIR/config-override.yaml" \
  CLEANUP_DRY_RUN=false CLEANUP_WAIT=true

snapshot_identities > "$LEASED_INFRA_IDENTITY_DIR/identities-after-cleanup.json"
diff -u "$LEASED_INFRA_IDENTITY_DIR/identities-before.json" \
  "$LEASED_INFRA_IDENTITY_DIR/identities-after-cleanup.json"

make personal-dev-env-leased-identities USE_LATEST_IMAGES=1
```

The identity RGs must not appear in the environment cleanup plan, and all 30
identity IDs, client IDs, and principals must survive unchanged. The target's
disabled step cache forces deployment after external resource deletion. Repeat
the checks in step 4; FICs must now use the recreated clusters' OIDC issuers.
This helper does not implement general admission cleanup of stale permissions.

When finished, clean up the environment again, then explicitly delete only the
three test identity RGs if you no longer need them. Do not delete them while
their identities are still used by running clusters.

### Offline checks

```bash
make prepare-leased-infra-identities
BICEP="$HOME/.azure/bin/bicep" bash hack/prepare-leased-infra-identities_test.sh
python3 hack/latest-services-override_test.py
```

The offline test compares provisioning names against the actual service and
management deployment inputs, checks the 14/8/8 inventory and explicit stamps,
compiles the provisioner, and checks override merging and invalid inputs.
It also checks that Fleet's Azure Monitor permissions select the correct identity
RG in both default and leased modes.
AROBit's service and management lookups and kube-applier's Cosmos permissions are
checked using the parameter files selected by the pipelines, including both
management stamps and both identity modes.
The image-override regression test uses offline lookups to check that merging
waits for every image lookup and stops if any lookup fails.
