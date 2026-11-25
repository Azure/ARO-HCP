# Rotating CI Bot Credentials

ARO HCP CI authenticates as one Entra application per environment in the **Red
Hat tenant**. Those applications are the CI bots; this page is the procedure for
rotating their client secrets.

## What This Covers

| Environment | Application | Key Vault secret | CI secret store group (`VAULT_SECRET_PROFILE`) |
| :---------- | :---------- | :--------------- | :--------------------------------------------- |
| DEV | `OpenShift Release Bot` | `ci-bot-dev-client-secret` | `aro-hcp-dev` (`dev`) and `aro-hcp-red-hat-tenant` (`red-hat-tenant`) |
| INT | `OpenShift Release Bot - INT` | `ci-bot-int-client-secret` | `aro-hcp-int-rh` (`int-rh`) |
| STG | `OpenShift Release Bot - STG` | `ci-bot-stg-client-secret` | `aro-hcp-stg-rh` (`stg-rh`) |
| PROD | `OpenShift Release Bot - Prod` | `ci-bot-prod-client-secret` | `aro-hcp-prod-rh` (`prod-rh`) |

The DEV application is the one exception: it backs two profiles. The
`red-hat-tenant` profile is mounted only by the expired app-registration cleanup,
but it authenticates as `OpenShift Release Bot`, so a DEV rotation publishes the
same value to both `aro-hcp-dev` and `aro-hcp-red-hat-tenant`.

Application names come from `ci.<environment>.bot.applicationName` and the
secrets are named by the `ensure-secret` mappings in
`dev-infrastructure/dev-ci/e2e-subscription-rbac/pipeline.yaml`. The vault is the
`opstool` one, named `opstool-kv-<region>` by `opstool.keyVault.name` in
`config/config-dev-ci.yaml`; today it renders as `opstool-kv-usw3`, the same
vault the DEV CI monitoring runbook uses. Confirm with `az keyvault list -o table`
if you are unsure, and export it once:

```bash
VAULT_NAME=opstool-kv-usw3
VAULT_URL="https://${VAULT_NAME}.vault.azure.net"
```

Not covered here:

