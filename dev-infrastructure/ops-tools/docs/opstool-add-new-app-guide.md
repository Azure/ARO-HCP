# Adding New Apps to the Opstool Cluster

This guide describes how to add a new operational tooling workload to the standalone `opstool` AKS cluster.

## Overview

Each app in the opstool cluster requires:
1. **Azure Managed Identity (UAMI)** - For workload identity and Azure resource access
2. **Kubernetes Resources** - Deployment, Service, ServiceAccount, etc.
3. **Pipeline Configuration** - For templatize deployment
4. **Topology Entry** - To integrate with the deployment system

## Prerequisites

- Access to the ARO-HCP repository
- Familiarity with Helm charts and Bicep
- Understanding of Azure Workload Identity

---

## Step 1: Add Managed Identity to Infrastructure

Edit `dev-infrastructure/templates/opstool-cluster.bicep` to add a new UAMI for your app.

### 1.1 Add to managedIdentities module

Find the `managedIdentities` module and add your app to the `identities` array:

```bicep
module managedIdentities '../modules/managed-identities.bicep' = {
  name: 'managed-identities'
  params: {
    location: location
    identities: [
      // ... existing identities ...
      {
        name: 'my-new-app'                    // UAMI name in Azure
        aksName: aksClusterName
        aksOidcIssuer: aks.outputs.aksOidcIssuer
        namespace: 'my-new-app'               // K8s namespace
        serviceAccountName: 'my-new-app'      // K8s service account name
      }
    ]
  }
}
```

### 1.2 Add helper variable to extract the identity

```bicep
var myNewAppMI = mi.getManagedIdentityByName(managedIdentities.outputs.managedIdentities, 'my-new-app')
```

### 1.3 Grant Key Vault access (if needed)

If your app needs secrets from the workload Key Vault:

```bicep
module myNewAppKVAccess '../modules/keyvault/keyvault-secret-access.bicep' = {
  name: 'my-new-app-kv-access'
  params: {
    keyVaultName: workloadKVName
    roleName: 'Key Vault Secrets User'
    managedIdentityPrincipalId: myNewAppMI.principalId
  }
  dependsOn: [
    workloadKV
  ]
}
```

### 1.4 Add output for the identity

```bicep
output myNewAppUAMIClientId string = myNewAppMI.clientId
output myNewAppUAMIId string = myNewAppMI.id
```

---

## Step 2: Add Output to output-opstool-cluster.bicep

Edit `dev-infrastructure/templates/output-opstool-cluster.bicep` to expose the UAMI for the pipeline:

```bicep
resource myNewAppUAMI 'Microsoft.ManagedIdentity/userAssignedIdentities@2024-11-30' existing = {
  name: 'my-new-app'
}

output myNewAppUAMIClientId string = myNewAppUAMI.properties.clientId
output myNewAppUAMIId string = myNewAppUAMI.id
```

---

## Step 3: Create Helm Chart

Create the directory structure for your app:

```
dev-infrastructure/ops-tools/my-new-app/
├── deploy/
│   ├── Chart.yaml
│   ├── values.yaml
│   ├── namespace.yaml
│   └── templates/
│       ├── deployment.yaml
│       ├── service.yaml
│       ├── serviceaccount.yaml
│       ├── servicemonitor.yaml           # If exposing Prometheus metrics
│       └── secretproviderclass.yaml      # If using Key Vault secrets
├── pipeline.yaml
├── Makefile                              
└── go.mod                                
```

### 3.1 Chart.yaml

```yaml
apiVersion: v2
name: my-new-app
description: My new operational tooling app
version: 0.1.0
appVersion: "1.0.0"
```

### 3.2 namespace.yaml

```yaml
apiVersion: v1
kind: Namespace
metadata:
  name: my-new-app
  labels:
    app: my-new-app
```

### 3.3 values.yaml

```yaml
# Image configuration
imageRegistry: "arohcpsvcdev.azurecr.io"
imageRepository: "my-new-app"
imageTag: "latest"

# Managed Identity Client ID
msiClientId: "__myNewAppUAMIClientId__"

# Secret Provider (if using Key Vault)
secretProvider:
  keyVault: "__keyVaultName__"
  msiClientId: "__myNewAppUAMIClientId__"
  tenantId: "64dc69e4-d083-49fc-9569-ebece1dd1408"  # Red Hat tenant

# App-specific configuration
# myConfig:
#   setting1: value1
```

