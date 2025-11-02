#!/bin/bash
# Common utilities for resource cleaner scripts

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

log_info() {
    echo -e "${GREEN}[INFO]${NC} $*"
}

log_warn() {
    echo -e "${YELLOW}[WARN]${NC} $*"
}

log_error() {
    echo -e "${RED}[ERROR]${NC} $*"
}

# Configuration
RETENTION_HOURS=3
MAESTRO_URL="http://localhost:8002"
MI_KEYVAULT="ah-cspr-mi-usw3-1"
CX_KEYVAULT="ah-cspr-cx-usw3-1"
ACR_NAME="arohcpocpdev"
ARO_HCP_DEV_SUBSCRIPTION="1d3378d3-5a3f-4712-85a1-2485495dfc4b"
ACR_RESOURCE_GROUP="global"
MANAGED_RG_PREFIX="e2e_tests_mrg_name"
CUSTOMER_RG_PREFIX="pr-check-e2e-tests-resource-group-"

