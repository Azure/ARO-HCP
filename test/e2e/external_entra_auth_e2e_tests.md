# External Entra Auth E2E Tests

The External Entra Auth E2E tests validate the full lifecycle of integrating Microsoft Entra ID with an ARO-HCP cluster.

## Test Flow

1. **Step 1:** Create Entra app registration + secret and save credentials
2. **Step 2:** Read saved credentials, fetch cluster console route (via breakglass), create allow group in Entra, and patch app redirect URIs
3. **Step 3:** Build and publish OIDC config to RP frontend (or via port-forward in dev)
4. **Step 4:** Delete the Entra app instance
5. **Step 5:** Delete the ExternalAuth config from RP

- Steps 1–3 must be run sequentially for a valid create flow.
- Steps 4–5 can be run together or independently to clean up.

---

```bash

 ./test/aro-hcp-tests list tests | jq -r '.[].name'
Customer should not be able to create a 4.18 HCP cluster
Customer should be able to create an HCP cluster using bicep templates
External Auth API Access should acquire token and call protected cluster API
ExternalAuth Full E2E creates a full HCP cluster and applies ExternalAuth config
ExternalEntra Create creates an Entra app registration and secret, and writes JSON
ExternalEntra Step 2: set redirect URI & create Entra group reads app creds, gets console route (via breakglass), creates allow group, and patches redirectUris [step2]
ExternalEntra: delete Entra app + RP config deletes Entra app instance, then deletes ExternalAuth from RP
ExternalEntra: publish OIDC config to RP builds OIDC config (incl. allow group) and POSTs to RP [step3]
Customer should be able to create an HCP cluster with Image Registry not present

```

## Environment Variables

| Variable | Required? | Description |
|----------|-----------|-------------|
| `HCP_CLUSTER_NAME` | | Name of the HCP cluster to operate on |
| `ENTRA_E2E_SECRET_PATH` | for step 2–3 | Path to JSON file where Step 1 saved the Entra app credentials |
| `ENTRA_APP_OBJECT_ID` |  for step 4 | Object ID of the Entra application to delete |
| `EXTERNAL_AUTH_ID` | Optional | ExternalAuth config ID in RP (default: `e2e-hypershift-oidc`) |
| `INSECURE_SKIP_TLS` | Optional (dev only) | Set to `true` to skip TLS verification when port-forwarding |
| `RP_BASE_URL` | Optional (stage/prod) | Direct RP endpoint base URL (e.g. `https://<rp-host>:8443`). If unset, tests will port-forward to `aro-hcp-frontend` |
| `CUSTOMER_SUBSCRIPTION` | Optional | Azure subscription for creating clusters (only needed if test provisions a cluster) |

---

## Running in Development (Port-Forward Mode)

In **dev**, RP and clusters-service are not directly accessible. Tests will port-forward automatically.

**Step 2:**
```bash
export HCP_CLUSTER_NAME=external-auth-cluster
export ENTRA_E2E_SECRET_PATH=test/e2e/out/entra_app_secret.json
export INSECURE_SKIP_TLS=true

./test/aro-hcp-tests run-test   "ExternalEntra Step 2: set redirect URI & create Entra group reads app creds, gets console route (via breakglass), creates allow group, and patches redirectUris [step2]"
```

**Step 3:**
```bash
./test/aro-hcp-tests run-test   "ExternalEntra: publish OIDC config to RP builds OIDC config (incl. allow group) and POSTs to RP [step3]"
```

---

## Running in Staging/Prod (Direct RP Access)

In **stage/prod**, set `RP_BASE_URL` to skip port-forwarding:

```bash
export HCP_CLUSTER_NAME=my-hcp-cluster
export ENTRA_E2E_SECRET_PATH=test/e2e/out/entra_app_secret.json
export RP_BASE_URL="https://rp.stage.hcp.example.com:8443"

./test/aro-hcp-tests run-test   "ExternalEntra: publish OIDC config to RP builds OIDC config (incl. allow group) and POSTs to RP [step3]"
```

---

## Running Delete Tests

Delete both Entra app and RP config:

```bash
export HCP_CLUSTER_NAME=my-hcp-cluster
export ENTRA_APP_OBJECT_ID=<app-object-id-from-step1>
export EXTERNAL_AUTH_ID="e2e-hypershift-oidc"
export INSECURE_SKIP_TLS=true  # or set RP_BASE_URL in stage/prod

./test/aro-hcp-tests run-test   "ExternalEntra: delete Entra app + RP config deletes Entra app instance, then deletes ExternalAuth from RP"
```
