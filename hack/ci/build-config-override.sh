#!/bin/bash
# Build a config-override.yaml from CI image refs and lease overrides.
# Sourced (not exec'd) by provision-environment.sh and upgrade scripts.
#
# Callers must set:
#   SHARED_DIR   — shared step directory (override file written here)
#   DEPLOY_ENV   — config environment name
#
# Optional env vars consumed:
#   *_IMAGE (BACKEND_IMAGE, FRONTEND_IMAGE, etc.) — digest-based image refs
#   LEASED_MSI_MOCK_SP      — MSI mock SP lease name
#   LEASED_ARM_HELPER_SP    — one or two whitespace-separated ARM helper SP lease names
#   LEASED_MSI_CONTAINERS   — MSI identity container lease (controls MGMT sizing)
#
# Outputs:
#   OVERRIDE_CONFIG_FILE — path to the generated config-override.yaml
#   USE_OC_LOGIN_REGISTRIES — space-separated list of registries needing oc login

: "${SHARED_DIR:?SHARED_DIR must be set}"
: "${DEPLOY_ENV:?DEPLOY_ENV must be set}"

ARM_HELPER_POOL_CATALOG="${ARM_HELPER_POOL_CATALOG:-dev-infrastructure/openshift-ci/arm-helper-pool.yaml}"

# --- CI image overrides (optional) ---
# Each *_IMAGE var is a full digest-based image ref like "registry/repo@sha256:...".
# When set, we parse them into registry/repo/digest and add them to the
# config overlay so the provisioned environment uses CI-built images.

declare -A IMAGE_DIGEST=()
declare -A IMAGE_REPO=()
declare -A IMAGE_REGISTRY=()

declare -A IMAGE_MAP=(
    [BACKEND]=backend
    [FRONTEND]=frontend
    [ADMIN_API]=adminApi
    [SESSIONGATE]=sessiongate
    [HCP_RECOVERY]=hcpRecovery
    [FLEET]=fleet
    [MGMT_AGENT]=mgmtAgent
    [KUBE_APPLIER]=kubeApplier
    [EXPORTER]=customExporter
)

CI_IMAGE_NAMES=()

for prefix in BACKEND FRONTEND ADMIN_API SESSIONGATE HCP_RECOVERY FLEET MGMT_AGENT KUBE_APPLIER EXPORTER; do
    var="${prefix}_IMAGE"
    if [[ -n "${!var:-}" ]]; then
        image="${!var}"
        if [[ "${image}" != *"@"* ]]; then
            echo "ERROR: ${var} must be a digest-based ref (registry/repo@sha256:...), got: ${image}" >&2
            exit 1
        fi
        IMAGE_DIGEST[${prefix}]=$(echo "${image}" | cut -d'@' -f2)
        IMAGE_REPO[${prefix}]=$(echo "${image}" | cut -d'@' -f1 | cut -d'/' -f2-)
        IMAGE_REGISTRY[${prefix}]=$(echo "${image}" | cut -d'@' -f1 | cut -d'/' -f1)
        echo "source registry set to ${IMAGE_REGISTRY[${prefix}]} and repo ${IMAGE_REPO[${prefix}]} for ${prefix} Image"
        CI_IMAGE_NAMES+=("${prefix}")
    fi
done

