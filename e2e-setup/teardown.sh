#!/bin/bash

source env.defaults

# Here we just delete the customer resource group.
#
# Note that:
#  * We assume that no resources are created outside of customer resource
#    group during E2E setup.
#  * In CI runs, we assume that the E2E tests run tests for cluster and node
#    pool removal, so that when this script is executed, the cluster is no
#    longer present.
#  * If that is not the case, the teardown script will still remove the cluster
#    as any other resource in the resource group (unles this is done in dev
#    enviroment).

az group delete --name "${CUSTOMER_RG_NAME}" --location "${LOCATION}"
