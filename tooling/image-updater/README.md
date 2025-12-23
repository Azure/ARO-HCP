# Image Updater

Automatically fetches the latest image digests from container registries and updates ARO-HCP configuration files.

## Supported Registries

The image-updater supports multiple container registry types with optimized clients:

- **Quay.io** - Uses Quay's proprietary API for enhanced tag discovery
- **Azure Container Registry (ACR)** - Uses Azure SDK with optional authentication
- **Microsoft Container Registry (MCR)** - Uses Docker Registry HTTP API v2
- **Generic Registries** - Any Docker Registry HTTP API v2 compatible registry (Docker Hub, Harbor, etc.)

## Key Features

### Registry Support

- **Universal Registry Support**: Works with any Docker Registry HTTP API v2 compatible registry
- **Anonymous by Default**: No authentication required for public registries (MCR, Docker Hub, public Quay.io)
- **Opt-in Authentication**: Explicitly enable authentication only for private registries
- **Azure Key Vault Integration**: Per-image Key Vault configuration for secure credential management
- **Smart Credential Caching**: Automatically deduplicates Key Vault secret fetches across images
- **Architecture-Aware**: Automatically filters images by architecture (defaults to amd64)
- **Multi-Registry Client**: Automatically selects the appropriate client based on registry URL
- **Flexible Digest Format**: Supports both `.digest` fields (with `sha256:` prefix) and `.sha` fields (hash only)

### Reliability & Performance

- **Automatic Retry Logic**: Exponential backoff retry for transient network errors and server failures (5xx, 429)
- **Context Cancellation Support**: Proper context propagation for graceful shutdown and timeout handling
- **Enhanced Logging**: Detailed structured logging with verbosity levels for debugging and monitoring
- **Optimized Pagination**: Configurable page size (100 tags/page) for efficient bulk tag fetching from Quay.io
- **Timestamp Enrichment**: Automatic tag timestamp retrieval and sorting for both Quay API and Registry V2 API
- **Descriptor Caching**: Eliminates duplicate API calls by caching image descriptors during tag processing (~50% reduction in API calls)

## Usage

```bash
# Update all images (default: dev and int environments)
make update

# Preview changes without modifying files
./image-updater update --config config.yaml --dry-run

# Update dev and int environments (default behavior - omit --env flag)
./image-updater update --config config.yaml

# Promote images from int to stage (copies digests, no registry fetch)
./image-updater update --config config.yaml --env stg --dry-run

# Promote images from stage to production (copies digests, no registry fetch)
./image-updater update --config config.yaml --env prod --dry-run

# Update specific components only
./image-updater update --config config.yaml --components maestro,hypershift

# Update all components except specific ones
./image-updater update --config config.yaml --exclude-components arohcpfrontend,arohcpbackend

# Combine environment and component filters
./image-updater update --config config.yaml --env stg --components maestro,hypershift --dry-run

# Enable verbose logging for debugging (shows all details including retry attempts, API calls)
./image-updater update --config config.yaml -v=2
```

## Environment Promotion Flow

The image updater supports a structured promotion flow across environments:

1. **dev & int** (default when `--env` omitted): Fetches latest images from registries
2. **stage** (`--env stg`): Promotes (copies) digests from int environment
3. **prod** (`--env prod`): Promotes (copies) digests from stage environment

When using `--env stg` or `--env prod`, the tool operates in **promotion mode**:
- No registry lookups are performed
- Digests are copied from the source environment to the target environment
- Ensures consistent promotion path: dev/int → stage → prod

**Example promotion workflow:**
```bash
# Step 1: Update dev and int with latest images
./image-updater update --config config.yaml

# Step 2: After validation, promote to stage
./image-updater update --config config.yaml --env stg

# Step 3: After stage validation, promote to production
./image-updater update --config config.yaml --env prod
```

## Output Format

When the tool updates image digests in YAML files, it automatically adds inline comments with version tag and timestamp information:

```yaml
defaults:
  pko:
    imagePackage:
      digest: sha256:abc123... # v1.18.4 (2025-11-24 14:30)
```

This helps track:

- **Tag name**: The version or tag name (e.g., `v1.18.4`)
- **Timestamp**: When the image was created/published (format: `YYYY-MM-DD HH:MM`)

The comments are automatically generated and updated each time the tool runs.

## Configuration

Define images to monitor and target files to update. Each image can optionally specify Azure Key Vault credentials for authentication.

### Image Configuration Examples

