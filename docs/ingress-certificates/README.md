# Customer-Managed Ingress Serving Certificates

This directory describes the design and implemented e2e test for letting an ARO HCP
customer store the ingress serving certificate for their hosted cluster in an
Azure Key Vault that they own, and have that certificate (and its private key)
delivered into the workload cluster as a standard `kubernetes.io/tls` Secret —
including automatic propagation of rotations performed in Key Vault.

- [design.md](design.md) — architecture, chosen sync mechanism, identity model,
  rotation flow, and the rationale for selecting External Secrets Operator over
  the alternatives.
- [e2e-test-plan.md](e2e-test-plan.md) — the current test flow: create a cluster
  and node pool, validate its apps domain in DigiCert CertCentral, issue an OV
  certificate through Key Vault's DigiCert issuer, sync it with External Secrets
  Operator, and verify a sample application serves the certificate before and
  after rotation. The checks validate the key pair, wildcard SAN, and chain
  against an explicitly pinned DigiCert root.
- [DigiCert test setup](../../test/e2e/README.md#digicert-credentials-for-the-key-vault-ingress-certificate-test)
  — credential files, local invocation, CI mounts, and the test's trust scope.
