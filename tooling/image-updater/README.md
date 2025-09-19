# Image Updater

Automatically fetches the latest image digests from container registries and updates ARO-HCP configuration files.

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

Common regex patterns for filtering tags:

- `^[a-f0-9]{40}$` - 40-character commit hashes
- `^latest$` - Only 'latest' tag
- `^v\\d+\\.\\d+\\.\\d+$` - Semantic versions (v1.2.3)
- `^main-.*` - Tags starting with 'main-'

If no pattern is specified, uses the most recently pushed tag.

## Registry Support

- **Quay.io**: Public repositories (no auth required)
- **Azure Container Registry**: Requires `az login` authentication

## Command Options

```
Flags:
      --config string   Path to configuration file (required)
      --dry-run         Preview changes without modifying files
```