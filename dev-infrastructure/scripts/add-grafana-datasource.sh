#!/bin/bash
# This script integrates an existing Azure Monitoring Workspace with the global Azure Managed Grafana Instance.
set -e

GRAFANA_NAME=$1
GRAFANA_RG=$2
MONITOR_NAME=$3
MONITOR_RG=$4

MONITOR_DATA_SOURCE=Managed_Prometheus_${MONITOR_NAME}

DATA_SOURCE_URL=$(az grafana data-source list --name ${GRAFANA_NAME} \
    --resource-group ${GRAFANA_RG} \
    --query "[?contains(name, '${MONITOR_DATA_SOURCE}')].url | [0]" -o tsv)

MONITOR_JSON=$(az monitor account list \
    --query "[?contains(name, '${MONITOR_NAME}')].{id:id, name: name, promUrl: metrics.prometheusQueryEndpoint}"[0])

PROM_QUERY_URL=$(echo $MONITOR_JSON | jq '.promUrl' -r )

MONITOR_ID=$(echo $MONITOR_JSON | jq '.id' )

# In dev resource groups are purged which causes data sources to become out of sync in the Azure Grafana Instance.
# If prometheus urls don't match then delete the integration to cleanup the data source.
if [[ -n "${DATA_SOURCE_URL}" && ${DATA_SOURCE_URL} != ${PROM_QUERY_URL} ]];
then
    echo "Removing ${MONITOR_NAME} integration from ${GRAFANA_NAME}"
    az grafana integrations monitor delete \
        --name ${GRAFANA_NAME} \
        --resource-group ${GRAFANA_RG} \
        --monitor-name ${MONITOR_NAME} \
        --monitor-resource-group-name ${MONITOR_RG} \
        --skip-role-assignment true

    az resource wait --updated --ids $(az grafana show --name ${GRAFANA_NAME} --resource-group ${GRAFANA_RG} --query 'id' -o tsv)
fi

MONITORS=$(az grafana integrations monitor list \
    --name ${GRAFANA_NAME} \
    --resource-group ${GRAFANA_RG})

IS_INTEGRATED=$(echo ${MONITORS} | jq "contains([${MONITOR_ID}])")

if [[ ${IS_INTEGRATED} == "false" ]];
then
    echo "Adding monitor account ${MONITOR_NAME} as a data source to ${GRAFANA_NAME}"
    az grafana integrations monitor add \
        --name ${GRAFANA_NAME} \
        --resource-group ${GRAFANA_RG} \
        --monitor-name ${MONITOR_NAME} \
        --monitor-resource-group-name ${MONITOR_RG} \
        --skip-role-assignments true
        
    az resource wait --updated --ids $(az grafana show --name ${GRAFANA_NAME} --resource-group ${GRAFANA_RG} --query 'id' -o tsv)
fi