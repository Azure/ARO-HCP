#!/bin/bash

set -o errexit
set -o nounset
set -o pipefail

if [ "${ADX_DATASOURCE_ENABLED}" != "true" ]; then
    echo "ADX datasource provisioning disabled, skipping"
    exit 0
fi

if [ -z "${ADX_CLUSTER_URL}" ]; then
    echo "No Kusto cluster URI available (manageInstance is likely false), skipping"
    exit 0
fi

GRAFANA_SUBSCRIPTION_ID=$(printf '%s' "${GRAFANA_RESOURCE_ID}" | cut -d/ -f3)
ENVIRONMENT_NAME=$(printf '%s' "${GRAFANA_NAME}" | sed 's/^arohcp-//')
EXPECTED_KUSTO_PREFIX="hcp-${ENVIRONMENT_NAME}-"

case "${KUSTO_NAME}" in
    "${EXPECTED_KUSTO_PREFIX}"*)
        GEO_SHORT_ID=${KUSTO_NAME#"${EXPECTED_KUSTO_PREFIX}"}
        ;;
    *)
        echo "ERROR: unexpected Kusto name '${KUSTO_NAME}', expected prefix '${EXPECTED_KUSTO_PREFIX}'"
        exit 1
        ;;
esac

if [ -n "${ADX_DATASOURCE_GEOGRAPHIES}" ]; then
    NORMALIZED_GEOS=$(printf '%s' "${ADX_DATASOURCE_GEOGRAPHIES}" | tr '[:upper:]' '[:lower:]' | tr -d '[:space:]')
    NORMALIZED_GEO=$(printf '%s' "${GEO_SHORT_ID}" | tr '[:upper:]' '[:lower:]')

    if ! printf '%s' "${NORMALIZED_GEOS}" | grep -Eq '^[a-z0-9-]+(,[a-z0-9-]+)*$'; then
        echo "ERROR: adxDatasourceGeographies has invalid format: '${ADX_DATASOURCE_GEOGRAPHIES}'"
        exit 1
    fi

    case ",${NORMALIZED_GEOS}," in
        *,"${NORMALIZED_GEO}",*) ;;
        *)
            echo "Geography ${GEO_SHORT_ID} not in allowlist (${NORMALIZED_GEOS}), skipping"
            exit 0
            ;;
    esac
fi

DATASOURCE_NAME="kusto-${ENVIRONMENT_NAME}-${GEO_SHORT_ID}"

az resource wait \
    --custom "properties.provisioningState=='Succeeded'" \
    --ids "${GRAFANA_RESOURCE_ID}" \
    --api-version 2024-10-01

DEFINITION=$(jq -nc \
    --arg name "${DATASOURCE_NAME}" \
    --arg clusterUrl "${ADX_CLUSTER_URL}" \
    --arg defaultDatabase "${ADX_DATABASE}" \
    '{
      name: $name,
      type: "grafana-azure-data-explorer-datasource",
      access: "proxy",
      jsonData: {
        clusterUrl: $clusterUrl,
        defaultDatabase: $defaultDatabase,
        dataConsistency: "strongconsistency",
        azureCredentials: {
          authType: "msi"
        }
      }
    }')

if az grafana data-source show \
    --name "${GRAFANA_NAME}" \
    --resource-group "${GRAFANA_RG}" \
    --subscription "${GRAFANA_SUBSCRIPTION_ID}" \
    --data-source "${DATASOURCE_NAME}" >/dev/null 2>&1; then
    echo "Datasource ${DATASOURCE_NAME} exists, updating"
    az grafana data-source update \
        --name "${GRAFANA_NAME}" \
        --resource-group "${GRAFANA_RG}" \
        --subscription "${GRAFANA_SUBSCRIPTION_ID}" \
        --data-source "${DATASOURCE_NAME}" \
        --definition "${DEFINITION}"
else
    echo "Datasource ${DATASOURCE_NAME} not found, creating"
    az grafana data-source create \
        --name "${GRAFANA_NAME}" \
        --resource-group "${GRAFANA_RG}" \
        --subscription "${GRAFANA_SUBSCRIPTION_ID}" \
        --definition "${DEFINITION}"
fi

az grafana data-source show \
    --name "${GRAFANA_NAME}" \
    --resource-group "${GRAFANA_RG}" \
    --subscription "${GRAFANA_SUBSCRIPTION_ID}" \
    --data-source "${DATASOURCE_NAME}" \
    --query "{name:name, type:type, uid:uid}" \
    -o table
