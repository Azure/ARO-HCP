APP_ID=$(az ad app list --display-name ${APPLICATION_NAME} --query '[*]'.appId -o tsv)
if [[ -n "${APP_ID}" ]];
then
    echo "Assigning role ${ROLE_DEFINITION_NAME} to appId ${APP_ID}"
    az role assignment create \
        --assignee "${APP_ID}" \
        --role "${ROLE_DEFINITION_NAME}" \
        --scope "/subscriptions/${SUBSCRIPTION_ID}"

    exit 0
fi
