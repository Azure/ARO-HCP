# Sessiongate

Sessiongate is a Kubernetes controller and proxy service that provides secure, time-limited, identity-based access to customer Hosted Control Plane (HCP) clusters in ARO-HCP. It enables SREs to perform debugging and break-glass operations while maintaining security and compliance requirements.

Sessiongate is designed to be triggered by the Admin API and Geneva Actions, operating as a dedicated component that segregates the elevated permissions needed for credential minting and network path establishment from other ARO-HCP services.

## Overview

Sessiongate manages the complete lifecycle of ephemeral debugging sessions. When triggered by the Admin API or Geneva Actions, it creates authorization policies to enforce identity-based access control and enable auditing, mints time-limited credentials for target clusters via Azure APIs, exposes authenticated proxy endpoints that forward Kubernetes API requests, and automatically expires sessions based on configured TTL to ensure temporary access only.

## Architecture

### Components

**Control Plane Controller**: Watches `Session` custom resources, reconciles desired state by managing credential Secrets and Istio `AuthorizationPolicy` objects, and participates in leader election to coordinate credential minting across replicas. Only the elected leader mints credentials and creates authorization policies. Once credentials and policies are ready, the control plane updates the Session CR status to signal the data plane that the session is valid and ready to serve requests.

**Data Plane Controller**: Watches `Session` custom resources for status updates and credential Secrets created by the control plane controller. When a session becomes ready (indicated by status updates), it extracts session configuration from the credential Secret and registers the session in the local session registry. Runs on all pods (both leader and followers) to enable distributed request handling across all replicas.

**HTTP Server & Session Registry**: Serves proxy endpoints at `/sessiongate/{sessionID}/kas/*` and maintains a registry of active sessions. The registry is populated by the data plane controller watching Session status updates and credential Secrets via shared informers.

**Credential Provider**: Mints time-limited cluster credentials by calling Azure APIs for AKS management clusters or generating client certificates for HCP hosted clusters, storing results in Kubernetes Secrets.

**Istio**: Adds session-specific authorization by enforcing `AuthorizationPolicy` resources that match JWT claims against the session owner.

**Mise**: Microsoft's authentication and authorization component that provides the first layer of security by validating JWTs before requests reach sessiongate.

### Endpoint Offering

The control plane controller updates the Session CR status with `endpoint`, `credentialsSecretRef`, and `backendKASURL` fields once credentials are minted and the authorization policy is in place. When all prerequisites are met, it sets the `Ready` condition to signal that the session is available.

The data plane controller watches Session CR updates on all pods (leader and followers). When a session's `Ready` condition becomes true, the data plane controller validates that `credentialsSecretRef` and `backendKASURL` are present, fetches the credentials from the Secret, and registers the session in the local in-memory registry. Each pod independently registers sessions based on the Session CR status, enabling distributed request handling across all replicas without relying on leader election.

Client requests flow through Mise for JWT validation, then through Istio for session-specific authorization based on the `AuthorizationPolicy`. Sessiongate proxies regular HTTP requests and WebSocket upgrades to the target cluster using the credentials and backend KAS URL from the Session CR status—note that SPDY is not supported due to Istio limitations. When a session expires, is deleted, or becomes not ready, the data plane controller unregisters it from the local registry, ensuring clean session termination.

**Endpoint Format**: `https://{ingressBaseURL}/sessiongate/{sessionID}/kas`

## Custom Resource Definition

```yaml
apiVersion: sessiongate.aro-hcp.azure.com/v1alpha1
kind: Session
metadata:
  name: my-debug-session
  namespace: sessiongate
spec:
  ttl: 1h
  managementCluster:
    resourceId: /subscriptions/.../resourceGroups/.../providers/Microsoft.ContainerService/managedClusters/the-mgmt-cluster
  hostedControlPlane:
    resourceId: /subscriptions/.../resourceGroups/.../providers/Microsoft.RedHatOpenshift/hcpOpenShiftClusters/the-hcp
    namespace: namespace-that-contains-the-hosted-control-plane-cr
  accessLevel:
    group: aro-sre-pso
  owner:
    type: User
    userPrincipal:
      name: user@example.com
      claim: upn
status:
  endpoint: https://sessiongate.example.com/sessiongate/my-debug-session/kas
  expiresAt: "2025-12-23T16:30:00Z"
  credentialsSecretRef: my-debug-session-credentials
  backendKASURL: https://api.my-hcp.example.com:6443
```

## Security Model

Sessiongate implements defense-in-depth with multiple security layers. Mise validates JWTs before requests reach sessiongate, providing the first authentication barrier. Istio then enforces session-specific `AuthorizationPolicy` resources that match JWT claims against the session owner, ensuring only the designated principal can access their session endpoint.

## Log Levels

Sessiongate uses [klog](https://github.com/kubernetes/klog) for structured logging. Control verbosity with the `-v` flag:

- **`-v=0` (default)**: Errors and critical events only
- **`-v=2` (recommended for production)**: Important operations - startup, session registration/unregistration, credential minting, leader election changes
- **`-v=4` (debug)**: Detailed flow - credential polling, event filtering, tombstone recovery
- **`-v=6` (trace)**: Deep debugging - URL construction, detailed request handling