# Set up registries that require oc login
if [[ ${#CI_IMAGE_NAMES[@]} -gt 0 ]]; then
    REGISTRIES=""
    for prefix in "${CI_IMAGE_NAMES[@]}"; do
        REGISTRIES="${REGISTRIES} ${IMAGE_REGISTRY[${prefix}]}"
    done
    if [[ -n "${USE_OC_LOGIN_REGISTRIES:-}" ]]; then
        USE_OC_LOGIN_REGISTRIES="${USE_OC_LOGIN_REGISTRIES}${REGISTRIES}"
    else
        USE_OC_LOGIN_REGISTRIES="${REGISTRIES# }"
    fi
    export USE_OC_LOGIN_REGISTRIES
    echo "USE_OC_LOGIN_REGISTRIES set to: ${USE_OC_LOGIN_REGISTRIES}"
fi

# --- Build config override ---

OVERRIDE_CONFIG_FILE="${SHARED_DIR}/config-override.yaml"

# Image overrides — use strenv() so values are never parsed as yq syntax
echo "{}" > "${OVERRIDE_CONFIG_FILE}"
if [[ ${#CI_IMAGE_NAMES[@]} -gt 0 ]]; then
    for prefix in "${CI_IMAGE_NAMES[@]}"; do
        config_key="${IMAGE_MAP[${prefix}]}"
        path=".clouds.dev.environments.${DEPLOY_ENV}.defaults.${config_key}.image"
        export _YQ_REG="${IMAGE_REGISTRY[${prefix}]}"
        export _YQ_REPO="${IMAGE_REPO[${prefix}]}"
        export _YQ_DIG="${IMAGE_DIGEST[${prefix}]}"
        yq eval -i \
          "${path}.registry = strenv(_YQ_REG) | ${path}.repository = strenv(_YQ_REPO) | ${path}.digest = strenv(_YQ_DIG)" \
          "${OVERRIDE_CONFIG_FILE}"
    done
    unset _YQ_REG _YQ_REPO _YQ_DIG
fi

# MSI mock SP overrides (if provided)
if [[ -n "${LEASED_MSI_MOCK_SP:-}" ]]; then
  MSI_MOCK_CLIENT_ID=$(yq ".miMockPool.\"${LEASED_MSI_MOCK_SP}\".clientId" dev-infrastructure/openshift-ci/msi-mock-pool.yaml)
  MSI_MOCK_PRINCIPAL_ID=$(yq ".miMockPool.\"${LEASED_MSI_MOCK_SP}\".principalId" dev-infrastructure/openshift-ci/msi-mock-pool.yaml)
  MSI_MOCK_CERT_NAME=$(yq ".miMockPool.\"${LEASED_MSI_MOCK_SP}\".certName" dev-infrastructure/openshift-ci/msi-mock-pool.yaml)
  if [[ -z "${MSI_MOCK_CLIENT_ID}" || "${MSI_MOCK_CLIENT_ID}" == "null" || \
        -z "${MSI_MOCK_PRINCIPAL_ID}" || "${MSI_MOCK_PRINCIPAL_ID}" == "null" || \
        -z "${MSI_MOCK_CERT_NAME}" || "${MSI_MOCK_CERT_NAME}" == "null" ]]; then
    echo "ERROR: LEASED_MSI_MOCK_SP='${LEASED_MSI_MOCK_SP}' not found in dev-infrastructure/openshift-ci/msi-mock-pool.yaml"
    exit 1
  fi
  echo "MSI mock SP override: ${LEASED_MSI_MOCK_SP} -> clientId=${MSI_MOCK_CLIENT_ID}"
  export _YQ_CID="${MSI_MOCK_CLIENT_ID}"
  export _YQ_PID="${MSI_MOCK_PRINCIPAL_ID}"
  export _YQ_CERT="${MSI_MOCK_CERT_NAME}"
  yq -i "
    .clouds.dev.environments.${DEPLOY_ENV}.defaults.miMockClientId = strenv(_YQ_CID) |
    .clouds.dev.environments.${DEPLOY_ENV}.defaults.miMockPrincipalId = strenv(_YQ_PID) |
    .clouds.dev.environments.${DEPLOY_ENV}.defaults.miMockCertName = strenv(_YQ_CERT)
  " "${OVERRIDE_CONFIG_FILE}"
  unset _YQ_CID _YQ_PID _YQ_CERT
else
  echo "No MSI mock SP lease provided, skipping mock SP overrides"
fi

# ARM helper SP overrides (if provided). armHelperFPAPrincipalId deliberately
# remains unchanged: it identifies the mock first-party principal that receives
# the simulated FPA grant, not the ARM helper that authenticates this client.
if [[ -n "${LEASED_ARM_HELPER_SP:-}" ]]; then
  read -r -a ARM_HELPER_LEASES <<< "${LEASED_ARM_HELPER_SP}"
  if [[ ${#ARM_HELPER_LEASES[@]} -gt 2 ]]; then
    echo "ERROR: LEASED_ARM_HELPER_SP must contain at most two lease names, got ${#ARM_HELPER_LEASES[@]}"
    exit 1
  fi

  BACKEND_ARM_HELPER_LEASE="${ARM_HELPER_LEASES[0]}"
  BACKEND_ARM_HELPER_CLIENT_ID=$(yq ".armHelperPool.\"${BACKEND_ARM_HELPER_LEASE}\".clientId" "${ARM_HELPER_POOL_CATALOG}")
  BACKEND_ARM_HELPER_PRINCIPAL_ID=$(yq ".armHelperPool.\"${BACKEND_ARM_HELPER_LEASE}\".principalId" "${ARM_HELPER_POOL_CATALOG}")
  BACKEND_ARM_HELPER_CERT_NAME=$(yq ".armHelperPool.\"${BACKEND_ARM_HELPER_LEASE}\".certName" "${ARM_HELPER_POOL_CATALOG}")
  if [[ -z "${BACKEND_ARM_HELPER_CLIENT_ID}" || "${BACKEND_ARM_HELPER_CLIENT_ID}" == "null" || \
        -z "${BACKEND_ARM_HELPER_PRINCIPAL_ID}" || "${BACKEND_ARM_HELPER_PRINCIPAL_ID}" == "null" || \
        -z "${BACKEND_ARM_HELPER_CERT_NAME}" || "${BACKEND_ARM_HELPER_CERT_NAME}" == "null" ]]; then
    echo "ERROR: Backend ARM helper lease '${BACKEND_ARM_HELPER_LEASE}' not found or incomplete in ${ARM_HELPER_POOL_CATALOG}"
    exit 1
  fi

  echo "Backend ARM helper SP override: ${BACKEND_ARM_HELPER_LEASE} -> clientId=${BACKEND_ARM_HELPER_CLIENT_ID}"
  export _YQ_ARM_HELPER_CID="${BACKEND_ARM_HELPER_CLIENT_ID}"
  export _YQ_ARM_HELPER_CERT="${BACKEND_ARM_HELPER_CERT_NAME}"
  yq -i "
    .clouds.dev.environments.${DEPLOY_ENV}.defaults.armHelperClientId = strenv(_YQ_ARM_HELPER_CID) |
    .clouds.dev.environments.${DEPLOY_ENV}.defaults.armHelperCertName = strenv(_YQ_ARM_HELPER_CERT)
  " "${OVERRIDE_CONFIG_FILE}"
  unset _YQ_ARM_HELPER_CID _YQ_ARM_HELPER_CERT

  if [[ ${#ARM_HELPER_LEASES[@]} -eq 2 ]]; then
    CLUSTERS_SERVICE_ARM_HELPER_LEASE="${ARM_HELPER_LEASES[1]}"
    CLUSTERS_SERVICE_ARM_HELPER_CLIENT_ID=$(yq ".armHelperPool.\"${CLUSTERS_SERVICE_ARM_HELPER_LEASE}\".clientId" "${ARM_HELPER_POOL_CATALOG}")
    CLUSTERS_SERVICE_ARM_HELPER_PRINCIPAL_ID=$(yq ".armHelperPool.\"${CLUSTERS_SERVICE_ARM_HELPER_LEASE}\".principalId" "${ARM_HELPER_POOL_CATALOG}")
    CLUSTERS_SERVICE_ARM_HELPER_CERT_NAME=$(yq ".armHelperPool.\"${CLUSTERS_SERVICE_ARM_HELPER_LEASE}\".certName" "${ARM_HELPER_POOL_CATALOG}")
    if [[ -z "${CLUSTERS_SERVICE_ARM_HELPER_CLIENT_ID}" || "${CLUSTERS_SERVICE_ARM_HELPER_CLIENT_ID}" == "null" || \
          -z "${CLUSTERS_SERVICE_ARM_HELPER_PRINCIPAL_ID}" || "${CLUSTERS_SERVICE_ARM_HELPER_PRINCIPAL_ID}" == "null" || \
          -z "${CLUSTERS_SERVICE_ARM_HELPER_CERT_NAME}" || "${CLUSTERS_SERVICE_ARM_HELPER_CERT_NAME}" == "null" ]]; then
      echo "ERROR: Clusters Service ARM helper lease '${CLUSTERS_SERVICE_ARM_HELPER_LEASE}' not found or incomplete in ${ARM_HELPER_POOL_CATALOG}"
      exit 1
    fi

    echo "Clusters Service ARM helper SP override: ${CLUSTERS_SERVICE_ARM_HELPER_LEASE} -> clientId=${CLUSTERS_SERVICE_ARM_HELPER_CLIENT_ID}"
    export _YQ_CS_ARM_HELPER_CID="${CLUSTERS_SERVICE_ARM_HELPER_CLIENT_ID}"
    export _YQ_CS_ARM_HELPER_CERT="${CLUSTERS_SERVICE_ARM_HELPER_CERT_NAME}"
    yq -i "
      .clouds.dev.environments.${DEPLOY_ENV}.defaults.clustersServiceArmHelperClientId = strenv(_YQ_CS_ARM_HELPER_CID) |
      .clouds.dev.environments.${DEPLOY_ENV}.defaults.clustersServiceArmHelperCertName = strenv(_YQ_CS_ARM_HELPER_CERT)
    " "${OVERRIDE_CONFIG_FILE}"
    unset _YQ_CS_ARM_HELPER_CID _YQ_CS_ARM_HELPER_CERT
  else
    echo "No Clusters Service ARM helper SP lease provided, preserving its configured defaults"
  fi
else
  echo "No ARM helper SP lease provided, skipping ARM helper overrides"
fi

# Healthcheck workflows provision without leases and don't need E2E-sized clusters.
# Override minCount to 1 so healthcheck clusters stay small.
if [[ -z "${LEASED_MSI_CONTAINERS:-}" ]]; then
  yq -i "
    .clouds.dev.environments.${DEPLOY_ENV}.defaults.mgmt.aks.userAgentPool.minCount = 1
  " "${OVERRIDE_CONFIG_FILE}"
fi

# Merge hypershift image overrides if present (written by aro-hcp-hypershift-images-push)
HYPERSHIFT_OVERRIDES="${SHARED_DIR}/hypershift-image-overrides.yaml"
if [[ -f "${HYPERSHIFT_OVERRIDES}" ]]; then
    echo "Merging hypershift image overrides:"
    cat "${HYPERSHIFT_OVERRIDES}"
    yq eval-all 'select(fileIndex == 0) * select(fileIndex == 1)' \
        "${OVERRIDE_CONFIG_FILE}" "${HYPERSHIFT_OVERRIDES}" > "${OVERRIDE_CONFIG_FILE}.tmp"
    mv "${OVERRIDE_CONFIG_FILE}.tmp" "${OVERRIDE_CONFIG_FILE}"
fi

echo "Created override config at: ${OVERRIDE_CONFIG_FILE}"
cat "${OVERRIDE_CONFIG_FILE}"
