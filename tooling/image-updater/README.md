# Image Updater

Automatically fetches the latest image digests from container registries and updates ARO-HCP configuration files.

## Supported Registries

The image-updater supports multiple container registry types with optimized clients:

- **Quay.io** - Uses Quay's proprietary API for enhanced tag discovery
- **Azure Container Registry (ACR)** - Uses Azure SDK with optional authentication
- **Microsoft Container Registry (MCR)** - Uses Docker Registry HTTP API v2
- **Generic Registries** - Any Docker Registry HTTP API v2 compatible registry (Docker Hub, Harbor, etc.)

## Key Features

- **Universal Registry Support**: Works with any Docker Registry HTTP API v2 compatible registry
- **Anonymous by Default**: No authentication required for public registries (MCR, Docker Hub, public Quay.io)
- **Opt-in Authentication**: Explicitly enable authentication only for private registries
- **Architecture-Aware**: Automatically filters images by architecture (defaults to amd64)
- **Multi-Registry Client**: Automatically selects the appropriate client based on registry URL

## Managed Images

| Image Name | Image Reference | Registry Type |
|------------|-----------------|---------------|
| maestro | quay.io/redhat-user-workloads/maestro-rhtap-tenant/maestro/maestro | Quay.io |
| hypershift | quay.io/acm-d/rhtap-hypershift-operator | Quay.io |
| pko-package | quay.io/package-operator/package-operator-package | Quay.io |
| pko-manager | quay.io/package-operator/package-operator-manager | Quay.io |
| pko-remote-phase-manager | quay.io/package-operator/remote-phase-manager | Quay.io |
| arohcpfrontend | arohcpsvcdev.azurecr.io/arohcpfrontend | ACR (Private) |
| arohcpbackend | arohcpsvcdev.azurecr.io/arohcpbackend | ACR (Private) |
| kubeEvents | kubernetesshared.azurecr.io/shared/kube-events | ACR (Public) |
| acrPull | mcr.microsoft.com/aks/msi-acrpull | MCR |
| secretSyncController | registry.k8s.io/secrets-store-sync/controller | Generic |

## Usage

```bash
# Update all images
make update

# Preview changes without modifying files
./image-updater update --config config.yaml --dry-run

# Update with custom config
./image-updater update --config config.yaml

# Update specific components only
./image-updater update --config config.yaml --components maestro,hypershift

# Update all components except specific ones
./image-updater update --config config.yaml --exclude arohcpfrontend,arohcpbackend
```

## Configuration

Define images to monitor and target files to update:

```yaml
images:
  # Quay.io image with commit hash tag pattern
  maestro:
    source:
      image: quay.io/redhat-user-workloads/maestro-rhtap-tenant/maestro/maestro
      tagPattern: "^[a-f0-9]{40}$"  # Optional regex to filter tags
    targets:
    - jsonPath: clouds.dev.defaults.maestro.image.digest
      filePath: ../../config/config.yaml

  # Single-arch image (explicitly targets amd64 only)
  hypershift:
    source:
      image: quay.io/acm-d/rhtap-hypershift-operator
      tagPattern: "^sha256-[a-f0-9]{64}$"
      architecture: amd64  # Target architecture - skips multi-arch manifests
    targets:
    - jsonPath: clouds.dev.defaults.hypershift.image.digest
      filePath: ../../config/config.yaml

  # Quay.io image with semantic version tags
  pko-package:
    source:
      image: quay.io/package-operator/package-operator-package
      tagPattern: "^v\\d+\\.\\d+\\.\\d+$"  # Match semver tags
    targets:
    - jsonPath: defaults.pko.imagePackage.digest
      filePath: ../../config/config.yaml

  # Private ACR image requiring authentication
  arohcpfrontend:
    source:
      image: arohcpsvcdev.azurecr.io/arohcpfrontend
      useAuth: true  # Explicitly require authentication
    targets:
    - jsonPath: clouds.dev.defaults.frontend.image.digest
      filePath: ../../config/config.yaml

  # Public ACR image (anonymous access)
  kubeEvents:
    source:
      image: kubernetesshared.azurecr.io/shared/kube-events
      tagPattern: "^\\d+\\.\\d+$"
      # useAuth defaults to false
    targets:
    - jsonPath: defaults.kubeEvents.image.digest
      filePath: ../../config/config.yaml

  # MCR (Microsoft Container Registry) image
  acrPull:
    source:
      image: mcr.microsoft.com/aks/msi-acrpull
      tagPattern: "^v\\d+\\.\\d+\\.\\d+$"
      # useAuth defaults to false for MCR
    targets:
    - jsonPath: defaults.acrPull.image.digest
      filePath: ../../config/config.yaml

  # Multi-arch manifest list (returns digest of manifest list, not single-arch image)
  secretSyncController:
    source:
      image: registry.k8s.io/secrets-store-sync/controller
      tagPattern: "^v\\d+\\.\\d+\\.\\d+$"
      multiArch: true  # Fetch multi-arch manifest list digest
    targets:
    - jsonPath: defaults.secretSyncController.image.digest
      filePath: ../../config/config.yaml
```

## Authentication

Authentication behavior varies by registry type.

### Default Behavior (useAuth defaults to `false`)

- **Quay.io**: Always uses anonymous access
- **MCR (mcr.microsoft.com)**: Always uses anonymous access
- **Generic registries**: Uses anonymous access by default
- **Azure Container Registry**: Uses anonymous access by default, set `useAuth: true` for private registries