```yaml
images:
  # Image with multi-environment targets
  maestro:
    source:
      image: quay.io/redhat-user-workloads/maestro-rhtap-tenant/maestro/maestro
      tagPattern: "^[a-f0-9]{40}$"  # Optional regex to filter tags
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

  # Single-arch image (explicitly targets amd64 only)
  hypershift:
    source:
      image: quay.io/acm-d/rhtap-hypershift-operator
      tagPattern: "^sha256-[a-f0-9]{64}$"
      architecture: amd64  # Target architecture - skips multi-arch manifests
    targets:
    - jsonPath: clouds.dev.defaults.hypershift.image.digest
      filePath: ../../config/config.yaml
      env: dev
    - jsonPath: clouds.public.environments.int.defaults.hypershift.image.digest
      filePath: ../../config/config.msft.clouds-overlay.yaml
      env: int

  # Quay.io image with semantic version tags
  pko-package:
    source:
      image: quay.io/package-operator/package-operator-package
      tagPattern: "^v\\d+\\.\\d+\\.\\d+$"  # Match semver tags
    targets:
    - jsonPath: defaults.pko.imagePackage.digest
      filePath: ../../config/config.yaml
      env: dev

  # Quay.io image pinned to specific version (e.g., during rollback)
  pko-manager:
    source:
      image: quay.io/package-operator/package-operator-manager
      tag: "v1.18.3"  # Pin to specific version instead of using pattern
    targets:
    - jsonPath: defaults.pko.imageManager.digest
      filePath: ../../config/config.yaml
      env: dev

  # Private ACR image requiring authentication
  arohcpfrontend:
    source:
      image: arohcpsvcdev.azurecr.io/arohcpfrontend
      useAuth: true  # Explicitly require authentication
    targets:
    - jsonPath: clouds.dev.defaults.frontend.image.digest
      filePath: ../../config/config.yaml
      env: dev

  # Public ACR image (anonymous access)
  kubeEvents:
    source:
      image: kubernetesshared.azurecr.io/shared/kube-events
      tagPattern: "^\\d+\\.\\d+$"
      # useAuth defaults to false
    targets:
    - jsonPath: defaults.kubeEvents.image.digest
      filePath: ../../config/config.yaml
      env: dev

  # MCR (Microsoft Container Registry) image
  acrPull:
    source:
      image: mcr.microsoft.com/aks/msi-acrpull
      tagPattern: "^v\\d+\\.\\d+\\.\\d+$"
      # useAuth defaults to false for MCR
    targets:
    - jsonPath: defaults.acrPull.image.digest
      filePath: ../../config/config.yaml
      env: dev

  # Multi-arch manifest list (returns digest of manifest list, not single-arch image)
  secretSyncController:
    source:
      image: registry.k8s.io/secrets-store-sync/controller
      tagPattern: "^v\\d+\\.\\d+\\.\\d+$"
      multiArch: true  # Fetch multi-arch manifest list digest
    targets:
    - jsonPath: defaults.secretSyncController.image.digest
      filePath: ../../config/config.yaml

  # Example using .sha field (stores hash without sha256: prefix)
  prometheus-operator:
    source:
      image: mcr.microsoft.com/oss/v2/prometheus/prometheus-operator
      tagPattern: "^v\\d+\\.\\d+\\.\\d+-?\\d?$"
      multiArch: true
    targets:
    - jsonPath: defaults.prometheus.prometheusOperator.image.sha
      filePath: ../../config/config.yaml
```

## Authentication

Authentication behavior varies by registry type.

### Default Behavior (useAuth defaults to `false`)

- **Quay.io**: Uses anonymous access by default, set `useAuth: true` for private repositories
- **MCR (mcr.microsoft.com)**: Always uses anonymous access
- **Generic registries**: Uses anonymous access by default, set `useAuth: true` for private registries
- **Azure Container Registry**: Uses anonymous access by default, set `useAuth: true` for private registries

### Registry-Specific Authentication

**Private Quay.io repositories (requires authentication)**:

```yaml
source:
  image: quay.io/your-org/private-repo
  useAuth: true  # Required for private Quay repositories
```

**Public Quay.io repositories (anonymous access)**:

```yaml
source:
  image: quay.io/redhat-user-workloads/maestro-rhtap-tenant/maestro/maestro
  # useAuth defaults to false for public repos
```

**Private Quay.io repositories (requires authentication)**:

```yaml
source:
  image: quay.io/app-sre/aro-hcp-clusters-service
  tagPattern: "^[a-f0-9]{7}$"
  useAuth: true
  keyVault:
    url: "https://arohcpdev-global.vault.azure.net/"
    secretName: "component-sync-pull-secret"
```

**Note**: For Quay.io, authentication can use either:

1. **Docker credentials from `~/.docker/config.json`**:

   ```bash
   docker login quay.io
   # Or use podman:
   podman login quay.io
   ```

2. **Azure Key Vault pull secrets** (recommended for CI/CD and private repositories):
   Configure the `keyVault` section in the image's source configuration (as shown above).
   The tool will automatically fetch the pull secret from Azure Key Vault and merge it with your local Docker config before authenticating. This requires Azure CLI authentication (`az login`).

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

