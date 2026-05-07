#!/bin/bash
set -euo pipefail

APPLICATION_NAME="OpenShift Release Bot"
SUBSCRIPTIONS=(
    "ARO HCP E2E Infrastructure (EA Subscription)"
    "ARO HCP E2E Hosted Clusters (EA Subscription)"
    "ARO HCP E2E Management Clusters (EA Subscription)"
    "ARO Hosted Control Planes (EA Subscription 1)"
)
GLOBAL_SUBSCRIPTION_NAME="ARO Hosted Control Planes (EA Subscription 1)"

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

APP_ID=$(az ad app list --display-name "${APPLICATION_NAME}" --query '[*]'.appId -o tsv)

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
  local existing_permissions=$(az ad app show --id "${app_id}" --query "requiredResourceAccess[?resourceAppId=='${GRAPH_APP_ID}'].resourceAccess[].id" -o tsv)

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

get_graph_permission_id() {
  local permission_value="$1"
  local permission_type="$2" # "Scope" or "Role"

  if [[ "${permission_type}" == "Scope" ]]; then
    az ad sp show \
      --id "${GRAPH_APP_ID}" \
      --query "oauth2PermissionScopes[?value=='${permission_value}'].id | [0]" \
      -o tsv
    return
  fi

  if [[ "${permission_type}" == "Role" ]]; then
    az ad sp show \
      --id "${GRAPH_APP_ID}" \
      --query "appRoles[?value=='${permission_value}' && contains(allowedMemberTypes, 'Application')].id | [0]" \
      -o tsv
    return
  fi

  echo "ERROR: Unsupported permission type '${permission_type}'" >&2
  return 1
}

if [[ -z "${APP_ID}" ]]; then
    header "Creating or update application ${APPLICATION_NAME}"
    SP_OUTPUT=$(az ad sp create-for-rbac \
        --years 10 \
        --display-name "${APPLICATION_NAME}" \
        -o json)

    # Extract variables from the JSON output
    APP_ID=$(echo "${SP_OUTPUT}" | jq -r '.appId')

    echo "Created service principal:"
    echo "  App ID: ${APP_ID}"
else
    header "Application ${APPLICATION_NAME} already exists with appId ${APP_ID}"
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

    # Assign AKS RBAC Cluster Admin for templatize Shell steps that need
    # kubeconfig access on freshly-created AKS clusters
    echo "  Assigning Azure Kubernetes Service RBAC Cluster Admin role..."
    az role assignment create \
        --assignee "${APP_ID}" \
        --role "Azure Kubernetes Service RBAC Cluster Admin" \
        --scope "/subscriptions/${SUBSCRIPTION_ID}" 2>/dev/null || echo "    (already assigned)"

    if [[ "${SUBSCRIPTION_NAME}" == "${GLOBAL_SUBSCRIPTION_NAME}" ]]; then
        GLOBAL_SUBSCRIPTION_ID="${SUBSCRIPTION_ID}"
    fi
done

if [[ -z "${GLOBAL_SUBSCRIPTION_ID:-}" ]]; then
    echo "ERROR: Could not determine global subscription ID for '${GLOBAL_SUBSCRIPTION_NAME}'"
    exit 1
fi

header "Assigning global data-plane roles"

# Global pipeline runs local templatize steps under this principal in CI.
# These steps call Key Vault and Grafana data planes directly.
echo "  Assigning Key Vault Administrator role..."
az role assignment create \
    --assignee "${APP_ID}" \
    --role "Key Vault Administrator" \
    --scope "/subscriptions/${GLOBAL_SUBSCRIPTION_ID}" 2>/dev/null || echo "    (already assigned)"

# Scope Grafana role assignment at the whole subscription so access remains valid
# even if Grafana is recreated in a different resource group or with a new name.
echo "  Assigning Grafana Admin role..."
az role assignment create \
    --assignee "${APP_ID}" \
    --role "Grafana Admin" \
    --scope "/subscriptions/${GLOBAL_SUBSCRIPTION_ID}" 2>/dev/null || echo "    (already assigned)"

header "Grant API Permissions"

echo "Grant API Permissions - delegated permission: User.Read"
USER_READ_SCOPE_ID="$(get_graph_permission_id "User.Read" "Scope")"
if [[ -z "${USER_READ_SCOPE_ID}" || "${USER_READ_SCOPE_ID}" == "null" ]]; then
  echo "ERROR: Could not resolve Microsoft Graph permission ID for User.Read (Scope)"
  exit 1
fi
grant_api_permission "${APP_ID}" "${USER_READ_SCOPE_ID}" "Scope"

echo "Grant API Permissions - application permission: Application.ReadWrite.OwnedBy"
APP_RW_OWNEDBY_ROLE_ID="$(get_graph_permission_id "Application.ReadWrite.OwnedBy" "Role")"
if [[ -z "${APP_RW_OWNEDBY_ROLE_ID}" || "${APP_RW_OWNEDBY_ROLE_ID}" == "null" ]]; then
  echo "ERROR: Could not resolve Microsoft Graph permission ID for Application.ReadWrite.OwnedBy (Role)"
  exit 1
fi
grant_api_permission "${APP_ID}" "${APP_RW_OWNEDBY_ROLE_ID}" "Role"

echo "Grant API Permissions - application permission: Directory.Read.All"
DIRECTORY_READ_ALL_ROLE_ID="$(get_graph_permission_id "Directory.Read.All" "Role")"
if [[ -z "${DIRECTORY_READ_ALL_ROLE_ID}" || "${DIRECTORY_READ_ALL_ROLE_ID}" == "null" ]]; then
  echo "ERROR: Could not resolve Microsoft Graph permission ID for Directory.Read.All (Role)"
  exit 1
fi
grant_api_permission "${APP_ID}" "${DIRECTORY_READ_ALL_ROLE_ID}" "Role"
