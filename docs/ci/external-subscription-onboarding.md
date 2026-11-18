# External Subscription Onboarding and Maintenance

## Scope and Responsibility

This runbook prepares DEV E2E customer subscriptions owned by another team in
the same Entra tenant, such as HyperShift. For subscriptions maintained by the
ARO-HCP team, see [E2E subscription onboarding](e2e-subscription-onboarding.md).

The subscription owner prepares and maintains customer-subscription assets;
ARO-HCP maintainers coordinate the catalog, CI credentials, shared certificate
delivery and infrastructure subscription. Do not add an external subscription
to a broad privileged rollout instead of agreeing that responsibility split.
All apply/deployment commands below are **mutating operator commands**, not
CI admission commands or offline validation.

## Identity and Asset Model

| Identity / asset | Location and use | Lifecycle |
| :--- | :--- | :--- |
| CI bot | Selected cluster-profile identity; runs provisioning and tests with Step 1 grants | Not the subscription Owner used to prepare mock bundles |
| `e2e_identities` | Dedicated UAMI container resource groups in the customer subscription; tests lease containers within the slot | Owner applies deployment stacks; admission cleans and validates reusable identities; release retains containers |
| `mock_identities` | One MSI mock SP plus distinct Backend and Clusters Service ARM helper SPs per primary slot | Owner prepares applications, SPs, certificates and six subscription role assignments; admission only reads; no separate mock lease |
| `infrastructure_identities` | Independently leased UAMI bundle in the environment's infrastructure subscription | Infrastructure owner prepares it; admission cleans federated credentials and role assignments there; release preserves UAMIs and groups |
| Shared FPA / personal-dev mocks | Shared first-party mock still supplies FPA semantics; personal-dev retains shared MSI/ARM configuration | Maintained by shared bootstrap, not replaced by slot bundles |

