# OADP and HCP Backup Infrastructure Architecture

## Table of Contents
- [Executive Summary](#executive-summary)
- [Overview](#overview)
- [Core Components](#core-components)
- [Architecture Layers](#architecture-layers)
- [Deployment Flow](#deployment-flow)
- [OLM Bundle to Helm Chart Conversion](#olm-bundle-to-helm-chart-conversion)
- [Storage and RBAC Infrastructure](#storage-and-rbac-infrastructure)
- [HyperShift OADP Plugin](#hypershift-oadp-plugin)
- [Configuration Management](#configuration-management)
- [Security Model](#security-model)
- [Open Questions and TODOs](#open-questions-and-todos)

---

## Executive Summary

The OADP (OpenShift API for Data Protection) and HCP (Hosted Control Plane) backup infrastructure provides automated backup capabilities for Hosted Control Planes in the ARO-HCP environment. The system deploys OADP operator and Velero components on management clusters to perform backups of HCP resources, including etcd PersistentVolumes, to Azure Blob Storage.

**Key Characteristics:**
- **Deployment Target**: Management clusters only (not service clusters)
- **Backup Scope**: Hosted Control Plane resources (control plane components, not customer workloads)
- **Storage**: One Azure Storage Account per management cluster, globally unique
- **Authentication**: Azure Workload Identity for keyless authentication
- **Platform**: Custom HyperShift OADP plugin for HCP-aware backup/restore operations

---

## Overview

### What is OADP?

OADP (OpenShift API for Data Protection) is an operator that wraps and manages [Velero](https://velero.io/), a CNCF backup and restore solution for Kubernetes. OADP provides OpenShift-specific enhancements and integrations on top of Velero.

### Purpose in ARO-HCP

In the ARO-HCP context, OADP is deployed to:
1. **Backup Hosted Control Planes**: Capture the state of HCP resources including:
   - HostedCluster and HostedControlPlane custom resources
   - NodePool resources
   - Control plane workloads (deployments, statefulsets, pods)
   - ETCD data via persistent volume backups
   - Platform-specific resources (Azure clusters, machines, etc.)
   - Kubernetes core resources (secrets, configmaps, services, RBAC)

2. **Store Backups in Azure**: Utilize Azure Blob Storage as the backup storage location

3. **Enable Future Restore Capabilities**: Foundation for disaster recovery (restore strategy not yet defined)

### Deployment Scope

- **Where**: Management clusters (one OADP deployment per management cluster)
- **Not Deployed**: Service clusters, customer HCP clusters
- **Scale**: Multiple management clusters per region, each with its own OADP instance

---

## Core Components

### 1. OADP Operator

**Source**: [github.com/openshift/oadp-operator](https://github.com/openshift/oadp-operator)

**Description**: Kubernetes operator that manages the lifecycle of Velero and related backup/restore components.

**Responsibilities**:
- Deploy and configure Velero
- Manage DataProtectionApplication (DPA) custom resources
- Configure backup storage locations (BSL) and volume snapshot locations (VSL)
- Deploy and manage Velero plugins (Azure, CSI, HyperShift)
- Manage node agents (Kopia) for file-system backups

**Deployment Details**:
- Namespace: `openshift-adp`
- Deployment: `openshift-adp-controller-manager`
- Watches: `openshift-adp` namespace (configured via `WATCH_NAMESPACE` env var)

### 2. Velero

**Description**: Upstream CNCF project for Kubernetes backup and restore.

**Key Features Used**:
- Backup scheduling and execution
- Resource filtering and selection
- Plugin architecture for cloud provider integration
- Volume snapshot management
- CSI snapshot integration

**Deployment Details**:
- Managed by OADP operator
- Configured via DataProtectionApplication CR
- Runs as deployment in `openshift-adp` namespace

### 3. HyperShift OADP Plugin

**Source**: [github.com/openshift/hypershift-oadp-plugin](https://github.com/openshift/hypershift-oadp-plugin)

**Description**: Custom Velero plugin that provides HCP-aware backup and restore capabilities.

**Key Capabilities**:
- **Platform-Specific Logic**: Handles different infrastructure platforms (AWS, Azure, IBM Cloud, KubeVirt, OpenStack, Agent, None)
- **DataMover Integration**: Manages VolumeSnapshotContent, VolumeSnapshot, and DataUpload resources
- **State Tracking**: Monitors backup progress through multiple phases
- **Etcd PV Handling**: Special handling for etcd persistent volume backups
- **Pause/Resume**: Can pause HostedCluster and NodePool reconciliation during backup

**Architecture**:
```
BackupPlugin (pkg/core/backup.go)
  ├── Validates HCP resources
  ├── Triggers platform-specific backup logic
  └── Monitors DataMover operations

RestorePlugin (pkg/core/restore.go)
  ├── Restores HCP resources
  └── Handles platform-specific restore logic
```

**Platform Support Matrix**:
| Platform | PV Required | DataUpload Required | Special Handling |
|----------|-------------|---------------------|------------------|
| AWS | ✅ | ✅ | Standard DataMover |
| Azure | ✅ | ❌ | Azure-specific (no DataUpload) |
| IBM Cloud | ✅ | ✅ | Standard DataMover |
| KubeVirt | ✅ | ✅ | Standard DataMover |
| OpenStack | ✅ | ✅ | Standard DataMover |
| Agent | ✅ | ✅ | Standard DataMover |
| None | ✅ | ✅ | Standard DataMover |

**TODO**: Document specific HyperShift-aware functionality this plugin provides beyond standard Velero. Research needed on:
- How it handles HCP namespace relationships
- Special treatment of etcd volumes
- Platform-specific backup strategies
- Migration support capabilities

### 4. Node Agent (Kopia)

**Description**: DaemonSet that runs on management cluster nodes to perform file-system level backups of pod volumes.

**Purpose**:
- Backup etcd PersistentVolumes using file-system copy
- Alternative to CSI snapshots for certain volume types
- Uses Kopia as the backup tool (modern replacement for Restic)

**Configuration** (from `oadp/deploy/templates/hcp.dpa.yaml`):
```yaml
nodeAgent:
  enable: true
  uploaderType: kopia
  podConfig:
    labels:
      azure.workload.identity/use: "true"
```

### 5. Velero Plugins

**Azure Plugin**: Handles Azure-specific backup storage operations
- Source: `quay.io/konveyor/velero-plugin-for-microsoft-azure`
- Provides Azure Blob Storage integration
- Manages Azure disk snapshots

**CSI Plugin**: Handles CSI volume snapshots
- Enables snapshot support for CSI-based storage
- Works with Azure Disk CSI driver

**OpenShift Plugin**: OpenShift-specific resource handlers
- Handles OpenShift-specific resources
- Provides OpenShift API integration

**HyperShift Plugin**: HCP-aware backup/restore (described in detail above)
- Source: `quay.io/redhat-user-workloads/ocp-art-tenant/oadp-hypershift-oadp-plugin-main`

---

## Architecture Layers

### Layer 1: Azure Infrastructure

**Components**:
- **Storage Account**: Stores backup data
  - Naming: `oadp{environment}{regionShort}{stamp}` (e.g., `oadppersuksouth01`)
  - SKU: Standard_ZRS (Zone-Redundant Storage)
  - Access Tier: Cool (optimized for infrequent access)
  - Encryption: Microsoft-managed keys
  - Public Access: Disabled
  - Container: `backups` (default)

- **Managed Identities**: Two separate identities for security isolation
  - `velero`: Used by Velero pod
  - `openshift-adp-controller-manager`: Used by OADP controller

**Security Configuration**:
```
Storage Account
  ├── Network: Allow Azure Services
  ├── TLS: Minimum 1.2
  └── Public Blob Access: Disabled
```

**Location**: `dev-infrastructure/modules/hcp-backups/`
- `storage.bicep`: Storage account and container creation
- `storage-rbac.bicep`: RBAC role assignments

### Layer 2: Kubernetes/AKS Infrastructure

**Management Cluster (AKS)**:
- Hosts OADP operator, Velero, and all backup infrastructure
- Federated identity configured for Azure Workload Identity
- CSI drivers installed for volume snapshots

**Namespaces**:
- `openshift-adp`: All OADP/Velero components
- `ocm-{cluster-id}`: Hosted Control Plane namespaces (backup targets)

### Layer 3: OADP Operator Layer

**Helm Charts**:
1. **oadp-operator** (`oadp/oadp-operator/`): Base operator deployment
   - ClusterServiceVersion manifests (converted from OLM)
   - CRDs (DataProtectionApplication, CloudStorage, etc.)
   - RBAC (ClusterRoles, ServiceAccounts)
   - Operator Deployment

2. **hcp-backups** (`oadp/deploy/`): HCP-specific configuration
   - DataProtectionApplication CR
   - CloudStorage CR
   - VolumeSnapshotClass
   - Secrets for Azure credentials

**Custom Resources**:
```yaml
# DataProtectionApplication (DPA)
apiVersion: oadp.openshift.io/v1alpha1
kind: DataProtectionApplication
spec:
  configuration:
    velero:
      defaultPlugins: [azure, csi]
      customPlugins:
        - name: hypershift-oadp-plugin
          image: <hypershift-plugin-image>
      featureFlags: [EnableCSI]
    nodeAgent:
      enable: true
      uploaderType: kopia
  backupLocations:
    - velero:
        provider: azure
        objectStorage:
          bucket: backups
          prefix: velero
        credential:
          name: cloud-credentials-azure
  snapshotLocations:
    - velero:
        provider: azure
```

### Layer 4: Backup Execution Layer

**Backup Custom Resources**:
```yaml
apiVersion: velero.io/v1
kind: Backup
metadata:
  name: hcp-backup
  namespace: openshift-adp
spec:
  includedNamespaces:
    - ocm-{cluster-id}
    - ocm-{cluster-id}-{cluster-name}
  includedResources:
    - hostedcluster
    - hostedcontrolplane
    - nodepool
    - secrets
    - configmaps
    - services
    - deployments
    - statefulsets
    - pvc
    - pv
    # ... (see oadp/backup.yaml for full list)
  storageLocation: azure-hypershift-dpa-1
  snapshotVolumes: true
  snapshotMoveData: true
  datamover: "velero"
  ttl: 720h0m0s
```

**Backup Flow**:
1. Backup CR created in `openshift-adp` namespace
2. Velero controller processes backup
3. HyperShift plugin intercepts HCP resources
4. Node agent (Kopia) backs up etcd PVs
5. Azure plugin uploads data to blob storage
6. CSI plugin creates volume snapshots
7. DataUpload resources created for snapshot data movement

---

## Deployment Flow

### Phase 1: Infrastructure Provisioning

**Pipeline**: `dev-infrastructure/mgmt-pipeline.yaml`

**Bicep Deployment** (`dev-infrastructure/templates/mgmt-infra.bicep`):
1. Create HCP Backups Storage Account
   ```bicep
   module hcpBackupsStorage '../modules/hcp-backups/storage.bicep' = {
     name: 'hcp-backups-storage'
     params: {
       storageAccountName: hcpBackupsStorageAccountName
       location: location
       containerName: hcpBackupsStorageAccountContainerName
     }
   }
   ```

**Management Cluster Configuration** (`dev-infrastructure/templates/mgmt-cluster.bicep`):
1. Create Managed Identities
   - Velero identity
   - OADP controller identity

2. Configure Federated Credentials for Azure Workload Identity
   ```bicep
   velero_wi: {
     uamiName: 'velero'
     namespace: 'openshift-adp'
     serviceAccountName: 'velero'
   }
   oadp_wi: {
     uamiName: 'openshift-adp-controller-manager'
     namespace: 'openshift-adp'
     serviceAccountName: 'openshift-adp-controller-manager'
   }
   ```

3. Assign RBAC Roles (`dev-infrastructure/modules/hcp-backups/storage-rbac.bicep`)

   **Velero Identity Roles**:
   - Storage Blob Data Contributor (read/write/delete blobs)
   - Storage Account Key Operator (list/regenerate keys)
   - Reader (read storage account properties)

   **OADP Controller Identity Roles**:
   - Storage Blob Data Contributor
   - Reader

### Phase 2: Image Mirroring

**Pipeline**: `oadp/pipeline.yaml` → `resourceGroups[name=global]`

**Images Mirrored to Service ACR**:
1. OADP Operator: `quay.io/konveyor/oadp-operator`
2. Velero: `quay.io/konveyor/velero`
3. OpenShift Plugin: `quay.io/konveyor/openshift-velero-plugin`
4. Azure Plugin: `quay.io/konveyor/velero-plugin-for-microsoft-azure`
5. HyperShift Plugin: `quay.io/redhat-user-workloads/.../oadp-hypershift-oadp-plugin-main`

**Process**:
- Source images pulled from upstream registries
- Pushed to `{svc-acr-name}.azurecr.io`
- Digests captured for reproducible deployments

### Phase 3: Operator Deployment

**Pipeline**: `oadp/pipeline.yaml` → `resourceGroups[name=management].steps[name=deploy-oadp-operator]`

**Helm Deployment**:
```yaml
action: Helm
releaseName: oadp-operator
releaseNamespace: openshift-adp
chartDir: ./oadp-operator
valuesFile: ./oadp-operator/values.yaml
inputVariables:
  veleroMsiClientId: <from bicep output>
  oadpControllerMsiClientId: <from bicep output>
  tenantId: <from bicep output>
  subscriptionId: <from bicep output>
```

**Key Configurations Applied**:
1. Azure Workload Identity annotations on ServiceAccounts
2. Azure Workload Identity labels on Pod templates
3. `WATCH_NAMESPACE` set to `{{ .Release.Namespace }}`
4. Image references templated with registry and digest

### Phase 4: Backup Configuration Deployment

**Pipeline**: `oadp/pipeline.yaml` → `resourceGroups[name=management].steps[name=deploy-backup]`

**Resources Deployed**:
1. **DataProtectionApplication** (`oadp/deploy/templates/hcp.dpa.yaml`)
   - Configures Velero with Azure BSL/VSL
   - Enables plugins (azure, csi, hypershift)
   - Configures node agent with Kopia

2. **CloudStorage** (`oadp/deploy/templates/cloudstorage.yaml`)
   - Azure-specific storage configuration
   - References managed identity

3. **VolumeSnapshotClass** (`oadp/deploy/templates/volumesnapshotclass.yaml`)
   - CSI snapshot configuration
   - Azure Disk CSI driver integration

4. **Secrets** (`oadp/deploy/templates/dpa.secret.yaml`)
   - Azure credentials for Velero
   - Subscription ID, Tenant ID, Resource Group

---

## OLM Bundle to Helm Chart Conversion

### Why Conversion is Needed

The OADP operator is distributed as an **OLM (Operator Lifecycle Manager) bundle**, designed for OpenShift's operator marketplace. However, ARO-HCP management clusters are **AKS-based** (not OpenShift), so OLM is not available. The solution is to convert the OLM bundle into a standard Helm chart.

### The olm-bundle-repkg Tool

**Location**: `tooling/olm-bundle-repkg/`

**Purpose**: Automated conversion of OLM bundles to Helm charts with configurable transformations.

**Architecture**:
```
┌─────────────────────────────────────────────────────────────────┐
│                    OLM Bundle Input                             │
│  • Container Image (tar.gz)  OR  • Manifest Directory          │
└────────────────────┬────────────────────────────────────────────┘
                     │
                     ▼
┌─────────────────────────────────────────────────────────────────┐
│                  Configuration File                             │
│              (olm-bundle-repkg-config.yaml)                     │
│  • Chart metadata                                               │
│  • Image parameterization rules                                 │
│  • Validation requirements                                      │
│  • Manifest overrides                                           │
│  • Annotation cleanup patterns                                  │
└────────────────────┬────────────────────────────────────────────┘
                     │
                     ▼
┌─────────────────────────────────────────────────────────────────┐
│               6-Phase Pipeline Process                          │
│                                                                 │
│  Phase 1: Configuration                                         │
│    └─ Load config, apply defaults, validate                    │
│                                                                 │
│  Phase 2: Input Detection                                       │
│    └─ Determine if input is image or directory                 │
│                                                                 │
│  Phase 3: Extraction                                            │
│    ├─ Container: crane + rukpak/convert                        │
│    └─ Directory: file walk + rukpak/convert                    │
│                                                                 │
│  Phase 4: Validation                                            │
│    └─ SanityCheck: required resources, deployments, env vars   │
│                                                                 │
│  Phase 5: Customization                                         │
│    ├─ Namespace templating                                     │
│    ├─ Image parameterization                                   │
│    ├─ Annotation cleanup                                       │
│    └─ Manifest overrides (Azure WI, WATCH_NAMESPACE)           │
│                                                                 │
│  Phase 6: Chart Generation                                      │
│    ├─ Load scaffold templates (optional)                       │
│    ├─ Create Chart.yaml                                        │
│    ├─ Generate values.yaml                                     │
│    └─ Organize templates/ and crds/                            │
└────────────────────┬────────────────────────────────────────────┘
                     │
                     ▼
┌─────────────────────────────────────────────────────────────────┐
│                  Output Helm Chart                              │
│  oadp-operator/                                                 │
│  ├── Chart.yaml                                                 │
│  ├── values.yaml                                                │
│  ├── templates/                                                 │
│  │   ├── deployment.yaml                                       │
│  │   ├── serviceaccount.yaml                                   │
│  │   ├── clusterrole.yaml                                      │
│  │   └── ...                                                   │
│  └── crds/                                                      │
│      └── ...                                                    │
└─────────────────────────────────────────────────────────────────┘
```

### Key Transformation Steps

#### 1. Image Parameterization

**Input** (OLM Bundle):
```yaml
containers:
  - name: manager
    image: quay.io/konveyor/oadp-operator@sha256:2c671a...
env:
  - name: RELATED_IMAGE_VELERO
    value: quay.io/konveyor/velero@sha256:abc123...
```

**Output** (Helm Template):
```yaml
containers:
  - name: manager
    image: '{{ .Values.imageRegistry }}/konveyor/oadp-operator@{{ .Values.oadpOperatorDigest }}'
env:
  - name: RELATED_IMAGE_VELERO
    value: '{{ .Values.imageRegistry }}/konveyor/velero@{{ .Values.velero.digest }}'
```

**Configuration** (`oadp/olm-bundle-repkg-config.yaml`):
```yaml
imageRegistryParam: imageRegistry
operandImageEnvPrefixes:
  - RELATED_IMAGE_
manifestOverrides:
  - selector:
      kind: Deployment
      name: openshift-adp-controller-manager
    operations:
      - op: replace
        path: spec.template.spec.containers[name=manager].image
        value: '{{ .Values.imageRegistry }}/konveyor/oadp-operator@{{ .Values.oadpOperatorDigest }}'
```

#### 2. Namespace Templating

**Input**:
```yaml
metadata:
  namespace: openshift-adp
```

**Output**:
```yaml
metadata:
  namespace: '{{ .Release.Namespace }}'
```

**Benefit**: Helm can install to any namespace, not hardcoded to `openshift-adp`.

#### 3. Azure Workload Identity Injection

**Configuration**:
```yaml
manifestOverrides:
  - selector:
      kind: ServiceAccount
      name: velero
    operations:
      - op: add
        path: metadata.annotations
        merge: true
        value:
          azure.workload.identity/client-id: '{{ .Values.velero.workloadIdentity.clientId }}'

  - selector:
      kind: Deployment
      name: openshift-adp-controller-manager
    operations:
      - op: add
        path: spec.template.metadata.labels
        merge: true
        value:
          azure.workload.identity/use: "true"
```

**Result**: ServiceAccounts and Pods are annotated/labeled for Azure Workload Identity.

#### 4. WATCH_NAMESPACE Fix

**OLM Original**:
```yaml
env:
  - name: WATCH_NAMESPACE
    valueFrom:
      fieldRef:
        fieldPath: metadata.annotations['olm.targetNamespaces']
```

**Helm Template**:
```yaml
env:
  - name: WATCH_NAMESPACE
    value: '{{ .Release.Namespace }}'
```

**Why**: OLM injects `olm.targetNamespaces` annotation, but Helm doesn't. Need static template.

#### 5. Annotation Cleanup

**Removed Annotations**:
- `openshift.io/*`
- `operatorframework.io/*`
- `olm.*`
- `alm-examples`
- `operators.coreos.com/*`

**Why**: These annotations are OLM-specific and not needed for standalone Helm deployment.

### Scaffold System

**Purpose**: Add custom resources not in the OLM bundle.

**Example** (`oadp/oadp-operator-scaffold/`):
- `templates/cluster.infrastructure.yaml`: Fake Infrastructure object for non-OpenShift
- `templates/azure-backup-storage.secret.yaml`: Azure credentials
- `templates/acrpullbinding.yaml`: ACR pull secret binding

**Usage**:
```bash
go run ./tooling/olm-bundle-repkg \
  -c oadp/olm-bundle-repkg-config.yaml \
  -b file://../oadp-operator/bundle/manifests \
  -s oadp/oadp-operator-scaffold \
  -o oadp/oadp-operator
```

### Regeneration Process

**Makefile Target** (`oadp/Makefile`):
```makefile
generate-chart:
	go run ../tooling/olm-bundle-repkg \
		-c olm-bundle-repkg-config.yaml \
		-b file://$(OADP_BUNDLE_PATH) \
		-s oadp-operator-scaffold \
		-o . \
		-l https://github.com/openshift/oadp-operator/tree/master/bundle
	$(MAKE) -C ../ yamlfmt
```

**When to Regenerate**:
- OADP operator version update
- New environment variables needed
- Additional plugins added
- Configuration changes

**Manual Files** (not regenerated):
- `oadp-operator/crds/0000_03_config-operator_01_securitycontextconstraints.crd.yaml`
- `oadp-operator/crds/0000_10_config-operator_01_infrastructures-Default.crd.yaml`
- `oadp-operator/crds/routes.crd.yaml`

These are OpenShift CRDs fetched from `github.com/openshift/api`, required because OADP expects them but they don't exist on AKS.

---

## Storage and RBAC Infrastructure

### Storage Account Design

**Naming Convention**:
```
oadp{environment}{regionShort}{stamp}

Examples:
- oadppersuksouth01  (personal dev, UK South, stamp 01)
- oadpdevwestus302   (dev environment, West US 3, stamp 02)
```

**Configuration** (from `config/config.yaml`):
```yaml
mgmt:
  hcpBackups:
    storageAccountName: "oadp{{ .ctx.environment }}{{ .ctx.regionShort }}{{ .ctx.stamp }}"
    storageAccountContainerName: "backups"
```

**Uniqueness**: Storage account names must be globally unique across Azure, hence the environment/region/stamp suffix.

**Storage Account Properties**:
```bicep
resource hcpBackupsStorageAccount 'Microsoft.Storage/storageAccounts@2022-09-01' = {
  name: storageAccountName
  location: location
  kind: 'StorageV2'
  sku: {
    name: 'Standard_ZRS'  // Zone-Redundant Storage
  }
  properties: {
    accessTier: 'Cool'  // Optimized for infrequent access
    minimumTlsVersion: 'TLS1_2'
    allowBlobPublicAccess: false
    supportsHttpsTrafficOnly: true
    encryption: {
      services: {
        blob: { enabled: true }
        file: { enabled: true }
      }
      keySource: 'Microsoft.Storage'  // Microsoft-managed keys
    }
    networkAcls: {
      bypass: 'AzureServices'
      defaultAction: 'Allow'
    }
  }
}
```

### RBAC Model

**Two Managed Identities**:

1. **Velero Identity** (`velero`)
   - **Purpose**: Used by Velero pod to store/retrieve backups
   - **Roles**:
     - `Storage Blob Data Contributor` (ba92f5b4-2d11-453d-a403-e96b0029c9fe)
       - Read, write, delete blob containers and data
     - `Storage Account Key Operator Service Role` (81a9662b-bebf-436f-a333-f67b29880f12)
       - List and regenerate storage account keys
     - `Reader` (acdd72a7-3385-48ef-bd42-f606fba81ae7)
       - Read storage account properties

2. **OADP Controller Identity** (`openshift-adp-controller-manager`)
   - **Purpose**: Used by OADP operator for cloud storage validation
   - **Roles**:
     - `Storage Blob Data Contributor`
     - `Reader`
   - **Note**: Does NOT have Storage Account Key Operator role (less privileged)

**Why Separate Identities?**
- **Security Isolation**: Principle of least privilege
- **Different Access Patterns**: Controller validates, Velero performs actual backup/restore
- **Auditability**: Can track which component performed which action

**Federated Identity Configuration**:
```bicep
velero_wi: {
  uamiName: 'velero'
  namespace: 'openshift-adp'
  serviceAccountName: 'velero'
}
oadp_wi: {
  uamiName: 'openshift-adp-controller-manager'
  namespace: 'openshift-adp'
  serviceAccountName: 'openshift-adp-controller-manager'
}
```

This binds Kubernetes ServiceAccounts to Azure Managed Identities via Azure Workload Identity.

---

## HyperShift OADP Plugin

### Plugin Architecture

**Source Code**: `/Users/tschneid/devel/hypershift-oadp-plugin`

**Plugin Type**: Velero Backup/Restore Item Action Plugin

**Entry Point** (`main.go`):
```go
framework.NewServer().
  RegisterBackupItemAction("hypershift-oadp-plugin/backup-item-action", newHCPBackupPlugin).
  RegisterRestoreItemAction("hypershift-oadp-plugin/restore-item-action", newHCPRestorePlugin).
  Serve()
```

### Backup Plugin

**File**: `pkg/core/backup.go`

**Functionality**:
1. **Resource Filtering**: Applies to HCP-specific resources:
   ```go
   IncludedResources: [
     // Common
     hostedclusters, hostedcontrolplanes, nodepools,
     secrets, configmaps, services, deployments, statefulsets,
     persistentvolumeclaims, persistentvolumes,

     // Platform-specific
     awsclusters, awsmachines, awsmachinetemplates,
     azureclusters, azuremachines, azuremachinetemplates,
     ibmpowervsclusters, ibmpowersvsmachines,
     kubevirtclusters, kubevirtmachines,
     openstackclusters, openstackmachines,
     agents,
   ]
   ```

2. **HCP Validation**: Checks if backup includes HostedControlPlane resources

3. **DataMover Management**: Platform-specific data movement strategies
   - **Azure**: VolumeSnapshotContent → VolumeSnapshot (no DataUpload)
   - **Other Platforms**: VolumeSnapshotContent → VolumeSnapshot → DataUpload

4. **State Tracking**: Boolean flags for progress monitoring
   - `pvBackupStarted`: PersistentVolume backup initiated
   - `pvBackupFinished`: PersistentVolume backup complete
   - `duStarted`: DataUpload initiated
   - `duFinished`: DataUpload complete

5. **Pause/Resume**: Can pause HCP reconciliation during backup
   - Sets `hostedcluster.hypershift.openshift.io/pausedUntil` annotation
   - Sets `nodepool.hypershift.openshift.io/pausedUntil` annotation

### DataMover Flow

**Phase 1: VolumeSnapshotContent Reconciliation**
```
1. Check if PV backup already finished → return true
2. If not started:
   - List VolumeSnapshotContent resources
   - Check if ReadyToUse = true
   - Set pvBackupStarted = true
   - Set pvBackupFinished = true
   - Return true
3. If started but not finished:
   - Wait for VolumeSnapshotContent ReadyToUse
   - Set pvBackupFinished = true when ready
```

**Phase 2: VolumeSnapshot Reconciliation**
```
1. Check if PV backup finished → proceed
2. List VolumeSnapshot resources
3. Check if ReadyToUse = true
4. Azure: Done (no DataUpload phase)
   Other: Proceed to DataUpload
```

**Phase 3: DataUpload Reconciliation** (not for Azure)
```
1. Check if DU already finished → return true
2. If not started:
   - List DataUpload resources
   - Check if Phase = Completed
   - Set duStarted = true
   - Set duFinished = true
3. If started but not finished:
   - Wait for DataUpload completion
   - Timeout after configured duration
```

**Timeout Configuration**:
```yaml
# Plugin ConfigMap
dataUploadTimeout: "15"      # minutes
dataUploadCheckPace: "30"    # seconds
```

### Restore Plugin

**File**: `pkg/core/restore.go`

**Functionality**:
1. **Validation**: Ensures restore is for a HyperShift backup
2. **Namespace Handling**: Maps backup namespaces to restore namespaces
3. **Resource Filtering**: Same resource types as backup
4. **Platform-Specific Restore**: Handles platform differences

**Restore Options**:
```go
type RestoreOptions struct {
  migration           bool   // Restore for migration purposes
  readoptNodes        bool   // Reprovision nodes during restore
  managedServices     bool   // For ROSA, ARO, etc.
  awsRegenPrivateLink bool   // Regenerate AWS PrivateLink
}
```

**TODO**: Document restore strategy once defined. Questions:
- Disaster recovery scenarios?
- Point-in-time recovery granularity?
- Cross-region restore support?
- Cross-cluster migration?

### Platform-Specific Logic

**Azure Differences**:
- **No DataUpload**: Azure Disk snapshots remain in Azure, no need for data movement
- **VolumeSnapshot Only**: Backup completes after VolumeSnapshot phase
- **Faster Backups**: Skip DataUpload wait time

**Other Platforms**:
- **Full DataMover Flow**: VolumeSnapshotContent → VolumeSnapshot → DataUpload
- **Data Movement**: Snapshot data uploaded to object storage (Azure Blob)
- **Longer Backups**: Wait for DataUpload completion

---

## Configuration Management

### Configuration Hierarchy

```
config/config.yaml (base)
  └─ clouds.dev.environments.{env}
       └─ regions.{region}
            ├── mgmt.hcpBackups.storageAccountName
            └── mgmt.hcpBackups.storageAccountContainerName

oadp/oadp-operator/values.yaml (operator chart)
  ├── velero.workloadIdentity.clientId
  ├── oadpControllerManager.workloadIdentity.clientId
  ├── imageRegistry
  └── *Digest parameters

oadp/deploy/values.yaml (hcp-backups chart)
  ├── azureCloud
  ├── tenantId
  ├── subscriptionId
  ├── clientId (velero)
  ├── resourceGroup
  ├── bucket
  ├── storageAccount
  ├── imageRegistry
  └── hypershiftPluginDigest
```

### Templating System

**Templatize Tool**: `tooling/templatize/`

**Process**:
1. Load `config/config.yaml` and environment-specific overlays
2. Render `.tmpl.bicepparam` files with Go templates
3. Render pipeline YAML files
4. Render Helm values files

**Example Template** (`dev-infrastructure/configurations/mgmt-infra.tmpl.bicepparam`):
```bicep
param hcpBackupsStorageAccountName = '{{ .mgmt.hcpBackups.storageAccountName }}'
param hcpBackupsStorageAccountContainerName = '{{ .mgmt.hcpBackups.storageAccountContainerName }}'
```

**Rendered Output** (for `DEPLOY_ENV=pers`, `REGION=uksouth`):
```bicep
param hcpBackupsStorageAccountName = 'oadppersuksouth01'
param hcpBackupsStorageAccountContainerName = 'backups'
```

### Image Configuration

**Base Config** (`config/config.yaml`):
```yaml
oadp:
  operatorImage:
    registry: quay.io
    repository: konveyor/oadp-operator
    digest: sha256:2c671a7043f3989937f30b9ac3d881a1cd18ba6e0063fc7d3fd760c62efc2409
  veleroImage:
    registry: quay.io
    repository: konveyor/velero
    digest: sha256:abc123...
  azurePluginImage:
    registry: quay.io
    repository: konveyor/velero-plugin-for-microsoft-azure
    digest: sha256:def456...
  hypershiftPluginImage:
    registry: quay.io
    repository: redhat-user-workloads/ocp-art-tenant/oadp-hypershift-oadp-plugin-main
    digest: sha256:adb840bf3890b4904a8cdda1a74c82cf8d96c52eba9944ac10e795335d6fd450
```

**Pipeline Configuration** (`oadp/pipeline.yaml`):
```yaml
- name: mirror-operator-image
  action: ImageMirror
  targetACR:
    configRef: 'acr.svc.name'
  sourceRegistry:
    configRef: oadp.operatorImage.registry
  repository:
    configRef: oadp.operatorImage.repository
  digest:
    configRef: oadp.operatorImage.digest
```

**Result**: All images mirrored to service ACR with same digest, referenced in Helm values.

---

## Security Model

### Authentication Flow (Azure Workload Identity)

```
┌────────────────────────────────────────────────────────────────┐
│                     Pod (Velero)                               │
│  ┌──────────────────────────────────────────────────────────┐ │
│  │  ServiceAccount: velero                                  │ │
│  │  Namespace: openshift-adp                                │ │
│  │  Annotations:                                            │ │
│  │    azure.workload.identity/client-id: {velero-mi-id}    │ │
│  │                                                          │ │
│  │  Pod Labels:                                             │ │
│  │    azure.workload.identity/use: "true"                  │ │
│  └────────────────────┬─────────────────────────────────────┘ │
└─────────────────────────┼─────────────────────────────────────┘
                          │
                          │ 1. Azure Workload Identity Webhook
                          │    injects projected SA token
                          ▼
┌─────────────────────────────────────────────────────────────────┐
│                  Federated Identity Credential                  │
│  ┌───────────────────────────────────────────────────────────┐ │
│  │  Subject: system:serviceaccount:openshift-adp:velero     │ │
│  │  Issuer: {AKS OIDC Issuer URL}                           │ │
│  │  Audience: api://AzureADTokenExchange                    │ │
│  └────────────────────┬─────────────────────────────────────┘ │
└─────────────────────────┼─────────────────────────────────────┘
                          │
                          │ 2. Token exchange
                          │    (K8s token → Azure AD token)
                          ▼
┌─────────────────────────────────────────────────────────────────┐
│              Azure Managed Identity (Velero)                    │
│  ┌───────────────────────────────────────────────────────────┐ │
│  │  Name: velero                                            │ │
│  │  Client ID: {guid}                                       │ │
│  │  Principal ID: {guid}                                    │ │
│  └────────────────────┬─────────────────────────────────────┘ │
└─────────────────────────┼─────────────────────────────────────┘
                          │
                          │ 3. RBAC evaluation
                          ▼
┌─────────────────────────────────────────────────────────────────┐
│                  Storage Account RBAC                           │
│  ┌───────────────────────────────────────────────────────────┐ │
│  │  Storage Blob Data Contributor                           │ │
│  │  Storage Account Key Operator                            │ │
│  │  Reader                                                  │ │
│  └────────────────────┬─────────────────────────────────────┘ │
└─────────────────────────┼─────────────────────────────────────┘
                          │
                          │ 4. Access granted
                          ▼
┌─────────────────────────────────────────────────────────────────┐
│            HCP Backups Storage Account                          │
│                    (Azure Blob Storage)                         │
└─────────────────────────────────────────────────────────────────┘
```

### Security Features

1. **No Stored Secrets**: Azure Workload Identity eliminates need for stored credentials
2. **Short-lived Tokens**: Tokens automatically rotated by Azure AD
3. **Scoped Access**: RBAC roles limit access to specific storage account
4. **Least Privilege**: OADP controller has fewer permissions than Velero
5. **TLS Encryption**: In-transit encryption enforced (minimum TLS 1.2)
6. **At-Rest Encryption**: Blob data encrypted with Microsoft-managed keys
7. **No Public Access**: `allowBlobPublicAccess: false`

### Network Security

**Storage Account**:
- Default Action: Allow
- Bypass: AzureServices
- HTTPS Only: true

**Management Cluster**:
- Private AKS cluster options available
- Network Security Groups (NSGs) for pod network isolation
- Azure Policy enforcement

---

## Open Questions and TODOs

### Restore Strategy (Not Yet Defined)

**Questions**:
1. What are the restore scenarios?
   - Disaster recovery (full HCP restore)?
   - Point-in-time recovery?
   - Cross-region restore?
   - Cross-management-cluster migration?

2. What is the restore procedure?
   - Manual Velero Restore CR creation?
   - Automated restore orchestration?
   - Pre-restore validation steps?

3. How are conflicts handled?
   - Existing resources with same names?
   - Different Azure subscriptions/regions?
   - Identity and RBAC mapping?

4. What is the RTO/RPO target?
   - How long should restore take?
   - How often should backups run?
   - How long to retain backups?

### HyperShift Plugin Deep Dive

**TODO**: Explore and document the following:

1. **Namespace Relationships**:
   - How does the plugin handle the relationship between:
     - `ocm-{cluster-id}` (control plane namespace)
     - `ocm-{cluster-id}-{cluster-name}` (nodepool namespace)?
   - Are both namespaces backed up together?
   - How are cross-namespace references maintained?

2. **Etcd Volume Handling**:
   - What special treatment does etcd get?
   - Is there a specific PVC/PV naming convention?
   - Are etcd backups consistent/point-in-time?
   - How is quorum maintained during backup?

3. **Platform-Specific Strategies**:
   - What exactly differs between AWS/Azure/KubeVirt/etc.?
   - Beyond DataUpload, what else is platform-specific?
   - Are there platform-specific CRDs backed up?

4. **Migration Support**:
   - What does `migration: true` flag enable?
   - Cross-platform migration support?
   - What resources need transformation during migration?

5. **Pause/Resume Behavior**:
   - When is pause triggered?
   - How long are HCPs paused?
   - What reconciliation is prevented during pause?
   - Impact on running workloads in the HCP?

### Backup Scheduling

**Current State**: Manual backup creation via YAML manifest

**Questions**:
1. Should backups be scheduled automatically?
2. What frequency (hourly, daily, weekly)?
3. Retention policy (how many backups to keep)?
4. Incremental vs. full backups?

**Velero Capability**: Supports scheduled backups via `Schedule` CRD
```yaml
apiVersion: velero.io/v1
kind: Schedule
metadata:
  name: hcp-daily-backup
spec:
  schedule: "0 1 * * *"  # Daily at 1 AM
  template:
    # ... backup spec
```

### Monitoring and Alerting

**Current State**: No documented monitoring

**TODO**:
1. What metrics should be monitored?
   - Backup success/failure rate
   - Backup duration
   - Storage usage
   - DataUpload timeouts
2. What alerts are needed?
   - Failed backups
   - Storage account errors
   - Plugin errors
   - Quota exhaustion
3. Integration with ARO-HCP observability stack?

### Testing and Validation

**Questions**:
1. How are backups validated?
   - Test restores in non-prod environments?
   - Automated backup verification?
2. What is the testing strategy for restore?
   - Test environment setup?
   - Restore validation criteria?
3. Disaster recovery drills?
   - Frequency of DR testing?
   - Runbook documentation?

### Multi-Region Considerations

**Current State**: One storage account per management cluster

**Questions**:
1. Cross-region restore support?
   - Can a backup from `uksouth` restore to `westus3`?
   - Azure resource ID mapping?
2. Geo-redundancy?
   - Should storage accounts use GRS instead of ZRS?
   - Cost vs. benefit analysis?
3. Replication?
   - Should backups be replicated across regions?
   - Active-active backup strategy?

### Cost Optimization

**Questions**:
1. Storage tier optimization?
   - Cool tier for all backups?
   - Archive tier for old backups?
   - Lifecycle policies?
2. Retention strategy?
   - How long to keep backups?
   - Automated cleanup of old backups?
3. Snapshot management?
   - CSI snapshots lifecycle?
   - VolumeSnapshotContent cleanup?

---

## References

### Documentation
- [OADP Official Docs](https://docs.openshift.com/container-platform/latest/backup_and_restore/application_backup_and_restore/oadp-intro.html)
- [Velero Documentation](https://velero.io/docs/)
- [Azure Workload Identity](https://azure.github.io/azure-workload-identity/)
- [HyperShift Documentation](https://hypershift-docs.netlify.app/)

### Source Repositories
- [github.com/openshift/oadp-operator](https://github.com/openshift/oadp-operator)
- [github.com/openshift/hypershift-oadp-plugin](https://github.com/openshift/hypershift-oadp-plugin)
- [github.com/vmware-tanzu/velero](https://github.com/vmware-tanzu/velero)

### Internal Files
- `oadp/README.md`: Chart creation and regeneration
- `oadp/MANUAL_CHANGES.md`: Manual modifications to generated chart
- `oadp/CONFIG_SCHEMA_PROPOSAL.md`: Manifest overrides design
- `tooling/olm-bundle-repkg/README.md`: OLM bundle conversion tool
- `hypershift-oadp-plugin/docs/references/DataMover/DataMover-implementation.md`: DataMover flows

---

## Appendix: File Locations

### Infrastructure
- `dev-infrastructure/modules/hcp-backups/storage.bicep`
- `dev-infrastructure/modules/hcp-backups/storage-rbac.bicep`
- `dev-infrastructure/templates/mgmt-infra.bicep` (lines 162-173)
- `dev-infrastructure/templates/mgmt-cluster.bicep` (lines 253-263, 584-592)
- `dev-infrastructure/configurations/mgmt-infra.tmpl.bicepparam` (lines 33-35)

### Helm Charts
- `oadp/oadp-operator/`: Generated operator chart
- `oadp/deploy/`: HCP-specific configuration chart
- `oadp/oadp-operator-scaffold/`: Scaffold templates

### Configuration
- `oadp/olm-bundle-repkg-config.yaml`: OLM → Helm conversion config
- `oadp/oadp-operator/values.yaml`: Operator values
- `oadp/deploy/values.yaml`: HCP backup values

### Pipelines
- `oadp/pipeline.yaml`: OADP deployment pipeline
- `dev-infrastructure/mgmt-pipeline.yaml`: Management cluster pipeline

### Examples
- `oadp/backup.yaml`: Example backup manifest
- `oadp/restore.yaml`: Example restore manifest

### Tooling
- `tooling/olm-bundle-repkg/`: OLM bundle conversion tool
- `tooling/templatize/`: Configuration templating tool
