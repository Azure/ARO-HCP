#!/bin/bash
set -euo pipefail

echo "Enable static Website on storage account ${StorageAccountName}"
az storage blob service-properties update --static-website true --account-name ${StorageAccountName} --auth-mode login
