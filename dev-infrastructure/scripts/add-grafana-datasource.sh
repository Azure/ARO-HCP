#!/bin/bash
# This script integrates an existing Azure Monitoring Workspace with the global Azure Managed Grafana Instance.
set -o errexit
set -o nounset
set -o pipefail

DRY_RUN="true" 

# az resource update retry to resolve conflict errors
function update_integrations_with_retry() {
    local payload="$1"
    
    if [[ "${DRY_RUN}" == "true" ]]; then
        echo "----------------------------------------------------------------"
        echo "### DRY RUN: Would execute update with the following payload ###"
        echo "----------------------------------------------------------------"
        echo "$payload" | jq . 
        echo "----------------------------------------------------------------"
        echo "DRY RUN: Returning success (0) to continue script flow..."
        return 0
    fi

    local attempt=1
    local max_attempts=10
    local sleep_sec=15

    while [ $attempt -le $max_attempts ]; do
        echo "Attempt $attempt/$max_attempts: Updating Grafana integrations..."
        
        set +e 
        OUTPUT=$(az resource update --ids "${GRAFANA_RESOURCE_ID}" --set properties.grafanaIntegrations.azureMonitorWorkspaceIntegrations="${payload}" --api-version 2024-10-01 2>&1)
        EXIT_CODE=$?
        set -e 

        if [ $EXIT_CODE -eq 0 ]; then
            echo "Update successful."
            return 0
        fi

        if echo "$OUTPUT" | grep -qE "Conflict|InvalidResourceOperation|AnotherOperationInProgress"; then
            echo "Hit concurrency error (Resource is busy/updating). Retrying in ${sleep_sec}s..."
            sleep $sleep_sec
            ((attempt++))
        else
            echo "CRITICAL ERROR: Update failed with non-retriable error."
            echo "$OUTPUT"
            return 1
        fi
    done

    echo "Timed out waiting for Grafana resource lock after $max_attempts attempts."
    return 1
}

# parse resource IDs
IFS='/'
read -ra ADDR <<< "$GRAFANA_RESOURCE_ID"
GRAFANA_SUBSCRIPTION_ID=${ADDR[2]}
GRAFANA_RG=${ADDR[4]}
GRAFANA_NAME=${ADDR[8]}
read -ra ADDR <<< "$MONITOR_ID"
MONITOR_NAME=${ADDR[8]}
MONITOR_RG=${ADDR[4]}
IFS=' '

# esnure valid RG
if [[ -z "${MONITOR_RG}" || "${MONITOR_RG}" == "/" ]]; then
    echo "ERROR: Failed to extract Resource Group from MONITOR_ID. Aborting."
    exit 1
fi

# lookup existing azure monitoring workspace registration
MONITORS=$(az resource show --ids "${GRAFANA_RESOURCE_ID}" --api-version 2024-10-01 -o json | jq .properties.grafanaIntegrations.azureMonitorWorkspaceIntegrations)
MONITOR_DATA_SOURCE="Managed_Prometheus_${MONITOR_NAME}"
EXISTING_DATA_SOURCE_URL=$(az grafana data-source list --name ${GRAFANA_NAME} \
    --resource-group ${GRAFANA_RG} --subscription ${GRAFANA_SUBSCRIPTION_ID} \
    --query "[?contains(name, '${MONITOR_DATA_SOURCE}')].url | [0]" -o tsv)

# wait for inflight updates to finish
if [[ "${DRY_RUN}" != "true" ]]; then
    az resource wait --custom "properties.provisioningState=='Succeeded'" --ids "${GRAFANA_RESOURCE_ID}" --api-version 2024-10-01
fi

# In dev resource groups are purged which causes data sources to become out of sync in the Azure Grafana Instance.
# If prometheus urls don't match then delete the integration to cleanup the data source.
if [[ -n "${EXISTING_DATA_SOURCE_URL}" && ${EXISTING_DATA_SOURCE_URL} != ${PROM_QUERY_URL} ]];
then
    echo "Removing ${MONITOR_NAME} integration from ${GRAFANA_NAME}"
    MONITOR_UPDATES=$(echo "${MONITORS}" | jq --arg rg "/resourceGroups/${MONITOR_RG}/" 'map(select(.azureMonitorWorkspaceResourceId | contains($rg) | not))')
    
    update_integrations_with_retry "${MONITOR_UPDATES}"
    
    # Refresh the list
    MONITORS=$(az resource show --ids "${GRAFANA_RESOURCE_ID}" --api-version 2024-10-01 -o json | jq .properties.grafanaIntegrations.azureMonitorWorkspaceIntegrations)
    
    if [[ "${DRY_RUN}" != "true" ]]; then
        az resource wait --custom "properties.provisioningState=='Succeeded'" --ids "${GRAFANA_RESOURCE_ID}" --api-version 2024-10-01
    fi
fi

# add the azure monitor workspace to grafana if it is not already integrated
IS_INTEGRATED=$(echo "$MONITORS" | jq --arg id "${MONITOR_ID}" 'map(.azureMonitorWorkspaceResourceId) | contains([$id])')
if [[ ${IS_INTEGRATED} == "false" ]];
then
    MONITOR_UPDATES=$(echo "${MONITORS}" | jq --arg id "${MONITOR_ID}" '. + [{"azureMonitorWorkspaceResourceId": $id}]')
    
    update_integrations_with_retry "${MONITOR_UPDATES}"
    
    if [[ "${DRY_RUN}" != "true" ]]; then
        az resource wait --custom "properties.provisioningState=='Succeeded'" --ids "${GRAFANA_RESOURCE_ID}" --api-version 2024-10-01
    fi
fi