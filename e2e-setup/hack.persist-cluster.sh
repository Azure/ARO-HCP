#!/bin/bash

# Optional hack script to be used in the dev subscription only to flag cluster
# resource groups with persist:true tag to avoid it to be deleted by a cleanup
# job. Use only when necessary.

set -o errexit
set -o nounset
set -o pipefail
set -o xtrace

source env.defaults

for RG in ${CUSTOMER_RG_NAME} ${MANAGED_RESOURCE_GROUP} ; do
  echo az tag create --resource-id "/subscriptions/${CUSTOMER_SUBSCRIPTION}/resourceGroups/${RG}" --tags persist=true
done
