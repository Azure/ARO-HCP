# Image Updater

Automatically fetches the latest image digests from container registries and updates ARO-HCP configuration files.

## Managed Images

| Image Name | Image Reference |
|------------|-----------------|
| maestro | quay.io/redhat-user-workloads/maestro-rhtap-tenant/maestro/maestro |
| hypershift | quay.io/acm-d/rhtap-hypershift-operator |
| pko-package | quay.io/package-operator/package-operator-package |
| pko-manager | quay.io/package-operator/package-operator-manager |
| pko-remote-phase-manager | quay.io/package-operator/remote-phase-manager |
| arohcpfrontend | arohcpsvcdev.azurecr.io/arohcpfrontend |
| arohcpbackend | arohcpsvcdev.azurecr.io/arohcpbackend |

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
      filterArchitecture: true  # Skip multi-arch manifests, select single-arch amd64 only
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
```

## Tag Patterns

Common regex patterns for filtering tags:

- `^[a-f0-9]{40}$` - 40-character commit hashes
- `^sha256-[a-f0-9]{64}$` - SHA256-prefixed single-arch images
- `^latest$` - Only 'latest' tag
- `^v\\d+\\.\\d+\\.\\d+$` - Semantic versions (v1.2.3)
- `^main-.*` - Tags starting with 'main-'

If no pattern is specified, uses the most recently pushed tag.

## Architecture Filtering

For QUAY repositories with multi-arch images, enable `filterArchitecture` to select only single-arch amd64/linux images:

```yaml
source:
  image: quay.io/acm-d/rhtap-hypershift-operator
  tagPattern: "^sha256-[a-f0-9]{64}$"
  filterArchitecture: true  # Required for multi-arch repos
```

**How it works:**
1. Fetches all tags matching the pattern
2. Iterates through tags (newest first)
3. Skips multi-arch manifest lists
4. Inspects config blob to verify architecture = amd64 and OS = linux
5. Returns the first matching single-arch image digest

## Registry Support

- **Quay.io**: Public repositories (no auth required)
- **Azure Container Registry**: Requires `az login` authentication

## Command Options

```
Flags:
      --config string   Path to configuration file (required)
      --dry-run         Preview changes without modifying files
```