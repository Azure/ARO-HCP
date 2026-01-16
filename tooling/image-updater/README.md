# Image Updater

A tool that automatically fetches the latest container image digests from registries and updates ARO-HCP configuration files. It supports multiple registry types, environment-based promotions, and secure credential management via Azure Key Vault.

## Table of Contents

- [Quick Start](#quick-start)
- [Supported Registries](#supported-registries)
- [How It Works](#how-it-works)
- [Key Features](#key-features)
- [Common Usage Patterns](#common-usage-patterns)
  - [Development Workflow](#development-workflow)
  - [Environment Promotion](#environment-promotion)
  - [Output to File](#output-to-file)
  - [Debugging](#debugging)
- [Configuration](#configuration)
  - [Basic Image Configuration](#basic-image-configuration)
  - [Registry-Specific Examples](#registry-specific-examples)
  - [Architecture Examples](#architecture-examples)
- [Authentication](#authentication)
  - [Authentication Methods by Registry](#authentication-methods-by-registry)
  - [Azure Key Vault Integration](#azure-key-vault-integration)
  - [Docker Credentials](#docker-credentials)
- [Tag Selection](#tag-selection)
  - [Specific Tag (Pinning)](#specific-tag-pinning)
  - [Tag Pattern (Auto-updates)](#tag-pattern-auto-updates)
  - [Version Labels](#version-labels)
- [Architecture Filtering](#architecture-filtering)
  - [Single-Architecture (Default)](#single-architecture-default)
  - [Multi-Architecture Manifests](#multi-architecture-manifests)
- [Output Format](#output-format)
  - [Inline Comments](#inline-comments)
  - [Output Formats](#output-formats)
- [Command Reference](#command-reference)
  - [Flags](#flags)
  - [Verbosity Levels](#verbosity-levels)
- [Configuration Reference](#configuration-reference)
  - [Source Fields](#source-fields)
  - [Target Fields](#target-fields)
- [Reliability Features](#reliability-features)
  - [Automatic Retry](#automatic-retry)
  - [Context Cancellation](#context-cancellation)

## Quick Start

```bash
# Update all images in dev/int environments
make update

# Preview changes without modifying files
./image-updater update --config config.yaml --dry-run

# Promote images from int to stage
./image-updater update --config config.yaml --env stg

# Promote images from stage to production
./image-updater update --config config.yaml --env prod

# Update specific components only
./image-updater update --config config.yaml --components maestro,hypershift

# Save output to file
./image-updater update --config config.yaml --output-file results.md --output-format markdown
```

## Supported Registries

The tool works with any Docker Registry HTTP API v2 compatible registry, with specialized clients for:

- **Quay.io** - Uses Quay's API for enhanced tag discovery and pagination
- **Azure Container Registry (ACR)** - Uses Azure SDK with DefaultAzureCredential
- **Microsoft Container Registry (MCR)** - Uses Docker Registry HTTP API v2
- **Generic Registries** - Docker Hub, Harbor, GHCR, and other compatible registries

All registries support anonymous access by default for public images. Private registries require explicit `useAuth: true` configuration.

## How It Works

1. **Load Configuration**: Reads `config.yaml` to get list of images and target YAML files to update

2. **Authenticate** (if needed): 
   - Fetches credentials from Azure Key Vault (deduplicates by vault URL + secret name)
   - Merges with local Docker config (`~/.docker/config.json`)
   - Supports Azure CLI, Managed Identity, and other Azure authentication methods

3. **Select Registry Client**: Automatically chooses the appropriate client based on registry URL
   - `quay.io` → QuayClient (Quay API with 100 tags/page pagination)
   - `*.azurecr.io` → ACRClient (Azure SDK)
   - Others → GenericRegistryClient (Docker Registry HTTP API v2)

4. **Fetch or Promote Images**:
   - **Default mode** (dev/int): Fetches latest digests from registry with retry logic
   - **Promotion mode** (stg/prod): Copies digests from source environment (no registry fetch)

5. **Filter and Validate**:
   - Filters tags by regex pattern (if specified)
   - Validates architecture (amd64, arm64, etc.) or multi-arch manifests
   - Sorts by timestamp to find the latest matching image

6. **Update YAML Files**:
   - Updates digest fields using JSONPath notation
   - Adds inline comments with version and timestamp: `# v1.2.3 (2025-01-15 10:30)`
   - Preserves YAML formatting, structure, and other comments

7. **Output Results**: Displays formatted table or writes to file (table/markdown/json)

## Key Features

### Environment Promotion

- **Structured Promotion Flow**: dev/int → stage → prod
- **No Registry Lookups in Promotion**: Copies digests directly from source environment
- **Version Preservation**: Maintains tags and timestamps during promotion

### Registry & Authentication

- **Universal Registry Support**: Works with any Docker Registry HTTP API v2 compatible registry
- **Anonymous by Default**: No authentication for public registries (MCR, Docker Hub, public Quay.io)
- **Azure Key Vault Integration**: Per-image credential configuration with automatic deduplication
- **Multiple Auth Methods**: Docker config, Azure CLI, Managed Identity

### Reliability & Performance

- **Automatic Retry Logic**: Exponential backoff for network errors and 5xx/429 responses
- **Smart Caching**: Eliminates duplicate API calls (~50% reduction)
- **Context Cancellation**: Graceful shutdown and timeout handling
- **Enhanced Logging**: Structured logging with verbosity levels (V(0), V(1), V(2))

### Flexibility

- **Architecture-Aware**: Filters by architecture (amd64, arm64, etc.) or multi-arch manifests
- **Flexible Tag Selection**: Exact tag or regex pattern matching
- **Component Filtering**: Update specific components or exclude certain ones
- **Multiple Output Formats**: Table, Markdown, or JSON to file or stdout
- **Digest Format Support**: Both `.digest` (sha256:...) and `.sha` (hash only) fields

## Common Usage Patterns

### Development Workflow

```bash
# Update dev and int environments with latest images
./image-updater update --config config.yaml

# Preview changes first
./image-updater update --config config.yaml --dry-run

# Update only specific components
./image-updater update --config config.yaml --components maestro,hypershift

# Exclude certain components
./image-updater update --config config.yaml --exclude-components arohcpfrontend
```

### Environment Promotion

The tool supports a structured promotion flow across environments:

1. **dev & int** (default): Fetches latest images from registries
2. **stage** (`--env stg`): Promotes digests from int environment
3. **prod** (`--env prod`): Promotes digests from stage environment

```bash
# Step 1: Update dev and int with latest images
./image-updater update --config config.yaml

# Step 2: After validation, promote to stage
./image-updater update --config config.yaml --env stg

# Step 3: After stage validation, promote to production
./image-updater update --config config.yaml --env prod
```

### Output to File

```bash
# Save results as markdown
./image-updater update --config config.yaml --output-file results.md --output-format markdown

# Save as JSON for automation
./image-updater update --config config.yaml --output-file results.json --output-format json

# Use Makefile variables
make promote-stage OUTPUT_FILE=stage-promotion.md OUTPUT_FORMAT=markdown
```

### Debugging

```bash
# Enable verbose logging (shows retry attempts, API calls)
./image-updater update --config config.yaml -v=2

# Combine with dry-run for debugging without changes
./image-updater update --config config.yaml --dry-run -v=2
```

## Configuration

### Basic Image Configuration

```yaml
images:
  # Multi-environment image with tag pattern
  maestro:
    source:
      image: quay.io/redhat-user-workloads/maestro-rhtap-tenant/maestro/maestro
      tagPattern: "^[a-f0-9]{40}$"  # Match 40-character commit hashes
    targets:
    - jsonPath: clouds.dev.defaults.maestro.image.digest
      filePath: ../../config/config.yaml
      env: dev
    - jsonPath: clouds.public.environments.int.defaults.maestro.image.digest
      filePath: ../../config/config.msft.clouds-overlay.yaml
      env: int
    - jsonPath: clouds.public.environments.stg.defaults.maestro.image.digest
      filePath: ../../config/config.msft.clouds-overlay.yaml
      env: stg
    - jsonPath: clouds.public.environments.prod.defaults.maestro.image.digest
      filePath: ../../config/config.msft.clouds-overlay.yaml
      env: prod

  # Pinned to specific version
  pko-manager:
    source:
      image: quay.io/package-operator/package-operator-manager
      tag: "v1.18.3"  # Exact version (useful for rollbacks)
    targets:
    - jsonPath: defaults.pko.imageManager.digest
      filePath: ../../config/config.yaml
      env: dev

  # Using generic tag with version label
  my-app:
    source:
      image: quay.io/example/my-app
      tag: "latest"
      versionLabel: "org.opencontainers.image.revision"  # Extract commit hash
    targets:
    - jsonPath: defaults.myApp.image.digest
      filePath: ../../config/config.yaml
      env: dev
```

### Registry-Specific Examples

**Quay.io (Public)**:
```yaml
pko-package:
  source:
    image: quay.io/package-operator/package-operator-package
    tagPattern: "^v\\d+\\.\\d+\\.\\d+$"  # Semantic versions
  targets:
  - jsonPath: defaults.pko.imagePackage.digest
    filePath: ../../config/config.yaml
    env: dev
```

**Quay.io (Private with Key Vault)**:
```yaml
clusters-service:
  source:
    image: quay.io/app-sre/aro-hcp-clusters-service
    tagPattern: "^[a-f0-9]{7}$"
    useAuth: true  # Required for private repos
    keyVault:
      url: "https://arohcpdev-global.vault.azure.net/"
      secretName: "component-sync-pull-secret"
  targets:
  - jsonPath: clouds.dev.defaults.clustersService.image.digest
    filePath: ../../config/config.yaml
    env: dev
```

**Azure Container Registry (Private)**:
```yaml
arohcpfrontend:
  source:
    image: arohcpsvcdev.azurecr.io/arohcpfrontend
    useAuth: true  # Uses DefaultAzureCredential
  targets:
  - jsonPath: clouds.dev.defaults.frontend.image.digest
    filePath: ../../config/config.yaml
    env: dev
```

**Azure Container Registry (Public)**:
```yaml
kubeEvents:
  source:
    image: kubernetesshared.azurecr.io/shared/kube-events
    tagPattern: "^\\d+\\.\\d+$"
    # useAuth defaults to false
  targets:
  - jsonPath: defaults.kubeEvents.image.digest
    filePath: ../../config/config.yaml
    env: dev
```

**Microsoft Container Registry**:
```yaml
acrPull:
  source:
    image: mcr.microsoft.com/aks/msi-acrpull
    tagPattern: "^v\\d+\\.\\d+\\.\\d+$"
    # Always uses anonymous access
  targets:
  - jsonPath: defaults.acrPull.image.digest
    filePath: ../../config/config.yaml
    env: dev
```

### Architecture Examples

**Single Architecture (Default)**:
```yaml
hypershift:
  source:
    image: quay.io/acm-d/rhtap-hypershift-operator
    tagPattern: "^sha256-[a-f0-9]{64}$"
    architecture: amd64  # Defaults to amd64, can use arm64, etc.
  targets:
  - jsonPath: clouds.dev.defaults.hypershift.image.digest
    filePath: ../../config/config.yaml
    env: dev
```

**Multi-Architecture Manifest**:
```yaml
secretSyncController:
  source:
    image: registry.k8s.io/secrets-store-sync/controller
    tagPattern: "^v\\d+\\.\\d+\\.\\d+$"
    multiArch: true  # Returns manifest list digest
  targets:
  - jsonPath: defaults.secretSyncController.image.digest
    filePath: ../../config/config.yaml
    env: dev
```

**Using .sha field (without sha256: prefix)**:
```yaml
prometheus-operator:
  source:
    image: mcr.microsoft.com/oss/v2/prometheus/prometheus-operator
    tagPattern: "^v\\d+\\.\\d+\\.\\d+-?\\d?$"
    multiArch: true
  targets:
  - jsonPath: defaults.prometheus.prometheusOperator.image.sha  # Stores hash only
    filePath: ../../config/config.yaml
    env: dev
```

## Authentication

### Authentication Methods by Registry

| Registry | Default | Auth Methods |
|----------|---------|--------------|
| Quay.io (public) | Anonymous | None needed |
| Quay.io (private) | Requires auth | Docker config, Key Vault |
| ACR (public) | Anonymous | None needed |
| ACR (private) | Requires auth | DefaultAzureCredential (Azure CLI, Managed Identity, etc.) |
| MCR | Anonymous | Always public |
| Generic/Docker Hub | Anonymous | Docker config |

### Azure Key Vault Integration

For private registries, configure Azure Key Vault on a per-image basis:

```yaml
source:
  image: quay.io/app-sre/private-repo
  useAuth: true
  keyVault:
    url: "https://arohcpdev-global.vault.azure.net/"
    secretName: "component-sync-pull-secret"
```

**Benefits**:
- Credentials stored securely in Azure Key Vault
- Different images can use different secrets
- Automatic deduplication (same vault+secret fetched only once)
- Works with `az login` and other Azure authentication

**Requirements**:
- Azure CLI authenticated (`az login`) or Managed Identity
- Read access to the Key Vault
- Pull secret in Docker config.json format (base64 or raw JSON)

### Docker Credentials

For Quay.io and generic registries, you can also use Docker credentials:

```bash
# Login to Quay.io
docker login quay.io
# or
podman login quay.io
```

Credentials are stored in `~/.docker/config.json` and automatically used when `useAuth: true`.

## Tag Selection

### Specific Tag (Pinning)

Use when you need a specific version:

```yaml
source:
  image: quay.io/package-operator/package-operator-package
  tag: "v1.18.3"  # Exact tag name
```

**Benefits**:
- Fast (no tag listing required)
- Useful for rollbacks or testing specific releases

### Tag Pattern (Auto-updates)

Use regex to automatically select the latest matching tag:

```yaml
source:
  image: quay.io/package-operator/package-operator-package
  tagPattern: "^v\\d+\\.\\d+\\.\\d+$"  # Match semver tags
```

**Common Patterns**:
- `^[a-f0-9]{7}$` - 7-char commit hashes
- `^[a-f0-9]{40}$` - Full commit hashes
- `^sha256-[a-f0-9]{64}$` - SHA256-prefixed images
- `^v\\d+\\.\\d+\\.\\d+$` - Semantic versions (v1.2.3)
- `^main-.*` - Tags starting with "main-"
- `^latest$` - Only "latest" tag

### Version Labels

When using `tag` (like `tag: "latest"`), the tool extracts version info from container labels:

```yaml
source:
  image: quay.io/example/my-app
  tag: "latest"
  versionLabel: "org.opencontainers.image.revision"  # Default for 'tag'
```

This provides meaningful version info (like commit hash) even with generic tags.

## Architecture Filtering

### Single-Architecture (Default)

Filters for specific architecture (defaults to amd64):

```yaml
source:
  architecture: amd64  # Can be amd64, arm64, ppc64le, etc.
```

The tool:
1. Lists all matching tags
2. Skips multi-arch manifest lists
3. Verifies architecture and OS match
4. Returns first matching single-arch digest

### Multi-Architecture Manifests

For multi-arch manifest lists:

```yaml
source:
  multiArch: true  # Returns manifest list digest
```

The tool:
1. Lists all matching tags
2. Finds multi-arch manifest lists
3. Returns manifest list digest (not single-arch image)

**Note**: `multiArch` and `architecture` are mutually exclusive.

## Output Format

### Inline Comments

The tool adds version and timestamp comments to YAML files:

```yaml
defaults:
  pko:
    imagePackage:
      digest: sha256:abc123... # v1.18.4 (2025-11-24 14:30)
```

- **Version**: From container label (e.g., commit hash) or tag name
- **Timestamp**: When the image was created (YYYY-MM-DD HH:MM)

### Output Formats

Write results to file in different formats:

```bash
# Table format (default)
./image-updater update --config config.yaml --output-file results.txt

# Markdown format
./image-updater update --config config.yaml --output-file results.md --output-format markdown

# JSON format
./image-updater update --config config.yaml --output-file results.json --output-format json
```

## Command Reference

### Flags

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--config` | string | - | Path to configuration file (required) |
| `--dry-run` | bool | false | Preview changes without modifying files |
| `--env` | string | - | Environment to target: `stg` or `prod` (omit for dev/int) |
| `--components` | string | - | Comma-separated list of components to update |
| `--exclude-components` | string | - | Comma-separated list of components to exclude |
| `--output-file` | string | - | Write results to file instead of stdout |
| `--output-format` | string | table | Output format: `table`, `markdown`, or `json` |
| `-v, --verbosity` | int | 0 | Log verbosity: 0=clean, 1=summary, 2+=debug |

### Verbosity Levels

- **Level 0-1** (default): Clean summary output
  - Formatted table with updates
  - Markdown commit message
  - No verbose logging

- **Level 2+** (debug): Detailed troubleshooting info
  - Registry API calls
  - Retry attempts with backoff
  - Tag filtering steps
  - Key Vault authentication
  - Manifest inspection

Use `-v=2` for debugging auth issues, tag filtering, or network failures.

## Configuration Reference

### Source Fields

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `image` | string | Yes | - | Full image reference (registry/repository) |
| `tag` | string | No | - | Exact tag (mutually exclusive with `tagPattern`) |
| `tagPattern` | string | No | - | Regex pattern (mutually exclusive with `tag`) |
| `versionLabel` | string | No | `org.opencontainers.image.revision` (with `tag`), empty (with `tagPattern`) | Container label to extract for version info |
| `architecture` | string | No | `amd64` | Target architecture (mutually exclusive with `multiArch`) |
| `multiArch` | bool | No | `false` | Fetch multi-arch manifest list (mutually exclusive with `architecture`) |
| `useAuth` | bool | No | `false` | Require authentication (needed for private registries) |
| `keyVault.url` | string | No | - | Azure Key Vault URL |
| `keyVault.secretName` | string | No | - | Pull secret name in Key Vault |

### Target Fields

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `filePath` | string | Yes | Path to YAML file to update |
| `jsonPath` | string | Yes | Dot-notation path to field (e.g., `defaults.image.digest`) |
| `env` | string | Yes | Environment tag: `dev`, `int`, `stg`, or `prod` |

**Note**: Fields ending with `.digest` store full digest (`sha256:...`), fields ending with `.sha` store hash only.

## Reliability Features

### Automatic Retry

- **Initial Interval**: 500ms
- **Max Interval**: 60s
- **Max Elapsed Time**: 5 minutes
- **Multiplier**: 2.0 (exponential backoff)
- **Randomization**: 0.1 (jitter)

Retries on:
- Network errors
- HTTP 5xx errors
- HTTP 429 (rate limiting)

### Context Cancellation

- Supports Ctrl+C for graceful shutdown
- Timeout enforcement
- Proper resource cleanup