The mock handler grants **only the slot's customer subscription**, never its
infrastructure subscription: MSI receives the subscription-local `dev-msi-mock`
role plus Key Vault Crypto User; each helper receives Contributor plus Role
Based Access Control Administrator. These are request-handler identities, not
the test runner. `armHelperFPAPrincipalId` remains the shared FPA principal, not
the authenticating helper SP. See the [mock handler](../../test/cmd/aro-hcp-tests/slot-manager/assets/mockidentities/handler.go),
[RBAC implementation](../../test/cmd/aro-hcp-tests/slot-manager/assets/mockidentities/azure.go)
and [asset lifecycles](../../test/cmd/aro-hcp-tests/slot-manager/DESIGN.md#asset-lifecycle).

Current DEV configuration sets both legacy indexed mock pools to zero and their
static YAML catalogs have been removed. This does **not** prove old Azure
objects, certificates or assignments have been deleted. Do not regenerate or
grant the old global catalogs to onboard slot-bound mocks. Shared FPA and
personal-dev identities remain necessary; see [DEV mock identities](dev-mock-identities.md).

| Work | Responsible party |
| :--- | :--- |
| Catalog pool, naming, regions and capacity | Subscription owner and ARO-HCP reviewers |
| Boskos generation / release PR and cluster-profile inventory | ARO-HCP / CI profile maintainers |
| Provider registration, CI bot grants and subscription-local bootstrap roles | Subscription owner |
| Dedicated E2E containers and mock bundles | Subscription owner with Entra and certificate administrators |
| Leased infrastructure capacity and runtime certificate delivery | Infrastructure / vault maintainers |
| Expired test-resource cleanup and explicit asset retirement | Subscription owner with CI maintainers |

## Catalog and Preflight

Use the current [slot catalog](../../test/e2e-config/e2e-slots.yaml), not a copied
inventory. HyperShift currently has `dev/hypershift` (2 slots, 20 containers per
slot, `ci01`) and `dev-xs/hypershift` (30 slots, 6 containers per slot, `ci02`),
both in `Hypershift Managed Azure`. Both use weighted `westus3`, `centralus`,
`canadacentral`, provision E2E containers in `westus3`, and lease from
`dev-infrastructure-identities`. These are separate pools, not interchangeable
aliases. Verify the current catalog before any capacity change.

For an externally prepared DEV pool, the asset declaration is:

```yaml
slot_assets:
  mock_identities:
    allocation: dedicated
  e2e_identities:
    allocation: dedicated
    provisioning: unmanaged
    provisioning_region: westus3
    resource_group_prefix: <unique-prefix-for-this-pool>
    resource_group_count: 6
  infrastructure_identities:
    allocation: leased
    asset_pool: dev-infrastructure-identities
```

This is an asset fragment, not a complete catalog. Keep the pool's unique
`resource_type`, subscription display name, region policy and agreed `slot_count`
in the surrounding declaration. **There is no `provisioning` field on
`mock_identities`:** its handler uses the E2E asset's `provisioning: unmanaged`
to skip broad pool apply/validate. The leased infrastructure asset has its own
inventory and does not inherit that skip. Unmanaged means externally prepared,
not optional or exempt from admission.

Coordinate Boskos sync and profile inventory before enabling the pool. After
building with `make -C test build`, maintainers use
`./test/aro-hcp-tests slot-manager sync-boskos-config --release-repo "<release-checkout>"`
and `validate-boskos-config` with the same argument, then the release repository's
generation/review workflow. The profile must resolve `subscriptions.e2e` by its
exact display name and contain the corresponding `customer-<shard>-subscription-name`
and `customer-<shard>-subscription-id` pair. See the
[profile resolution and credential implementation](../../test/cmd/aro-hcp-tests/slot-manager/slots/runtime_contract.go).

Before applying, arrange these **separate** authorization domains:

- **Azure:** active, unconditional Owner on the customer subscription or an
  ancestor, directly or through group membership. Activate eligible access.
  Mock apply explicitly checks for Owner; Contributor plus RBAC Administrator
  is not a substitute for this preflight. Do not grant Owner to runtime CI.
- **Entra / Microsoft Graph:** application and SP discovery/creation, application
  credential updates, owner enumeration/addition/removal on **both** objects,
  and reads of the configured owner group's membership. Subscription Owner
  grants none of these directory permissions. Coordinate the tenant-authorized
  delegated or application permissions with the directory administrator; the
  code does not define a universal least-privilege Graph role/scope recipe.
- **Key Vault:** certificate read and create permissions on the shared
  `https://aro-hcp-dev-svc-kv.vault.azure.net` used by the handler. Public
  certificate admission reads are distinct from runtime private-key/secret
  delivery. Coordinate the existing delivery path with vault and service
  maintainers; customer-subscription Owner does not authorize the shared vault.
  The certificate policy uses issuer `Self`, not an external CA; this does not
  remove the need for Key Vault data-plane authorization to create certificates.

In DEV, `serviceKeyVault.name` in [the configuration](../../config/config.yaml)
selects that shared vault. The bundle supplies certificate names, not private
keys. [Backend values](../../backend/values.yaml) map the MSI and Backend helper
names into the [Secrets Store CSI objects](../../backend/deploy/templates/backend.service-key-vault.secretproviderclass.yaml),
which read the certificate's **secret** representation using the Backend
workload identity. [Clusters Service values](../../cluster-service/values.yaml)
likewise select the MSI and its distinct helper certificate. Before enabling
jobs, have service/vault maintainers verify that the deployed service identities
can retrieve those named certificate bundles through the existing delivery
path. Successful operator apply or CI public-certificate reads do not prove
runtime secret access; do not export private keys into the catalog or slot env.

See [Graph and ARM operations](../../test/cmd/aro-hcp-tests/slot-manager/assets/mockidentities/azure.go),
[owner reconciliation](../../test/cmd/aro-hcp-tests/slot-manager/assets/mockidentities/owners.go)
and [certificate permissions](../../tooling/entra-app-credentials/README.md).

Install Azure CLI, Go and Bicep as required by the repository build, and
[Mike Farah `yq`](https://github.com/mikefarah/yq) v4 for the Make owner settings.
Use the intended operator's tenant/session. `AROHCP_ENV=development` selects
Azure CLI credentials when a complete service-principal environment is not set;
if all three `AZURE_CLIENT_ID`, `AZURE_CLIENT_SECRET`, `AZURE_TENANT_ID` are set,
the framework uses them instead. Do not inadvertently run as the CI bot or dump
credential values while checking the execution context.

## Step 1: Providers and CI Bot Grants

Register the resource providers needed by E2E operations in the target subscription:

```sh
SUBSCRIPTION_ID="<target-subscription-id>"
for ns in \
  Microsoft.Compute Microsoft.Insights Microsoft.KeyVault \
  Microsoft.ManagedIdentity Microsoft.Network Microsoft.Quota \
  Microsoft.RedHatOpenShift Microsoft.Storage; do
  az provider register --namespace "$ns" --subscription "${SUBSCRIPTION_ID}"
done
```

The backend's provider-registration validation checks `Microsoft.Authorization`,
`Microsoft.Compute`, `Microsoft.Network` and `Microsoft.Storage`; missing
registration prevents cluster provisioning. Authorization is normally registered
by default. Registration is asynchronous: wait and repeat this read until all
providers report `Registered` (not just `Registering`):

```sh
for ns in \
  Microsoft.Authorization Microsoft.Compute Microsoft.Insights \
  Microsoft.KeyVault Microsoft.ManagedIdentity Microsoft.Network \
  Microsoft.Quota Microsoft.RedHatOpenShift Microsoft.Storage; do
  echo "$ns: $(az provider show --namespace "$ns" \
    --subscription "${SUBSCRIPTION_ID}" --query registrationState -o tsv)"
done
```

The DEV OpenShift Release Bot (application `38335e22-716a-4a21-bf20-15ab141823f0`,
object `c209f8df-52ae-48fb-98ea-380f58b04652`) executes tests. Verify that the
selected profile uses the intended bot before granting access.

| Role | Condition / purpose |
| :--- | :--- |
| Contributor | Create/manage test resources and ARM deployments |
| Role Based Access Control Administrator | ABAC condition excludes assigning Owner, User Access Administrator and RBAC Administrator |
| Azure Kubernetes Service RBAC Cluster Admin | Only when this subscription also hosts AKS service/management infrastructure |

Use the [CI bot RBAC template](../../dev-infrastructure/templates/ci-bot-rbac-subscription.bicep)
to preserve those conditions. A customer-resource-only subscription does not
need the AKS grant; change `grantAksRbac` to `true` only if it hosts that infrastructure.

```sh
BOT_PRINCIPAL_ID="c209f8df-52ae-48fb-98ea-380f58b04652"
az deployment sub create \
  --subscription "${SUBSCRIPTION_ID}" --location westus3 \
  --template-file dev-infrastructure/templates/ci-bot-rbac-subscription.bicep \
  --parameters botPrincipalId="${BOT_PRINCIPAL_ID}" \
    isGlobalSubscription=false grantAksRbac=false
```

These ARM grants do not supply Graph reads or shared-vault certificate access
for admission. Profile maintainers must arrange those separately. Do not relax
the bot's ABAC condition to let it run Owner-only mock preparation.

## Step 2: Bootstrap Role Definitions and Shared Grants

The [subscription bootstrap template](../../dev-infrastructure/templates/e2e-subscription-rbac-assignment-subscription.bicep)
defines subscription-local custom roles, including
`dev-msi-mock-<subscription-id>`, which the slot handler requires but does not
create. It also assigns shared FPA, ARM helper and MSI roles. This is not the
per-slot bundle provisioner. The FPA grant remains required for slot jobs.

Coordinate with the shared identity maintainers to obtain the current **SP object
IDs**, not application/client IDs, for the applications named by
`config/config-dev-ci.yaml` (`aro-dev-first-party2`, `aro-dev-arm-helper2`,
`aro-dev-msi-mock2` today). Verify unique application/SP resolution in the tenant;
do not choose an arbitrary display-name match. The privileged pipeline's
[Graph lookup](../../dev-infrastructure/templates/entra-app-lookup.bicep) and
[bootstrap caller](../../dev-infrastructure/templates/mock-identity-rbac.bicep)
are the canonical resolution path. Retain shared personal-dev grants; this
template has no role-definitions-only switch.

```sh
FIRST_PARTY_PRINCIPAL_ID="<verified-first-party-sp-object-id>"
ARM_HELPER_PRINCIPAL_ID="<verified-shared-arm-helper-sp-object-id>"
MSI_MOCK_PRINCIPAL_ID="<verified-shared-msi-mock-sp-object-id>"
az deployment sub create \
  --subscription "${SUBSCRIPTION_ID}" --location westus3 \
  --template-file dev-infrastructure/templates/e2e-subscription-rbac-assignment-subscription.bicep \
  --parameters firstPartyPrincipalId="${FIRST_PARTY_PRINCIPAL_ID}" \
    armHelperPrincipalId="${ARM_HELPER_PRINCIPAL_ID}" \
    miMockPrincipalId="${MSI_MOCK_PRINCIPAL_ID}"
```

The template's legacy `msiMockPoolPrincipals` and `armHelperPoolPrincipals`
parameters default to empty arrays. Do not feed removed catalogs into them.
It defines custom-role display names with the subscription ID suffix to avoid
tenant-wide name collisions. Do not override the MSI role's name: the slot
handler looks up that exact subscription-local definition.

## Step 3: Prepare and Validate the Selected Pool

Run from the repository root, using the owner/operator credentials established
above. For HyperShift's smaller-per-job `dev-xs` pool, use **both** selectors and an explicit
asset kind on every invocation:

```sh
AROHCP_ENV=development make -C test apply-pool-assets \
  ENVIRONMENT=dev-xs POOL=hypershift SUBSCRIPTION="Hypershift Managed Azure" ASSET=e2e_identities
AROHCP_ENV=development make -C test validate-pool-assets \
  ENVIRONMENT=dev-xs POOL=hypershift SUBSCRIPTION="Hypershift Managed Azure" ASSET=e2e_identities
AROHCP_ENV=development make -C test apply-pool-assets \
  ENVIRONMENT=dev-xs POOL=hypershift SUBSCRIPTION="Hypershift Managed Azure" ASSET=mock_identities
AROHCP_ENV=development make -C test validate-pool-assets \
  ENVIRONMENT=dev-xs POOL=hypershift SUBSCRIPTION="Hypershift Managed Azure" ASSET=mock_identities
```

For the separate `dev/hypershift` pool, repeat with `ENVIRONMENT=dev`, after
confirming its desired capacity. Do not run a bare environment-wide apply.
The [Makefile](../../test/Makefile) regenerates Bicep-derived ARM artifacts before
building the binary; bypassing it with `go run` or a stale binary risks applying
stale embedded templates. `apply-identity-pool` is an E2E-container-only alias;
it neither prepares mock bundles nor forwards `POOL`.

The [command implementation](../../test/cmd/aro-hcp-tests/slot-manager/assets_cmd.go)
maps `ENVIRONMENT` to `--environment`, `POOL` to `--pool`, `SUBSCRIPTION` to
`--subscription` (the **display name**, not ID), and `ASSET` to `--asset`.
Pool and subscription filters intersect; a nonmatching selection errors. Either
explicit selector includes unmanaged E2E and mock assets. `ASSET` alone does
not include unmanaged assets and, if omitted, all registered kinds may run,
including infrastructure identities in another subscription. There is no
`--include-unmanaged` switch. Keep the explicit asset selections above.

For mock apply **and** validation, Make exports `ENTRA_APP_OWNER_IDS` and
`ENTRA_APP_OWNERS_GROUP_ID` from `.clouds.dev.defaults.entraAppOwnerIds` and
`entraAppOwnersGroupId` in `config/config-dev-ci.yaml`. `MOCK_IDENTITY_CONFIG`
selects another file; explicit environment/Make values are supported. Do not
substitute a local arbitrary owner list: it must match the configured live
Entra group's members. Invalid settings or group drift fail before mock writes.

Apply authoritatively replaces owners on **both applications and SPs** with
that list. Missing owners on both objects are added and verified before extras
are removed. The creator is removed if absent from the list. An operator whose
only application access is ownership can therefore **lock themselves out**;
arrange continuing management authority or hand off to an authorized owner /
tenant administrator. Repository OWNERS membership and subscription Owner are
not Entra application ownership. Reconciliation is sequential, not a transaction
across objects, slots or asset handlers. Serialize operations; after partial
failure, correct permissions/drift and retry the same scoped command.

## Step 4: Enable Cleanup and Verify Admission

Arrange an expired-resource-group cleanup job in `openshift/release`, scoped to
the customer subscription, for example:

```yaml
- as: delete-expired-dev-ci-hypershift-resource-groups
  cron: 35 * * * *
  steps:
    env:
      CLEANUP_MODE: no-rp
      CUSTOMER_SUBSCRIPTION: Hypershift Managed Azure
    test:
    - ref: aro-hcp-deprovision-expired-resource-groups
```

Coordinate a rehearsal only after provisioning, profile delivery and Boskos
inventory are ready. Admission must resolve all three distinct principals,
valid pinned certificates and six persistent assignments using the selected
profile. Verify that the job exports `LEASED_MOCK_IDENTITY_BUNDLE` and uses the
intended pool/subscription; malformed bundles fail rather than falling back to
old catalogs. Runtime configuration overrides retain their normal precedence.
Verify acquire, test-resource creation, release and subsequent reuse. Local
schema checks alone do not establish live readiness.

## Maintenance and Repair

- **Regular verification:** repeat the two scoped `validate-pool-assets`
  commands after role, owner-group, certificate or catalog changes and before
  reopening drained capacity. Mock validation checks owners as well as the
  bundle; CI admission does not inspect owners. Reconcile shared bootstrap
  definitions/grants in Step 2 when their source changes, then validate slot RBAC.
- **Fail-closed admission:** mock admission reads Graph, public certificates
  and subscription RBAC on every lease/readmission. It never repairs, rotates,
  deletes assignments or resets credentials. A failure with an Owner-repair
  instruction must be handled by the authorized operator, not a broader CI role
  or a fallback identity. Fix the reported prerequisite, run the scoped mock
  apply and validation from Step 3, then coordinate a new admission attempt.
- **Certificate creation/pinning:** normal apply uses `--create-missing`
  semantics from [entra-app-credentials](../../tooling/entra-app-credentials/README.md):
  it preserves an existing certificate and policy and pins the current public
  certificate. Pinning replaces the application's **entire** `keyCredentials`
  collection. Use this only for the dedicated applications. Names are
  `aro-hcp-slot-<UUIDv5>-{msi,backend,cs}`, derived from customer subscription ID
  and primary slot name, not region; certificate name equals application name,
  with DNS `<name>.hcp.osadev.cloud`.
- **Rotation:** expired/disabled or incorrectly pinned certificates fail
  admission. Expiry is not repaired by `create-missing`. Drain affected jobs and
  coordinate explicit disruptive `entra-app-credentials pin --rotate` with
  the vault/delivery maintainers as described in
  [dedicated slot operations](dev-mock-identities.md#dedicated-slot-identity-operations).
  **Current CLI limitation:** the handler derives certificate DNS names as
  `<slot-application-name>.hcp.osadev.cloud`, but these exceed the `pin` CLI's
  64-character common-name limit. Copying that generated DNS name into
  `--mapping` fails locally, before credential acquisition. Do not present it as
  a working rotation command or silently truncate it. Before rotating, obtain
  a vault-owner-approved certificate DNS name of at most 64 characters (which
  changes the certificate subject/SAN), or arrange a separately reviewed
  tooling correction. The CLI restriction is in
  [mapping validation](../../tooling/entra-app-credentials/cmd/pin/options.go);
  the self-signed subject/SAN policy is in
  [certificate creation](../../tooling/entra-app-credentials/pkg/pinning/keyvault.go).
  For a verified dedicated application and an approved DNS name, the operation is:

  ```sh
  SLOT_APP_NAME="<verified-dedicated-slot-application-name>"
  SLOT_CERT_DNS="<approved-certificate-dns-name-at-most-64-characters>"
  AZURE_TOKEN_CREDENTIALS=dev go run ./tooling/entra-app-credentials pin \
    --vault-url https://aro-hcp-dev-svc-kv.vault.azure.net \
    --mapping "${SLOT_APP_NAME};${SLOT_APP_NAME};${SLOT_CERT_DNS}" \
    --rotate
  ```

  If rotation creates a certificate but pinning fails, retry scoped mock apply
  to pin the current version, not another rotation. Shared-pipeline reconciliation
  alone does not repair dedicated slot apps.
- **Capacity and non-destructive reduction:** provision/validate new capacity
  before making it available through Boskos. Budget all pools sharing the
  subscription, including six persistent RBAC assignments per mock slot and
  any remaining legacy grants. For a lower leased container count, keep the
  old physical `identity_container_provisioning_count` until users of the old
  count drain. Stop new leases while removing physical resources and verify no
  affected resources are still in use before applying the smaller physical count.
  See [capacity management](identity-leasing.md#managing-identity-container-capacity).
  Reducing a deployment stack's physical count can **delete** removed containers;
  do not describe all apply operations as non-destructive. Reducing slot counts
  or mock pool declarations does not itself delete the old mock applications,
  certificates or RBAC grants, or sweep old per-slot stacks.
- **Retirement:** first stop new leases and drain all affected primary and
  independent leases; then separately inventory and remove only the retired
  slot's backing resources and grants with the responsible owners. Renaming
  primary slots changes mock identity names and can leave old resources behind.
  Never delete identities or certificates still used by leased slots, shared
  FPA/personal-dev consumers, or old-baseline jobs.
- **Cleanup ownership:** release retains persistent mock objects, baseline RBAC,
  certificates and identity containers. E2E/infrastructure admission cleanup
  has different semantics from read-only mock admission. Periodic cleanup is
  for expired test resources, not bundle retirement. Test-scoped HSM grants
  can remain until resource cleanup; soft deletion/purge protection and failed
  purge mean immediate erasure is not guaranteed. Monitor cleanup failures;
  see [CI cleanup](cleanup.md) and [HSM lifecycle](dev-mock-identities.md#test-scoped-managed-hsm-access).

## See Also

- [E2E Subscription Onboarding](e2e-subscription-onboarding.md)
- [DEV Mock Identities](dev-mock-identities.md)
- [CI Identity Leasing](identity-leasing.md)
- [Dev-CI Topology](dev-ci-topology.md)
