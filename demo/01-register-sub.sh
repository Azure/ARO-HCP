#!/bin/bash

set -o errexit
set -o nounset
set -o pipefail

source "$(dirname "$0")"/common.sh

correlation_headers | curl -sSi -H @- -X PUT "localhost:8443/subscriptions/1d3378d3-5a3f-4712-85a1-2485495dfc4b?api-version=2.0" --json '{"state":"Registered", "registrationDate": "now", "properties": { "tenantId": "64dc69e4-d083-49fc-9569-ebece1dd1408"}}'
