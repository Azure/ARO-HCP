#!/bin/bash
# Shared provisioning logic for ARO HCP CI environments.
# Called by both aro-hcp-provision-environment and aro-hcp-hypershift-deploy
# step registry steps. Do not run directly.
set -o errexit
set -o nounset
set -o pipefail

: "${CLUSTER_PROFILE_DIR:?CLUSTER_PROFILE_DIR must be set}"
: "${ARO_HCP_DEPLOY_ENV:?ARO_HCP_DEPLOY_ENV must be set}"
: "${SHARED_DIR:?SHARED_DIR must be set}"
: "${ARTIFACT_DIR:?ARTIFACT_DIR must be set}"
: "${LEASED_MSI_MOCK_SP:?LEASED_MSI_MOCK_SP must be set}"
: "${LOCATION:?LOCATION must be set}"

export AZURE_CLIENT_ID; AZURE_CLIENT_ID=$(cat "${CLUSTER_PROFILE_DIR}/client-id")
export AZURE_TENANT_ID; AZURE_TENANT_ID=$(cat "${CLUSTER_PROFILE_DIR}/tenant")
export AZURE_CLIENT_SECRET; AZURE_CLIENT_SECRET=$(cat "${CLUSTER_PROFILE_DIR}/client-secret")
INFRA_SUBSCRIPTION_ID=$(cat "${CLUSTER_PROFILE_DIR}/infra-${ARO_HCP_DEPLOY_ENV}-subscription-id")
export INFRA_SUBSCRIPTION_ID
export DEPLOY_ENV="${ARO_HCP_DEPLOY_ENV}"
export AZURE_TOKEN_CREDENTIALS=prod

az login --service-principal -u "${AZURE_CLIENT_ID}" -p "${AZURE_CLIENT_SECRET}" --tenant "${AZURE_TENANT_ID}" --output none
az account set --subscription "${INFRA_SUBSCRIPTION_ID}"
oc version
kubelogin --version

# --- CI image overrides (optional — set by aro-hcp-provision-environment) ---
# Each *_IMAGE var is a full image ref like "registry/repo@sha256:...".
# When set, we parse them into registry/repo/digest and add them to the
# config overlay so the provisioned environment uses CI-built images.

parse_image() {
    local image="${1}"
    local prefix="${2}"
    local digest repo registry
    digest=$(echo "${image}" | cut -d'@' -f2)
    repo=$(echo "${image}" | cut -d'@' -f1 | cut -d'/' -f2-)
    registry=$(echo "${image}" | cut -d'@' -f1 | cut -d'/' -f1)
    echo "source registry set to ${registry} and repo ${repo} for ${prefix} Image"
    eval "export ${prefix}_DIGEST='${digest}'"
    eval "export ${prefix}_REPOSITORY='${repo}'"
    eval "export ${prefix}_SOURCE_REGISTRY='${registry}'"
}

CI_IMAGE_NAMES=()
CI_IMAGE_REGISTRIES=()

declare -A IMAGE_MAP=(
    [BACKEND]=backend
    [FRONTEND]=frontend
    [ADMIN_API]=adminApi
    [SESSIONGATE]=sessiongate
    [HCP_RECOVERY]=hcpRecovery
    [MGMT_AGENT]=mgmtAgent
    [KUBE_APPLIER]=kubeApplier
)

for prefix in BACKEND FRONTEND ADMIN_API SESSIONGATE HCP_RECOVERY MGMT_AGENT KUBE_APPLIER; do
    var="${prefix}_IMAGE"
    if [[ -n "${!var:-}" ]]; then
        parse_image "${!var}" "${prefix}"
        CI_IMAGE_NAMES+=("${prefix}")
        CI_IMAGE_REGISTRIES+=("$(eval echo "\${${prefix}_SOURCE_REGISTRY}")")
    fi
done

