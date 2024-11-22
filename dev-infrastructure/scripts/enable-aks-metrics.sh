#!/bin/bash

MONITORING_WORSPACE_ID=$(az monitor account list -g ${GRAFANA_RESOURCEGROUP} --query "[?name=='${MONITORING_WORKSPACE_NAME}'].id" -o tsv)
GRAFANA_ID=$(az grafana list -g ${GRAFANA_RESOURCEGROUP} --query "[?name=='${GRAFANA_NAME}'].id" -o tsv)
ALREADY_ENABLED=$(az aks show --resource-group ${RESOURCEGROUP} --name ${AKS_NAME} --query 'azureMonitorProfile.metrics.enabled' -o tsv)

if [ "$ALREADY_ENABLED" == "true" ]; then
    echo "monitoring already enabled"
else
    az aks update --enable-azure-monitor-metrics \
       --resource-group ${RESOURCEGROUP} \
       --name ${AKS_NAME} \
       --azure-monitor-workspace-resource-id ${MONITORING_WORSPACE_ID} \
       --grafana-resource-id ${GRAFANA_ID}
fi
