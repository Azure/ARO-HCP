#!/bin/bash
#
# This script reads an EV2 ServiceConfig json file and gets a region short name
#

set -euo pipefail

# Bash current file directory
SCRIPT_DIR=$(dirname "$(realpath "${BASH_SOURCE[0]}")")


REGION=${1:-}

if [ -z "${REGION}" ]; then
  echo "Usage: $0 <region>"
  exit 1
fi

REGION_SHORT=$(
  jq -r --arg region "${REGION}" '
    .Geographies[].Regions[]
    | select(.Name == $region)
    | .Settings.regionShortName
  ' "${SCRIPT_DIR}/serviceconfig.json"
)

if [[ -z "$REGION_SHORT" ]]; then
    echo "Region name (or short name) not found for region: $REGION" >&2
    echo "Please check the region name in the serviceconfig.json file and update the file as necessary" >&2
    exit 1
fi

printf "%s" "${REGION_SHORT}"
