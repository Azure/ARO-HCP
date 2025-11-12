#!/bin/bash

# This file contains arbitrary commands that will be executed after the image tag validation
# Add your custom commands below:

echo "\n=== Testing clusters-service API endpoint ==="
curl -sS -X GET http://clusters-service:8000/api/clusters_mgmt/v1/provision_shards

