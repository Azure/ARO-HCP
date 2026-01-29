#!/bin/bash
#
# Service status page needs access to Azure storage account to fetch release information.
#
# This script creates:
#   A custom role ("ARO HCP Release Service Status Reader") to read blobs and filter by tags
#   An Azure AD app and service principal assigned with that role
#
# Target:
#   Subscription: ARO Hosted Control Planes (EA Subscription 1)
#   Resource group: global
#   Storage account: aroreleases
# 

set -xe

az role definition create --role-definition ./service-status-reader-role.json

az ad sp create-for-rbac --name service-status-release-reader \
    --role "ARO HCP Release Service Status Reader" \
    --scopes "/subscriptions/1d3378d3-5a3f-4712-85a1-2485495dfc4b/resourceGroups/global/providers/Microsoft.Storage/storageAccounts/aroreleases"