**Authentication Methods by Registry**:

- **Quay.io**:
  - Docker credentials from `~/.docker/config.json` (via `docker login quay.io`)
  - Azure Key Vault pull secrets (per-image configuration)
- **Azure Container Registry**: Uses `DefaultAzureCredential` which supports:
  - Managed Identity
  - Azure CLI credentials (`az login`)
  - Environment variables
  - And other Azure authentication methods
- **Generic registries**: Uses Docker credentials from `~/.docker/config.json`

### Azure Key Vault Authentication

For private registries that require authentication, you can configure Azure Key Vault credentials on a per-image basis. This is the recommended approach for CI/CD pipelines and production environments.

**Benefits**:

- **Secure**: Credentials stored in Azure Key Vault, not in configuration files
- **Flexible**: Different images can use different Key Vault secrets
- **Efficient**: Automatic deduplication prevents fetching the same secret multiple times
- **Integrated**: Works seamlessly with Azure CLI authentication (`az login`)

**Configuration**:

```yaml
images:
  clusters-service:
    source:
      image: quay.io/app-sre/aro-hcp-clusters-service
      tagPattern: "^[a-f0-9]{7}$"
      useAuth: true
      keyVault:
        url: "https://arohcpdev-global.vault.azure.net/"
        secretName: "component-sync-pull-secret"
    targets:
      - jsonPath: clouds.dev.defaults.clustersService.image.digest
        filePath: ../../config/config.yaml
```

**How it works**:

1. Tool authenticates to Azure using `DefaultAzureCredential` (supports `az login`, managed identity, etc.)
2. Fetches the pull secret from the specified Key Vault
3. Merges credentials with your local `~/.docker/config.json`
4. Uses merged credentials to authenticate with the registry
5. Multiple images with the same Key Vault URL + secret name are deduplicated (fetched only once)

**Requirements**:

- Azure CLI installed and authenticated (`az login`)
- Read access to the specified Key Vault
- Pull secret must be stored in Key Vault in Docker config.json format (supports both base64-encoded and raw JSON)

## Tag Selection

You can specify which image tag to use in two ways:

### Option 1: Specific Tag (Recommended for pinning versions)

Use the `tag` field to specify an exact tag name:

```yaml
source:
  image: quay.io/package-operator/package-operator-package
  tag: "v1.18.3"  # Pin to specific version
```

**Use cases:**
- Pinning to a specific version temporarily (e.g., during a rollback)
- Testing a specific release
- Production stability requirements

**Performance benefits:**
- **No pagination required** - fetches only the specified tag directly from the registry
- Faster execution compared to pattern matching which requires listing all tags

### Option 2: Tag Pattern (Recommended for automatic updates)

Use the `tagPattern` field with a regex pattern to automatically select the latest matching tag:

```yaml
source:
  image: quay.io/package-operator/package-operator-package
  tagPattern: "^v\\d+\\.\\d+\\.\\d+$"  # Match any semantic version
```

**Common regex patterns:**
- `^[a-f0-9]{7}$` - 7-character commit hashes (short)
- `^[a-f0-9]{40}$` - 40-character commit hashes (full)
- `^sha256-[a-f0-9]{64}$` - SHA256-prefixed single-arch images
- `^latest$` - Only 'latest' tag
- `^v\\d+\\.\\d+\\.\\d+$` - Semantic versions (v1.2.3)
- `^main-.*` - Tags starting with 'main-'

**Use cases:**
- Continuous updates to the latest version matching a pattern
- Development and staging environments
- Following a release branch

### Important Notes

- `tag` and `tagPattern` are **mutually exclusive** - you can only specify one
- If neither is specified, the tool uses the most recently pushed tag
- When using `tag`, the tool will find and use that exact tag (case-sensitive)

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
      --config string             Path to configuration file (required)
      --dry-run                   Preview changes without modifying files
      --components string         Comma-separated list of components to update (optional)
      --exclude-components string Comma-separated list of components to exclude (optional)
  -v, --verbosity int             Log verbosity level (default 0)
