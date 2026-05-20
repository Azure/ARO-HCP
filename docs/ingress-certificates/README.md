# Customer-Managed Ingress Serving Certificates

This directory holds the design and e2e test plan for letting an ARO HCP
customer store the ingress serving certificate for their hosted cluster in an
Azure Key Vault that they own, and have that certificate (and its private key)
delivered into the workload cluster as a standard `kubernetes.io/tls` Secret —
including automatic propagation of rotations performed in Key Vault.

- [design.md](design.md) — architecture, chosen sync mechanism, identity model,
  rotation flow, and the rationale for selecting External Secrets Operator over
  the alternatives.
- [e2e-test-plan.md](e2e-test-plan.md) — how this is exercised end-to-end:
  create cluster, add nodepool, provision the AKV certificate + sync glue,
  point the IngressController at the Secret, rotate the certificate in Key
  Vault, and verify the new material is served.
