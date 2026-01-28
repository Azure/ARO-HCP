#!/bin/bash
# This script migrates a CosmosDB SQL container from manual throughput to autoscaling.
set -o errexit
set -o nounset
set -o pipefail

# Usage function
usage() {
    cat <<EOF
Usage: $0 <resource-group> <cosmosdb-account-name> <database-name> <container-name>

Migrates a CosmosDB SQL container to autoscaling throughput.

Arguments:
  resource-group          Azure resource group name
  cosmosdb-account-name   CosmosDB account name
  database-name           SQL database name
  container-name          SQL container name

Example:
  $0 my-rg my-cosmosdb my-database my-container
EOF
    exit 1
}

# Check arguments
if [ $# -ne 4 ]; then
    echo "Error: Invalid number of arguments"
    usage
fi

RESOURCE_GROUP="$1"
COSMOSDB_ACCOUNT_NAME="$2"
DATABASE_NAME="$3"
CONTAINER_NAME="$4"

echo "Checking throughput configuration for container: ${CONTAINER_NAME}"
echo "  Account: ${COSMOSDB_ACCOUNT_NAME}"
echo "  Database: ${DATABASE_NAME}"
echo "  Resource Group: ${RESOURCE_GROUP}"

# First check if rg and cosmosdb account exists
if ! az group exists --name "${RESOURCE_GROUP}" --output json; then
    echo "Resource group '${RESOURCE_GROUP}' does not exist (yet?). Exiting."
    exit 0
fi

if ! az cosmosdb show --name "${COSMOSDB_ACCOUNT_NAME}" --resource-group "${RESOURCE_GROUP}" --output json; then
    echo "CosmosDB account '${COSMOSDB_ACCOUNT_NAME}' does not exist (yet?). Exiting."
    exit 0
fi

# Get current throughput configuration
THROUGHPUT_CONFIG=$(az cosmosdb sql container throughput show \
    --account-name "${COSMOSDB_ACCOUNT_NAME}" \
    --resource-group "${RESOURCE_GROUP}" \
    --database-name "${DATABASE_NAME}" \
    --name "${CONTAINER_NAME}" \
    --output json)

# Check if autoscaling is enabled
AUTOSCALE_MAX_THROUGHPUT=$(echo "${THROUGHPUT_CONFIG}" | jq -r '.resource.autoscaleSettings.maxThroughput // empty')

if [ -n "${AUTOSCALE_MAX_THROUGHPUT}" ] && [ "${AUTOSCALE_MAX_THROUGHPUT}" != "null" ]; then
    echo "✓ Container '${CONTAINER_NAME}' already has autoscaling enabled (max throughput: ${AUTOSCALE_MAX_THROUGHPUT})"
    exit 0
fi

# Migrate to autoscaling
az cosmosdb sql container throughput migrate \
    --account-name "${COSMOSDB_ACCOUNT_NAME}" \
    --resource-group "${RESOURCE_GROUP}" \
    --database-name "${DATABASE_NAME}" \
    --name "${CONTAINER_NAME}" \
    --throughput-type autoscale

echo "✓ Successfully migrated container '${CONTAINER_NAME}' to autoscaling"

# Verify the migration
echo "Verifying migration..."
NEW_THROUGHPUT_CONFIG=$(az cosmosdb sql container throughput show \
    --account-name "${COSMOSDB_ACCOUNT_NAME}" \
    --resource-group "${RESOURCE_GROUP}" \
    --database-name "${DATABASE_NAME}" \
    --name "${CONTAINER_NAME}" \
    --output json)

NEW_AUTOSCALE_MAX_THROUGHPUT=$(echo "${NEW_THROUGHPUT_CONFIG}" | jq -r '.autoscaleSettings.maxThroughput // empty')

if [ -n "${NEW_AUTOSCALE_MAX_THROUGHPUT}" ] && [ "${NEW_AUTOSCALE_MAX_THROUGHPUT}" != "null" ]; then
    echo "✓ Migration verified: autoscaling enabled with max throughput: ${NEW_AUTOSCALE_MAX_THROUGHPUT}"
else
    echo "Warning: Migration completed but autoscaling may not be fully enabled yet"
    echo "Current configuration:"
    echo "${NEW_THROUGHPUT_CONFIG}" | jq .
fi
