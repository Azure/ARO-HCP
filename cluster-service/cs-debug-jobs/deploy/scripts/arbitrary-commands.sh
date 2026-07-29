#!/bin/bash

set -euo pipefail

# Add your custom commands below:
# NOTE: the implemented logic MUST be idempotent and
# tolerant to failures, as the script can be executed
# multiple times because of new rollouts occurring or
# because of additional retries on failure.

COMPACT_JSON_OUTPUT=1
COMPACT_JSON_JQ_FLAG=""
if [ "${COMPACT_JSON_OUTPUT}" -eq 1 ]; then
  COMPACT_JSON_JQ_FLAG="-c"
fi

CS_HOST="clusters-service"
# We set the old provision shard '1' to maintenance status
PROVISION_SHARD_ID="1"

echo "running CS K8s Job script to put provision shard '${PROVISION_SHARD_ID}' to maintenance status in ARO-HCP production environment"

echo "listing provision shards using CS API"
OUTPUT=$(curl -sS -X GET "http://${CS_HOST}:8000/api/clusters_mgmt/v1/provision_shards")
echo -n "${OUTPUT}" | jq ${COMPACT_JSON_JQ_FLAG} .

# We check if the provision shard exists. If it doesn't we return early
echo "checking if provision shard '${PROVISION_SHARD_ID}' exists"
OUTPUT=$(curl -sS -X GET "http://${CS_HOST}:8000/api/clusters_mgmt/v1/provision_shards/${PROVISION_SHARD_ID}")
echo -n "${OUTPUT}" | jq ${COMPACT_JSON_JQ_FLAG} .
if echo -n "${OUTPUT}" | jq -e '(has("kind") and has("id")) and (.kind == "Error" and .id == "404")' > /dev/null 2>&1; then
  echo "provision shard id '${PROVISION_SHARD_ID}' does not exist. Exiting normally."
  exit 0
fi
if echo -n "${OUTPUT}" | jq -e '(has("kind") and has("id")) and (.kind == "Error" and .id != "404")' > /dev/null 2>&1; then
  echo "provision shard id '${PROVISION_SHARD_ID}' retrieval returned unexpected error. End of execution."
  exit 1
fi

PROVISION_SHARD_STATUS_BEFORE=$(echo -n "${OUTPUT}" | jq -r .status)
# We check if the provision shard has the status value set to 'maintenance'.
# If it does we consider we are done at this point
if [ "${PROVISION_SHARD_STATUS_BEFORE}" == "maintenance" ]; then
  echo "provision shard '${PROVISION_SHARD_ID}' already in 'maintenance' status. End of execution"
  exit 0
fi
# At this point, if the shard is not in active status we consider
# we are in an unknwon state so we return at this point failing.
if [ "${PROVISION_SHARD_STATUS_BEFORE}" != "active" ]; then
  echo "unexpected error: provision shard '${PROVISION_SHARD_ID}' not in 'active' status. Status is '${PROVISION_SHARD_STATUS_BEFORE}'. End of execution"
  exit 1
fi

echo "setting provision shard '${PROVISION_SHARD_ID}' to maintenance status using CS API"
OUTPUT=$(curl -sS -X PATCH "http://${CS_HOST}:8000/api/clusters_mgmt/v1/provision_shards/${PROVISION_SHARD_ID}" -d '{"status":"maintenance"}')

echo "listing provision shards using CS API after setting shard to maintenance status"
OUTPUT=$(curl -sS -X GET "http://${CS_HOST}:8000/api/clusters_mgmt/v1/provision_shards")
echo -n "${OUTPUT}" | jq ${COMPACT_JSON_JQ_FLAG} .

echo "listing provision shard '${PROVISION_SHARD_ID}'"
OUTPUT=$(curl -sS -X GET "http://${CS_HOST}:8000/api/clusters_mgmt/v1/provision_shards/${PROVISION_SHARD_ID}")
echo -n "${OUTPUT}" | jq ${COMPACT_JSON_JQ_FLAG} .

echo "end of script"