### Registry-Specific Authentication

**Private ACR (requires authentication)**:

```yaml
source:
  image: arohcpsvcdev.azurecr.io/arohcpfrontend
  useAuth: true  # Required for private ACR
```

**Public ACR (anonymous access)**:

```yaml
source:
  image: kubernetesshared.azurecr.io/shared/kube-events
  # useAuth defaults to false
```

**MCR images**:

```yaml
source:
  image: mcr.microsoft.com/aks/msi-acrpull
  # useAuth defaults to false, MCR is always public
```

**Note**: For Azure Container Registry, authentication uses `DefaultAzureCredential` which supports:

- Managed Identity
- Azure CLI credentials (`az login`)
- Environment variables
- And other Azure authentication methods

## Tag Patterns

Common regex patterns for filtering tags:

- `^[a-f0-9]{40}$` - 40-character commit hashes
- `^sha256-[a-f0-9]{64}$` - SHA256-prefixed single-arch images
- `^latest$` - Only 'latest' tag
- `^v\\d+\\.\\d+\\.\\d+$` - Semantic versions (v1.2.3)
- `^main-.*` - Tags starting with 'main-'

If no pattern is specified, uses the most recently pushed tag.

## Architecture Filtering

### Single-Architecture Images (Default)

By default, the tool filters for single-architecture images matching the specified architecture (defaults to `amd64`):

```yaml
source:
  image: quay.io/acm-d/rhtap-hypershift-operator
  tagPattern: "^sha256-[a-f0-9]{64}$"
  architecture: amd64  # Defaults to amd64 if omitted
```

**How it works:**

1. Fetches all tags matching the pattern
2. Iterates through tags (newest first)
3. **Skips multi-arch manifest lists**
4. Verifies architecture matches and OS = linux
5. Returns the first matching single-arch image digest

**Supported architectures**: `amd64`, `arm64`, `ppc64le`, etc.

### Multi-Architecture Manifests

To fetch multi-arch manifest list digests instead of single-arch images, set `multiArch: true`:

```yaml
source:
  image: registry.k8s.io/secrets-store-sync/controller
  tagPattern: "^v\\d+\\.\\d+\\.\\d+$"
  multiArch: true  # Returns the manifest list digest
```

**How it works:**

1. Fetches all tags matching the pattern
2. Iterates through tags (newest first)
3. **Finds multi-arch manifest lists** (skips single-arch images)
4. Returns the first multi-arch manifest list digest

**Use cases**:

- Images that only publish multi-arch manifests
- When you need the manifest list digest for platform-agnostic deployments
- Container runtimes that resolve architecture-specific images from manifest lists

**Note**: `multiArch` and `architecture` are mutually exclusive. If `multiArch: true`, the `architecture` field is ignored.

### Registry-Specific Implementation

- **Quay.io**: Uses `go-containerregistry` to inspect image manifests and detect multi-arch via `MediaType.IsIndex()`
- **Azure Container Registry**: Uses Azure SDK's `GetManifestProperties` API and detects multi-arch via `RelatedArtifacts` field
- **Generic/MCR**: Uses `go-containerregistry` to inspect Docker Registry HTTP API v2 manifests

## Command Options

```text
Flags:
      --config string       Path to configuration file (required)
      --dry-run             Preview changes without modifying files
      --components string   Comma-separated list of components to update (optional)
      --exclude string      Comma-separated list of components to exclude (optional)
```

**Component Filtering**:

- Use `--components` to update only specific images: `--components maestro,hypershift`
- Use `--exclude` to update all images except specific ones: `--exclude arohcpfrontend,arohcpbackend`
- If `--components` is specified, `--exclude` is ignored
- If neither is specified, all images are updated

## Configuration Reference

### Source Configuration Options

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `image` | string | Yes | - | Full image reference (registry/repository) |
| `tagPattern` | string | No | - | Regex pattern to filter tags (uses most recent if omitted) |
| `architecture` | string | No | `amd64` | Target architecture for single-arch images (`amd64`, `arm64`, etc.) |
| `multiArch` | bool | No | `false` | If `true`, fetches multi-arch manifest list digest |
| `useAuth` | bool | No | `false` | If `true`, uses authentication (required for private ACR) |

**Notes**:

- `multiArch` and `architecture` are mutually exclusive
- `useAuth` defaults to `false` for all registries
- For private Azure Container Registries, explicitly set `useAuth: true`

### Target Configuration Options

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `filePath` | string | Yes | Path to YAML file to update |
| `jsonPath` | string | Yes | Dot-notation path to digest field (e.g., `defaults.image.digest`) |

## How It Works

1. **Registry Client Selection**: Automatically selects the appropriate client based on registry URL:
   - `quay.io` → QuayClient (uses Quay API)
   - `*.azurecr.io` → ACRClient (uses Azure SDK)
   - `mcr.microsoft.com` → GenericRegistryClient (uses Docker Registry HTTP API v2)
   - Others → GenericRegistryClient (uses Docker Registry HTTP API v2)

2. **Tag Discovery**: Fetches all tags from the registry and filters by `tagPattern` (if specified)

3. **Architecture Validation**:
   - For single-arch mode: Inspects each tag to find matching architecture and OS
   - For multi-arch mode: Finds multi-arch manifest lists

4. **Digest Update**: Updates the specified YAML files with the latest digest using JSONPath notation

5. **Preserves Formatting**: Maintains YAML structure, comments, and formatting when updating files
