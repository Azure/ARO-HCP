# E2E Test: Customer-Managed Ingress Cert from Azure Key Vault

This document describes the current test for [the Key Vault sync design](design.md).
It issues a DigiCert OV certificate through the customer Key Vault, syncs its
PFX into a workload-cluster TLS Secret with External Secrets Operator (ESO), and
checks that a sample application serves it. It then issues another version and
verifies automatic propagation through both the Secret and the router.

## Test identity and prerequisites

- **Implementation:** [`cluster_ingress_cert_from_kv.go`](../../test/e2e/cluster_ingress_cert_from_kv.go).
- **Spec name:** `Customer should be able to source the default ingress serving certificate from an Azure Key Vault and have rotations propagate automatically`.
- **Labels:** `RequireNothing`, `High`, `Positive`, `CreateCluster`,
  `AroRpApiCompatible`, and `MIContainers(1)`.
- **Cluster:** A per-test cluster named `ingress-cert-kv` with a two-replica
  node pool named `np-1`, using the `20240610` framework helpers.

The test needs mounted DigiCert `account-id`, `api-key`, and `organization-id`
files. The organization must already be OV validated. Missing files skip the test
before provisioning; unreadable or empty files and invalid organization IDs fail
before provisioning. See [credential setup and local invocation](../../test/e2e/README.md#digicert-credentials-for-the-key-vault-ingress-certificate-test).

The Azure runner credential needs the usual cluster/customer-infrastructure
permissions, plus permission to create a UAMI and federated credential, assign
vault roles, configure the Key Vault issuer, request certificates, and read/update
DNS in the cluster's managed resource group. The DigiCert API key is shared by
Key Vault issuance and CertCentral domain validation. The runner must reach
CertCentral, Key Vault, the pinned ESO release manifest, and the sample
application's public HTTPS route. ESO must reach Microsoft Entra and Key Vault
from the workload cluster.

## What the test does, end to end

```mermaid
sequenceDiagram
    participant Test as e2e test
    participant ARM as Azure ARM and DNS
    participant DC as DigiCert CertCentral
    participant AKV as Customer Key Vault
    participant RP as ARO HCP RP
    participant HC as Workload cluster and ESO
    participant App as Sample application route

    Test->>Test: Read credentials and load pinned DigiCert root
    Test->>ARM: Create customer resource group and infrastructure including AKV
    Test->>RP: Create HCP cluster and two-worker node pool
    Test->>HC: Get admin credentials and verify cluster viability
    Test->>ARM: Create ESO UAMI, federated credential, and vault role assignments
    Test->>AKV: Configure DigiCert issuer
    Test->>DC: Register or find apps domain in the configured organization
    opt Domain needs current OV validation
        Test->>ARM: Publish apps TXT token in delegated DNS zone
        Test->>Test: Check delegation and TXT through configured resolver
        Test->>DC: Request DCV check and wait for OV domain validation
    end
    Test->>AKV: Issue cert v1 through DigiCert with AutoRenew policy
    Test->>HC: Install pinned ESO release and wait for controller readiness
    Test->>HC: Verify guest DNS resolves Microsoft Entra
    Test->>HC: Create ServiceAccount and ClusterSecretStore; wait for Ready
    Test->>HC: Create ExternalSecret; wait for Ready
    Test->>HC: Verify TLS Secret v1 fingerprint, key pair, SAN, and trusted chain
    Test->>HC: Deploy sample app and verify ACM certificate Secret is absent
    Test->>HC: Patch default IngressController to use ingress-tls
    Test->>App: Verify HTTP 200 over TLS with cert v1 and pinned trust roots
    Test->>AKV: Issue cert v2 under the same certificate name
    Test->>HC: Verify TLS Secret v2 fingerprint, key pair, SAN, and trusted chain
    Test->>App: Verify HTTP 200 over TLS with cert v2 and pinned trust roots
    Test->>HC: Verify ACM certificate Secret remains absent
    Test->>ARM: Remove test-owned TXT value
    Test->>DC: Remove domain only if this test created it
    Test->>RP: TestContext deletes HCP cluster
    Test->>ARM: TestContext deletes customer resource group
```

## Step-by-step

### 1. Provision cluster and node pool

The test creates a `framework.NewTestContext()`, reads credentials, and loads
the pinned DigiCert root before allocating cloud resources. When pooled
identities are enabled, it assigns one identity container.

It creates a unique customer resource group with
`tc.NewResourceGroup(ctx, "ingress-cert-kv", tc.Location())` and prepares
`framework.NewDefaultClusterParams20240610()`. The managed resource group name
uses `framework.SuffixName` with `-managed` and a 64-character limit.
`tc.CreateClusterCustomerResources20240610` uses
`framework.RBACScopeResourceGroup` and the embedded customer-infrastructure
fixture. This supplies `clusterParams.KeyVaultName`; the ingress certificate
shares that vault with the etcd encryption key.

The test creates the cluster and node pool with
`CreateHCPClusterFromParam20240610` and `CreateNodePoolFromParam20240610`, using
`framework.ClusterCreationTimeout` and `framework.NodePoolCreationTimeout`.
It retrieves admin credentials using `GetAdminRESTConfigForHCPCluster20240610`
with `framework.GetAdminRESTConfigTimeout`, then calls `VerifyHCPCluster`.

From the cluster response it reads `Properties.Platform.IssuerURL` and derives
the apps base domain from `Properties.Console.URL`. It asserts that domain equals
`apps.aro.<DNS.BaseDomainPrefix>.<DNS.BaseDomain>`. The delegated Azure DNS zone
is `aro.<DNS.BaseDomainPrefix>.<DNS.BaseDomain>` in the managed resource group;
the expected certificate wildcard is `*.<apps-base-domain>`.

### 2. Create the ESO identity and configure vault permissions

The test uses Azure SDK clients to create UAMI `eso-akv` and federated credential
`eso-akv-fic` in the customer resource group. The federated credential uses:

- Issuer: the workload cluster's OIDC issuer URL.
- Subject: `system:serviceaccount:external-secrets:eso-azure-kv`.
- Audience: `api://AzureADTokenExchange`.

It assigns **Key Vault Certificate User** and **Key Vault Secrets User** to the
UAMI at vault scope, retrying `PrincipalNotFound` while the identity propagates.
It identifies the Azure runner's object ID from its ARM token and grants that
caller **Key Vault Certificates Officer** on the same vault. This separates ESO's
read access from the caller's issuer/certificate write access.

### 3. Configure DigiCert and validate the apps domain

The test configures an enabled Key Vault issuer named `digicert`, with provider
`DigiCert`, the mounted account ID/API key, and the mounted organization ID.
Issuer setup is retried for up to three minutes to allow vault RBAC to propagate.

[`DigiCertClient.EnsureDomain`](../../test/util/framework/digicert_helper.go)
finds the exact apps domain in that organization or registers it for OV validation
using `dns-txt-token`. It rejects a domain belonging to another organization.
Current validation is reused only when OV is active, DCV is complete, and the
reported validation/DCV expiry times are still current.

If validation is needed, the test obtains a nonempty, unexpired TXT token and
uses [`PresentAzureDNSTXT`](../../test/util/framework/azure_dns_txt_helper.go)
to add it at record name `apps` in the delegated zone. Existing TXT values and
metadata are preserved, and ETags protect concurrent changes. The
[`dnsreadback.VerifyTXT`](../../test/util/dnsreadback/txt.go) precheck uses
`net.DefaultResolver` to compare the zone's NS records with Azure's nameserver
list and find the expected TXT token. It does not query external nameservers
directly. DigiCert's subsequent DCV check establishes public visibility.

The test triggers DCV once, then polls until OV domain validation is current and
complete. It does not attempt to validate the organization itself.

### 4. Issue certificate v1 through Key Vault

[`CreateOrRotateDigiCertKVCert`](../../test/util/framework/keyvault_cert_helper.go)
submits one Key Vault issuance request for certificate `ingress-default`, using
issuer `digicert`. The policy requests:

- Certificate type `OV-SSL`.
- An exportable RSA-2048 key, with `ReuseKey: false`.
- Secret content type `application/x-pkcs12`.
- Subject `CN=*.<apps-base-domain>` and SANs for both the apps base domain and
  its wildcard.
- Six months of validity (subject to DigiCert's issuance limits).
- `AutoRenew` at 80% of the certificate lifetime.

The helper polls the asynchronous Key Vault operation and returns its version
and the lowercase SHA-256 of the leaf DER. It logs CSR metadata once when
available, and reports operation status/details on failure. A timeout does not
cancel the pending Key Vault operation; inspect that operation before submitting
another request.

### 5. Install ESO and wait for authentication and synchronization

`applyManifestFromURL` downloads the pinned **v0.16.0** ESO release manifest and
creates its objects through the Kubernetes dynamic client; the runner does not
invoke Helm or `kubectl`. The test waits for a ready replica of deployment
`default/external-secrets`.

It separately creates namespace `external-secrets` for the Azure-authentication
ServiceAccount and a temporary guest DNS probe. Before configuring the store,
`waitForIngressCertificateDNS` requires an available, nondegraded DNS
ClusterOperator, ready UDP endpoints for `openshift-dns/dns-default`, and a
pod-side A-record answer for `login.microsoftonline.com`. The probe uses
`agnhost:2.39` with `ClusterFirst` DNS, mounts no ServiceAccount token, and is
deleted when the check finishes.

The test creates:

- `ServiceAccount external-secrets/eso-azure-kv`, annotated with the UAMI client
  ID and tenant ID (falling back to the UAMI's tenant if the context has none).
- `ClusterSecretStore customer-akv`, using Azure Key Vault `WorkloadIdentity`
  auth and the ServiceAccount reference.
- `ExternalSecret openshift-ingress/ingress-tls`, using the store and the
  versionless Key Vault certificate secret name, with `refreshInterval: 30s`.
  Its template converts the base64 PFX using `b64dec | pkcs12cert` and
  `b64dec | pkcs12key` into `tls.crt` and `tls.key` in a `kubernetes.io/tls` Secret.

The store must reach `Ready=True` before the ExternalSecret is created. The
ExternalSecret then has its own `Ready=True` wait. These waits have separate
budgets from certificate-content checks because ESO error backoff is independent
of `refreshInterval`. See [the manifests](design.md#the-externalsecret).

### 6. Verify v1 in the Secret and on a sample application

`expectTLSSecretCertificate` checks that `openshift-ingress/ingress-tls`:

- Has type `kubernetes.io/tls` and a parseable certificate/private-key pair whose
  public keys match.
- Has a leaf SHA-256 matching the Key Vault result.
- Includes the exact apps wildcard SAN.
- Has a valid certificate chain to the explicitly pinned DigiCert root, using
  the remaining certificates in `tls.crt` as intermediates.

The test then calls `DeploySampleApp` to create an application with an edge TLS
route that uses the router's default certificate. It checks that ACM's
`openshift-ingress/cluster-ingress-cert` Secret is absent and patches
`IngressController/default` in `openshift-ingress-operator`:

```yaml
spec:
  defaultCertificate:
    name: ingress-tls
```

`VerifyCustomIngressCertificate` waits for route DNS, then polls HTTPS until the
sample application returns HTTP 200. TLS verification requires a chain to the
pinned roots, hostname coverage, the expected wildcard SAN, a positive serial
number, current validity dates, and the exact leaf certificate validated from the
Secret. TLS verification stays enabled in development environments too. The test
checks ACM's Secret is still absent after the override.

The embedded `DigiCert High Assurance EV Root CA` is trusted explicitly for both
Secret and HTTPS checks; these assertions do not establish public browser trust.
[`TestPinnedDigiCertRoot`](../../test/e2e/cluster_ingress_cert_from_kv_test.go)
checks the embedded root's fingerprint and loading independently of system CAs.

### 7. Rotate and verify v2 automatically propagates

The test calls `CreateOrRotateDigiCertKVCert` again with the same name and policy
and asserts that v2's SHA-256 differs from v1. It repeats all Secret checks for v2,
then calls `VerifyCustomIngressCertificate` for the same sample route with the
new expected leaf and pinned roots. It does not repatch the IngressController or
manually restart the router. It checks ACM's Secret remains absent after rotation.

This exercises propagation of a new latest version. The `AutoRenew` policy is
configured, but the test does not wait for its lifetime threshold or assert that
router pods avoid an automatic rollout.

## Wait budgets and diagnostics

Shared provisioning, admin-access, guest-DNS, and route-DNS budgets come from
[`framework` constants](../../test/util/framework/constants.go) and
[`WaitForDNSResolution`](../../test/util/framework/dns.go).
The test-specific waits are:

| Check | Timeout | Poll interval |
| --- | --- | --- |
| UAMI vault role assignment, per role | 5 minutes | 15 seconds |
| DigiCert issuer setup | 3 minutes | 15 seconds |
| Runner delegation/TXT precheck | 5 minutes | 15 seconds; 5-second lookup context |
| DigiCert domain validation | 10 minutes | 15 seconds |
| Key Vault issuance, per version | 15 minutes | 2 seconds |
| ESO controller readiness | 10 minutes | 10 seconds |
| Guest DNS prerequisite | `IngressDNSVerifyTimeout` | 5 seconds |
| ClusterSecretStore creation while CRD becomes available | 2 minutes | 5 seconds |
| Store and ExternalSecret readiness, separately | 10 minutes each | 10 seconds |
| TLS Secret matching, per version | 5 minutes | 10 seconds |
| Sample-application HTTPS, per version, after DNS resolution | 10 minutes | `verifiers.DefaultPollInterval` |

The 30-second refresh interval is not a deadline for initial authentication or
rotation convergence. The current test allows the independent waits above.

DNS, ESO readiness, Secret matching, and HTTPS verification log changed failure
states instead of repeating unchanged errors. ESO diagnostics include readiness
conditions and the latest Warning event for the current resource UID, exposing
Azure authentication, authorization, and template failures. Once the Kubernetes
clients exist, a failure cleanup logs state for both the store and ExternalSecret
before teardown. HTTPS timeout diagnostics print the negotiated certificate.
The test does not collect dedicated router logs or dump TLS Secret contents.

## Cleanup

Cleanup callbacks remove the sample-application namespace, the test-added TXT
value, and the CertCentral domain only if this test created it. Existing domain
registrations and TXT values are preserved. The temporary DNS probe is deleted
by its helper on success or failure.

`TestContext` deletes the HCP cluster and then the customer resource group,
which contains the Key Vault and ESO UAMI. ESO and the remaining in-cluster
resources disappear with the cluster. The framework then attempts to purge
soft-deleted vaults from that resource group; purge-protected vaults are skipped
and purge errors are logged without failing cleanup. The test does not revoke
issued certificates.

## Coverage and follow-ups

The test covers DigiCert issuance through Key Vault, workload-identity vault
access, PFX-to-TLS templating, router use of a customer Secret, trusted HTTPS to
a sample application, and automatic propagation of an explicitly issued new
version. ACM Secret absence is checked before the override, after it, and after
rotation.

Follow-up e2e scenarios include revoking vault access and supplying an invalid
PFX/template, then verifying the previous working certificate remains available.
Reusable customer Bicep/manifests and testing the actual lifetime-triggered
renewal remain outside the current test.
