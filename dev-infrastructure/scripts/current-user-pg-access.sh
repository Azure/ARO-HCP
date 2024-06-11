#!/bin/sh

RESOURCEGROUP=$1
DB_SERVER_NAME_PREFIX=$2

CURRENTUSER=$(az ad signed-in-user show | jq -r '.id')
CURRENTUSER_NAME=$(az ad signed-in-user show | jq -r '.userPrincipalName')

CS_DB=$(az postgres flexible-server list -g ${RESOURCEGROUP} | jq --arg prefix "${DB_SERVER_NAME_PREFIX}" '.[] | select(.name | startswith($prefix))')
CS_DB_NAME=$(echo ${CS_DB} | jq -r .name)

ALREADY_ADMIN=$(az postgres flexible-server ad-admin list -g  ${RESOURCEGROUP} -s ${CS_DB_NAME} | jq  --arg principalname "${CURRENTUSER_NAME}" '[.[] | select(.principalName == $principalname)] | length')
if [ $ALREADY_ADMIN -eq 0 ]; then
    echo "grainting ${CURRENTUSER_NAME} admin permissions on ${CS_DB_NAME}"
    az postgres flexible-server ad-admin create --server-name ${CS_DB_NAME} --resource-group ${RESOURCEGROUP} --object-id ${CURRENTUSER} --display-name ${CURRENTUSER_NAME}
fi

echo export PGHOST=$(echo ${CS_DB} | jq -r .fullyQualifiedDomainName)
echo export PGUSER=$CURRENTUSER_NAME
echo export PGPASSWORD=$(az account get-access-token --resource='https://ossrdbms-aad.database.windows.net' | jq .accessToken -r)
