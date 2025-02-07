#!/bin/bash

# List Azure regions that support availability zones
# https://docs.microsoft.com/en-us/azure/availability-zones/az-overview

az rest \
  --method get \
  --uri "/subscriptions/$(az account show --query id --output tsv)/locations?api-version=2024-11-01" \
  | jq -r '
    reduce .value[] as $item ({}; .[$item.name] = {
      availabilityZones: (
        if $item.availabilityZoneMappings then
          $item.availabilityZoneMappings | map(.logicalZone)
        else
          []
        end
      )
    }) | to_entries | sort_by(.key) | from_entries'
