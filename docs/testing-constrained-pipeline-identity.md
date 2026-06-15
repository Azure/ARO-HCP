# Testing tools under a constrained EV2 pipeline identity

Some EV2 steps run a tool as a dedicated **`shellIdentity`** managed identity
(for example `globalMSIId`) rather than as a developer's credentials. That
identity has a **deliberately narrow RBAC scope**, so a tool can behave
differently — and fail — under the pipeline identity even though it works fine
when a developer runs it locally.

A normal `make personal-dev-*` run authenticates as the **developer's Owner
credentials**. Owner can do almost anything in the subscription, so it will
**not** reproduce authorization failures that only occur under the constrained
pipeline identity. To catch those, you must run the tool under the **same
principal and RBAC** the EV2 step uses.

This guide documents a repeatable way to do that in a personal-dev management
cluster, using an AKS **workload-identity** pod. It is generic to any tool that
authenticates with `DefaultAzureCredential` and runs as an EV2 `shellIdentity`.

> **Worked example.** This technique was used to reproduce the
> `403 LinkedAuthorizationFailed` from incident ITN-2026-00192 / AROSLSRE-1172,
> where the `recreate-broken-pools` tool issued a full VMSS `PUT` that re-ran a
> cross-subscription linked-image authorization check the pipeline identity was
> not entitled to. The bug was invisible to ordinary Owner-credential dev runs.

## Why this is needed

```
make personal-dev-*        →  developer Owner credentials  →  bug hidden
EV2 step (shellIdentity)   →  constrained managed identity →  bug reproduces
```

The determinant of an ARM authorization outcome is the **principal's RBAC**, not
the token transport. EV2 delivers the managed-identity token via IMDS in its
shell-extension container; an AKS workload-identity pod delivers a token for the
**same kind of principal** via a projected OIDC token. If you give a test
managed identity **exactly** the EV2 identity's role assignments, ARM makes the
**same** authorization decision — so the pod is a faithful stand-in for the EV2
step.

## Prerequisites

- A personal-dev management cluster (`*-mgmt-1`) with **workload identity** and
  **OIDC issuer** enabled (the default for ARO-HCP management AKS clusters).
- `az`, `kubectl`, and (for local AKS access) `kubelogin`.
- Permission to create a user-assigned managed identity and role assignments in
  the management subscription (your personal-dev Owner role is sufficient).

## Step 1 — Identify the EV2 step's identity and RBAC

Find the step in the pipeline and read its `shellIdentity`, then trace that
identity's role assignments in the templates. For the `recreate-broken-pools`
example:

| What | Where |
| --- | --- |
| Step + identity | `dev-infrastructure/mgmt-pipeline.yaml` → `shellIdentity: globalMSIId` |
| Subscription **Contributor** | `dev-infrastructure/templates/rg-ownership.bicep` |
| Subscription **Reader** | `dev-infrastructure/templates/pipeline-msi-reader-permissions.bicep` |

The goal of the next step is to mint a test identity with **the same scopes and
roles, and nothing more**.

## Step 2 — Create a constrained test managed identity

```bash
SUB=<management-subscription-id>
MSI_RG=<management-underlay-resource-group>   # e.g. hcp-underlay-<cluster>

az identity create -n rbp-test-msi -g "$MSI_RG" --subscription "$SUB" -o json > /tmp/msi.json
PRIN=$(python3 -c 'import json;print(json.load(open("/tmp/msi.json"))["principalId"])')
CLIENT=$(python3 -c 'import json;print(json.load(open("/tmp/msi.json"))["clientId"])')

# Wait a few seconds for the service principal to propagate, then mirror the
# EV2 identity's role assignments EXACTLY (here: sub Contributor + Reader only).
sleep 20
az role assignment create --assignee-object-id "$PRIN" --assignee-principal-type ServicePrincipal \
  --role Contributor --scope "/subscriptions/$SUB" --subscription "$SUB"
az role assignment create --assignee-object-id "$PRIN" --assignee-principal-type ServicePrincipal \
  --role Reader --scope "/subscriptions/$SUB" --subscription "$SUB"
```

