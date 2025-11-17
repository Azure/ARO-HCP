# Sessiongate

Sessiongate is a Kubernetes controller and proxy service that provides secure, time-limited, identity-based access to customer Hosted Control Plane (HCP) clusters in ARO-HCP (Azure Red Hat OpenShift on Hosted Control Planes). It enables SREs to perform debugging and break-glass operations while maintaining security and compliance requirements.

Sessiongate is designed to be triggered by the Admin API and Geneva Actions, operating as a dedicated component that segregates the elevated permissions needed for credential minting and network path establishment from other ARO-HCP services.

## Overview

Sessiongate manages the complete lifecycle of ephemeral debugging sessions. When triggered by the Admin API or Geneva Actions, it creates authorization policies to enforce identity-based access control and enable auditing, mints time-limited credentials for target clusters via Azure APIs, exposes authenticated proxy endpoints that forward Kubernetes API requests, and automatically expires sessions based on configured TTL to ensure temporary access only.

## Architecture

### Components

**Mise**: Microsoft's authentication and authorization component that provides the first layer of security by validating JWTs before requests reach sessiongate.

**Controller**: Watches `Session` custom resources, reconciles desired state by managing credential Secrets and Istio `AuthorizationPolicy` objects, and participates in leader election to coordinate credential minting across replicas.

**HTTP Server & Session Registry**: Serves proxy endpoints at `/sessiongate/{sessionID}/kas/*` and maintains a registry of active sessions. All pods (both leader and followers) watch for credential Secrets via shared informers and host sessions with credentials minted by the leader, enabling distributed request handling.

**Credential Provider**: Mints time-limited cluster credentials by calling Azure APIs for AKS management clusters or generating client certificates for HCP hosted clusters, storing results in Kubernetes Secrets.

**Istio**: Adds session-specific authorization by enforcing `AuthorizationPolicy` resources that match JWT claims against the session owner.

### Session Reconciliation

The controller reconciles each `Session` resource by first validating required fields and adding a finalizer for cleanup. It calculates the session expiration time based on the configured TTL and checks if the session has expired, deleting it if necessary. For active sessions, the controller ensures credential Secrets exist by minting new cluster credentials when needed. Before allowing any access, it creates an Istio `AuthorizationPolicy` that restricts the session endpoint to the designated owner—this happens before registration to prevent a security gap where the session would be accessible without authorization. Finally, the controller updates the session status with the public endpoint URL and expiration timestamp, then requeues the reconciliation to occur when the session expires.

**Leader Election**: Only the elected leader controller mints credentials and manages Secrets and AuthorizationPolicies. When leadership changes, the new leader takes over reconciliation duties while the former leader stops processing the work queue. All pods continue to watch for Secret changes regardless of leadership status.

### Endpoint Offering

When the leader controller mints credentials, it stores them in a Secret with label `sessiongate.aro-hcp.azure.com/session-name` and data fields containing the kubeconfig and target cluster resource ID. All sessiongate pods (leader and followers) watch for these credential Secrets and extract the session configuration to host the session locally. Each pod creates a proxy handler that maintains a connection pool to the target cluster, enabling distributed request handling across all replicas.

Client requests flow through Mise for JWT validation, then through Istio for session-specific authorization based on the `AuthorizationPolicy`. Sessiongate proxies regular HTTP requests and WebSocket upgrades to the target cluster—note that SPDY is not supported due to Istio limitations. When a session expires or is deleted, the controller removes the credential Secret, triggering all pods to close their proxy connections and terminate any long-running operations including watches and WebSockets, ensuring clean session termination.

**Endpoint Format**: `https://{ingressBaseURL}/sessiongate/{sessionID}/kas`

## Custom Resource Definition

```yaml
apiVersion: sessiongate.aro-hcp.azure.com/v1alpha1
kind: Session
metadata:
  name: my-debug-session
spec:
  ttl: 1h
  managementCluster: /subscriptions/.../resourceGroups/.../managementClusters/my-mgmt
  hostedControlPlane: /subscriptions/.../resourceGroups/.../hostedControlPlanes/my-hcp  # optional
  accessLevel:
    group: cluster-admins  # becomes certificate subject for HCP RBAC
  owner:
    type: User
    userPrincipal:
      name: user@example.com
      claim: upn
status:
  endpoint: https://sessiongate.example.com/sessiongate/my-debug-session/kas
  expiresAt: "2025-12-11T16:30:00Z"
```

## Access Patterns

### Hosted Control Plane Access

When a `hostedControlPlane` resource ID is specified, sessiongate discovers the HCP namespace in the management cluster using a Hypershift client. It then mints a client certificate with the access group as both the certificate subject and group membership for RBAC enforcement. The certificate is valid for the session's TTL duration and is not rotated during the session lifetime—credentials remain static once minted and stored in the Secret.

## Security Model

Sessiongate implements defense-in-depth with multiple security layers. Mise validates JWTs before requests reach sessiongate, providing the first authentication barrier. Istio then enforces session-specific `AuthorizationPolicy` resources that match JWT claims against the session owner, ensuring only the designated principal can access their session endpoint.

Each session uses dedicated credentials stored in isolated Kubernetes Secrets, preventing credential sharing across sessions. Sessions automatically expire after their configured TTL, enforcing time-limited access with automatic cleanup of endpoints and credential termination.

Finalizers prevent premature Session deletion before cleanup completes, while owner references ensure that Secrets and AuthorizationPolicies are automatically garbage-collected when the parent Session is removed, maintaining consistent resource lifecycle management.

## Deployment

The controller runs as a Deployment in the ARO-HCP service cluster and is triggered by the Admin API or Geneva Actions to create Session resources. Multiple replicas provide high availability—each pod can accept requests for any session since all pods watch for credential Secrets and host sessions independently. The deployment includes an Istio sidecar for authorization enforcement and exposes standard observability endpoints including Prometheus metrics at `/metrics` and health checks at `/healthz` and `/readyz`.