### 3.4 templates/serviceaccount.yaml

```yaml
apiVersion: v1
kind: ServiceAccount
metadata:
  name: my-new-app
  namespace: {{ .Release.Namespace }}
  labels:
    app: my-new-app
  annotations:
    azure.workload.identity/client-id: "{{ .Values.msiClientId }}"
```

### 3.5 templates/deployment.yaml

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: my-new-app
  namespace: {{ .Release.Namespace }}
  labels:
    app: my-new-app
spec:
  replicas: 1
  selector:
    matchLabels:
      app: my-new-app
  template:
    metadata:
      labels:
        app: my-new-app
        azure.workload.identity/use: "true"
    spec:
      serviceAccountName: my-new-app
      # Schedule on infra nodes
      tolerations:
      - key: "infra"
        operator: "Equal"
        value: "true"
        effect: "NoSchedule"
      affinity:
        nodeAffinity:
          requiredDuringSchedulingIgnoredDuringExecution:
            nodeSelectorTerms:
            - matchExpressions:
              - key: "aro-hcp.azure.com/role"
                operator: "In"
                values:
                - "infra"
      containers:
      - name: my-new-app
        image: "{{ .Values.imageRegistry }}/{{ .Values.imageRepository }}:{{ .Values.imageTag }}"
        ports:
        - containerPort: 8080
          name: http
        # If using Key Vault secrets
        volumeMounts:
        - name: secrets
          mountPath: "/mnt/secrets"
          readOnly: true
        env:
        - name: AZURE_CLIENT_ID
          value: "{{ .Values.msiClientId }}"
      volumes:
      - name: secrets
        csi:
          driver: secrets-store.csi.k8s.io
          readOnly: true
          volumeAttributes:
            secretProviderClass: my-new-app-secretprovider
```

### 3.6 templates/service.yaml

```yaml
apiVersion: v1
kind: Service
metadata:
  name: my-new-app
  namespace: {{ .Release.Namespace }}
  labels:
    app: my-new-app
spec:
  type: ClusterIP
  selector:
    app: my-new-app
  ports:
  - port: 8080
    targetPort: 8080
    name: metrics
```

### 3.7 templates/servicemonitor.yaml (for Prometheus scraping)

```yaml
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  name: my-new-app
  namespace: {{ .Release.Namespace }}
  labels:
    app: my-new-app
    release: arohcp-monitor
spec:
  selector:
    matchLabels:
      app: my-new-app
  namespaceSelector:
    matchNames:
    - {{ .Release.Namespace }}
  endpoints:
  - port: metrics
    interval: 60s
    path: /metrics
```

### 3.8 templates/secretproviderclass.yaml (for Key Vault access)

```yaml
apiVersion: secrets-store.csi.x-k8s.io/v1
kind: SecretProviderClass
metadata:
  name: my-new-app-secretprovider
  namespace: {{ .Release.Namespace }}
spec:
  provider: azure
  parameters:
    clientID: "{{ .Values.secretProvider.msiClientId }}"
    tenantId: "{{ .Values.secretProvider.tenantId }}"
    keyvaultName: "{{ .Values.secretProvider.keyVault }}"
    objects: |
      array:
        - |
          objectName: my-secret-name
          objectType: secret
```

---

## Step 4: Create Pipeline Configuration

Create `dev-infrastructure/ops-tools/my-new-app/pipeline.yaml`:

```yaml
name: my-new-app
serviceGroup: Microsoft.Azure.ARO.HCP.Opstool.MyNewApp

steps:
- name: output
  type: ARM
  template: ../../templates/output-opstool-cluster.bicep
  parameters: ../../configurations/output-opstool-cluster.tmpl.bicepparam

