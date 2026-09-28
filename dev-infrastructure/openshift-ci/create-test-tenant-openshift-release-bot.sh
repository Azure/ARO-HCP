#!/bin/bash
set -euo pipefail

APPLICATION_NAME="OpenShift Release Bot MSFT Test"
TENANT_ID="93b21e64-4824-439a-b893-46c9b2a51082"
SUBSCRIPTIONS=(
    "ARO HCP E2E"
    "ARO HCP E2E - Staging"
)

# Role assignment condition to prevent assigning privileged administrator roles
UAA_CONDITION="(
 (
  !(ActionMatches{'Microsoft.Authorization/roleAssignments/write'})
 )
 OR
 (
  @Request[Microsoft.Authorization/roleAssignments:RoleDefinitionId] ForAnyOfAllValues:GuidNotEquals {8e3af657-a8ff-443c-a75c-2fe8c4bcb635, 18d7d88d-d35e-4fb5-a5c3-7773c20a72d9, f58310d9-a9f6-439a-9e8d-f62e7b41a168}
 )
)
AND
(
 (
  !(ActionMatches{'Microsoft.Authorization/roleAssignments/delete'})
 )
 OR
 (
  @Resource[Microsoft.Authorization/roleAssignments:RoleDefinitionId] ForAnyOfAllValues:GuidNotEquals {8e3af657-a8ff-443c-a75c-2fe8c4bcb635, 18d7d88d-d35e-4fb5-a5c3-7773c20a72d9, f58310d9-a9f6-439a-9e8d-f62e7b41a168}
 )
)"

GRAPH_APP_ID="00000003-0000-0000-c000-000000000000"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

header() {
    echo ""
    echo "------"
    echo "$1"
    echo "------"
    echo ""
}

grant_api_permission() {
  local app_id="$1"
  local permission="$2"
  local type="$3" # "Scope" or "Role"

  # Check if permission already exists
  local existing_permissions
  existing_permissions=$(az ad app show --id "${app_id}" --query "requiredResourceAccess[?resourceAppId=='${GRAPH_APP_ID}'].resourceAccess[].id" -o tsv)

  if echo "${existing_permissions}" | grep -q "${permission}"; then
    echo "  Permission ${permission} already exists, skipping"
    return 0
  fi

  echo "  Adding permission ${permission} (${type})"
  az ad app permission add \
    --id "${app_id}" \
    --api "${GRAPH_APP_ID}" \
    --api-permissions "${permission}=${type}"
}

if ! command -v az &> /dev/null; then
    echo "Error: az CLI is not installed"
    exit 1
fi

if ! command -v jq &> /dev/null; then
    echo "Error: jq is not installed"
    exit 1
fi

ACTIVE_TENANT_ID=$(az account show --query tenantId -o tsv)
if [[ "${ACTIVE_TENANT_ID}" != "${TENANT_ID}" ]]; then
    echo "Error: az CLI is authenticated to tenant ${ACTIVE_TENANT_ID}, expected Test Tenant ${TENANT_ID}"
    echo "Run: az login --tenant ${TENANT_ID}"
    exit 1
fi

APP_ID=$(az ad app list --display-name "${APPLICATION_NAME}" --query '[*].appId' -o tsv)

if [[ -z "${APP_ID}" ]]; then
    header "Creating Test Tenant application ${APPLICATION_NAME}"
    APP_ID=$(az ad app create \
        --display-name "${APPLICATION_NAME}" \
        --query appId \
        -o tsv)
    az ad sp create --id "${APP_ID}" --output none

    echo "Created service principal:"
    echo "  App ID: ${APP_ID}"
else
    header "Test Tenant application ${APPLICATION_NAME} already exists with appId ${APP_ID}"
fi

for SUBSCRIPTION_NAME in "${SUBSCRIPTIONS[@]}"; do
    header "Assigning roles for subscription ${SUBSCRIPTION_NAME}"

    # Get subscription ID from name
    SUBSCRIPTION_ID=$(az account list --query "[?name=='${SUBSCRIPTION_NAME}'].id" -o tsv)
    if [[ -z "${SUBSCRIPTION_ID}" ]]; then
        echo "  ERROR: Could not find subscription ID for '${SUBSCRIPTION_NAME}'"
        continue
    fi
    echo "  Subscription ID: ${SUBSCRIPTION_ID}"

    # Assign Contributor role
    echo "  Assigning Contributor role..."
    az role assignment create \
        --assignee "${APP_ID}" \
        --role "Contributor" \
        --scope "/subscriptions/${SUBSCRIPTION_ID}" 2>/dev/null || echo "    (already assigned)"

    # Assign Role Based Access Control Administrator role with conditions
    echo "  Assigning Role Based Access Control Administrator role with conditions..."
    az role assignment create \
        --assignee "${APP_ID}" \
        --role "Role Based Access Control Administrator" \
        --scope "/subscriptions/${SUBSCRIPTION_ID}" \
        --condition "${UAA_CONDITION}" \
        --condition-version "2.0" \
        --description "Allow user to assign all roles except privileged administrator roles Owner, UAA, RBAC (Recommended)" 2>/dev/null || echo "    (already assigned)"
done

header "Grant API Permissions"

echo "Grant API Permissions - delegated permission: User.Read"
grant_api_permission "${APP_ID}" "e1fe6dd8-ba31-4d61-89e7-88639da4683d" "Scope"

echo "Grant API Permissions - application permission: Application.ReadWrite.OwnedBy"
grant_api_permission "${APP_ID}" "18a4783c-866b-4cc7-a460-3d5e5662c884" "Role"

echo "Grant admin consent to the application"
az ad app permission admin-consent --id "${APP_ID}"

header "Storing Test Tenant Credentials in Google Secret Manager"

"${SCRIPT_DIR}/rotate-test-tenant-openshift-release-bot-credentials.sh"

header "Setup Complete"
echo ""
echo "Application: ${APPLICATION_NAME}"
echo "App ID: ${APP_ID}"
echo ""
echo "Test Tenant identity fields updated in hcm-aro:"
echo "  - aro-hcp-prod"
echo "  - aro-hcp-msft-test-tenant"
