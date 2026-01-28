#!/bin/bash
# This script integrates an existing Azure Monitoring Workspace with the global Azure Managed Grafana Instance.
set -o errexit
set -o nounset
set -o pipefail

# parse resource IDs
IFS='/'
read -ra ADDR <<< "$GRAFANA_RESOURCE_ID"
GRAFANA_SUBSCRIPTION_ID=${ADDR[2]}
GRAFANA_RG=${ADDR[4]}
GRAFANA_NAME=${ADDR[8]}
read -ra ADDR <<< "$MONITOR_ID"
MONITOR_NAME=${ADDR[8]}
read -ra MON_ADDR <<< "$MONITOR_ID"
MONITOR_RG=${MON_ADDR[4]}
IFS=' '

# lookup existing azure monitoring workspace registration
MONITORS=$(az resource show --ids "${GRAFANA_RESOURCE_ID}" --api-version 2024-10-01 -o json | jq .properties.grafanaIntegrations.azureMonitorWorkspaceIntegrations)
MONITOR_DATA_SOURCE="Managed_Prometheus_${MONITOR_NAME}"
EXISTING_DATA_SOURCE_URL=$(az grafana data-source list --name ${GRAFANA_NAME} \
    --resource-group ${GRAFANA_RG} --subscription ${GRAFANA_SUBSCRIPTION_ID} \
    --query "[?contains(name, '${MONITOR_DATA_SOURCE}')].url | [0]" -o tsv)

# wait for inflight updates to finish
az resource wait --custom "properties.provisioningState=='Succeeded'" --ids "${GRAFANA_RESOURCE_ID}" --api-version 2024-10-01

# In dev resource groups are purged which causes data sources to become out of sync in the Azure Grafana Instance.
# If prometheus urls don't match then delete the integration to cleanup the data source.
echo "DEBUG: Extracted Resource Group: '${MONITOR_RG}'"
if [[ -n "${EXISTING_DATA_SOURCE_URL}" && ${EXISTING_DATA_SOURCE_URL} != ${PROM_QUERY_URL} ]];
then
    echo "Removing all integrations for resource group ${MONITOR_RG} from ${GRAFANA_NAME}"
    MONITOR_UPDATES=$(echo "${MONITORS}" | jq --arg rg "/resourceGroups/${MONITOR_RG}/" 'map(select(.azureMonitorWorkspaceResourceId | contains($rg) | not))')
    #dry run
    echo "#### DRY RUN MODE ####"
    echo "az resource update --ids ${GRAFANA_RESOURCE_ID} ..."
    echo "Payload would be:"
    echo "${MONITOR_UPDATES}" | jq .
    echo "######################"
    
    #az resource update --ids ${GRAFANA_RESOURCE_ID} --set properties.grafanaIntegrations.azureMonitorWorkspaceIntegrations="${MONITOR_UPDATES}" --api-version 2024-10-01
    #az resource wait --custom "properties.provisioningState=='Succeeded'" --ids "${GRAFANA_RESOURCE_ID}" --api-version 2024-10-01
fi

# add the azure monitor workspace to grafana if it is not already integrated
IS_INTEGRATED=$(echo "$MONITORS" | jq --arg id "${MONITOR_ID}" 'map(.azureMonitorWorkspaceResourceId) | contains([$id])')
if [[ ${IS_INTEGRATED} == "false" ]];
then
    MONITOR_UPDATES=$(echo "${MONITORS}" | jq --arg id "${MONITOR_ID}" '. + [{"azureMonitorWorkspaceResourceId": $id}]')
    az resource update --ids "${GRAFANA_RESOURCE_ID}" --set properties.grafanaIntegrations.azureMonitorWorkspaceIntegrations="${MONITOR_UPDATES}" --api-version 2024-10-01
    az resource wait --custom "properties.provisioningState=='Succeeded'" --ids "${GRAFANA_RESOURCE_ID}" --api-version 2024-10-01
fi
