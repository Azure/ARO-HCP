#!/bin/bash
set -e

HCPDEVSUBSCRIPTION=$1

if [[ -z "$HCPDEVSUBSCRIPTION" ]]; then
    echo "ERROR: Must provide Subscription Name"
    echo "usage: create-application.sh '<SUBSCRIPTION_NAME>'"
    exit 1
fi

az account set --name "${HCPDEVSUBSCRIPTION}"

SUBSCRIPTION=$(az account show -o json | jq -r '.id')
TENANT=$(az account show -o json | jq -r '.tenantId')

APP_NAME="aro-github-actions-identity"
APP_ID=$(az ad app list --display-name "${APP_NAME}" --query "[0].appId" -o tsv)

# Create new Azure AD application for GitHub Actions if it doesn't exist
if [[ -z "$APP_ID" || "$APP_ID" == "None" ]]; then
    echo "Creating new Azure AD application for GitHub Actions"
    APP_ID=$(az ad app create --display-name "${APP_NAME}" --query 'appId' -o tsv)
    if [[ -z "$APP_ID" || "$APP_ID" == "None" ]]; then
        echo "ERROR: Failed to create Azure AD application."
        exit 1
    fi
else
    echo "Using existing Azure AD application for GitHub Actions: ${APP_ID}"
fi

OBJ_ID=$(az ad sp show --id $APP_ID --query "id" -o tsv)

# Create new Azure AD service principal for GitHub Actions if it doesn't exist
if [[ -z "$OBJ_ID" || "$OBJ_ID" == "None" ]]; then
    echo "Creating new Azure AD service principal for GitHub Actions"
    OBJ_ID=$(az ad sp create --id $APP_ID | jq -r '.id')
    if [[ -z "$OBJ_ID" || "$OBJ_ID" == "None" ]]; then
        echo "ERROR: Failed to create Azure AD service principal."
        exit 1
    fi
else
    echo "Using existing Azure AD service principal for GitHub Actions: ${OBJ_ID}"
fi

# Subscription IDs
subscription_id() {
    case $1 in
        'dev')   echo "1d3378d3-5a3f-4712-85a1-2485495dfc4b";;
        'int')   echo "64f0619f-ebc2-4156-9d91-c4c781de7e54";;
        'stage') echo "b23756f7-4594-40a3-980f-10bb6168fc20";;
    esac
}

# Environments where automation accounts are deployed
AUTOMATION_ACCOUNT_ENVS=(dev int stage)

# Role assignments to apply to the automation account
ROLE_ASSIGNMENTS=(
 "Contributor"
 "Role Based Access Control Administrator"
 "User Access Administrator"
 "Grafana Admin"
 "Key Vault Secrets Officer"
 "Key Vault Certificates Officer"
 "Key Vault Crypto Officer"
 "Azure Kubernetes Service RBAC Cluster Admin"
)

for env in "${AUTOMATION_ACCOUNT_ENVS[@]}"; do
    SUBSCRIPTION_ID=$(subscription_id ${env})
    echo "Checking role assignments for ${env} with subscription ${SUBSCRIPTION_ID}"
    for role in "${ROLE_ASSIGNMENTS[@]}"; do
        ROLE_ASSIGNMENTS_ID=$(az role assignment list \
          --assignee "${OBJ_ID}" --role "${role}" \
          --scope /subscriptions/${SUBSCRIPTION_ID} --query "[0].id" -o tsv
        )
        if [[ -z "$ROLE_ASSIGNMENTS_ID" || "$ROLE_ASSIGNMENTS_ID" == "None" ]]; then
            echo "Creating role assignment for ${env} with role ${role}"
            az role assignment create --assignee "${OBJ_ID}" --role "${role}" --scope /subscriptions/${SUBSCRIPTION_ID}
        else
            echo "Role assignment already exists for ${env} with role ${role}"
        fi
    done
done

# Create federated credential for PRs only if it doesn't already exist
if ! az ad app federated-credential list --id "${APP_ID}" --query "[?name=='aro-hcp-pr-runner']" | grep -q 'aro-hcp-pr-runner'; then
    az ad app federated-credential create --id "${APP_ID}" --parameters \
    '{
      "audiences": [
          "api://AzureADTokenExchange"
      ],
      "description": "https://github.com/Azure/ARO-HCP runner",
      "issuer": "https://token.actions.githubusercontent.com",
      "name": "aro-hcp-pr-runner",
      "subject": "repo:Azure/ARO-HCP:pull_request"
    }'
else
    echo "Federated credential 'aro-hcp-pr-runner' already exists for app ${APP_ID}"
fi

# Create federated credential for main branch only if it doesn't already exist
if ! az ad app federated-credential list --id "${APP_ID}" --query "[?name=='aro-hcp-main']" | grep -q 'aro-hcp-main'; then
    az ad app federated-credential create --id "${APP_ID}" --parameters \
    '{
        "audiences": [
            "api://AzureADTokenExchange"
        ],
        "description": "https://github.com/Azure/ARO-HCP runner",
        "issuer": "https://token.actions.githubusercontent.com",
        "name": "aro-hcp-main",
        "subject": "repo:Azure/ARO-HCP:ref:refs/heads/main"
    }'
else
    echo "Federated credential 'aro-hcp-main' already exists for app ${APP_ID}"
fi

echo "----------- Configure GitHub with the below secrets -----------"
echo "AZURE_CLIENT_ID: ${APP_ID}"
echo "AZURE_SUBSCRIPTION_ID: ${SUBSCRIPTION}"
echo "AZURE_TENANT_ID: ${TENANT}"
