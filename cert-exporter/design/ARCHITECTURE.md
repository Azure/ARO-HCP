# Certificate Exporter Architecture

## Deployment Architecture

```mermaid
flowchart TB
    Prometheus["Prometheus"]
    SourceRegistry["Source image registries"]
    ServiceACR["Environment service ACR"]

    subgraph ServiceCluster["Service cluster"]
        SvcExporter["cert-exporter"]
        SvcSA["Exporter ServiceAccount"]
        SvcRole["Secret-reader ClusterRole"]
        SvcBinding["Static RoleBinding\naks-istio-ingress"]
        SvcSecrets["TLS Secrets\naks-istio-ingress"]

        SvcBinding --> SvcRole
        SvcBinding --> SvcSA
        SvcExporter --> SvcSA
        SvcExporter -->|get/list/watch| SvcSecrets
    end

    subgraph ManagementCluster["Management cluster"]
        MgmtExporter["cert-exporter"]
        MgmtExporterSA["Exporter ServiceAccount"]
        MgmtRole["Secret-reader ClusterRole"]
        Controller["RBAC controller"]
        ControllerSA["Controller ServiceAccount"]
        Admission["ValidatingAdmissionPolicy"]
        DynamicBindings["Dynamic RoleBindings\nocm-* and policy namespace"]
        MgmtSecrets["Selected TLS Secrets"]

        Controller --> ControllerSA
        Controller -->|RoleBinding mutations| Admission
        Admission -->|validated requests| DynamicBindings
        DynamicBindings --> MgmtRole
        DynamicBindings --> MgmtExporterSA
        MgmtExporter --> MgmtExporterSA
        MgmtExporter -->|list/watch| MgmtSecrets
    end

    SourceRegistry -->|ImageMirror| ServiceACR
    ServiceACR --> SvcExporter
    ServiceACR --> MgmtExporter
    ServiceACR --> Controller
    SvcExporter -->|certificate metrics :9793| Prometheus
    MgmtExporter -->|certificate metrics :9793| Prometheus
    Controller -->|health and RBAC metrics :8081| Prometheus
```

## Authorization Boundaries

```mermaid
flowchart LR
    subgraph StaticBoundary["Service cluster: static namespace"]
        Helm["Helm release"]
        StaticRB["RoleBinding\naks-istio-ingress"]
        SvcIdentity["Exporter identity"]
        SvcData["Secrets only in\naks-istio-ingress"]

        Helm --> StaticRB
        StaticRB --> SvcIdentity
        SvcIdentity --> SvcData
    end

    subgraph DynamicBoundary["Management cluster: dynamic namespaces"]
        NamespaceList["Namespace list"]
        Selector["prefix ocm-* OR\nexact policy namespace"]
        ControllerIdentity["Controller identity"]
        Policy["Fail-closed admission policy"]
        DynamicRB["Exact cert-exporter\nRoleBinding shape"]
        MgmtIdentity["Exporter identity"]
        MgmtData["Secrets only in\nselected namespaces"]

        NamespaceList --> Selector
        Selector --> ControllerIdentity
        ControllerIdentity --> Policy
        Policy --> DynamicRB
        DynamicRB --> MgmtIdentity
        MgmtIdentity --> MgmtData
    end
```

The RoleBindings in both clusters refer to a ClusterRole, but Kubernetes limits
the granted permissions to each RoleBinding's namespace. Neither exporter
ServiceAccount receives a Secret-reader ClusterRoleBinding.

## Management Reconciliation Sequence

```mermaid
sequenceDiagram
    participant C as RBAC controller
    participant N as Namespace API
    participant A as Admission policy
    participant R as RoleBinding API
    participant E as cert-exporter
    participant S as TLS Secrets

    C->>N: List namespaces
    N-->>C: Current namespace names
    loop Every selected namespace
        C->>R: Get cert-exporter RoleBinding
        alt Binding is missing
            C->>A: Create fixed RoleBinding shape
            A->>R: Allow validated request
        else Owned binding drifted
            C->>A: Update or recreate fixed shape
            A->>R: Allow validated request
        else Unmanaged name collision
            C-->>C: Record error; do not adopt
        end
    end
    loop Every no-longer-selected namespace
        C->>A: Delete owned RoleBinding
        A->>R: Allow cleanup
    end
    E->>S: List and watch through namespaced grants
    S-->>E: TLS certificate data
```

## Request Validation

```mermaid
flowchart TD
    Request["Controller RoleBinding request"]
    Identity{"Controller ServiceAccount?"}
    Delete{"DELETE?"}
    Owned{"Named cert-exporter and\ncontroller-owned?"}
    Target{"Approved namespace?"}
    Shape{"Exact roleRef, subject,\nname, and label?"}
    Allow["Allow"]
    Deny["Deny"]

    Request --> Identity
    Identity -->|No| Allow
    Identity -->|Yes| Delete
    Delete -->|Yes| Owned
    Owned -->|Yes| Allow
    Owned -->|No| Deny
    Delete -->|No| Target
    Target -->|No| Deny
    Target -->|Yes| Shape
    Shape -->|Yes| Allow
    Shape -->|No| Deny
```

The policy matches the controller identity. Requests from other identities are
outside this policy's match condition and remain subject to Kubernetes RBAC and
other cluster admission controls.

## Failure and Recovery

```mermaid
flowchart LR
    Failure["Controller unavailable"]
    Existing["Existing RoleBindings remain"]
    Export["Existing namespaces continue exporting"]
    NewNS["New target namespace"]
    Pending["No exporter access yet"]
    Recovery["Controller recovers"]
    Reconcile["Next reconciliation creates binding"]

    Failure --> Existing --> Export
    Failure --> NewNS --> Pending
    Pending --> Recovery --> Reconcile --> Export
```

This behavior preserves monitoring for existing hosted clusters while keeping
new namespace authorization fail-closed until the controller reconciles.
