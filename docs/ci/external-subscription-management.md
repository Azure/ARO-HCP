# External Subscription Management

Use this procedure to onboard and maintain an externally owned subscription used by ARO HCP DEV E2E jobs in the Red Hat tenant. The HyperShift team's subscription is an example. For subscriptions owned by ARO-HCP, use [E2E Subscription Onboarding](e2e-subscription-onboarding.md). Cross-tenant PROD subscriptions have different identity and configuration requirements and are not supported by this command.

External subscriptions deliberately stay outside `ci.dev.e2eSubscriptions`: our privileged rollout does not have Owner on them. Their catalog pools declare `slot_assets.e2e_identities.provisioning: unmanaged`. That changes who maintains the backing resources, not the requirement to pass slot admission. The owning team runs reconciliation from an updated, trusted checkout instead of translating template and tooling changes into a new sequence of manual commands.

## Check And Reconcile

Build with the repository's Go and Bicep prerequisites. Azure Resource Manager, Microsoft Graph and Key Vault operations use their Go SDKs with one shared credential. For automation, select the SDK credential with `AZURE_TOKEN_CREDENTIALS` (for example `EnvironmentCredential`, `WorkloadIdentityCredential` or `ManagedIdentityCredential`) and supply that credential's standard Azure environment configuration. Environment credentials use `AZURE_TENANT_ID`, `AZURE_CLIENT_ID` and `AZURE_CLIENT_SECRET`; do not pass secrets as command arguments. Azure CLI is not needed when using these SDK-native credentials.

For interactive `az login`-only use, the development fallback is an Azure CLI credential pinned to the target subscription. Sign in with `az login --tenant 64dc69e4-d083-49fc-9569-ebece1dd1408`; set `AZURE_TOKEN_CREDENTIALS=AzureCLICredential` to select this explicitly. Without an explicit selector, complete client-secret environment credentials take precedence over the local CLI fallback.

Apply requires active subscription Owner; Graph read access is needed to discover existing principals. Readiness also reads the slot identities' public certificates in the shared service Key Vault to verify their current pins. Arrange certificate-read access with the ARO-HCP operators; subscription Owner does not grant it. Routine reconciliation needs no vault certificate-create or private-key/secret access and never grants itself access to the infrastructure subscription. The CI bot does not receive Owner.

From the repository root, check readiness without modifying Azure:

```bash
make -C test reconcile-external-subscription SUBSCRIPTION_ID='<subscription UUID>'
```

To repair the subscription baseline and safe asset drift:

```bash
make -C test reconcile-external-subscription SUBSCRIPTION_ID='<subscription UUID>' APPLY=true
```

The Make target compiles the canonical subscription RBAC Bicep templates and regenerates the existing slot-asset artifacts before building the command. Both RBAC preview and deployment submit that embedded ARM JSON through the SDK; the executable does not invoke Azure CLI or Bicep for cloud operations. Use the Make target rather than an independently rebuilt binary with potentially stale templates. The underlying interface is `aro-hcp-tests slot-manager reconcile-external-subscription --repo-root <checkout> --subscription-id <UUID>`, with `--apply` to enable writes. The checkout supplies configuration, catalog and revision metadata, not runtime template compilation.

Only the subscription UUID is required. The command checks its tenant and catalog binding, discovers all matching DEV pools across slot environments, and reports the checkout revision and scope. A subscription used by both `dev` and `dev-xs` is reconciled in one invocation. Unsupported environments and ambiguous subscription bindings fail rather than falling back to DEV settings.

Reconciliation registers and waits for the configured resource providers, deploys the existing CI bot and subscription-local mock RBAC templates, reconciles customer identity containers without destructive changes, repairs subscription grants for existing slot mock principals, and verifies readiness. It consumes the same configuration, templates and asset implementation as the ARO-HCP tooling; no provider, role or principal-ID list is maintained in this document. A failed stage or unresolved prerequisite returns a nonzero exit status. Correct the reported prerequisite and rerun after a partial failure.

## Identity Preparation

