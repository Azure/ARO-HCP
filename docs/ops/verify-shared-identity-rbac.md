# Verify Shared Identity RBAC

## Background

[AROSLSRE-1994](https://redhat.atlassian.net/browse/AROSLSRE-1994): three INT shared service
principals (`aro-hcp-int-fp`, `aro-hcp-int-arm-helper`, `aro-hcp-int-msi-mock`) were hard-deleted
and recreated with new object IDs, but the recreation skipped assigning their subscription-scope
RBAC (`Contributor` and `Role Based Access Control Administrator`). The gap went undetected for
roughly a day, causing a sustained flood of `AuthorizationFailed` errors in Clusters Service logs
and blocking cluster create/delete in INT.

This mirrors the related incident [AROSLSRE-1969](https://redhat.atlassian.net/browse/AROSLSRE-1969)
(DEV mock identities soft-deleted by the `cleanup-sweeper` `shared-leftovers` job, fixed in
[PR #6762](https://github.com/Azure/ARO-HCP/pull/6762)). That fix prevents the sweeper from
prematurely deleting RBAC for a soft-deleted-but-recoverable principal; it does not detect the case
handled here, where an identity is recreated (new object ID) and RBAC is simply never (re)applied.

## What this checks

`dev-infrastructure/scripts/verify-shared-identity-rbac.sh` compares a target identity's
subscription-scope role assignments against a known-good reference identity in the same
subscription. Any target missing one or more of the reference identity's roles (including having
zero role assignments at all) is reported and the script exits non-zero.

## Usage

```bash
dev-infrastructure/scripts/verify-shared-identity-rbac.sh \
  <subscription-id> \
  <reference-object-id> \
  <target-object-id> [<target-object-id> ...]
```

Example, using the healthy `aro-hcp-int-cs-arm-helper` identity as the reference for the three
identities involved in AROSLSRE-1994:

```bash
dev-infrastructure/scripts/verify-shared-identity-rbac.sh \
  64f0619f-ebc2-4156-9d91-c4c781de7e54 \
  dfcca0f7-a439-45e8-9284-9a2b876d605a \
  e02ddacf-108f-4f1b-914b-1aee78fd4cb2 \
  3fcba429-5bf0-40b1-bd4e-efc8136390a3 \
  6096f642-63a0-4dd6-958d-715a87f33535
```

Requires an `az` session with at least read access to role assignments on the target subscription
(`Microsoft.Authorization/roleAssignments/read`).

## Recommended usage

Run this daily per environment (DEV/INT/STG/PROD) against each environment's shared first-party,
ARM-helper, and MSI-mock identities, alerting to the environment's on-call channel on a non-zero
exit code. This closes the detection gap that let AROSLSRE-1994 go unnoticed for a day; see
AROSLSRE-1995 for the tracked follow-up to wire this into a periodic job.

This script only detects drift; it does not repair it. To fix a detected gap, re-run the
create/reset flow for the affected identity and re-apply the missing role assignments, for example:

```bash
az role assignment create --subscription <subscription-id> \
  --assignee-object-id <object-id> --assignee-principal-type ServicePrincipal \
  --role Contributor --scope /subscriptions/<subscription-id>
```
