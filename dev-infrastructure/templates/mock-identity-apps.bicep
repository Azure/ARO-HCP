// Creates the mock identity Entra applications and their service principals via
// ../modules/entra/app.bicep. It does NOT configure any authentication mechanism
// on the apps — that is done out of band by pinning the certificate (see below).
//
// AUTHENTICATION: PINNED CERTIFICATE (keyCredentials), NOT SNI
// The mock identity certificates are SELF-SIGNED and Clusters Service
// authenticates as these mock SPs in send-certificate-chain mode. An earlier
// iteration configured Subject Name and Issuer (SNI) trust here via
// `trustedSubjectNameAndIssuers` (self-signed sentinel authority). That does not
// work: when the client sends the chain AND the app has trustedSubjectNameAndIssuers
// set, Entra validates the full CA chain and rejects the self-signed leaf with
// `AADSTS7000213: Invalid certificate chain`, without falling back to
// leaf-thumbprint matching. This template therefore does NOT set SNI trust; the
// module passes an empty `trustedSubjectNameAndIssuers`, which clears any stale
// SNI config on redeploy.
//
// Instead, each app's Key Vault certificate PUBLIC key is registered as a pinned
// `keyCredential` (AsymmetricX509Cert / Verify) by the privileged dev-ci
// pipeline's Shell steps, which read the cert from Key Vault (Bicep cannot read
// Key Vault certificate material) and PATCH it onto the app via Microsoft Graph.
// The `make pin-mock-identity-certs` / `make pin-int-mock-identity-certs` targets
// remain available for targeted repair or rotation. Entra
// then authenticates by matching the presented leaf's thumbprint against the
// pinned keyCredentials — the proven, pre-SNI-migration behaviour. This module
// deliberately does not manage `keyCredentials` (it defaults to empty, which
// ../modules/entra/app.bicep maps to null / "leave unmanaged"), so redeploying
// this template does NOT wipe the pinned certificates.
//
// CERTIFICATE CREATION (separate step, not this template):
// The certificates themselves are created out of band by `make
// create-mock-identity-certs` (DEV) / `make create-int-mock-identity-certs`
// (INT), which call scripts/create-kv-cert.sh (idempotent az keyvault
// certificate create) into the environment Key Vault (aro-hcp-dev-svc-kv for
// DEV, aro-hcp-int-kv for INT). Because auth is by pinned thumbprint, rotating a
// certificate requires re-running the pin target so the new thumbprint is
// registered.
// For a fresh bootstrap, run the cert-create target, deploy this template, then
// run the pin target (see docs/ci/dev-mock-identities.md).
@description('Shared mock identity definitions: array of {applicationName, certDns}')
param identities array

@description('Number of pooled MSI mock identities to create')
param poolSize int = 0

@description('Base application name for pooled identities (e.g. aro-dev-msi-mock-pool)')
@minLength(1)
param poolAppBaseName string

module sharedApps '../modules/entra/app.bicep' = [
  for identity in identities: {
    name: 'mock-app-${identity.applicationName}'
    params: {
      applicationName: identity.applicationName
      uniqueName: toLower(replace(identity.applicationName, ' ', '-'))
      manageSp: true
    }
  }
]

module poolApps '../modules/entra/app.bicep' = [
  for i in range(0, poolSize): {
    name: 'mock-app-pool-${i}'
    params: {
      applicationName: '${poolAppBaseName}-${i}'
      uniqueName: toLower(replace('${poolAppBaseName}-${i}', ' ', '-'))
      manageSp: true
    }
  }
]
