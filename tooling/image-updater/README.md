# Image Updater

Automatically fetches the latest image digests from container registries and updates ARO-HCP configuration files. Only fetches digests for images with **amd64** or **x86_64** architecture to ensure compatibility.

## Key Features

- **Architecture Filtering**: Automatically selects only amd64/x86_64 compatible images
- **Metadata Filtering**: Skips signature files (.sig, .att, .sbom) for faster processing
- **Pattern Matching**: Use regex patterns to target specific tags (commit hashes, versions, etc.)
- **Performance Optimized**: Caching and early-exit optimizations for fast execution
- **Registry Support**: Works with Quay.io and Azure Container Registry

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

  pko-package:
    source:
      image: quay.io/package-operator/package-operator-package
      tagPattern: "^v\\d+\\.\\d+\\.\\d+$"  # Match semver tags
    targets:
    - jsonPath: defaults.pko.imagePackage.digest
      filePath: ../../config/config.yaml
```

## Tag Patterns

Use regex patterns to target specific tags:

- `^[a-f0-9]{40}$` - Commit hashes (40 chars)
- `^sha256-[a-f0-9]{64}$` - SHA256-based tags
- `^v\\d+\\.\\d+\\.\\d+$` - Semantic versions (v1.2.3)
- `^latest$` - Latest tag only

If no pattern is specified, uses the most recently pushed tag.

## Architecture Filtering

The image updater automatically ensures only **amd64/x86_64** compatible images are selected:

- **Skips incompatible architectures**: arm64, s390x, ppc64le, etc.
- **Metadata file filtering**: Automatically ignores signature files (.sig, .att, .sbom)
- **Caching optimization**: Remembers architecture info to avoid redundant API calls
- **Early exit**: Stops searching once a valid image is found for faster execution

## Performance Optimizations

- **Pattern-based filtering**: Use tag patterns to target specific image types (commit hashes, semantic versions)
- **Architecture caching**: Avoids repeated architecture checks for the same digest
- **Metadata filtering**: Skips non-image tags automatically
- **Single-arch focus**: Optimized for repositories with single-architecture images

## Registry Support

- **Quay.io**: Public repositories with automatic architecture detection
- **Azure Container Registry**: Requires `az login` authentication, assumes amd64 images

## Command Options

```
Flags:
      --config string   Path to configuration file (required)
      --dry-run         Preview changes without modifying files
```