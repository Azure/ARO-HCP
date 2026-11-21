# DEV Mock Identities

DEV, and the INT environment that runs in the MSIT tenant, cannot use two Azure services the product depends on in production. There is no Microsoft first-party application (FPA) for the Resource Provider and Clusters Service to act through, and the Managed Identities Data Plane, which hands out the runtime credentials of a cluster's user-assigned managed identities, only exists in tenants that have the FPA integration.

To run end to end anyway, CI substitutes a few ordinary Entra service principals, the *mock identities*. The product code calls the matching mocked clients the *hardcoded identities*, as in `hardcoded_identity_mi_dataplane_client.go`, so you will see both names. This page explains what each one stands in for, why it holds the roles it does, how slot-managed jobs get their own copies, and how all of them are created and maintained.

## The Identities

| Identity | Stands in for | Roles |
| :------- | :------------ | :---- |
| First-party mock (`aro-dev-first-party2`) | The Microsoft first-party application | Custom role `dev-first-party-mock` |
| ARM helper | The FPA's ability to create Azure resources and assign roles for the customer ("MockFPA") | Built-in `Contributor` and `Role Based Access Control Administrator` |
| MSI mock | Every per-cluster operator managed identity, via the mocked data plane | Custom role `dev-msi-mock` and built-in `Key Vault Crypto User` |

The application definitions (names, certificate names and DNS names) live in `config/config-dev-ci.yaml` under `.ci.<env>.mockIdentities`. Service principal object IDs are not stored anywhere; they are resolved through Microsoft Graph whenever they are needed. Clusters Service receives the identities through its deployment values (see `cluster-service/deploy/templates/deployment.yaml`).

