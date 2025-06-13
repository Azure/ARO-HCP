#!/bin/bash

source env.defaults

# Run of Setup Steps scripts

./10-infra-setup.sh
./11-infra-mi-setup.sh
export CLUSTER_TMPL_FILE=templates/cluster.default.json
./20-cluster-create.arocurl.sh
export NODEPOOL_TMPL_FILE=templates/nodepool.default.json
export NODEPOOL_NAME=pool-one
./30-nodepool-create.arocurl.sh

# Name of the file with setup e2e metadata
SETUP_FILENAME="${CLUSTER_NAME}.e2e-setup.json"

# Describe the setup configuration in a json structure
./aro-setup-metadata.sh - "${SETUP_FILENAME}" << EOF
{
  "e2e_setup": {
    "name": "cluster-demo",
    "tags": []
  },
  "cluster": {
    "name": "${CLUSTER_NAME}"
  },
  "nodepools": [
    {
      "name": "${NODEPOOL_NAME}"
    }
  ]
}
EOF
