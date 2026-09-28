#!/bin/bash
set -euo pipefail

# Rotate credentials for OpenShift Release Bot MSFT Test in the Test Tenant.
#
# Usage:
#   SECRET_MANAGER_CLI=/path/to/openshift/release/hack/secret-manager.sh \
#     ./rotate-test-tenant-openshift-release-bot-credentials.sh
#
# Existing application credentials are retained. Remove them manually only
# after CI has consumed and validated the new GSM values.

APPLICATION_NAME="OpenShift Release Bot MSFT Test"
TENANT_ID="93b21e64-4824-439a-b893-46c9b2a51082"
SECRET_COLLECTION="hcm-aro"
SECRET_GROUP="aro-hcp-prod"
SECRET_MANAGER_CLI="${SECRET_MANAGER_CLI:-sm}"
CREDENTIAL_LIFETIME_YEARS="${CREDENTIAL_LIFETIME_YEARS:-2}"

header() {
    echo ""
    echo "------"
    echo "$1"
    echo "------"
    echo ""
}

write_temp_value() {
    local value="$1"
    local output_file="$2"

    printf '%s' "${value}" > "${output_file}"
    chmod 0600 "${output_file}"
}

update_secret_field() {
    local group="$1"
    local field="$2"
    local value_file="$3"

    "${SECRET_MANAGER_CLI}" update \
        -c "${SECRET_COLLECTION}" \
        "${group}/${field}" \
        --from-file="${value_file}"
}

if [[ "${1:-}" == "--help" || "${1:-}" == "-h" ]]; then
    cat <<EOF
Usage: SECRET_MANAGER_CLI=/path/to/openshift/release/hack/secret-manager.sh $0

Rotates the OpenShift Release Bot MSFT Test client secret and updates the
Test Tenant identity fields in the hcm-aro Google Secret Manager collection.

Environment variables:
  SECRET_MANAGER_CLI          Secret Manager CLI executable (default: sm)
  CREDENTIAL_LIFETIME_YEARS   New client-secret lifetime (default: 2)
EOF
    exit 0
fi

if [[ $# -gt 0 ]]; then
    echo "Unknown option: $1"
    exit 1
fi

if ! command -v az &> /dev/null; then
    echo "Error: az CLI is not installed"
    exit 1
fi

if ! command -v jq &> /dev/null; then
    echo "Error: jq is not installed"
    exit 1
fi

if ! command -v "${SECRET_MANAGER_CLI}" &> /dev/null; then
    echo "Error: Secret Manager CLI '${SECRET_MANAGER_CLI}' is not available"
    echo "Set SECRET_MANAGER_CLI to openshift/release/hack/secret-manager.sh"
    exit 1
fi

if ! [[ "${CREDENTIAL_LIFETIME_YEARS}" =~ ^[1-9][0-9]*$ ]]; then
    echo "Error: CREDENTIAL_LIFETIME_YEARS must be a positive integer"
    exit 1
fi

ACTIVE_TENANT_ID=$(az account show --query tenantId -o tsv)
if [[ "${ACTIVE_TENANT_ID}" != "${TENANT_ID}" ]]; then
    echo "Error: az CLI is authenticated to tenant ${ACTIVE_TENANT_ID}, expected Test Tenant ${TENANT_ID}"
    echo "Run: az login --tenant ${TENANT_ID}"
    exit 1
fi

# Get the app ID
header "Looking up application: ${APPLICATION_NAME}"
APP_ID=$(az ad app list --display-name "${APPLICATION_NAME}" --query '[*].appId' -o tsv)

if [[ -z "${APP_ID}" ]]; then
    echo "Error: Application '${APPLICATION_NAME}' not found"
    echo "Run create-test-tenant-openshift-release-bot.sh first"
    exit 1
fi

echo "  App ID: ${APP_ID}"

header "Creating Test Tenant Credential"
CRED_OUTPUT=$(az ad app credential reset \
    --id "${APP_ID}" \
    --append \
    --display-name "OpenShift CI Test Tenant $(date -u +%Y-%m-%d)" \
    --years "${CREDENTIAL_LIFETIME_YEARS}" \
    -o json)

CLIENT_ID=$(echo "${CRED_OUTPUT}" | jq -r '.appId')
CLIENT_SECRET=$(echo "${CRED_OUTPUT}" | jq -r '.password')

if [[ -z "${CLIENT_ID}" || -z "${CLIENT_SECRET}" ]]; then
    echo "Error: Azure CLI did not return a complete credential"
    exit 1
fi

echo "  New ${CREDENTIAL_LIFETIME_YEARS}-year credential generated; existing credentials retained"

header "Validating New Credential"
AZURE_CONFIG_DIR=$(mktemp -d)
TEMP_DIR=$(mktemp -d)
trap 'rm -rf "${AZURE_CONFIG_DIR}" "${TEMP_DIR}"; unset CLIENT_SECRET' EXIT

AZURE_CONFIG_DIR="${AZURE_CONFIG_DIR}" az login \
    --service-principal \
    --username "${CLIENT_ID}" \
    --password "${CLIENT_SECRET}" \
    --tenant "${TENANT_ID}" \
    --allow-no-subscriptions \
    --output none
echo "  New credential authenticated successfully"

write_temp_value "${CLIENT_ID}" "${TEMP_DIR}/client-id"
write_temp_value "${CLIENT_SECRET}" "${TEMP_DIR}/client-secret"
write_temp_value "${TENANT_ID}" "${TEMP_DIR}/tenant"

header "Updating Google Secret Manager"
echo "Updating ${SECRET_COLLECTION}/${SECRET_GROUP} identity fields"
update_secret_field "${SECRET_GROUP}" "client-id" "${TEMP_DIR}/client-id"
update_secret_field "${SECRET_GROUP}" "client-secret" "${TEMP_DIR}/client-secret"
update_secret_field "${SECRET_GROUP}" "tenant" "${TEMP_DIR}/tenant"

header "Credential Rotation Complete"
echo ""
echo "Updated Test Tenant identity fields in:"
echo "  - ${SECRET_COLLECTION}/${SECRET_GROUP}"
echo ""
echo "Existing application credentials remain valid."
echo "After secret propagation and CI validation, list credentials with:"
echo "  az ad app credential list --id ${APP_ID} -o table"
echo "Delete an old credential explicitly with:"
echo "  az ad app credential delete --id ${APP_ID} --key-id <old-key-id>"
