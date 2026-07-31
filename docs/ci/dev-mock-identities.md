# DEV Mock Identities

DEV (and the CSPR PR-check and MSIT INT environments) cannot use the real Azure
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

The DEV bootstrap layer manages four logical identities. Their Entra **application
definitions** (display name + certificate DNS) come from `config/config-dev-ci.yaml`
under `.ci.dev.mockIdentities`; the service principal **object IDs are no longer
stored in config** — they are resolved at deployment time via Microsoft Graph
lookup (see [How The Roles Are Assigned](#how-the-roles-are-assigned)):

| Identity | Mocks | Roles it receives |
|---|---|---|
| `aro-dev-first-party2` (firstParty) | The Microsoft First Party Application | `dev-first-party-mock` (custom) |
| `aro-dev-arm-helper2` (armHelper) | The FPA's ARM/RBAC delegation ("MockFPA") | `Contributor` + `Role Based Access Control Administrator` (built-in) |
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
  service principals via `modules/entra/app.bicep` and configures them for
  Subject Name and Issuer (SNI) certificate authentication. It does **not**
  create the Key Vault certificates themselves — those are created by a separate
  `make create-mock-identity-certs` step (see [Certificates](#certificates)).
- `templates/mock-identity-rbac.bicep` looks up each service principal's object
  ID via Microsoft Graph (by `uniqueName`, the normalized application name), then
  fans out over the target subscriptions and deploys
  `e2e-subscription-rbac-assignment-subscription.bicep` into each. That per-
  subscription module is **self-contained**: it defines the custom roles
  (`dev-first-party-mock`, `dev-msi-mock`) inline in the target subscription and
  creates all the role assignments. Defining the roles locally avoids any cross-
  subscription `assignableScopes` dependency, so the pipeline can onboard
  subscriptions it does not own. Because each subscription gets its own copy and
  Azure enforces custom-role display-name uniqueness **per tenant**, the display
  name is suffixed with the subscription id (e.g. `dev-msi-mock-<subscriptionId>`)
  to avoid `RoleDefinitionWithSameNameExists`.

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

`mock-identity-apps.bicep` only declares which certificate subject name each app
trusts (SNI); Bicep cannot create Key Vault certificates, so they are created by a
separate idempotent step: **`make create-mock-identity-certs`** (DEV) and **`make
create-int-mock-identity-certs`** (INT), which call `scripts/create-kv-cert.sh`
(`az keyvault certificate create`) into the environment Key Vault
(`aro-hcp-dev-svc-kv` for DEV, `aro-hcp-int-kv` for INT). Because SNI validates the
certificate's subject name and issuer rather than pinning a public key, rotation
works without redeploying the template as long as the subject (certDns) is
unchanged. For a fresh bootstrap or a subject-name change, run the cert target
first, then deploy `mock-identity-apps.bicep`.

## Where To Look

- `config/config-dev-ci.yaml` — `.ci.dev.mockIdentities` / `.ci.int.mockIdentities`
  application definitions and pool settings
- `dev-infrastructure/templates/mock-identity-apps.bicep` — creates the Entra
  apps + service principals with SNI auth
- `dev-infrastructure/templates/mock-identity-rbac.bicep` — Graph lookup of
  principal IDs + fan-out RBAC across home and E2E subscriptions
- `dev-infrastructure/templates/e2e-subscription-rbac-assignment-subscription.bicep`
  — inline custom role definitions + all assignments (per subscription)
- `dev-infrastructure/dev-ci/e2e-subscription-rbac-grants/pipeline.yaml` — the
  privileged pipeline that deploys the two templates above
- `dev-infrastructure/scripts/delete-legacy-mock-identity-rbac.sh` — one-time
  pre-merge cleanup of the legacy random-named role assignments
- `backend/pkg/azure/client/hardcoded_identity_mi_dataplane_client.go` — the mock MSI
- `internal/azure/cluster_scoped_identities_config.go` — product operator-role mapping
- `cluster-service/helm-charts/cluster-service/templates/deployment.yaml` — how CS
  consumes the mock identities

## See Also

- [CI Overview](README.md)
- [Dev-CI Topology](dev-ci-topology.md)
- [E2E Subscription Onboarding](e2e-subscription-onboarding.md)
- [CI Identity Leasing](identity-leasing.md)
