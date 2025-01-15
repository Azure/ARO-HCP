set -euo pipefail

if [[ $(az ad app list --display-name ${APPLICATION_NAME} --query '[*]'.appId -o tsv | wc -l ) == 1 ]];
then
    echo "Application exists, existing"
    exit 0
fi

az ad sp create-for-rbac \
--years 10 \
--display-name "${APPLICATION_NAME}" \
--keyvault "${KEY_VAULT_NAME}" \
--cert "${CERTIFICATE_NAME}" \
--role "${ROLE_DEFINITION_NAME}" \
--scopes "/subscriptions/${SUBSCRIPTION_ID}"