- The Microsoft **Test Test tenant** identity behind the `prod` and
  `msft-test-tenant` profiles. That application belongs to a tenant this
  repository has no pipeline for; follow
  [Rotating the CI Client Secret](test-test-tenant-access.md#rotating-the-ci-client-secret).
- Mock identity **certificates** (shared and per-slot), which follow
  [Rotating A Certificate](../ci/dev-mock-identities.md#rotating-a-certificate).
- The identity behind the `aro-hcp-red-hat-tenant` profile is the DEV application
  from the table above, so it is covered here: its value is published together
  with `aro-hcp-dev`.

## Where The Secret Lives

There are two copies, and rotation has to update both:

1. The **`opstool` Key Vault** copy, which
   [`tooling/entra-app-credentials`](../../tooling/entra-app-credentials/README.md)
   maintains. This is the copy the DEV CI privileged entrypoint reconciles.
2. The **CI secret store** copy under `hcm-aro`, which is what CI steps mount.
   Nothing in this repository copies the vault value into the secret store, so
   publishing it is a step of this procedure.

The vault copy is readable and versioned, the secret-store copy is not, which is
why the procedure reads from the vault and pushes into the secret store rather
than the other way round.

## When To Rotate

Each secret is minted for two years. The privileged entrypoint checks the stored
secret on every run: an expired secret — or one that no longer matches any
credential of its application — fails the run, and one expiring inside 90 days
logs a warning. A normal run is therefore also how you find out a rotation is due.

The previous secret keeps working until you delete it, so rotating does not
break anything by itself. A job that is already running, however, keeps using
the secret it loaded at start, so rotate during a quiet window for the
environment and delete the old credential only after the runs from before the
publication have drained.

## Prerequisites

- **Azure**: `az login` to the Red Hat tenant, with permission to read and write
  the `ci-bot-<env>-client-secret` vault secret and to read and add credentials
  on the application. An `aro-hcp-owners` member has these rights when running
  the privileged entrypoint, which is how the bots are maintained.
- **Secret store**: the
  [Secret Manager CLI](https://docs.ci.openshift.org/architecture/cli-secret-manager/)
  after `sm login`, with access to the `hcm-aro` collection. This uses Red Hat
  SSO, so it is a separate login from `az`.
- A shell that is neither recorded nor logged for the steps that handle the
  value, and no copy of it in chat, tickets, email or AI tools.

## Procedure

Rotate one environment at a time.

1. **Secret store**: confirm you can update this environment's group. Stop here
   if the line is missing, before anything is minted. For DEV, that is both
   groups, because one application backs both:

   ```bash
   sm list -c hcm-aro | grep -E '^aro-hcp-(dev|red-hat-tenant|int-rh|stg-rh|prod-rh)/client-secret'
   ```

2. **Azure**: check the current expiry without changing anything. `--dry-run
   --rotate` never fails on expiry; it reports the current expiry and what it
   would do:

   ```bash
   AZURE_TOKEN_CREDENTIALS=dev go run ./tooling/entra-app-credentials ensure-secret \
     --vault-url "${VAULT_URL}" \
     --mapping 'OpenShift Release Bot - INT;ci-bot-int-client-secret' \
     --dry-run --rotate
   ```

3. **Azure**: rotate. This mints a new client secret, stores it as a new version
   of the Key Vault secret, and leaves the client ID, the tenant ID and the
   application's existing credentials alone. Take the command from step 2 with
   `--dry-run` removed:

   ```bash
   AZURE_TOKEN_CREDENTIALS=dev go run ./tooling/entra-app-credentials ensure-secret \
     --vault-url "${VAULT_URL}" \
     --mapping 'OpenShift Release Bot - INT;ci-bot-int-client-secret' \
     --rotate
   ```

   The privileged entrypoint passes `--grant-consent` as well; it is idempotent
   and unrelated to the secret, so a rotation does not need it unless consent is
   also missing.

   The tool mints the password first and writes it to Key Vault afterwards, so a
   failure can land between the two. A run that dies there leaves a live
   credential on the application whose value no longer exists anywhere: Graph
   returns only a hint, never the secret. If `ensure-secret` fails with
   `store secret "<name>": ...`, the newest credential named
   `<name>-managed` is that orphan — list the credentials as in step 6, delete
   the orphan, and run step 3 again. If instead the vault write succeeded and a
   later step failed, check the vault first: a new version means the value is
   safe, so go on at step 4 and do not rotate again.

4. **Secret store**: publish the new value into the group. Read it into a
   variable so the value never reaches shell history, write it without a
   trailing newline, and delete the file afterwards:

   ```bash
   S=$(az keyvault secret show --vault-name "${VAULT_NAME}" \
        --name ci-bot-int-client-secret --query value -o tsv)
   F=$(mktemp "${HOME}/.ci-bot-secret.XXXXXX") && chmod 600 "${F}" \
     && printf '%s' "${S}" > "${F}" && unset S \
     && test -s "${F}" \
     && sm update -c hcm-aro aro-hcp-int-rh/client-secret --from-file="${F}" \
     && echo "PUBLISHED to aro-hcp-int-rh"
   rm -f "${F}"
   ```

   Continue only if `PUBLISHED` is printed. A trailing newline in the file would
   be published as part of the secret, which is why this writes with `printf`
   rather than redirecting `az` output. `az keyvault secret show` without a
   version returns the current version, so re-running the step republishes the
   same value and is safe. Do not edit `client-id` or `tenant` in the group:
   rotation changes the secret value only.

   For DEV the same value goes to both groups, because both authenticate as
   `OpenShift Release Bot`:

   ```bash
   S=$(az keyvault secret show --vault-name "${VAULT_NAME}" \
        --name ci-bot-dev-client-secret --query value -o tsv)
   F=$(mktemp "${HOME}/.ci-bot-secret.XXXXXX") && chmod 600 "${F}" \
     && printf '%s' "${S}" > "${F}" && unset S \
     && test -s "${F}" \
     && sm update -c hcm-aro aro-hcp-dev/client-secret --from-file="${F}" \
     && sm update -c hcm-aro aro-hcp-red-hat-tenant/client-secret --from-file="${F}" \
     && echo "PUBLISHED to both DEV groups"
   rm -f "${F}"
   ```

   Stop if the second publish fails and rerun it with the same file before moving
   on; a group left on the old value fails its next job.

5. **Verify**: new CI jobs use the new value immediately. Wait for a run that
   started after step 4 to pass in the environment you rotated — the hourly
   expired-resource-group periodic for that environment exercises the bot's
   login and cleanup path:

   | Environment | Job |
   | :---------- | :-- |
   | DEV | `delete-expired-dev-ci-shard0-resource-groups`, and `delete-expired-red-hat-tenant-app-registrations` for the `red-hat-tenant` group |
   | INT | `delete-expired-integration-resource-groups` |
   | STG | `delete-expired-stage-resource-groups` |
   | PROD | `delete-expired-prod-00-resource-groups` |

6. **Azure**: once every run that started before step 4 has finished — allow a
   day — delete the superseded credential. List them first, and delete by key
   ID:

   ```bash
   APP_ID=$(az ad app list --filter "displayName eq 'OpenShift Release Bot - INT'" --query '[].appId' -o tsv)
   echo "${APP_ID}"   # must be exactly one app ID: stop if it is empty or lists more than one
   az ad app credential list --id "${APP_ID}" --query '[].{keyId:keyId,name:displayName,end:endDateTime}' -o table
   az ad app credential delete --id "${APP_ID}" --key-id <superseded-key-id>
   ```

   An undeleted credential is an orphaned long-lived secret on the application.
   It does not affect the expiry check, which follows the credential the stored
   secret was minted from, but leaving it is exactly the accumulation this
   rotation is meant to avoid.

## If Something Goes Wrong

- **Step 3 succeeded, step 4 failed.** Fix forward: rerun step 4 with the same
  value. The vault copy is readable, so unlike the Test Test tenant procedure
  there is nothing you cannot retrieve.
- **A job fails after step 4.** Before minting another secret, roll back by
  publishing the previous Key Vault secret version — `az keyvault secret show`
  with `--version <previous-version>` returns it — and rerun step 4. Those older
  versions are the rollback path, not a substitute for one.
- **The application rejects the new credential.** Check the mapping's
  application name against `ci.<environment>.bot.applicationName`; a name that
  matches a different application mints the secret for that one.
- **A rotation run is interrupted.** Do not assume nothing happened; the mint
  comes before the vault write. Check the vault: a new version means the value is
  safe and you resume at step 4. An error from `store secret` means a live
  credential exists whose value is gone, so delete that orphan as step 3
  describes and rotate again. Running `--rotate` twice just to be sure is what
  creates the second situation, so decide from what you can see, not by retrying.

## Related Documentation

- [Cluster Profile Secret Contract](../ci/cluster-profile-secret-contract.md)
  for what a profile holds and which field a consumer may read.
- [DEV CI Topology](../ci/dev-ci-topology.md#the-privileged-entrypoint) for the
  privileged entrypoint that reconciles the bots, their consent and their RBAC.
- [`tooling/entra-app-credentials`](../../tooling/entra-app-credentials/README.md#expiry-and-rotation)
  for the expiry policy and the flags in full.
