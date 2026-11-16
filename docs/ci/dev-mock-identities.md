# DEV Mock Identities

DEV (and the MSIT INT environment) cannot use the real Azure
control planes that the product relies on in production:

- there is no Microsoft **First Party Application (FPA)** that the Resource Provider
  and Clusters Service can act through, and
- the **Managed Identities Data Plane** service — which hands out the runtime
  credentials of a cluster's user-assigned managed identities — is only available in
  tenants where that FPA integration exists.

To run end to end without those, DEV substitutes a small set of **mock identities**:
ordinary Entra service principals that impersonate the identities the product would
otherwise use. This document explains what each mock identity stands in for, why it
needs exactly the roles it is granted, and how those roles are assigned today.

## The Identities At A Glance

The DEV bootstrap layer manages five logical identity groups. Their Entra
**application definitions** (display name, certificate DNS, and certificate
vault URL) come from `config/config-dev-ci.yaml` under
`.ci.dev.mockIdentities`; the service principal **object IDs are no longer stored
in config** — they are resolved at deployment time via Microsoft Graph lookup
(see [How The Roles Are Assigned](#how-the-roles-are-assigned)):

| Identity | Mocks | Roles it receives |
|---|---|---|
| `aro-dev-first-party2` (firstParty) | The Microsoft First Party Application | `dev-first-party-mock` (custom) |
| `aro-dev-arm-helper2` (armHelper) | The FPA's ARM/RBAC delegation ("MockFPA") | `Contributor` + `Role Based Access Control Administrator` (built-in) |
| `aro-dev-arm-helper-pool-<i>` (ARM helper pool) | Same as armHelper, one per Boskos lease | Same as armHelper |
| `aro-dev-msi-mock2` (miMock) | Every per-cluster operator managed identity | `dev-msi-mock` (custom) + `Key Vault Crypto User` (built-in) |
| `aro-dev-msi-mock-pool-<i>` (pool) | Same as miMock, one per Boskos lease | Same as miMock |

Clusters Service consumes them via its deployment arguments
(`cluster-service/helm-charts/cluster-service/templates/deployment.yaml`):
`azureFirstPartyApplicationClientId`, `azureArmHelperIdentityClientId` +
`azureArmHelperMockFpaPrincipalId`, and `azureMiMockServicePrincipal*`.

## First Party Mock — `aro-dev-first-party2`

This principal stands in for the Microsoft First Party Application. In production the
FPA performs the subnet-delegation handshake (service association links) when a
customer virtual network is attached to a managed cluster, and reads/writes the
managed resource group.

Its custom role `dev-first-party-mock` therefore grants only:

- `Microsoft.Network/virtualNetworks/subnets/serviceAssociationLinks/*` — create,
  read, validate and delete the service association link that delegates a subnet.
- `Microsoft.Resources/subscriptions/resourceGroups/read|write` — manage the
  managed resource group.

That narrow scope is deliberate: the first-party mock does **not** create the
cluster's Azure resources or assign roles. Those are the ARM helper's job.

## ARM Helper — `aro-dev-arm-helper2` (the "MockFPA")

In production the FPA can act on behalf of the customer to create Azure resources and
to **assign roles to the cluster's operator identities**. DEV has no FPA, so the ARM
helper impersonates that capability — hence the config key
`azureArmHelperMockFpaPrincipalId` ("Mock FPA principal id").

It is granted two **built-in** roles at subscription scope:

- **Contributor** (`b24988ac-…`) — create and manage the Azure resources a cluster
  needs.
- **Role Based Access Control Administrator** (`f58310d9-…`) — create the role
  assignments that bind each cluster operator identity to its operator role.

Note that Azure does **not** require the granting principal to hold the permissions
it hands out; *Role Based Access Control Administrator* can assign `Key Vault Crypto
User` (and the operator roles) without holding them itself. This is why the ARM
helper, not the MSI mock, is the identity that performs operator-role assignments.

The pooled `aro-dev-arm-helper-pool-<i>` principals are identical ARM helper
clones on the DEV home and E2E customer subscriptions. The home-subscription
grants make it possible to test a pool member in a personal development
environment. CheckAccess throttles third-party applications per
identity/application, while each concurrent E2E environment runs its own
backend-local limiter. Leasing one clone per environment prevents those
independent backends from aggregating their CheckAccess requests through
`aro-dev-arm-helper2`.

At runtime the first lease overrides `armHelperClientId` and
`armHelperCertName` for Backend. The second lease overrides
`clustersServiceArmHelperClientId` and `clustersServiceArmHelperCertName`.
`armHelperFPAPrincipalId` remains the shared first-party mock principal: despite
its similar name, it identifies the principal that receives the simulated FPA
grant, not the ARM helper authenticating the request.

INT uses named ARM helper identities rather than the DEV lease pool. Backend
uses `armHelperClientId` and `armHelperCertName`; Clusters Service can use the
independent `clustersServiceArmHelperClientId` and
`clustersServiceArmHelperCertName` values. Environments without a dedicated
Clusters Service identity set both pairs to the same identity; the Clusters
Service chart reads only the `clustersServiceArmHelper*` pair.

## MSI Mock — `aro-dev-msi-mock2` and the pool

This is the most important — and most easily misunderstood — identity.

Because the Managed Identities Data Plane is unavailable, the backend/CS uses a mock
client (`backend/pkg/azure/client/hardcoded_identity_mi_dataplane_client.go`) whose
own documentation states it plainly: *"all requests made with it return a single
Azure Service Principal identity, disguised as a Managed Identity."* In other words,
**every per-cluster operator identity — Cloud Controller Manager, Ingress, the
network operator, the KMS plugin, and so on — authenticates at runtime as the single
MSI mock service principal.**

That is why its custom role `dev-msi-mock` is the **union** of what those operators
actually do at runtime:

- `Microsoft.ManagedIdentity/userAssignedIdentities/read` and
  `…/federatedIdentityCredentials/*` — workload-identity federation for the
  operators.
- A broad set of `Microsoft.Network/*` actions on load balancers, subnets, NSGs,
  route tables, NAT gateways, private DNS zone links and virtual networks — the
  operations the cloud-controller-manager, ingress and network operators perform.

For the same reason it also needs Key Vault crypto access: when a cluster enables
etcd data encryption, the **KMS plugin authenticates as the MSI mock** and calls
Key Vault key operations. This path is exercised by DEV E2E
(`test/e2e/cluster_create_private_kv.go`,
`test/e2e/cluster_create_complex_cilium_kv.go`,
`test/e2e/cluster_version_backlevel.go`). The MSI mock is therefore granted the
**built-in `Key Vault Crypto User`** role (`12338af0-0e69-4776-bea7-57ae8d297424`) —
the same role the product assigns to the per-cluster KMS identity for both Dev and
Public (`internal/azure/cluster_scoped_identities_config.go`).

The pooled `aro-dev-msi-mock-pool-<i>` principals are functionally identical clones
of the MSI mock. They exist so concurrent presubmit jobs can each lease a distinct
principal via Boskos, spreading subscription-level ARM throttling. They receive the
exact same roles. See [CI Identity Leasing](identity-leasing.md) for the lease model.

## Why Some Roles Are Custom And Others Built-In

Historically the operator-equivalent permissions were packaged as **custom** roles
(`dev-operator-roles`, and a custom `Azure Red Hat OpenShift KMS Plugin - Dev` role)
because the corresponding Azure **built-in** roles were not yet approved or available
across the tenants. As the built-ins became available, the setup migrates to them:

- ARM helper already uses built-in `Contributor` + `Role Based Access Control
  Administrator`.
- The MSI mock's KMS grant now uses the built-in `Key Vault Crypto User` instead of
  the retired custom KMS role — matching INT and the product.

The two remaining custom roles, `dev-first-party-mock` and `dev-msi-mock`, persist
because they bundle a bespoke set of actions with no single built-in equivalent.

## How The Roles Are Assigned

The mock identities are created and granted their roles by two Bicep templates,
both deployed by the `dev-ci` topology's **Owner-only, on-demand**
`Microsoft.Azure.ARO.HCP.DevCI.Privileged` entrypoint, run by an OWNERS-group
member with `make dev-ci-privileged-local-run` (it is excluded from the
unattended `dev-ci` postsubmit because it needs subscription Owner):

- `templates/mock-identity-apps.bicep` creates the Entra applications and their
  service principals via `modules/entra/app.bicep`. It configures **no**
  authentication mechanism on the apps: the mock certificates are self-signed and
  Clusters Service authenticates in send-certificate-chain mode, so Subject Name
  and Issuer (SNI) trust does not validate (Entra rejects the self-signed chain
  with `AADSTS7000213`). Instead the privileged pipeline's DEV
  `pin-mock-certs` Shell step pins the certificate's public key onto each shared
  and pooled app as a `keyCredential` immediately after app deployment (see
  [Certificates](#certificates)), and Entra authenticates by matching the
  presented leaf's thumbprint. The template deliberately does not manage
  `keyCredentials`, so redeploying it does not wipe the pinned certs. It also
  does **not** create the Key Vault certificates itself; the following
  privileged Shell step owns certificate creation and pinning.
- `templates/mock-identity-rbac.bicep` looks up each shared and pooled service
  principal's object ID via Microsoft Graph (by `uniqueName`, the normalized
  application name), then fans out over the target subscriptions and deploys
  `e2e-subscription-rbac-assignment-subscription.bicep` into each. That per-
  subscription module is **self-contained**: it defines the custom roles
  (`dev-first-party-mock`, `dev-msi-mock`) inline in the target subscription and
  creates all the role assignments. Defining the roles locally avoids any cross-
  subscription `assignableScopes` dependency, so the pipeline can onboard
  subscriptions it does not own. Because each subscription gets its own copy and
  Azure enforces custom-role display-name uniqueness **per tenant**, the display
  name is suffixed with the subscription id (e.g. `dev-msi-mock-<subscriptionId>`)
  to avoid `RoleDefinitionWithSameNameExists`. Pooled ARM helpers receive
  Contributor plus Role Based Access Control Administrator in both the DEV home
  subscription and the E2E customer subscriptions.

The set of subscriptions that receive grants is controlled by two parameters:

- `e2eSubscriptionIds` — the onboarded E2E customer subscriptions, from
  `.ci.dev.e2eSubscriptions` (DEV) / `.ci.int.e2eSubscriptions` (INT).
- `grantHomeSubscription` — whether to also grant the mock identities on the
  **deployment** ("home") subscription. DEV deploys into its own home (global)
  subscription, so it sets this `true`. INT leaves it `false` (the default),
  because INT's apps are deployed from the DEV global subscription while INT's
  real home subscription is already listed in `.ci.int.e2eSubscriptions` —
  setting it true would wrongly grant INT roles on the DEV global sub.

Application definitions, role names, and the subscription lists are supplied by
`configurations/mock-identity-apps*.tmpl.bicepparam` and
`configurations/mock-identity-rbac*.tmpl.bicepparam` from `config/config-dev-ci.yaml`.

### Certificates

The mock identity apps authenticate with a **pinned certificate**
(`keyCredentials`), not SNI. Bicep can neither create Key Vault certificates nor
read their material, so the privileged pipeline invokes
`tooling/entra-app-credentials` immediately after deploying the apps:

Application names, certificate names, certificate DNS names, and indexed pool
bases all come from the environment's `mockIdentities` configuration.

1. Read the current certificate from the environment Key Vault
   (`aro-hcp-dev-svc-kv` for DEV, `aro-hcp-int-kv` for INT).
2. If it does not exist, create it with the established self-signed certificate
   policy. Existing certificates and policies are left untouched.
3. Register the current public certificate as the app's sole pinned
   `keyCredential` and verify the resulting thumbprint through Microsoft Graph.
4. Apply RBAC only after every requested certificate has been reconciled.

This makes `make dev-ci-privileged-local-run` the only routine write entrypoint
for mock applications, certificates, pinning, and RBAC. Increasing a configured
pool size creates and wires the missing indexed resources automatically.
Decreasing a size does not delete higher-index resources.

Templatize executes the Shell steps with the invoking OWNERS member's Azure CLI
credentials. The caller needs Key Vault certificate create/read, Graph
application ownership/write, and the subscription permissions documented above.

### Disruptive Certificate Rotation

Rotation is intentionally excluded from the pipeline. It is rare, disruptive,
and must be requested directly through the certificate CLI. **Expect mock
identity authentication to fail temporarily between Key Vault replacement and
successful Graph pinning. Do not rotate certificates while DEV or INT E2E work
needs uninterrupted authentication.**

From the repository root, rotate only the intended application:

```bash
AZURE_TOKEN_CREDENTIALS=dev go run ./tooling/entra-app-credentials pin \
  --vault-url https://<vault-name>.vault.azure.net \
  --mapping '<application-name>;<certificate-name>;<certificate-dns-name>' \
  --rotate
```

Use the application, certificate, DNS, and vault values from
`config/config-dev-ci.yaml` and the pipeline mappings. The command replaces the
Key Vault certificate, waits until the new version is current, pins its
thumbprint, and verifies the Graph update.

If rotation creates the certificate but pinning fails, **do not immediately run
another rotation**. Run `make dev-ci-privileged-local-run`; its normal
create-if-missing reconciliation reads the current certificate and repairs the
application without creating another version.

Key Vault lifetime policies remain unchanged and may auto-renew certificates.
An auto-renewed certificate can likewise drift from the pinned thumbprint until
the privileged pipeline is run.

> Historical note: an earlier iteration configured SNI
> (`trustedSubjectNameAndIssuers`) on the apps instead of pinning. That does not
> work for self-signed certs when the client sends the chain (Entra validates the
> full CA chain and fails with `AADSTS7000213`), which caused a CI-wide auth
> outage. `mock-identity-apps.bicep` now clears any stale SNI config on redeploy.

## Where To Look

- `config/config-dev-ci.yaml` — `.ci.dev.mockIdentities` / `.ci.int.mockIdentities`
  application definitions and pool settings
- `dev-infrastructure/templates/mock-identity-apps.bicep` — creates the Entra
  apps + service principals (auth is pinned out-of-band, see Certificates)
- `dev-infrastructure/templates/mock-identity-rbac.bicep` — Graph lookup of
  principal IDs + fan-out RBAC across home and E2E subscriptions
- `dev-infrastructure/templates/e2e-subscription-rbac-assignment-subscription.bicep`
  — inline custom role definitions + all assignments (per subscription)
- `dev-infrastructure/dev-ci/e2e-subscription-rbac-grants/pipeline.yaml` — the
  privileged pipeline that deploys the templates above
- `backend/pkg/azure/client/hardcoded_identity_mi_dataplane_client.go` — the mock MSI
- `internal/azure/cluster_scoped_identities_config.go` — product operator-role mapping
- `cluster-service/helm-charts/cluster-service/templates/deployment.yaml` — how CS
  consumes the mock identities

## See Also

- [CI Overview](README.md)
- [Dev-CI Topology](dev-ci-topology.md)
- [E2E Subscription Onboarding](e2e-subscription-onboarding.md)
- [CI Identity Leasing](identity-leasing.md)

## Slot-bound CI bundles

DEV/dev-xs slot-manager jobs use the dedicated asset described in the
[slot-manager design](../../test/cmd/aro-hcp-tests/slot-manager/DESIGN.md#dedicated-dev-mock-identities).
New jobs consume its bundle, not legacy static-pool leases. Personal-dev
shared-principal configuration remains supported. Old in-flight jobs retain their
old checked-out consumers.

The legacy DEV pools are retired. `armHelperPool` and `pool` are configured with
`size: 0`, so the privileged pipeline creates no members, and the Boskos catalogs
`arm-helper-pool.yaml` and `msi-mock-pool.yaml` no longer exist. The existing pool
applications and their certificates are deleted separately, because decreasing a
pool size does not delete higher-index resources.

### Dedicated slot identity operations

The operator needs an active, unconditional customer-subscription Owner
assignment (direct, inherited or through group membership), Entra application/SP
management rights and certificate access to `https://aro-hcp-dev-svc-kv.vault.azure.net`.
Activate eligible Owner access before applying. The preflight uses ARM's
`atScope() and assignedTo(...)` filter for inherited and transitive group access.
Install the existing subscription RBAC template first: its subscription-local
`dev-msi-mock-<subscription-id>` role is reused.

```bash
AROHCP_ENV=development make -C test apply-pool-assets ENVIRONMENT=dev POOL=shard0 ASSET=mock_identities
AROHCP_ENV=development make -C test validate-pool-assets ENVIRONMENT=dev POOL=shard0 ASSET=mock_identities
```

Select the intended pool and environment (`dev` or `dev-xs`). Unmanaged pools
are skipped unless explicitly selected with `SUBSCRIPTION`, for example
`SUBSCRIPTION='Hypershift Managed Azure'` for its subscription Owner. Do not run
Owner reconciliation from CI. Run one reconciliation per selected pool at a time.
Failed applies can be retried: application unique names, SP application IDs,
certificate names and role assignment IDs are stable. Ambiguous or mismatched
Graph objects require investigation, never arbitrary selection or deletion.

Apply also reconciles Entra ownership of every slot application **and** service
principal to exactly `entraAppOwnerIds` (`config/config-dev-ci.yaml`, mirroring the
`aro-hcp-owners` group), the same authoritative `replace` semantics as the dev-ci
pipeline. This grants the configured Entra group members ownership, not repository
OWNERS membership or ARM subscription roles. `make -C test apply-pool-assets`
and `validate-pool-assets` export `ENTRA_APP_OWNER_IDS` and
`ENTRA_APP_OWNERS_GROUP_ID` from that file (requires Mike Farah `yq` v4). Explicit
environment or Make overrides are supported but must still match the live group;
`MOCK_IDENTITY_CONFIG` selects an alternate config file. The binary fails before
mock-asset writes when these settings are invalid or the list has drifted from
the group. Re-running apply
fixes objects created earlier: missing owners are added first, then unexpected
owners (including the creator, if not in the list) are removed. Both application
and SP additions must be visible before either object's removals begin; complete
owner enumeration and final equality checks fail closed. Reconciliation is
sequential across the three application/SP pairs: earlier pairs can be repaired
even if a later pair fails. Retry after partial failure or propagation delay; no
transaction or concurrent-owner-update isolation is provided. Keep reconciliation
serialized. `validate` reports
any difference as an error. Lease admission in CI never reads or changes owners.

The applying operator must retain Entra application-management authorization
throughout reconciliation: use a configured owner with the necessary delegated
permissions, or an appropriately authorized tenant application administrator /
application credential whose access does not depend on being an object owner.
Adding other owners does not preserve the creator's own access after removal.
An ownership-only operator excluded from the desired list can lose access and
must hand off retries to an authorized owner or administrator. ARM subscription
Owner alone does not grant these Graph permissions. Pool validation additionally
needs group membership and application/SP owner reads; CI admission does not.

Each slot has six persistent assignments at its customer subscription: MSI gets
the MSI custom role and Key Vault Crypto User; each helper gets Contributor and
Role Based Access Control Administrator. No grant is added to the infrastructure
subscription. CI needs Graph application/SP reads, public certificate reads and
subscription RBAC reads; it never applies this baseline.

Owner apply reuses `tooling/entra-app-credentials/pkg/pinning` to create missing
certificates and pin the current certificate without rotating healthy credentials
or changing certificate policies. Pinning replaces the application's entire
`keyCredentials` collection, so only the dedicated applications are eligible.
Admission checks certificate validity and exact pinned thumbprints using reads;
expired certificates fail closed. Keep the existing vault and delivery pipelines.
Rotation is an explicit disruptive `entra-app-credentials pin --rotate` operation
with affected jobs drained, not a deployment/admission/release operation. Use the
deterministic application name for the certificate name and `<name>.hcp.osadev.cloud`
for its DNS name.

### Test-scoped Managed HSM access

The Managed HSM Crypto User grants made by tests are HSM-local data-plane
assignments tied to the test HSM lifecycle, not part of the persistent ARM
subscription RBAC baseline. Mock admission is read-only: it verifies Graph,
certificates and the persistent identity bundle's subscription baseline, but
does not synchronously revoke test-scoped grants. Acquire and release are no-ops
for this asset. A reused slot principal can therefore retain access to an old
test HSM until the test resource group's expiry and successful background cleanup
if test teardown did not remove it. Expiry alone does not revoke access.

The test framework tags resource groups with `e2e.aro-hcp-ci.redhat.com=true`
and `deleteAfter.aro-hcp-ci.redhat.com`. Periodic expired-resource-group cleanup
selects these groups; its `no-rp` workflow deletes the resource group and then
attempts to purge its soft-deleted Key Vaults and Managed HSMs. HSM purge is
best-effort. Failed resource group deletions remain discoverable by later scans,
but a failed purge after successful resource group deletion is not retried by
the expired-resource-group scan because that group no longer exists.

Resource group deletion and permanent HSM purge are distinct: Azure soft-delete
retention and purge protection apply, so cleanup does not guarantee immediate
permanent erasure. See [CI cleanup](cleanup.md) and
[Managed HSM soft delete](https://learn.microsoft.com/en-us/azure/key-vault/managed-hsm/soft-delete-overview).
The ARM orphan-role sweeper handles assignments whose Graph principals no longer
exist; it does not clean up HSM-local grants to live persistent mock principals.
