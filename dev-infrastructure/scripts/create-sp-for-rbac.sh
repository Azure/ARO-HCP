set -euo pipefail

APP_ID=$(az ad app list --display-name ${APPLICATION_NAME} --query '[*]'.appId -o tsv)
if [[ -n "${APP_ID}" ]];
then
    echo "Resetting credentials for existing application ${APPLICATION_NAME} with appId ${APP_ID}"

    az ad app credential reset \
        --id "${APP_ID}" \
        --keyvault "${KEY_VAULT_NAME}" \
        --cert "${CERTIFICATE_NAME}"

    echo "Assigning role ${ROLE_DEFINITION_NAME} to appId ${APP_ID}"
    az role assignment create \
        --assignee "${APP_ID}" \
        --role "${ROLE_DEFINITION_NAME}" \
        --scope "/subscriptions/${SUBSCRIPTION_ID}"

    exit 0
fi

az ad sp create-for-rbac \
    --years 10 \
    --display-name "${APPLICATION_NAME}" \
    --keyvault "${KEY_VAULT_NAME}" \
    --cert "${CERTIFICATE_NAME}" \
    --role "${ROLE_DEFINITION_NAME}" \
    --scopes "/subscriptions/${SUBSCRIPTION_ID}"
