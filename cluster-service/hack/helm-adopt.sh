#!/bin/bash

# helm-adopt.sh - Helm Resource Adoption Script
#
# DESCRIPTION:
#   This script adopts existing Kubernetes resources into Helm management by adding
#   the necessary Helm labels and annotations. It's used to transition from manually
#   deployed resources to Helm-managed resources without recreating them. It can also
#   be used to adopt resources that are managed by another Helm release.
#
# HOW IT WORKS:
#   1. Renders the provided Helm chart templates to discover expected resources
#   2. For each resource in the rendered templates:
#      - Checks if the resource exists in the cluster
#      - Verifies if it already has correct Helm metadata
#      - Patches the resource with Helm management labels/annotations if needed
#   3. Skips resources that don't exist or are already properly adopted
#   4. Handles API version disambiguation for CRDs with same kind names
#
# USAGE:
#   ./helm-adopt.sh <release_name> <chart_path> <namespace> [helm_flags...]
#
# PARAMETERS:
#   release_name    - Name of the Helm release to adopt resources into
#   chart_path      - Path to the Helm chart directory or package
#   namespace       - Kubernetes namespace where resources are located
#   helm_flags      - Optional: Any additional flags to pass to helm template
#
# EXAMPLES:
#   ./helm-adopt.sh my-app ./charts/my-app default
#   ./helm-adopt.sh mce-operator /path/to/charts/mce multicluster-engine
#   ./helm-adopt.sh my-app ./charts/my-app default --set image.tag=v1.0 -f values-prod.yaml
#
# DEPENDENCIES:
#   - helm (for rendering chart templates)
#   - kubectl (for cluster operations)
#   - yq (for YAML/JSON processing)
#
# EXIT CODES:
#   0 - Success (all resources processed successfully)
#   1 - Error (missing dependencies, invalid parameters, or processing failure)
#
# HELM METADATA ADDED:
#   Labels:
#     app.kubernetes.io/managed-by: Helm
#   Annotations:
#     meta.helm.sh/release-name: <release_name>
#     meta.helm.sh/release-namespace: <namespace>

set -o errexit
set -o nounset
set -o pipefail

RELEASE_NAME=$1
CHART_PATH=$2
RELEASE_NAMESPACE=$3
shift 3

