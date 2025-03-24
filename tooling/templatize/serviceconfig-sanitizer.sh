#!/bin/bash
#
# This script reads an EV2 ServiceConfig json file and sanitizes it for use in our own tooling
#

set -euo pipefail

INPUT_FILE=${1:-}

if [ -z "${INPUT_FILE}" ]; then
  echo "Usage: $0 <ServiceConfig.json>"
  exit 1
fi

jq -r '
{
  Settings: {},
  Geographies: [
    .Geographies[] | {
      Name,
      Settings: {
        geoShortId: .Settings.geoShortId
      },
      Regions: [
        .Regions[] | {
          Name,
          Settings: {
            regionShortName: .Settings.regionShortName
          }
        }
      ]
    }
  ]
}' "${INPUT_FILE}"