- name: deploy
  type: Helm
  chart: deploy/
  namespace: my-new-app
  namespaceFile: deploy/namespace.yaml
  releaseName: my-new-app
  wait: true
  values:
    imageRegistry: "{{ .svc.acr.name }}.azurecr.io"
    imageRepository: "my-new-app"
    imageTag: "latest"
    msiClientId:
      fromStep:
        resourceGroup: opstool
        step: output
        name: myNewAppUAMIClientId
    secretProvider:
      keyVault:
        fromStep:
          resourceGroup: opstool
          step: output
          name: workloadKVName
      msiClientId:
        fromStep:
          resourceGroup: opstool
          step: output
          name: myNewAppUAMIClientId
      tenantId: "64dc69e4-d083-49fc-9569-ebece1dd1408"
    # Kusto placeholders (required by schema)
    kustoEndpoint:
      fromStep:
        resourceGroup: opstool
        step: output
        name: kustoUri
    kustoDatabase:
      fromStep:
        resourceGroup: opstool
        step: output
        name: kustoDatabase
    kustoTable:
      fromStep:
        resourceGroup: opstool
        step: output
        name: kustoTable
  dependsOn:
  - resourceGroup: opstool
    step: output
```

---

## Step 5: Add to Topology

Edit `topology-opstool.yaml` to add your app as a child of the Infra service group:

```yaml
entrypoints:
- identifier: 'Microsoft.Azure.ARO.HCP.Opstool.Infra'
  metadata:
    name: Opstool Infrastructure
services:
- serviceGroup: Microsoft.Azure.ARO.HCP.Opstool.Infra
  pipelinePath: dev-infrastructure/opstool-pipeline.yaml
  purpose: Deploy the opstool AKS cluster and Prometheus monitoring stack.
  children:
  - serviceGroup: Microsoft.Azure.ARO.HCP.Opstool.TenantQuota
    pipelinePath: dev-infrastructure/ops-tools/tenant-quota/pipeline.yaml
    purpose: Deploy the tenant-quota-collector.
  # Add your new app here
  - serviceGroup: Microsoft.Azure.ARO.HCP.Opstool.MyNewApp
    pipelinePath: dev-infrastructure/ops-tools/my-new-app/pipeline.yaml
    purpose: Deploy my-new-app.
```

---

## Step 6: Build and Push Container Image

If your app has custom code:

```bash
# Build the image
cd dev-infrastructure/ops-tools/my-new-app
podman build -t my-new-app:latest .

# Tag for ACR
podman tag my-new-app:latest arohcpsvcdev.azurecr.io/my-new-app:latest

# Login and push
az acr login --name arohcpsvcdev
podman push arohcpsvcdev.azurecr.io/my-new-app:latest
```

---

## Step 7: Add Secrets to Key Vault (if needed)

```bash
az keyvault secret set \
  --vault-name opstool-kv-usw3 \
  --name my-secret-name \
  --value "secret-value"
```

---

## Step 8: Add Alerting (Optional)

If your app exposes Prometheus metrics, you can add alerts that use the **shared Action Group** (`opstool-email-alerts`).

### 8.1 Create alerting.bicep for your app

Create `dev-infrastructure/ops-tools/my-new-app/alerting.bicep`:

```bicep
// Prometheus alert rules for my-new-app
// Uses the shared Action Group from the Infra pipeline

@description('Azure Monitor Workspace resource ID')
param azureMonitorWorkspaceId string

@description('Shared Action Group resource ID from Infra pipeline')
param sharedActionGroupId string

@description('Enable or disable alerting')
param alertingEnabled bool = true

resource myNewAppAlerts 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'my-new-app-alerts'
  location: resourceGroup().location
  properties: {
    enabled: alertingEnabled
    interval: 'PT1M'
    scopes: [
      azureMonitorWorkspaceId
    ]
    rules: [
      {
        alert: 'MyNewAppCritical'
        enabled: true
        expression: 'my_metric >= 95'
        for: 'PT5M'
        severity: 2
        labels: {
          severity: 'critical'
        }
        annotations: {
          summary: 'My app metric is critical'
          description: 'Value is {{ $value }}'
        }
        actions: [
          {
            actionGroupId: sharedActionGroupId  // Uses shared Action Group
          }
        ]
        resolveConfiguration: {
          autoResolved: true
          timeToResolve: 'PT10M'
        }
      }
    ]
  }
}

