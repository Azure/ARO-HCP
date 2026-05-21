# hypershift / pkiOperatorEvents

## Summary

Lists events for certificate signing request processing by the control-plane-pki-operator in the hosted control plane namespace.

## What to Look For

The certificate signing request should be approved, then marked valid, then fulfilled:

| timestamp                 | objectKind | objectName                 | reason                             | message            |
|---------------------------|------------|----------------------------|------------------------------------|--------------------|
| 5/12/2026, 4:19:55.927 AM | Deployment | control-plane-pki-operator | CertificateSigningRequestApproved  | "xxx" is approved  |
| 5/12/2026, 4:19:55.927 AM | Deployment | control-plane-pki-operator | CertificateSigningRequestValid     | "xxx" is valid     |
| 5/12/2026, 4:19:56.023 AM | Deployment | control-plane-pki-operator | CertificateSigningRequestFulfilled | "xxx" is fulfilled |

## Where to Go Next

Review the control-plane-pki-operator's logs and events relating to those pods.