Subscription Owner does not grant permission to create Entra applications or certificates in the shared service Key Vault. Initial slot mock preparation requires an operator with all the rights described in [Preparing A Slot's Identities](dev-mock-identities.md#preparing-a-slots-identities). That operator uses the same command with explicit preparation enabled:

```bash
make -C test reconcile-external-subscription SUBSCRIPTION_ID='<subscription UUID>' APPLY=true PREPARE_IDENTITIES=true
```

Routine reconciliation repairs Azure grants to existing slot principals; it does not replace their credentials or Entra owners. Explicit preparation is not permission to disrupt running jobs. A changed pinned credential, authoritative owner replacement or other disruptive identity change requires the deliberate drained maintenance workflow in [DEV Mock Identities](dev-mock-identities.md#slot-identity-certificates). Shared tenant identities must already exist; this command does not recreate shared identity infrastructure.

## Running Periodically

The subscription owner can schedule the normal `APPLY=true` invocation using an owner-controlled automation identity with the necessary subscription, directory-read and public-certificate-read access described above. Do not use the CI test bot for privileged maintenance, and do not enable identity preparation in the periodic job. Configure the job not to overlap itself; local command locking does not coordinate separate runners. Preserve command output and alert on a nonzero exit status.

Use a reviewed checkout or image and update its revision through the owner's normal review process. A periodic job permanently pinned to old tooling cannot reconcile prerequisites introduced by new features. Never download and execute an unreviewed script as Owner. The job must not wait for an interactive login or PIM activation.

## Onboard And Publish A Pool

Reconciliation manages Azure prerequisites; publishing CI capacity still needs coordination:

1. Propose the pool in the [slot catalog](../../test/e2e-config/e2e-slots.yaml), with the external E2E identity asset marked unmanaged. Budget quota for every workload sharing the subscription; see [E2E Capacity And Quotas](e2e-capacity.md) and [Quota Requests](e2e-subscription-onboarding.md#quota-requests).
2. Run reconciliation, including privileged identity preparation where needed, and obtain a passing readiness check.
3. Have an ARO-HCP operator provision and validate the shared infrastructure identity capacity. It lives in the infrastructure subscription, which this command never modifies. See [Managing Slot Assets](slot-assets.md#managing-slot-assets).
4. Have an ARO-HCP maintainer add the subscription's exact display name and UUID to the DEV cluster profile's `customer-<shard>-subscription-name` and `customer-<shard>-subscription-id` entries. Confirm the profile contains the configured DEV bot's credentials.
5. Generate and validate Boskos inventory and propose the `openshift/release` changes, including the subscription display name in the relevant jobs' `ALLOWED_SUBSCRIPTIONS`. Publish inventory only after backing assets are ready; see [DEV E2E Subscription Onboarding](e2e-subscription-onboarding.md#dev-e2e-subscription-onboarding).
6. Add the owning team's cleanup periodic in `openshift/release`. Use `aro-hcp-deprovision-expired-resource-groups` with `CLEANUP_MODE: no-rp` and `CUSTOMER_SUBSCRIPTION` set to the display name. HyperShift keeps this in its `arohcp-cleanup` variant. Because the subscription is not in the managed inventory, do not assume the managed subscriptions' tag-policy sweeper covers it; see [CI Cleanup](cleanup.md).
7. Run a rehearsal E2E job against the pool. Confirm slot acquisition, resource creation and clean release.

The reconciliation command does not edit cluster-profile secrets, publish Boskos inventory, deploy cleanup jobs or request quota.

## Disruptive Maintenance And Retirement

Routine reconciliation refuses destructive container changes and does not perform lease admission cleanup. Do not bypass those guards with unrestricted asset apply while jobs are running. Follow [Changing Capacity](e2e-capacity.md#changing-capacity) to drain and reduce capacity deliberately. Certificate changes follow [Slot Identity Certificates](dev-mock-identities.md#slot-identity-certificates).

To retire or rename a pool, stop new leases and drain running jobs before deliberately removing its backing resources and obsolete grants. Releasing a slot retains its containers, mock identities and role assignments. Renaming changes identity names and can orphan the old resources. Incremental RBAC deployment does not revoke obsolete grants automatically; review removals separately. Permission reductions can affect running jobs even when resource names are unchanged.
