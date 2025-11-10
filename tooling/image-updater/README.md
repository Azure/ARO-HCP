# Image Updater

Automatically fetches the latest image digests from container registries and updates ARO-HCP configuration files.

Supports multiple registry types:
- **Quay.io**: Uses proprietary Quay API for enhanced tag discovery
- **Azure Container Registry (ACR)**: Uses Azure SDK with optional authentication
- **Generic Docker Registry v2**: Any compatible registry (MCR, Docker Hub, GCR, etc.)

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
| arohcpfrontend | arohcpsvcdev.azurecr.io/arohcpfrontend | ACR (auth) |
| arohcpbackend | arohcpsvcdev.azurecr.io/arohcpbackend | ACR (auth) |
| acrPull | mcr.microsoft.com/aks/msi-acrpull | MCR (public) |

## Usage

```bash
# Update all images
make update

# Preview changes without modifying files
./image-updater update --config config.yaml --dry-run

# Update with custom config
./image-updater update --config config.yaml
```

## Configuration

Define images to monitor and target files to update:

```yaml
images:
  maestro:
    source:
      image: quay.io/redhat-user-workloads/maestro-rhtap-tenant/maestro/maestro
      tagPattern: "^[a-f0-9]{40}$"  # Optional regex to filter tags
    targets:
    - jsonPath: clouds.dev.defaults.maestro.image.digest
      filePath: ../../config/config.yaml

  hypershift:
    source:
      image: quay.io/acm-d/rhtap-hypershift-operator
      tagPattern: "^sha256-[a-f0-9]{64}$"
      architecture: amd64  # Target architecture - skips multi-arch manifests
    targets:
    - jsonPath: clouds.dev.defaults.hypershift.image.digest
      filePath: ../../config/config.yaml

  pko-package:
    source:
      image: quay.io/package-operator/package-operator-package
      tagPattern: "^v\\d+\\.\\d+\\.\\d+$"  # Match semver tags
    targets:
    - jsonPath: defaults.pko.imagePackage.digest
      filePath: ../../config/config.yaml

  acr-private-image:
    source:
      image: arohcpsvcdev.azurecr.io/arohcpfrontend
      useAuth: true  # Enable Azure authentication for private ACR
    targets:
    - jsonPath: defaults.frontend.image.digest
      filePath: ../../config/config.yaml

  mcr-public-image:
    source:
      image: mcr.microsoft.com/aks/msi-acrpull
      tagPattern: "^v\\d+\\.\\d+\\.\\d+$"
      # useAuth defaults to false - works with public MCR images
    targets:
    - jsonPath: defaults.acrPull.image.digest
      filePath: ../../config/config.yaml
```

## Authentication

By default, the image-updater uses **anonymous access** for all registries. For private registries requiring authentication, you must explicitly enable it.

### Registry-Specific Authentication

#### Azure Container Registry (ACR)

For private ACR images, enable Azure credential authentication:

```yaml
source:
  image: arohcpsvcdev.azurecr.io/arohcpfrontend
  useAuth: true  # Use Azure credentials for authentication
```

#### Generic Registries (MCR, Docker Hub, etc.)

Most public registries work without authentication:

```yaml
source:
  image: mcr.microsoft.com/aks/msi-acrpull
  # useAuth defaults to false - no authentication needed
```

**Authentication Behavior**:

- **Default**: `useAuth: false` (anonymous access)
- **ACR with `useAuth: true`**: Uses Azure SDK with Azure credentials
- **All other registries**: Uses Docker Registry HTTP API v2 (anonymous)

**Benefits**:

- Works out-of-the-box with public registries (MCR, Quay.io, Docker Hub)
- No credentials needed for public images
- Explicit opt-in for private registry authentication
- Useful in CI/CD environments without registry credentials

## Tag Patterns

Common regex patterns for filtering tags:

- `^[a-f0-9]{40}$` - 40-character commit hashes
- `^sha256-[a-f0-9]{64}$` - SHA256-prefixed single-arch images
- `^latest$` - Only 'latest' tag
- `^v\\d+\\.\\d+\\.\\d+$` - Semantic versions (v1.2.3)
- `^main-.*` - Tags starting with 'main-'

If no pattern is specified, uses the most recently pushed tag.

## Architecture Filtering

The tool **always** filters images by architecture. Specify the target architecture (defaults to `amd64` if not specified):

```yaml
source:
  image: quay.io/acm-d/rhtap-hypershift-operator
  tagPattern: "^sha256-[a-f0-9]{64}$"
  architecture: amd64  # Defaults to amd64 if omitted
```

**How it works:**
1. Fetches all tags matching the pattern
2. Iterates through tags (newest first)
3. Skips multi-arch manifest lists
4. Verifies architecture matches and OS = linux
5. Returns the first matching single-arch image digest

**Registry-Specific Implementation:**

- **Quay.io**: Uses proprietary Quay API for tag listing and `go-containerregistry` to inspect image config blobs
- **Azure Container Registry**: Uses Azure SDK's `GetManifestProperties` API to read architecture metadata directly from manifest attributes
- **Generic (MCR, Docker Hub, etc.)**: Uses Docker Registry HTTP API v2 for tag listing and `go-containerregistry` for manifest/config inspection

## Command Options

```text
Flags:
      --config string   Path to configuration file (required)
      --dry-run         Preview changes without modifying files
```
