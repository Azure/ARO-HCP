#!/bin/bash

source env.defaults

# Run of Setup Steps scripts
./10-infra-setup.sh
./11-infra-mi-setup.sh

# Name of the file with setup e2e metadata
SETUP_FILENAME="${CLUSTER_NAME}.e2e-setup.json"

# Describe the setup configuration in a json structure
./aro-setup-metadata.sh - "${SETUP_FILENAME}" << EOF
{
  "e2e_setup": {
    "name": "infra-only-demo",
    "tags": ["customer-infra-only"]
  },
  "cluster": {
    "name": "${CLUSTER_NAME}"
  },
  "nodepools": []
}
EOF