> Do **not** grant anything broader than the EV2 identity has. The whole point
> is to match the constrained scope so failures reproduce.

## Step 3 — Federate the identity to a Kubernetes ServiceAccount

```bash
OIDC=$(az aks show -n <mgmt-aks-name> -g <mgmt-aks-rg> --subscription "$SUB" \
  --query oidcIssuerProfile.issuerUrl -o tsv)

az identity federated-credential create \
  --name rbp-test-fc --identity-name rbp-test-msi -g "$MSI_RG" --subscription "$SUB" \
  --issuer "$OIDC" \
  --subject "system:serviceaccount:rbp-test:rbp-test" \
  --audiences "api://AzureADTokenExchange"
```

## Step 4 — Create the namespace, ServiceAccount, and pod

The pod must (a) use the annotated ServiceAccount and (b) carry the label
`azure.workload.identity/use: "true"` — the `azure-wi-webhook` only injects the
identity env + projected token when **both** are present.

```yaml
apiVersion: v1
kind: Namespace
metadata: { name: rbp-test }
---
apiVersion: v1
kind: ServiceAccount
metadata:
  name: rbp-test
  namespace: rbp-test
  annotations:
    azure.workload.identity/client-id: "<CLIENT_ID from step 2>"
    azure.workload.identity/tenant-id: "<TENANT_ID>"
---
apiVersion: v1
kind: Pod
metadata:
  name: rbp-test
  namespace: rbp-test
  labels:
    azure.workload.identity/use: "true"
spec:
  serviceAccountName: rbp-test
  restartPolicy: Never
  containers:
  - name: rbp
    # Any base image with CA certificates; install `tar` if you need `kubectl cp`.
    image: mcr.microsoft.com/azurelinux/base/core:3.0
    command: ["sleep", "infinity"]
```

Verify the injection:

```bash
kubectl -n rbp-test exec rbp-test -- sh -c 'env | grep ^AZURE_'
# Expect AZURE_CLIENT_ID (your MSI), AZURE_TENANT_ID,
# AZURE_FEDERATED_TOKEN_FILE, AZURE_AUTHORITY_HOST.
```

## Step 5 — Run the tool in the pod

Copy the binary in (`kubectl cp` needs `tar` in the image) and run it with the
same environment the EV2 step sets. Tools using
`DefaultAzureCredential` with `RequireAzureTokenCredentials` need
`AZURE_TOKEN_CREDENTIALS` set; `WorkloadIdentityCredential` selects the
federated identity explicitly:

```bash
kubectl cp ./mytool rbp-test/rbp-test:/tmp/mytool
kubectl -n rbp-test exec rbp-test -- env \
  AZURE_TOKEN_CREDENTIALS=WorkloadIdentityCredential \
  SUBSCRIPTION_ID="$SUB" \
  <other EV2 step env vars> \
  /tmp/mytool
```

ARM now authorizes the request against the **constrained test identity**, so
any RBAC-gated failure (e.g. `403 LinkedAuthorizationFailed`) reproduces exactly
as it would in the pipeline. Run the fixed binary the same way to confirm the
fix.

> **Tip.** To exercise a single unexported function rather than the whole tool,
> add a guarded in-package Go test that calls it and build a test binary with
> `go test -c` — it links the real production code path and authenticates via
> the same `DefaultAzureCredential`.

## Step 6 — Restore state and tear down

```bash
# Restore anything the test mutated (e.g. re-enable a setting you disabled).
kubectl delete ns rbp-test --wait=false
az role assignment delete --assignee "$PRIN" --scope "/subscriptions/$SUB" --subscription "$SUB"
az identity delete -n rbp-test-msi -g "$MSI_RG" --subscription "$SUB"   # removes the federated credential too
```

Keep the blast radius small: target a single resource and avoid the node hosting
your test pod.

## See also

- [`personal-dev.md`](./personal-dev.md) — standing up a personal-dev environment.
- [`ev2-deployment.md`](./ev2-deployment.md) — how EV2 rollouts and shell steps work.
- [`pipeline-concept.md`](./pipeline-concept.md) — pipeline steps and `shellIdentity`.
