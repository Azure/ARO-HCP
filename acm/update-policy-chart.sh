#!/bin/bash

set -o errexit
set -o nounset
set -o pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(dirname "$SCRIPT_DIR")"

TMP_DIR=$(mktemp -d)

policy_helm_charts_base_dir="$PROJECT_ROOT/acm/deploy/helm/multicluster-engine-config/charts/policy"
policy_helm_charts_dir="$policy_helm_charts_base_dir/charts"

# Check required environment variables
if [[ -z "${ACM_VERSION:-}" ]]; then
  echo "Error: ACM_VERSION environment variable must be set."
  exit 1
fi

# Validate ACM_VERSION format (must be a.b.c, where a, b, c are numbers(e.g., 2.14.3).)
if ! [[ "$ACM_VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "Error: ACM_VERSION must be in the format a.b.c (e.g., 2.14.3) but got $ACM_VERSION."
  exit 1
fi

echo "# The ACM version is $ACM_VERSION."

if [[ -z "${ACM_OPERATOR_BUNDLE_IMAGE:-}" ]]; then
  echo "Error: ACM_OPERATOR_BUNDLE_IMAGE environment variable must be set."
  exit 1
fi

echo "# The ACM operator bundle image is $ACM_OPERATOR_BUNDLE_IMAGE."
echo "# Start updating the Policy helm chart."

# Extract a.b as BRANCH
branch=$(echo "$ACM_VERSION" | awk -F. '{print $1"."$2}')

echo "## Sync the Policy helm chart from the branch release-$branch of multiclusterhub-operator repo."
git clone --depth 1 --branch "release-$branch" https://github.com/stolostron/multiclusterhub-operator.git "$TMP_DIR/multiclusterhub-operator"

echo "## Update the CRDs."
crds_dir="$TMP_DIR/multiclusterhub-operator/pkg/templates/crds"
crd_files=(
  "$crds_dir/grc/policy.open-cluster-management.io_placementbindings.yaml"
  "$crds_dir/grc/policy.open-cluster-management.io_policies.yaml"
  "$crds_dir/grc/policy.open-cluster-management.io_policyautomations.yaml"
  "$crds_dir/grc/policy.open-cluster-management.io_policysets.yaml"
  "$crds_dir/cluster-lifecycle/agent.open-cluster-management.io_klusterletaddonconfigs_crd.yaml"
  "$crds_dir/multicloud-operators-subscription/apps.open-cluster-management.io_placementrules_crd_v1.yaml"
)

for file in "${crd_files[@]}"; do
 if [[ -f "$file" ]]; then
    cp "$file" "$policy_helm_charts_base_dir/crds/"
  else
    echo "Error: CRD file not found: $file"
    exit 1
  fi
done

echo "## Update the grc sub-chart."
grc_chart_dir="$TMP_DIR/multiclusterhub-operator/pkg/templates/charts/toggle/grc"
grc_files=(
  "grc-clusterrole.yaml"
  "grc-policy-addon-role.yaml"
  "grc-policy-addon-clusterrole.yaml"
  "grc-role.yaml"
)

for file in "${grc_files[@]}"; do 
  if [[ -f "$grc_chart_dir/templates/$file" ]]; then
    cp "$grc_chart_dir/templates/$file" "$policy_helm_charts_dir/grc/templates/" 
    sed -E '/^[[:space:]]*(chart:|release:|app.kubernetes.io)/d' "$policy_helm_charts_dir/grc/templates/$file" > tmp && mv tmp "$policy_helm_charts_dir/grc/templates/$file"
  else
    echo "Error: the grc file not found: $grc_chart_dir/templates/$file"
    exit 1
  fi
done

# TODO: remove the namespace in the clusterrole and clusterrolebinding files of the upstream helm chart.
grc_clusterrole_files=(
  "$policy_helm_charts_dir/grc/templates/grc-clusterrole.yaml"
  "$policy_helm_charts_dir/grc/templates/grc-policy-addon-clusterrole.yaml"
)
for file in "${grc_clusterrole_files[@]}"; do 
  if [[ -f "$file" ]]; then
    sed -E '/^[[:space:]]*(namespace:)/d' "$file" > tmp && mv tmp "$file"
  else
    echo "Error: the grc clusterole file not found: $file"
    exit 1
  fi
done

echo "## Update the cluster-lifecycle sub-chart."
cluster_lifecycle_dir="$TMP_DIR/multiclusterhub-operator/pkg/templates/charts/toggle/cluster-lifecycle"
cluster_lifecycle_files=(
  "$cluster_lifecycle_dir/templates/klusterlet-addon-role.yaml"
  "$cluster_lifecycle_dir/templates/klusterlet-addon-role_binding.yaml" 
  "$cluster_lifecycle_dir/templates/klusterlet-addon-deployment.yaml"
)
for file in "${cluster_lifecycle_files[@]}"; do 
  if [[ -f "$file" ]]; then
    cp "$file" "$policy_helm_charts_dir/cluster-lifecycle/templates/"
  else
    echo "Error: the clc file not found: $file"
    exit 1
  fi
done

# the Values.global.registryOverride is not defined in the upstream helm chart so need override here.
sed -i.bak 's|^\([[:space:]]*image:[[:space:]]*\)"{{ .Values.global.imageOverrides.klusterlet_addon_controller }}"|\1"{{ .Values.global.registryOverride }}/{{ .Values.global.imageOverrides.klusterlet_addon_controller }}"|' "$policy_helm_charts_dir/cluster-lifecycle/templates/klusterlet-addon-deployment.yaml"
rm -f "$policy_helm_charts_dir/cluster-lifecycle/templates/klusterlet-addon-deployment.yaml.bak"


echo "## Update version in policy chart."
chart_files=(
  "$policy_helm_charts_base_dir/Chart.yaml"
  "$policy_helm_charts_dir/grc/Chart.yaml"
  "$policy_helm_charts_dir/cluster-lifecycle/Chart.yaml"
)

for file in "${chart_files[@]}"; do
  if [[ -f "$file" ]]; then
    sed -E "s/version: .*/version: v$ACM_VERSION/" "$file" > tmp && mv tmp "$file"
    sed -E "s/appVersion: .*/appVersion: v$ACM_VERSION/" "$file" > tmp && mv tmp "$file"
  else
    echo "Error: the chart file not found: $file"
    exit 1
  fi
done

echo "# The Policy helm chart is updated."

# Check for podman or docker
if command -v podman &> /dev/null; then
  OCI_TOOL="podman"
elif command -v docker &> /dev/null; then
  OCI_TOOL="docker"
else
  echo "Error: Neither podman nor docker is installed."
  exit 1
fi

echo "# Start updating the images in the values.yaml."
echo "## Pull the acm-operator-bundle image $ACM_OPERATOR_BUNDLE_IMAGE."
if [ "$OCI_TOOL" = "podman" ]; then
  $OCI_TOOL pull --arch amd64 "$ACM_OPERATOR_BUNDLE_IMAGE"
else
  $OCI_TOOL pull "$ACM_OPERATOR_BUNDLE_IMAGE"
fi

echo "## Create a temporary container temp_acm_bundle."
if [ "$OCI_TOOL" = "podman" ]; then
  if $OCI_TOOL container exists temp_acm_bundle; then
    $OCI_TOOL rm -f temp_acm_bundle
  fi
  $OCI_TOOL create --arch amd64 --name temp_acm_bundle "$ACM_OPERATOR_BUNDLE_IMAGE"
else
  if $OCI_TOOL ps -a --format '{{.Names}}' | grep -w temp_acm_bundle &>/dev/null; then
    $OCI_TOOL rm -f temp_acm_bundle
  fi
  $OCI_TOOL create --name temp_acm_bundle "$ACM_OPERATOR_BUNDLE_IMAGE" sh
fi

echo "## Copy the contents out of the container to local directory."
image_json_file=$ACM_VERSION.json
$OCI_TOOL cp "temp_acm_bundle:/extras/$image_json_file" "$TMP_DIR/"

echo "## Remove the temporary container temp_acm_bundle."
$OCI_TOOL rm -f temp_acm_bundle

values_file="$policy_helm_charts_base_dir/values.yaml"

echo "## Update the images in the $values_file "
keys=$(yq e '.global.imageOverrides | keys | .[]' "$values_file")
for key in $keys; do
    current_image=$(yq e ".global.imageOverrides.${key}" "$values_file")
    if [[ "$current_image" == "null" ]]; then
      echo "Error: Key not found: $key"
      exit 1
    fi

    image_name=$(jq -r '.[] | select(."image-key" == "'"$key"'") | ."image-name"' "$TMP_DIR/$image_json_file")
    if [[ -z "$image_name" ]]; then
      echo "Error: No image found in the image for key: $key"
      exit 1
    fi
    image_digest=$(jq -r '.[] | select(."image-key" == "'"$key"'") | ."image-digest"' "$TMP_DIR/$image_json_file")
    if [[ -z "$image_digest" ]]; then
      echo "Error: No image digest in the image for key: $key"
      exit 1
    fi

    yq e -i ".global.imageOverrides.${key} = \"${image_name}@${image_digest}\"" "$values_file"
    echo "### Update the image $key tags updated to ${image_name}@${image_digest} in $values_file"
done

rm -rf "$TMP_DIR"

echo "!!! Policy Helm chart is update successfully !!!"
