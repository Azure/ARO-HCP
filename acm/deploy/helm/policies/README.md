# Policies helm chart

The policy helm chart allows the deployment of ACM Policies to Managed Clusters.

Policies are deployed after ACM/MCE installation is complete but before any hosted clusters are installed.

This helm chart deploys the following:
- **ManagedClusterSetBinding** is created to bind the **ManagedClusterSet** "hypershift-managed-clusters" to the **namespace** "open-cluster-management-policies".
- **Placement** object 'all-hosted-clusters' references the **ManagedClusterSet** "hypershift-management-clusters" so that policies can be bound it.

Add policies to this helm chart by creating a <policy-name>.policy.yaml file that includes:
- A **Policy** object.
- A **PlacementBinding**

Below is an example policy that would create a config map in the hosted cluster kube-system namespace.
```yaml
---
apiVersion: policy.open-cluster-management.io/v1
kind: Policy
metadata:
  annotations:
    policy.open-cluster-management.io/categories: CM Configuration Management
    policy.open-cluster-management.io/controls: CM-2 Baseline Configuration
    policy.open-cluster-management.io/standards: NIST SP 800-53
  name: demo-config-policy
  namespace: '{{ .Release.Namespace }}'
spec:
  disabled: false
  remediationAction: enforce 
  policy-templates:
    - objectDefinition:
        apiVersion: policy.open-cluster-management.io/v1
        kind: ConfigurationPolicy
        metadata:
          name: demo-config
        spec:
          evaluationInterval:
            compliant: 2h
            noncompliant: 45s
          object-templates:
          - complianceType: MustHave
            objectDefinition:
              apiVersion: v1
              kind: ConfigMap
              metadata:
                name: demo-config
                namespace: kube-system
              data:
                key: value
---
apiVersion: policy.open-cluster-management.io/v1
kind: PlacementBinding
metadata:
  name: configmap-placement-binding
  namespace: '{{ .Release.Namespace }}'
placementRef:
  apiGroup: cluster.open-cluster-management.io
  kind: Placement
  name: all-hosted-clusters
subjects:
- apiGroup: policy.open-cluster-management.io
  kind: Policy
  name: demo-config-policy
```
