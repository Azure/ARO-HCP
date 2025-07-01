# Whats is MISE?

Microsoft Identity Service Essentials (MISE) is an internal Microsoft service providing:
- Validation of Azure Active Directory (AAD) tokens, including Proof-of-Possession (PoP) tokens and Bearer tokens.
- Authorization based on token claims (appid, roles, scp, etc.), PoP key-binding, and custom policies.
- Integration with Istio external authorizer, enabling token validation at the ingress/service mesh layer.

# Deployment

- MISE is deployed in its own dedicated namespace within the service cluster
- MISE operates as a central authorization service for the RP frontend and other services requiring secure API validation like the Admin API and Backplane API
- mTLS is enforced for communication between Istio components, MISE, and the APIs

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