# Set up registries that require oc login
if [[ ${#CI_IMAGE_REGISTRIES[@]} -gt 0 ]]; then
    EXTRA_REGISTRIES="${CI_IMAGE_REGISTRIES[*]}"
    if [[ -n "${USE_OC_LOGIN_REGISTRIES:-}" ]]; then
        USE_OC_LOGIN_REGISTRIES="${USE_OC_LOGIN_REGISTRIES} ${EXTRA_REGISTRIES}"
    else
        USE_OC_LOGIN_REGISTRIES="${EXTRA_REGISTRIES}"
    fi
    export USE_OC_LOGIN_REGISTRIES
    echo "USE_OC_LOGIN_REGISTRIES set to: ${USE_OC_LOGIN_REGISTRIES}"
fi

# --- Build config override ---

OVERRIDE_CONFIG_FILE="${SHARED_DIR}/config-override.yaml"

MSI_MOCK_CLIENT_ID=$(yq ".miMockPool.\"${LEASED_MSI_MOCK_SP}\".clientId" dev-infrastructure/openshift-ci/msi-mock-pool.yaml)
MSI_MOCK_PRINCIPAL_ID=$(yq ".miMockPool.\"${LEASED_MSI_MOCK_SP}\".principalId" dev-infrastructure/openshift-ci/msi-mock-pool.yaml)
MSI_MOCK_CERT_NAME=$(yq ".miMockPool.\"${LEASED_MSI_MOCK_SP}\".certName" dev-infrastructure/openshift-ci/msi-mock-pool.yaml)
echo "MSI mock SP override: ${LEASED_MSI_MOCK_SP} -> clientId=${MSI_MOCK_CLIENT_ID}"

# Start with base overrides (MSI config + VM sizes)
YQ_EXPR="
  .clouds.dev.environments.${DEPLOY_ENV}.defaults.miMockClientId = \"${MSI_MOCK_CLIENT_ID}\" |
  .clouds.dev.environments.${DEPLOY_ENV}.defaults.miMockPrincipalId = \"${MSI_MOCK_PRINCIPAL_ID}\" |
  .clouds.dev.environments.${DEPLOY_ENV}.defaults.miMockCertName = \"${MSI_MOCK_CERT_NAME}\" |
  .clouds.dev.environments.${DEPLOY_ENV}.defaults.svc.aks.systemAgentPool.vmSize = \"Standard_D4ds_v6\" |
  .clouds.dev.environments.${DEPLOY_ENV}.defaults.svc.aks.userAgentPool.vmSize = \"Standard_D8ds_v6\" |
  .clouds.dev.environments.${DEPLOY_ENV}.defaults.svc.aks.infraAgentPool.vmSize = \"Standard_D4ds_v6\" |
  .clouds.dev.environments.${DEPLOY_ENV}.defaults.mgmt.aks.systemAgentPool.vmSize = \"Standard_D4ds_v6\" |
  .clouds.dev.environments.${DEPLOY_ENV}.defaults.mgmt.aks.userAgentPool.vmSize = \"Standard_D16ds_v6\" |
  .clouds.dev.environments.${DEPLOY_ENV}.defaults.mgmt.aks.infraAgentPool.vmSize = \"Standard_D4ds_v6\"
"

# Append CI image overrides if present
for prefix in "${CI_IMAGE_NAMES[@]}"; do
    config_key="${IMAGE_MAP[${prefix}]}"
    digest=$(eval echo "\${${prefix}_DIGEST}")
    repo=$(eval echo "\${${prefix}_REPOSITORY}")
    registry=$(eval echo "\${${prefix}_SOURCE_REGISTRY}")
    YQ_EXPR="${YQ_EXPR} |
  .clouds.dev.environments.${DEPLOY_ENV}.defaults.${config_key}.image.registry = \"${registry}\" |
  .clouds.dev.environments.${DEPLOY_ENV}.defaults.${config_key}.image.repository = \"${repo}\" |
  .clouds.dev.environments.${DEPLOY_ENV}.defaults.${config_key}.image.digest = \"${digest}\""
done

yq eval -n "${YQ_EXPR}" > "${OVERRIDE_CONFIG_FILE}"

# Merge hypershift image overrides if present (written by aro-hcp-hypershift-images-push)
HYPERSHIFT_OVERRIDES="${SHARED_DIR}/hypershift-image-overrides.yaml"
if [[ -f "${HYPERSHIFT_OVERRIDES}" ]]; then
    echo "Merging hypershift image overrides:"
    cat "${HYPERSHIFT_OVERRIDES}"
    yq eval-all 'select(fileIndex == 0) * select(fileIndex == 1)' \
        "${OVERRIDE_CONFIG_FILE}" "${HYPERSHIFT_OVERRIDES}" > "${OVERRIDE_CONFIG_FILE}.tmp"
    mv "${OVERRIDE_CONFIG_FILE}.tmp" "${OVERRIDE_CONFIG_FILE}"
fi

echo "Final override config:"
cat "${OVERRIDE_CONFIG_FILE}"

# --- Provision ---

CONFIG_PROV="${SHARED_DIR}/config-prov.yaml"

finalize() {
    if [[ -s "${CONFIG_PROV}" ]]; then
        mv "${CONFIG_PROV}" "${SHARED_DIR}/config.yaml"
        cp "${SHARED_DIR}/config.yaml" "${ARTIFACT_DIR}/config.yaml"
    fi
}
trap finalize EXIT

unset GOFLAGS
make -o tooling/templatize/templatize entrypoint/Region \
  DEPLOY_ENV="${DEPLOY_ENV}" \
  OVERRIDE_CONFIG_FILE="${OVERRIDE_CONFIG_FILE}" \
  EXTRA_ARGS="--region ${LOCATION} --abort-if-regional-exist" \
  TIMING_OUTPUT=${SHARED_DIR}/steps.yaml.gz \
  ENTRYPOINT_JUNIT_OUTPUT=${ARTIFACT_DIR}/junit_entrypoint.xml \
  CONFIG_OUTPUT=${CONFIG_PROV}

touch "${SHARED_DIR}/provision-complete"
