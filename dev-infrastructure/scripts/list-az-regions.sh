#!/bin/bash

# List Azure regions that support availability zones
# https://docs.microsoft.com/en-us/azure/availability-zones/az-overview

az rest \
  --method get \
  --uri "/subscriptions/$(az account show --query id --output tsv)/locations?api-version=2024-11-01" \
  | jq -r '
    # First, print items with availabilityZoneMappings set
    "Regions with Availability Zones:",
    (.value[] | select(.availabilityZoneMappings) | .name),
    "",
    # Then, print items without availabilityZoneMappings
    "Regions without Availability Zones:",
    (.value[] | select(.availabilityZoneMappings | not) | .name)
  '