output alertRuleGroupId string = myNewAppAlerts.id
```

### 8.2 Create parameter file

Create `dev-infrastructure/configurations/my-new-app-alerting.tmpl.bicepparam`:

```bicep
using '../ops-tools/my-new-app/alerting.bicep'

param azureMonitorWorkspaceId = '__azureMonitorWorkspaceId__'
param sharedActionGroupId = '__sharedActionGroupId__'
param alertingEnabled = {{ .opstool.alerting.enabled }}
```

### 8.3 Add alerting step to your pipeline.yaml

```yaml
- name: alerting
  action: ARM
  template: alerting.bicep
  parameters: ../../configurations/my-new-app-alerting.tmpl.bicepparam
  deploymentLevel: ResourceGroup
  variables:
  - name: azureMonitorWorkspaceId
    input:
      resourceGroup: opstool
      step: output
      name: azureMonitorWorkspaceId
  - name: sharedActionGroupId
    input:
      resourceGroup: opstool
      step: output
      name: sharedActionGroupId
  dependsOn:
  - resourceGroup: opstool
    step: output
```

> **Note**: The shared Action Group is configured in `config-opstool.yaml` under `opstool.alerting.email`. All apps share the same email recipient.

---

## Step 9: Deploy

### Deploy Infrastructure First (if you modified Bicep)

```bash
./tooling/templatize/templatize pipeline run \
  --config-file="$(pwd)/config/config-opstool.yaml" \
  --topology-file="$(pwd)/topology-opstool.yaml" \
  --dev-settings-file="$(pwd)/tooling/templatize/settings.yaml" \
  --dev-environment opstool \
  --service-group Microsoft.Azure.ARO.HCP.Opstool.Infra
```

### Deploy Your App

```bash
./tooling/templatize/templatize pipeline run \
  --config-file="$(pwd)/config/config-opstool.yaml" \
  --topology-file="$(pwd)/topology-opstool.yaml" \
  --dev-settings-file="$(pwd)/tooling/templatize/settings.yaml" \
  --dev-environment opstool \
  --service-group Microsoft.Azure.ARO.HCP.Opstool.MyNewApp
```

---

## Step 10: Verify Deployment

```bash
# Check pods
kubectl get pods -n my-new-app

# Check logs
kubectl logs -n my-new-app deployment/my-new-app

# Check if Prometheus is scraping (if you added ServiceMonitor)
kubectl get servicemonitor -n my-new-app

# Port-forward to test locally
kubectl port-forward -n my-new-app svc/my-new-app 8080:8080
curl http://localhost:8080/metrics
```

---

## Checklist

- [ ] Added UAMI to `opstool-cluster.bicep`
- [ ] Added output to `output-opstool-cluster.bicep`
- [ ] Created Helm chart in `dev-infrastructure/ops-tools/my-new-app/`
- [ ] Created `pipeline.yaml`
- [ ] Added to `topology-opstool.yaml`
- [ ] Built and pushed container image
- [ ] Added secrets to Key Vault (if needed)
- [ ] Added alerting using shared Action Group (if applicable)
- [ ] Deployed infrastructure changes
- [ ] Deployed app
- [ ] Verified pod is running
- [ ] Verified metrics are being scraped (if applicable)
- [ ] Verified alerts are configured in Azure Portal (if applicable)

---

## Troubleshooting

### Pod stuck in ContainerCreating
- Check `kubectl describe pod -n my-new-app <pod-name>`
- Common issues: SecretProviderClass missing tenantId, Key Vault access denied

### Pod can't pull image
- Verify ACR is attached: `az aks check-acr --name opstool-usw3 --resource-group opstool-westus3 --acr arohcpsvcdev`
- Check image name and tag

### Workload Identity not working
- Verify ServiceAccount has `azure.workload.identity/client-id` annotation
- Verify Pod has `azure.workload.identity/use: "true"` label
- Check federation is configured in the UAMI

### Prometheus not scraping
- Verify ServiceMonitor has `release: arohcp-monitor` label
- Check `kubectl get servicemonitor -A` to confirm it exists