```

**Component Filtering**:

- Use `--components` to update only specific images: `--components maestro,hypershift`
- Use `--exclude` to update all images except specific ones: `--exclude arohcpfrontend,arohcpbackend`
- If `--components` is specified, `--exclude` is ignored
- If neither is specified, all images are updated

**Logging Verbosity Levels**:

- **Level 0 or 1** (default): Clean summary output only
  - Shows a formatted summary table with total images checked and updates applied
  - Displays markdown-formatted commit message with changes
  - No verbose logging noise
  - Ideal for CI/CD pipelines and regular usage

- **Level 2+** (debug): Detailed debug logging for troubleshooting
  - Registry API calls and responses
  - Retry attempts with backoff durations
  - Tag filtering and architecture validation steps
  - Key Vault authentication details
  - Individual tag inspection operations
  - Manifest fetching and parsing details
  - All debug information for troubleshooting

Use `--verbosity 2` or higher when debugging authentication issues, tag filtering problems, or transient network failures.

## Configuration Reference

### Source Configuration Options

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `image` | string | Yes | - | Full image reference (registry/repository) |
| `tag` | string | No | - | Exact tag to use (mutually exclusive with `tagPattern`) |
| `tagPattern` | string | No | - | Regex pattern to filter tags (mutually exclusive with `tag`) |
| `architecture` | string | No | `amd64` | Target architecture for single-arch images (`amd64`, `arm64`, etc.) |
| `multiArch` | bool | No | `false` | If `true`, fetches multi-arch manifest list digest |
| `useAuth` | bool | No | `false` | If `true`, uses authentication (required for private registries) |
| `keyVault` | object | No | - | Azure Key Vault configuration for fetching pull secrets |
| `keyVault.url` | string | No | - | Azure Key Vault URL (e.g., `https://vault.vault.azure.net/`) |
| `keyVault.secretName` | string | No | - | Name of the pull secret in Key Vault |

**Notes**:

- `tag` and `tagPattern` are mutually exclusive - only one can be specified
- If neither `tag` nor `tagPattern` is specified, uses the most recently pushed tag
- `multiArch` and `architecture` are mutually exclusive
- `useAuth` defaults to `false` for all registries
- For private registries, explicitly set `useAuth: true`
- `keyVault` is optional and only needed for registries requiring Azure Key Vault credentials
- When `keyVault` is configured, credentials are automatically fetched before registry authentication

### Target Configuration Options

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `filePath` | string | Yes | Path to YAML file to update |
| `jsonPath` | string | Yes | Dot-notation path to digest field (e.g., `defaults.image.digest` or `defaults.image.sha`) |

**Note on digest vs sha fields:**

- Fields ending with `.digest` will store the full digest including the `sha256:` prefix
- Fields ending with `.sha` will store only the hash value without the `sha256:` prefix
- This allows the tool to work with different configuration formats that have different digest field conventions

## Reliability Features

### Automatic Retry with Exponential Backoff

The image-updater implements robust retry logic to handle transient failures gracefully:

**Configuration** (default values):

- **Initial Interval**: 500ms - Starting delay before first retry
- **Max Interval**: 60s - Maximum delay between retries
- **Max Elapsed Time**: 5 minutes - Total time before giving up
- **Multiplier**: 2.0 - Exponential backoff multiplier
- **Randomization Factor**: 0.1 - Jitter to prevent thundering herd

**Retryable Scenarios**:

- Network errors (connection failures, timeouts)
- HTTP 5xx server errors
- HTTP 429 (rate limiting)

**Registry Support**:

- **Quay.io**: Retry on API calls and Registry V2 API requests
- **Generic Registries**: Retry on all HTTP requests
- **ACR**: Uses Azure SDK's built-in retry mechanisms

**Logging**:

- Logs each retry attempt with backoff duration
- Logs final failure after all retries exhausted

### Context Cancellation

All registry operations support context cancellation for:

- Graceful shutdown on interrupt signals (Ctrl+C)
- Timeout enforcement for long-running operations
- Proper cleanup of resources (HTTP connections, etc.)

Context is propagated through all layers:

- Registry client operations
- HTTP requests with retry logic
- Tag fetching and manifest inspection

## How It Works

1. **Key Vault Authentication** (if configured):
   - Collects all unique Key Vault configurations from images
   - Deduplicates by vault URL + secret name combination
   - Fetches each unique secret from Azure Key Vault
   - Merges credentials with local `~/.docker/config.json`

2. **Registry Client Selection**: Automatically selects the appropriate client based on registry URL:
   - `quay.io` → QuayClient (uses Quay API with 100 tags/page pagination)
   - `*.azurecr.io` → ACRClient (uses Azure SDK)
   - `mcr.microsoft.com` → GenericRegistryClient (uses Docker Registry HTTP API v2)
   - Others → GenericRegistryClient (uses Docker Registry HTTP API v2)

3. **Tag Discovery** (with retry logic):
   - Fetches all tags from the registry with automatic retries on failures
   - Filters by `tagPattern` (if specified)
   - Enriches tags with timestamps for proper sorting (both Quay API and Registry V2 API)

4. **Architecture Validation**:
   - For single-arch mode: Inspects each tag to find matching architecture and OS
   - For multi-arch mode: Finds multi-arch manifest lists

5. **Digest Update**: Updates the specified YAML files with the latest digest using JSONPath notation

6. **Tag and Timestamp Comments**: Automatically adds inline comments with the tag name and creation timestamp

7. **Preserves Formatting**: Maintains YAML structure, comments, and formatting when updating files
