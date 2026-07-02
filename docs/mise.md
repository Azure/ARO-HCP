# Whats is MISE?

Microsoft Identity Service Essentials (MISE) is an internal Microsoft service providing:
- Validation of Azure Active Directory (AAD) tokens, including Proof-of-Possession (PoP) tokens and Bearer tokens.
- Authorization based on token claims (appid, roles, scp, etc.), PoP key-binding, and custom policies.
- Integration with Istio external authorizer, enabling token validation at the ingress/service mesh layer.

# Deployment

- MISE is deployed in its own dedicated namespace within the service cluster
- MISE operates as a central authorization service for the RP frontend and other services requiring secure API validation like the Admin API and Backplane API
- mTLS is enforced for communication between Istio components, MISE, and the APIs
- An `ext-authz` provider is defined in the Istio mesh config and referenced by AuthorizationPolicies on each protected service (frontend, admin, sessiongate)

# Configuration

MISE uses v2 JSON configuration delivered via a ConfigMap (`appsettings.json` mounted into the pod). The config defines three inbound policies:
- **ARM** — PoP (Proof-of-Possession) protocol, validating signed HTTP request fields
- **Geneva Actions** — Bearer token protocol for admin/Geneva-originated requests
- **SessionGate** — Bearer token protocol for session management

See `istio/deploy/charts/mise/templates/configmap.yaml` for the full template.

# Frontend Authorization Model
- ARM sends an API call with a PoP token:
- Istio external authorizer intercepts the request.
- Istio forwards the request to MISE for validation:
    - Checks PoP token signature, expiration, and claims.
    - Verifies PoP key-binding.
    - Enforces expected audience and app ID.
- MISE returns Allow/Deny (200/403).
- Istio either forwards the request to the RP frontend or rejects it.

# Geneva Action Requests
- Geneva Action sends a request to the Admin API with its AAD token.
- Istio external authorizer intercepts the traffic.
- Istio calls MISE in the service cluster namespace to validate:
    - Token authenticity (issuer, audience, signature, expiration).
    - Expected app ID / service principal identity of Geneva Action.
    - Optional claim validation (e.g., Geneva-specific roles or scopes).
- MISE returns a decision.
- Istio enforces the decision (forward or reject).
Note: This retrofit ensures that Geneva Action traffic is consistently validated through the same MISE-based framework, providing a unified security model for both ARM and Geneva-originated requests.

# Audit Logging

MISE v2 (1.27.0+) automatically audits 100% of authentication requests. Audit records are written to the `AsmAuditCPRPMISE` Geneva table via the OpenTelemetry Geneva Log Exporter over a Unix domain socket provided by mdsd on the node.

## How it works

On Linux, MISE resolves the audit connection in this order:
1. If `AzureAd:AuditOptions:CustomConnectionString` is set, use it.
2. If `/var/run/mdsd/asa/default_fluent.socket` exists (AzSecPack/AutoConfig), use it.
3. Otherwise fall back to `Endpoint=unix:/var/run/mdsd/default_fluent.socket`.

ARO-HCP service cluster nodes run mdsd, exposing `/var/run/mdsd/` on the host. The MISE deployment mounts this directory into the pod via a `hostPath` volume when `audit.connectSocket` is enabled.

## Configuration

Audit logging is controlled by the `audit.connectSocket` toggle, following the same pattern as the RP frontend and Admin API:

- **Default** (`config/config.yaml`): `mise.audit.connectSocket: false`
- **Production overlay** (`config/config.msft.clouds-overlay.yaml`): set to `true` for int, stg, and prod environments

When enabled, the MISE Helm chart:
- Mounts `/var/run/mdsd` from the host into the pod
- Adds `AuditOptions.CustomConnectionString` to `appsettings.json` pointing to `Endpoint=unix:/var/run/mdsd/default_fluent.socket`

See `istio/deploy/charts/mise/templates/deployment.yaml` and `istio/deploy/charts/mise/templates/configmap.yaml` for the implementation.

## Emergency disable

In an emergency, audit logging can be disabled via the MISE config without removing the socket mount:

```json
{
  "AzureAd": {
    "AuditOptions": {
      "EmergencyDisableAllAuditLogging": true
    }
  }
}
```