# Purpose: Processes a single Kubernetes resource for Helm adoption by patching
#          ownership annotations and labels.
# Parameters: a JSON object representing the resource from rendered templates.
# Returns: 0 on success, 1 on error.
adopt_resource() {
  local obj="$1"

  # Validate input parameter
  if [[ -z "$obj" ]]; then
    echo "[ERROR] adopt_resource: missing resource object parameter" >&2
    return 1
  fi

  local kind apiVersion name namespace
  if ! kind=$(echo "$obj" | yq eval '.kind' - 2>/dev/null) || [[ "$kind" == "null" ]]; then
    echo "[ERROR] Failed to extract kind from resource object" >&2
    return 1
  fi
  if ! apiVersion=$(echo "$obj" | yq eval '.apiVersion' - 2>/dev/null) || [[ "$apiVersion" == "null" ]]; then
    echo "[ERROR] Failed to extract apiVersion from resource object" >&2
    return 1
  fi
  if ! name=$(echo "$obj" | yq eval '.metadata.name' - 2>/dev/null) || [[ "$name" == "null" ]]; then
    echo "[ERROR] Failed to extract name from resource object" >&2
    return 1
  fi
  namespace=$(echo "$obj" | yq eval '.metadata.namespace // ""' - 2>/dev/null || echo "")

  # Build the full resource type with API version for kubectl commands
  # This handles disambiguation when multiple CRDs have the same kind name
  local resource_type
  if [[ "$apiVersion" == "v1" ]] || [[ "$apiVersion" =~ ^apps/ ]] || [[ "$apiVersion" =~ ^rbac\.authorization\.k8s\.io/ ]] || [[ "$apiVersion" =~ ^apiextensions\.k8s\.io/ ]]; then
    # For core Kubernetes resources, use just the kind (e.g., "Pod", "Service")
    resource_type="$kind"
  else
    # For CRDs, use kind.apiVersion format (e.g., "ServiceMonitor.monitoring.coreos.com/v1")
    resource_type="$kind.$apiVersion"
  fi

  # Skip resource types that Helm doesn't manage (e.g., Namespaces)
  if [[ "$kind" == "Namespace" ]]; then
    echo "[SKIP] $kind/$name"
    return 0
  fi

  # Check if resource exists in the cluster and retrieve its current metadata
  local current_resource
  if [[ -n "$namespace" ]]; then
    current_resource=$(kubectl get "$resource_type" "$name" -n "$namespace" -o json 2>/dev/null || echo "")
  else
    current_resource=$(kubectl get "$resource_type" "$name" -o json 2>/dev/null || echo "")
  fi

  # Silently skip if resource doesn't exist in the cluster
  # (This allows the script to be idempotent when run multiple times)
  if [[ -z "$current_resource" ]]; then
    return 0
  fi

  # Extract current Helm metadata to check if resource is already adopted
  local current_managed_by current_release_name current_release_namespace
  current_managed_by=$(echo "$current_resource" | yq eval '.metadata.labels."app.kubernetes.io/managed-by" // ""' - 2>/dev/null || echo "")
  current_release_name=$(echo "$current_resource" | yq eval '.metadata.annotations."meta.helm.sh/release-name" // ""' - 2>/dev/null || echo "")
  current_release_namespace=$(echo "$current_resource" | yq eval '.metadata.annotations."meta.helm.sh/release-namespace" // ""' - 2>/dev/null || echo "")

  # Check if all required metadata is already present and correct
  if [[ "$current_managed_by" == "Helm" ]] && \
     [[ "$current_release_name" == "$RELEASE_NAME" ]] && \
     [[ "$current_release_namespace" == "$RELEASE_NAMESPACE" ]]; then
    echo "[SKIP] $kind/$name (already adopted)"
    return 0
  fi

  # Resource exists but needs adoption - prepare patch
  echo "adopt $kind/$name (apiVersion: $apiVersion) for $RELEASE_NAME in $RELEASE_NAMESPACE"
  local patch
  patch=$(cat <<EOF
{
  "metadata": {
    "labels": {
      "app.kubernetes.io/managed-by": "Helm"
    },
    "annotations": {
      "meta.helm.sh/release-name": "${RELEASE_NAME}",
      "meta.helm.sh/release-namespace": "${RELEASE_NAMESPACE}"
    }
  }
}
EOF
  )

  # Apply the patch
  if [[ -n "$namespace" ]]; then
    echo "[PATCH] kubectl patch $resource_type $name -n $namespace --type merge -p '$patch'"
    kubectl patch "$resource_type" "$name" -n "$namespace" --type merge -p "$patch"
  else
    echo "[PATCH] kubectl patch $resource_type $name --type merge -p '$patch'"
    kubectl patch "$resource_type" "$name" --type merge -p "$patch"
  fi
}

# Use helm template to discover what resources the chart expects to create
echo "[INFO] Rendering chart templates from ${CHART_PATH} ..."

# Filter flags to only include those compatible with helm template
TEMPLATE_ARGS=()
SKIP_NEXT=false

for arg in "$@"; do
  if [[ "$SKIP_NEXT" == true ]]; then
    TEMPLATE_ARGS+=("$arg")
    SKIP_NEXT=false
    continue
  fi

  case $arg in
    # Flags that take a following argument
    --set|--set-string|--set-file|--values|-f|--api-versions)
      TEMPLATE_ARGS+=("$arg")
      SKIP_NEXT=true
      ;;
    # Flags that combine flag=value in one argument
    --set=*|--set-string=*|--set-file=*|--values=*|-f=*|--api-versions=*)
      TEMPLATE_ARGS+=("$arg")
      ;;
    # Boolean flags
    --include-crds|--skip-crds|--validate|--no-hooks)
      TEMPLATE_ARGS+=("$arg")
      ;;
    # Ignore deployment-specific flags like --wait, --timeout, etc.
  esac
done

if [[ ${#TEMPLATE_ARGS[@]} -gt 0 ]]; then
  echo "[INFO] Using template flags: ${TEMPLATE_ARGS[*]}"
fi

if ! RENDERED=$(helm template "$RELEASE_NAME" "$CHART_PATH" -n "$RELEASE_NAMESPACE" "${TEMPLATE_ARGS[@]+"${TEMPLATE_ARGS[@]}"}" 2>&1); then
  echo "[ERROR] Failed to render chart templates: $RENDERED" >&2
  exit 1
fi

if [[ -z "$RENDERED" ]]; then
  echo "[ERROR] No resources found in rendered templates"
  exit 1
fi

# Convert YAML to JSON and filter for valid resource objects
# Process each resource object individually (-I=0 renders one object per line)
echo "$RENDERED" | yq eval -o=json -I=0 '. | select(.kind != null)' - | while read -r obj; do
  adopt_resource "$obj"
done
