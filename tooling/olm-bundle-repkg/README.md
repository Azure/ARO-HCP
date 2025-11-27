# OLM Bundle Repackage Tool

This tool repackages any OLM (Operator Lifecycle Manager) bundle as a Helm chart, making it possible to deploy operators using Helm instead of OLM.

## Table of Contents

- [Overview](#overview)
- [Usage](#usage)
  - [Basic Usage with Bundle Image](#basic-usage-with-bundle-image)
  - [Basic Usage with Manifest Directory](#basic-usage-with-manifest-directory)
  - [CLI Reference](#cli-reference)
- [Input Formats](#input-formats)
  - [1. OLM Bundle Container Images (tar.gz)](#1-olm-bundle-container-images-targz)
  - [2. Bundle Manifest Directories](#2-bundle-manifest-directories)
- [Configuration System](#configuration-system)
  - [Configuration File Format](#configuration-file-format)
  - [Image Reference Formats](#image-reference-formats)
  - [Example Configuration](#example-configuration)
- [Preparing OLM Bundle Inputs](#preparing-olm-bundle-inputs)
  - [Option 1: Bundle Images](#option-1-bundle-images)
  - [Option 2: Manifest Directories](#option-2-manifest-directories)
- [Output Structure](#output-structure)
- [Creating Custom Configurations](#creating-custom-configurations)
- [Adding Custom Manifests with Scaffolding](#adding-custom-manifests-with-scaffolding)
  - [When to Use Scaffolding](#when-to-use-scaffolding)
  - [Creating Scaffold Templates](#creating-scaffold-templates)
  - [Using Scaffold Templates](#using-scaffold-templates)
  - [Best Practices](#best-practices)
- [Developer Documentation](#developer-documentation)
  - [Architecture Overview](#architecture-overview)
  - [Key Libraries and Dependencies](#key-libraries-and-dependencies)
  - [Code Flow and Implementation Details](#code-flow-and-implementation-details)
  - [Extending the Tool](#extending-the-tool)

## Overview

The tool provides a configurable way to convert OLM bundles to Helm charts by:
- Extracting manifests from OLM bundle container images or loading from manifest directories
- Performing configurable validation and sanity checks
- Templating namespace and image references for Helm
- Generating a complete Helm chart with values.yaml

## Usage

### Basic Usage with Bundle Image

```sh
go run main.go \
  -c configs/example.yaml \
  -b oci://operator-bundle.tgz \
  -o output-chart/
```

### Basic Usage with Manifest Directory

```sh
go run main.go \
  -c configs/example.yaml \
  -b file:///path/to/bundle-manifests/directory \
  -o output-chart/
```

### CLI Reference

```
Usage:
  olm-bundle-repkg [flags]

Flags:
      --chart-description string   Override chart description
      --chart-name string          Override chart name
  -c, --config string              Path to configuration file (YAML) [REQUIRED]
      --crd-chart-name string      Name for a separate CRD chart (if not specified, CRDs will be included in the main chart)
  -h, --help                       help for olm-bundle-repkg
  -b, --olm-bundle string          OLM bundle input with protocol prefix: oci:// for bundle images, file:// for manifest directories [REQUIRED]
  -o, --output-dir string          Output directory for the generated Helm Charts [REQUIRED]
  -s, --scaffold-dir string        Directory containing additional templates to be added to the generated Helm Chart
  -l, --source-link string         Link to the Bundle image that is repackaged
```

## Input Formats

The tool supports two input formats:

### 1. OLM Bundle Container Images (tar.gz)

Traditional OLM bundle images packaged as tar.gz files. These can be created by pulling and saving container images:

```sh
podman pull quay.io/operator/bundle:v1.0.0
podman save -o bundle.tgz quay.io/operator/bundle:v1.0.0
go run main.go -c configs/example.yaml -b oci://bundle.tgz -o helm-chart/
```

### 2. Bundle Manifest Directories

Directories containing pre-extracted OLM bundle manifests. This is useful when:
- Bundle images are not publicly available
- You have access to manifest repositories
- Working with extracted manifests from bundle repositories

Example directory structure:
```
manifests/
├── ClusterServiceVersion.yaml
├── CustomResourceDefinition_resource1.yaml  
├── CustomResourceDefinition_resource2.yaml
├── RoleBinding.yaml
├── Role.yaml
└── ServiceAccount.yaml
```

Usage:
```sh
go run main.go -c configs/example.yaml -b file:///path/to/manifests/ -o helm-chart/
```

## Configuration System

The tool uses YAML configuration files to customize the conversion process for different types of operators. A configuration file is required.

#### Environment Variable Patterns

The tool can identify operand image environment variables using either prefixes or suffixes (or both):

- **Prefixes** (`operandImageEnvPrefixes`): Matches environment variables that start with specified patterns
  - Example: `OPERAND_IMAGE_` matches `OPERAND_IMAGE_CONTROLLER`, `OPERAND_IMAGE_WEBHOOK`
- **Suffixes** (`operandImageEnvSuffixes`): Matches environment variables that end with specified patterns  
  - Example: `_IMAGE` matches `CONTROLLER_IMAGE`, `WEBHOOK_IMAGE`

Both patterns can be used simultaneously, and the tool will process all matching environment variables.

### Configuration File Format

```yaml
# Chart metadata
chartName: my-operator
chartDescription: A Helm chart for my-operator

# Operator deployment identification
operatorDeploymentNames:
  - my-operator
  - my-controller

# Alternative: use label selectors
operatorDeploymentSelector:
  app.kubernetes.io/component: controller

# Image environment variable patterns (can use prefixes, suffixes, or both)
operandImageEnvPrefixes:
  - OPERAND_IMAGE_
  - RELATED_IMAGE_
operandImageEnvSuffixes:
  - _IMAGE
  - _CONTAINER

# Image parameterization options (all optional)
imageRegistryParam: imageRegistry     # Parameterize registry part
imageRepositoryParam: imageRepository # Parameterize repository part (includes full path)
imageTagParam: imageTag               # Parameterize tag part (mutually exclusive with imageDigestParam)
imageDigestParam: imageDigest         # Parameterize digest part (mutually exclusive with imageTagParam)
perImageCustomization: false          # If true, each image gets individual parameters with suffixes (default: false)

# Validation requirements
requiredEnvVarPrefixes:
  - OPERAND_IMAGE_

requiredResources:
  - Deployment
  - ServiceAccount

# Annotation cleanup patterns
annotationPrefixesToRemove:
  - openshift.io
  - operatorframework.io
  - olm
```

### Image Reference Formats

The tool supports both tag-based and digest-based image references:

**Tag-based format**: `registry.io/repo/image:v1.0.0`  
**Digest-based format**: `registry.io/repo/image@sha256:abc123def456...`

#### Image Parameterization Modes

The tool can operate in two mutually exclusive modes for image parameterization:

1. **Tag Mode** (`imageTagParam`): Converts all image references to use tags
   ```yaml
   imageTagParam: imageTag
   # Results in: registry.io/repo/image:{{ .Values.imageTag }}
   ```

2. **Digest Mode** (`imageDigestParam`): Converts all image references to use SHA256 digests
   ```yaml
   imageDigestParam: imageDigest  
   # Results in: registry.io/repo/image@sha256:{{ .Values.imageDigest }}
   ```

#### Format Conversion

The configuration drives the output format regardless of the input format:
- **Digest input + Tag config**: `registry.io/repo/image@sha256:abc123` → `registry.io/repo/image:{{ .Values.imageTag }}`
- **Tag input + Digest config**: `registry.io/repo/image:v1.0.0` → `registry.io/repo/image@sha256:{{ .Values.imageDigest }}`

This ensures consistent image reference formats in the generated Helm chart based on your deployment requirements.

#### Per-Image Customization

By default, all image references share the same parameter names (global parameterization). However, you can enable per-image customization to generate unique parameters for each image:

```yaml
perImageCustomization: true
imageRegistryParam: imageRegistry
imageTagParam: imageTag
```

**Global Parameterization (default behavior):**
```yaml
# All images use the same parameters
containers:
  - image: "{{ .Values.imageRegistry }}/controller:{{ .Values.imageTag }}"
  - image: "{{ .Values.imageRegistry }}/webhook:{{ .Values.imageTag }}"
```

**Per-Image Customization:**
```yaml
# Each image gets unique parameters with suffixes
containers:
  - image: "{{ .Values.imageRegistryManager }}/controller:{{ .Values.imageTagManager }}"
  - image: "{{ .Values.imageRegistryWebhook }}/webhook:{{ .Values.imageTagWebhook }}"
```

**When to use per-image customization:**
- Different containers need different registries or tags
- Fine-grained control over individual images is required
- You want to override specific container images independently

**Parameter suffix generation:**
- **Container images**: Uses the container name (e.g., `manager` → `Manager`)
- **Environment variable images (prefix matching)**: Uses the env var name without prefix (e.g., `OPERAND_IMAGE_CONTROLLER` → `Controller`)
- **Environment variable images (suffix matching)**: Uses the env var name without suffix (e.g., `CONTROLLER_IMAGE` → `Controller`)

### Example Configuration

The tool includes an example configuration in the `configs/` directory.


## Preparing OLM Bundle Inputs

### Option 1: Bundle Images

1. **Find the OLM bundle image** for your operator (usually available in operator repositories or registries)

2. **Pull and save the bundle image**:
   ```sh
   podman pull --arch x86_64 $BUNDLE_IMAGE
   podman save -o operator-bundle.tgz $BUNDLE_IMAGE
   ```

3. **Use the tool** with appropriate configuration:
   ```sh
   go run main.go -c configs/example.yaml -b oci://operator-bundle.tgz -o helm-chart/
   ```

### Option 2: Manifest Directories

1. **Obtain the manifest files** from bundle repositories, CI systems, or extracted bundles

2. **Organize manifests in a directory** (flat structure with all YAML files)

3. **Use the tool** with appropriate configuration:
   ```sh
   go run main.go -c configs/example.yaml -b file:///path/to/manifests/ -o helm-chart/
   ```

## Output Structure

The generated Helm chart will have the following structure:

```
helm-chart/
├── Chart.yaml          # Chart metadata
├── values.yaml         # Default values with parameterized images
├── templates/          # Kubernetes manifests
│   ├── deployment.yaml
│   ├── serviceaccount.yaml
│   └── ...
└── crds/              # Custom Resource Definitions
    └── ...
```

## Creating Custom Configurations

1. **Start with the example config**: Copy the example configuration from `configs/`
2. **Modify for your operator**: Adjust the patterns to match your operator's structure
3. **Test the configuration**: Run the tool with your operator bundle
4. **Iterate**: Refine the configuration based on the results

## Adding Custom Manifests with Scaffolding

The scaffolding feature allows you to include additional Kubernetes manifests in your generated Helm chart that are not part of the original OLM bundle. This is particularly useful when you need to supplement the operator with custom resources, additional RBAC rules, or deployment-specific configurations.

### When to Use Scaffolding

Scaffolding is helpful in several scenarios:

**Missing Dependencies**: When the OLM bundle doesn't include all the resources needed for your specific deployment. Some examples are:
- Additional ServiceAccounts with specific annotations
- Custom RBAC policies for your environment

**Environment-Specific Resources**: When you need to add resources that are specific to your deployment environment, for example:
- ConfigMaps with environment-specific settings
- Secrets for external service integration
- Monitoring and observability resources (ServiceMonitors, PodMonitors)
- Backup and restore jobs
- Custom metrics exporters

### Creating Scaffold Templates

1. **Create a scaffold directory** with your additional manifests:

```
scaffold/
├── additional-rbac.yaml
├── monitoring.yaml
├── network-policy.yaml
└── debug-tools.yaml
```

2. **Write standard Kubernetes manifests** with Helm templating:

```yaml
# scaffold/additional-rbac.yaml
apiVersion: v1
kind: ServiceAccount
metadata:
  name: {{ include "chart.fullname" . }}-viewer
  namespace: {{ .Release.Namespace }}
  annotations:
    description: "Additional service account for read-only access"
---
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: {{ include "chart.fullname" . }}-viewer
  namespace: {{ .Release.Namespace }}
rules:
- apiGroups: [""]
  resources: ["pods", "configmaps"]
  verbs: ["get", "list", "watch"]
```

```yaml
# scaffold/monitoring.yaml
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  name: {{ include "chart.fullname" . }}-metrics
  namespace: {{ .Release.Namespace }}
spec:
  selector:
    matchLabels:
      app.kubernetes.io/name: {{ include "chart.name" . }}
  endpoints:
  - port: metrics
    interval: 30s
    path: /metrics
```

3. **Use Helm templating features** in your scaffold manifests:
   - `{{ .Release.Namespace }}` for namespace templating
   - `{{ .Values.* }}` for configurable values
   - `{{ include "chart.name" . }}` for chart name consistency
   - Standard Helm helper functions

### Using Scaffold Templates

Include scaffold templates using the `--scaffold-dir` flag:

```sh
# With bundle image
go run main.go \
  -c configs/my-operator.yaml \
  -b oci://operator-bundle.tgz \
  -s scaffold/ \
  -o helm-chart/

# With manifest directory
go run main.go \
  -c configs/my-operator.yaml \
  -b file:///path/to/manifests/ \
  -s scaffold/ \
  -o helm-chart/
```

The scaffold manifests will be:
- Loaded and validated during chart generation
- Merged with the operator manifests from the OLM bundle
- Included in the final Helm chart's `templates/` directory
- Subject to the same Helm templating processing

---

# Developer Documentation

## Architecture Overview

The tool follows a pipeline architecture with six main phases:

1. **Configuration**: Load and validate configuration settings with CLI overrides
2. **Input Detection**: Automatically detect whether input is a container image or manifest directory
3. **Extraction**: Load and extract manifests from OLM bundle container image or manifest directory
4. **Validation**: Perform configurable sanity checks on extracted manifests
5. **Customization**: Transform manifests for Helm templating using configuration-driven customizers
6. **Chart Generation**: Build and save Helm chart with configured metadata and scaffold templates

## Key Libraries and Dependencies

### Container Registry Operations
- **`github.com/google/go-containerregistry/pkg/crane`**: Load container images from tar.gz files
- **`github.com/google/go-containerregistry/pkg/v1`**: Container image manipulation

### OLM Bundle Processing
- **`github.com/operator-framework/api/pkg/operators/v1alpha1`**: OLM types (ClusterServiceVersion, etc.)
- **`pkg.package-operator.run/cardboard/kubeutils/kubemanifests`**: Kubernetes manifest loading from bytes

### Helm Chart Generation
- **`helm.sh/helm/v3/pkg/chart`**: Helm chart structures
- **`helm.sh/helm/v3/pkg/chartutil`**: Chart saving utilities

### Kubernetes Object Manipulation
- **`k8s.io/apimachinery/pkg/apis/meta/v1/unstructured`**: Dynamic Kubernetes object handling
- **`k8s.io/api/*`**: Typed Kubernetes API objects (appsv1, rbacv1, etc.)
- **`sigs.k8s.io/yaml`**: YAML marshaling/unmarshaling

### Internal Components
- **`internal/rukpak`**: Borrowed from [operator-controller](https://github.com/operator-framework/operator-controller/tree/main/internal/rukpak), converts OLM registry v1 to plain manifests
- **`internal/olm`**: OLM bundle extraction logic for both container images and directories
- **`internal/customize`**: Manifest customization, configuration management, validation, and scaffolding

## Code Flow and Implementation Details

### Phase 1: Configuration (`internal/customize/config.go`)

```go
// Configuration loading workflow:
1. Load configuration from YAML file (required)
2. Apply default values for any missing fields
3. Apply CLI overrides for chart name and description
4. Validate configuration completeness and consistency
5. Return validated configuration for use in subsequent phases
```

### Phase 2: Input Detection (`main.go`)

```go
// Input type detection workflow:
1. Use os.Stat() to check if input path is a file or directory
2. Route to appropriate extraction method based on file type
3. Handle errors for non-existent or inaccessible inputs
```

### Phase 3: Bundle Extraction (`internal/olm/extract.go`)

The tool supports two extraction methods with unified output format:

#### Container Image Extraction (`ExtractOLMBundleImage`)
```go
// ExtractOLMBundleImage workflow:
1. Load container image from tar.gz file using crane
2. Extract tar layers into in-memory filesystem (fstest.MapFS)
3. Use rukpak/convert.RegistryV1ToPlain() to convert OLM bundle to static manifests
4. Parse converted manifests into Kubernetes unstructured objects
5. Return objects and extracted registry metadata
```

#### Directory Extraction (`ExtractOLMManifestsDirectory`)
```go  
// ExtractOLMManifestsDirectory workflow:
1. Walk directory for YAML/YML files using filepath.WalkDir()
2. Parse each file as Kubernetes manifests using kubemanifests
3. Categorize objects (ClusterServiceVersion, CRDs, Others) into RegistryV1 structure
4. Call rukpak/convert.Convert() to resolve CSV into static Kubernetes objects
5. Handle mixed structured/unstructured objects from conversion
6. Return unified object list and registry metadata
```

### Phase 4: Validation (`internal/customize/validation.go`)

```go
// SanityCheck workflow:
1. Validate required resources are present (configurable via RequiredResources)
2. Validate operator deployments exist and match configuration patterns
3. Validate required environment variables in operator deployments
4. Aggregate all validation errors using k8s.io/apimachinery/pkg/util/errors
5. Return comprehensive error with all issues found
```

### Phase 5: Customization (`internal/customize/customize.go`)

```go
// CustomizeManifests workflow:
1. Apply built-in customizers in sequence:
   - parameterizeNamespace: Replace namespaces with {{ .Release.Namespace }}
   - parameterizeRoleBindingSubjectsNamespace: Template ServiceAccount namespaces
   - parameterizeClusterRoleBindingSubjectsNamespace: Template ClusterRole subject namespaces
   - createParameterizeOperandsImageRegistries: Template operand image registries
   - createParameterizeDeployment: Template operator deployment image registries
   - createAnnotationCleaner: Remove unwanted annotations
2. Apply any custom customizers from configuration
3. Collect parameters from all customizers for values.yaml generation
4. Return customized manifests and nested parameter map
```

#### Image Reference Processing (`internal/customize/utils.go`)

The tool includes image reference processing that supports both tag and digest formats:

```go
// Image reference parsing supports multiple formats:
- registry.io/repo/image:tag
- registry.io/repo/image@sha256:digest
- registry.io/image:tag
- image:tag (no registry)

// parameterizeImageComponents workflow:
1. Parse image reference using regex: ^(?:([^/]+)/)?([^:@]+)(?::(.+)|@sha256:([a-f0-9]+))?$
2. Extract components: registry, repository, tag, digest
3. Apply parameterization based on configuration:
   - ImageRegistryParam: Replace registry with {{ .Values.registry }}
   - ImageRepositoryParam: Replace repository with {{ .Values.repository }} (includes full path)
   - ImageTagParam: Force tag format, clear digest
   - ImageDigestParam: Force digest format, clear tag
4. Rebuild image reference in target format
5. Return templated image reference and parameter map
```

**Key Features**:
- **Format Conversion**: Configuration drives output format regardless of input format
- **Component Templating**: Registry, repository, tag/digest can be independently parameterized
- **Mutual Exclusivity**: ImageTagParam and ImageDigestParam cannot be used together
- **Robust Parsing**: Handles various image reference formats with simplified 3-component structure

### Phase 6: Chart Generation (`main.go`)

```go
// Chart generation workflow:
1. Load scaffold templates using LoadScaffoldTemplates() (optional)
2. Combine customized manifests with scaffold templates
3. Create Chart.yaml with metadata extracted from ClusterServiceVersion
4. Generate values.yaml from collected parameters using nested structure
5. Organize manifests into templates/ and crds/ directories based on Kind
6. Save complete Helm chart using chartutil.Save()
```

### Scaffold System (`internal/customize/scaffold.go`)

```go
// LoadScaffoldTemplates workflow:
1. If scaffoldDir is provided, validate directory exists
2. Walk directory for YAML/YML files using filepath.Walk()
3. Parse each file into unstructured.Unstructured objects
4. Return scaffold manifests for inclusion in final chart
```

## Extending the Tool

### Adding New Customizers
1. Implement `Customizer` function signature: `func(unstructured.Unstructured) (unstructured.Unstructured, map[string]string, error)`
2. Add configuration options to `BundleConfig` struct if needed
3. Create factory function following pattern `createCustomizerName(config *BundleConfig) Customizer`
4. Add to customizer list in `CustomizeManifests()` function
5. Update configuration documentation and examples

### Adding New Validation Rules
1. Implement validation function in `internal/customize/validation.go`
2. Add configuration options to `BundleConfig` struct for validation parameters
3. Call validation function from `SanityCheck()` and append errors to slice
4. Update `BundleConfig.Validate()` method if new required fields are added
5. Update configuration schema and documentation

### Adding Configuration Options
1. Add new fields to `BundleConfig` struct with appropriate YAML tags
2. Update `DefaultConfig()` function with sensible defaults
3. Update `applyDefaults()` method to handle new fields
4. Add validation logic in `Validate()` method if needed
5. Update configuration file documentation and examples

### Manifest Override Path Syntax and Limitations

The manifest override system uses a JSONPath-like syntax to navigate and modify Kubernetes object fields. Understanding the supported syntax and its limitations is important for creating effective manifest overrides.

#### Supported Path Syntax

The path parser supports three types of path expressions:

**1. Simple Nested Fields**
```
metadata.name
spec.template.metadata.labels
```

**2. Array Indexing (by numeric index)**
```
spec.containers[0].image
spec.containers[1].name
```

**3. Array Filtering (by field value)**
```
spec.containers[name=manager].image
spec.containers[name=manager].env[name=WATCH_NAMESPACE].value
```

#### Path Parsing Limitations

**Dots in Filter Values**

Filter values **cannot contain dots** (`.`) because the parser splits the path on dots before applying regex matching. This is a fundamental limitation of the current implementation.

❌ **Does NOT work:**
```
spec.containers[name=manager.v1].image
# Parsed as: ["spec", "containers[name=manager", "v1]", "image"]
```

✅ **Works:**
```
spec.containers[name=manager-v1].image
# Parsed as: ["spec", "containers[name=manager-v1]", "image"]
```

**Brackets in Filter Values**

Filter values containing brackets will cause regex match failures.

❌ **Does NOT work:**
```
spec.containers[name=manager[0]].image
```

**Supported Special Characters in Filter Values**

Filter values can contain:
- Hyphens: `manager-v1`
- Underscores: `WATCH_NAMESPACE`
- Alphanumeric characters: `abc123`

Filter values cannot contain:
- Dots: `manager.v1`
- Brackets: `manager[0]`

#### Implementation Details

The parser operates in two stages:
1. **Split on dots**: `strings.Split(path, ".")` splits the path into segments
2. **Regex matching**: Each segment is matched against array indexing and filtering patterns
   - Array index pattern: `^(\w+)\[(\d+)\]$`
   - Array filter pattern: `^(\w+)\[(\w+)=([^\]]+)\]$`

Because splitting happens first, any dots within `[...]` brackets are treated as path separators, breaking the filter syntax.

#### Workarounds

If you need to match resources with dots in their names:
- Use array indexing instead: `containers[0]` instead of `containers[name=manager.v1]`
- Ensure resource names don't contain dots when possible
- Use alternative naming conventions (hyphens instead of dots)