There are two generations of ARM helper and MSI mock. The *shared* ones (`aro-dev-arm-helper2`, `aro-dev-msi-mock2`, and the first-party mock) serve personal development environments and the persistent shared services. Slot-managed DEV E2E jobs use *slot mock identities* instead: every slot gets its own MSI mock and two ARM helpers, described [below](#slot-mock-identities). The first-party mock is always the shared one. INT has its own named set of the same three roles, including a separate ARM helper for Clusters Service.

## What Each One Is For

### First-Party Mock

In production the FPA performs the subnet delegation handshake when a customer virtual network is attached to a managed cluster, and reads and writes the managed resource group. The `dev-first-party-mock` role grants exactly that: create, read, validate and delete service association links on subnets, and read and write on resource groups. It deliberately does not create the cluster's Azure resources or assign roles; that is the ARM helper's job.

### ARM Helper

In production the FPA can act on behalf of the customer to create resources and to assign roles to a cluster's operator identities. The ARM helper impersonates that capability in DEV, which is why the configuration key that names the FPA principal is `azureArmHelperMockFpaPrincipalId`. It holds two built-in roles at subscription scope: `Contributor`, to create what a cluster needs, and `Role Based Access Control Administrator`, to bind each operator identity to its role. Azure does not require the party handing out a role to hold it, so the helper can assign `Key Vault Crypto User` and the operator roles without holding them itself. That is why the helper, and not the MSI mock, performs the operator-role assignments.

One naming trap: the configuration value `armHelperFPAPrincipalId` is the principal that *receives* the simulated FPA grant, that is, the shared first-party mock. It is not the helper that authenticates the request.

Backend and Clusters Service each have their own ARM helper in slot-managed jobs and in INT. This matters because Azure throttles CheckAccess calls per application, and each concurrent environment runs its own backend rate limiter. If all environments authenticated as one shared helper, their independent limiters would add up against a single Azure limit.

### MSI Mock

This is the most important and most easily misunderstood identity. Because the Managed Identities Data Plane is unavailable, the backend and Clusters Service use a mock client (`backend/pkg/azure/client/hardcoded_identity_mi_dataplane_client.go`) that answers every request with one service principal disguised as a managed identity. In effect, every per-cluster operator identity (cloud controller manager, ingress, network operator, the KMS plugin and the rest) authenticates at runtime as the MSI mock.

Its custom role, `dev-msi-mock`, is therefore the union of what those operators do: manage federated identity credentials for workload identity, and a broad set of network actions on load balancers, subnets, security groups, route tables, NAT gateways, private DNS links and virtual networks. When a cluster enables etcd encryption the KMS plugin also authenticates as the MSI mock and calls Key Vault key operations, so the MSI mock holds the built-in `Key Vault Crypto User` role as well, the same one the product assigns to a per-cluster KMS identity. DEV E2E exercises this path in the private key vault and Cilium key vault tests.

### Custom Versus Built-In Roles

The operator-equivalent permissions used to be custom roles because the matching built-in roles were not yet available in every tenant. Where a built-in role now exists, the setup uses it, which is why the ARM helper and the KMS grant use built-ins. `dev-first-party-mock` and `dev-msi-mock` remain custom because they bundle a set of actions that no single built-in role covers.

## Slot Mock Identities

A slot-managed DEV job gets its own MSI mock and two ARM helpers through the `mock_identities` [slot asset](slot-assets.md#mock-identities). They are named `aro-hcp-slot-<UUID>-{msi,backend,cs}`, where the UUID is derived from the customer subscription and the slot name, so they survive a change of region but not a rename of the slot.

Unlike the shared identities, these are created and reconciled by the slot-manager asset commands, not by the dev-ci rollout. The legacy Boskos pools that played this role before (`aro-dev-msi-mock-pool-<i>` and `aro-dev-arm-helper-pool-<i>`) are retired, and the rollout no longer manages them: their configuration, Bicep modules and static catalogs are gone. The old applications, certificates and role assignments are not removed by the rollout and must be deleted separately.

### Preparing A Slot's Identities

Apply is a privileged, operator-run action. CI admission only reads. The operator needs three separate things, none of which implies the others:

- **Azure:** for a subscription the rollout manages, the rights to create role assignments on the customer subscription, such as an active `Owner` assignment; activate any eligible assignment first. A refused assignment surfaces as the Azure authorization error. Not needed for an unmanaged pool, which the operator cannot see: the owner supplies its subscription ID, as described below.
- **Microsoft Graph:** rights to create and manage the applications and service principals and their credentials, and to read the membership of the configured owners group. Owning the subscription grants none of this.
- **Key Vault:** permission to read and create certificates in the shared service vault, `aro-hcp-dev-svc-kv`.

For an externally owned DEV subscription this operator needs no rights on the subscription and cannot see it: the owner gives the operator its UUID, and the apply creates only the identities, and [External Subscription Management](external-subscription-management.md) describes how the subscription owner then deploys the role definitions and grants the roles.

For a subscription the rollout manages, the subscription must also already carry the subscription-local `dev-msi-mock-<subscription-id>` role definition. The slot code reuses that role and does not create it; it comes from the shared subscription RBAC template described [below](#how-the-shared-identities-are-set-up). Then run, per pool and one pool at a time:

```bash
AROHCP_ENV=development make -C test apply-pool-assets    ENVIRONMENT=<dev|dev-xs> POOL=<pool> ASSET=mock_identities
AROHCP_ENV=development make -C test validate-pool-assets ENVIRONMENT=<dev|dev-xs> POOL=<pool> ASSET=mock_identities
```

For a pool that is marked unmanaged, such as an externally owned subscription, add `SUBSCRIPTION='<display name>'` and `EXTERNAL_SUBSCRIPTION_ID=<subscription UUID>` as well, with the UUID given by the subscription's owner, because the operator cannot resolve the display name and the slot identity names derive from the ID (a wrong UUID shows up as missing identities when the owner runs `reconcile-external-subscription`); `EXTERNAL_SUBSCRIPTION_ID` holds one ID, so select one external subscription per run: selecting unmanaged pools of several subscriptions, for example `SUBSCRIPTION=a,b`, fails before anything is changed. Without `SUBSCRIPTION` or `POOL`, unmanaged pools are not selected and the variable is not needed; selecting by pool or subscription is what includes unmanaged pools (see [Managing Slot Assets](slot-assets.md#managing-slot-assets)). The Make target needs Mike Farah's `yq` v4 to read the owner settings from `config/config-dev-ci.yaml`. Never run this from CI.

For each slot, apply creates the application and service principal for the three identities, creates a Key Vault certificate for each and pins it to the application, and, for a subscription the rollout manages, grants six role assignments on the customer subscription: the MSI mock gets `dev-msi-mock` and `Key Vault Crypto User`, and each ARM helper gets `Contributor` and `Role Based Access Control Administrator`. For a pool marked unmanaged, apply and validate handle the identities only and grant or check no role; the subscription's owner grants those six with `reconcile-external-subscription`. Nothing is granted on the infrastructure subscription. Every step is safe to retry because the application names, certificate names and assignment IDs are deterministic; if Graph returns an ambiguous or mismatched object, stop and investigate rather than picking one.

Apply also makes the Entra owners of each application and service principal exactly the list in `entraAppOwnerIds` in `config/config-dev-ci.yaml`, which mirrors the `aro-hcp-owners` group, adding missing owners before removing extras. The command refuses to write anything if that list has drifted from the group. The replacement is authoritative, so an operator whose only access to the applications is as their creator can remove themselves and lose access mid-run. Run it as someone on the list or with tenant-level application administration. Reconciliation is not transactional, so serialize runs and simply retry after a partial failure. `validate-pool-assets` reports any owner difference as an error; CI admission never looks at owners.

### Slot Identity Certificates

The slot identities use the same pinned-certificate approach as the shared ones, explained under [Certificates](#certificates). Apply creates missing certificates and pins the current one without touching healthy credentials or certificate policies. Because pinning replaces an application's whole credential set, it must only ever be used on these dedicated applications. Admission checks that each certificate is valid and that its thumbprint matches what is pinned, and an expired or mismatched certificate fails the job.

Rotation is a deliberate, disruptive operation with affected jobs drained; see [Rotating A Certificate](#rotating-a-certificate). One current limitation: the generated DNS name for a slot certificate, `<application name>.hcp.osadev.cloud`, is longer than the 64-character common-name limit enforced by `entra-app-credentials pin`, so it cannot be pasted into a rotation command. Agree a shorter DNS name with the vault owners first, or fix the tooling.

### Test-Scoped Managed HSM Access

Tests that use a Managed HSM grant the mock principal `Managed HSM Crypto User` on the HSM itself. That grant belongs to the test's HSM and is not part of the persistent subscription baseline, and mock admission is read-only, so it is not revoked when the slot is released. If a test fails to remove it, the principal keeps access to that HSM until the test resource group is deleted and cleaned up. Expiry of the resource group alone does not revoke anything. Periodic [cleanup](cleanup.md) deletes expired resource groups and then attempts to purge their soft-deleted vaults and HSMs on a best-effort basis; Azure soft-delete retention and purge protection mean erasure is not immediate. The ARM orphan-role sweeper only handles assignments whose principals no longer exist, so it will not remove these.

## How The Shared Identities Are Set Up

The shared identities, and the subscription RBAC that every E2E subscription needs, are created by the on-demand, Owner-only `Microsoft.Azure.ARO.HCP.DevCI.Privileged` entrypoint of the [dev-ci topology](dev-ci-topology.md#the-privileged-entrypoint), run by an OWNERS-group member with `make dev-ci-privileged-local-run`. It is kept out of the unattended dev-ci postsubmit because it needs subscription Owner. Two Bicep templates do the work:

- `mock-identity-apps.bicep` creates the Entra applications and service principals. It deliberately configures no authentication on them; certificates are handled separately, as described next.
- `mock-identity-rbac.bicep` looks up each principal's object ID through Graph and deploys `e2e-subscription-rbac-assignment-subscription.bicep` into every target subscription. That per-subscription template defines the custom roles locally, suffixing their names with the subscription ID because Azure requires custom role names to be unique per tenant, which also lets the pipeline onboard subscriptions it does not own, and creates all the role assignments.

The target subscriptions are the entries of `.ci.<env>.e2eSubscriptions`, plus, for DEV only, the home subscription the pipeline deploys into, so that mock identities can be tested from personal development environments. INT leaves the home subscription out because its real home is already in the INT list.

### Certificates

The mock applications authenticate with a pinned certificate: the certificate's public key is registered on the application as a credential, and Entra matches the thumbprint of the certificate the client presents. Subject-name-and-issuer trust, the usual alternative, does not work here. The certificates are self-signed and Clusters Service sends the whole chain, and Entra rejects such a chain (`AADSTS7000213`). An earlier configuration based on that mechanism caused a CI-wide authentication outage, and `mock-identity-apps.bicep` now clears any leftover configuration of that kind.

Bicep can neither create Key Vault certificates nor read them, so after the apps are deployed a Shell step in the privileged pipeline runs `tooling/entra-app-credentials`. For each mock application it reads the current certificate from the environment's Key Vault, creates it from the standard self-signed policy if it does not exist, pins its public key on the application, and verifies the resulting thumbprint through Graph. RBAC is applied only after every certificate has been reconciled. The step runs with the invoking operator's credentials, so that person needs certificate create and read rights on the vault and ownership of the applications.

Key Vault's own lifetime policy can auto-renew a certificate, which leaves the pinned thumbprint stale until the privileged pipeline is run again.

### Rotating A Certificate

Rotation is excluded from the pipeline because it is rare and disruptive: authentication fails between the moment Key Vault gets the new certificate and the moment Graph pins it. Do not rotate while E2E work needs uninterrupted authentication. From the repository root, rotate only the intended application:

```bash
AZURE_TOKEN_CREDENTIALS=dev go run ./tooling/entra-app-credentials pin \
  --vault-url https://<vault-name>.vault.azure.net \
  --mapping '<application-name>;<certificate-name>;<certificate-dns-name>' \
  --rotate
```

For a shared identity, take the three values from its entry in `config/config-dev-ci.yaml`. A slot identity is not listed there: its application and certificate names are generated from the slot, so take them from the names the slot's asset commands report (derived by `mockidentities.IdentityNames` in the slot-manager code), and do not rotate a shared identity in their place. If the command creates the certificate but pinning fails, do not rotate again. Re-run the normal reconciliation, which finds the current certificate and pins it: the privileged pipeline for a shared identity, or the scoped `apply-pool-assets` for a slot identity.

## Where To Look

- `config/config-dev-ci.yaml`: `.ci.<env>.mockIdentities` application definitions, the owner list, and the E2E subscription inventory
- `dev-infrastructure/templates/mock-identity-apps.bicep`, `mock-identity-rbac.bicep` and `e2e-subscription-rbac-assignment-subscription.bicep`: the shared identities and the roles
- `dev-infrastructure/dev-ci/e2e-subscription-rbac-grants/pipeline.yaml`: the privileged pipeline that deploys them and pins their certificates
- `test/cmd/aro-hcp-tests/slot-manager/assets/mockidentities/`: the slot asset implementation
- `backend/pkg/azure/client/hardcoded_identity_mi_dataplane_client.go`: the mocked data plane
- `internal/azure/cluster_scoped_identities_config.go`: the product's own operator-role mapping
