#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage:
  apply-msi-pool.sh [--location <azure-location>] [--environment <env>] [--pool-size <n>]

Options:
  --location       Azure location for the deployment (default: westus3)
  --environment    Environment suffix used in resourceGroupBaseName (default: int)
  --pool-size      poolSize parameter passed to the bicep template (default: 120)
  -h, --help       Show this help message

Examples:
  ./test/hack/apply-msi-pool.sh
  ./test/hack/apply-msi-pool.sh --location eastus --environment stg --pool-size 200
  ./test/hack/apply-msi-pool.sh --location=eastus --environment=stg --pool-size=200
EOF
}

LOCATION="westus3"
ENVIRONMENT="int"
POOL_SIZE="120"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --location)
      LOCATION="${2:-}"
      shift 2
      ;;
    --location=*)
      LOCATION="${1#*=}"
      shift 1
      ;;
    --environment)
      ENVIRONMENT="${2:-}"
      shift 2
      ;;
    --environment=*)
      ENVIRONMENT="${1#*=}"
      shift 1
      ;;
    --pool-size)
      POOL_SIZE="${2:-}"
      shift 2
      ;;
    --pool-size=*)
      POOL_SIZE="${1#*=}"
      shift 1
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "ERROR: unknown argument: $1" >&2
      echo >&2
      usage >&2
      exit 2
      ;;
  esac
done

if [[ -z "${LOCATION}" ]]; then
  echo "ERROR: --location cannot be empty" >&2
  exit 2
fi

if [[ -z "${ENVIRONMENT}" ]]; then
  echo "ERROR: --environment cannot be empty" >&2
  exit 2
fi

if ! [[ "${POOL_SIZE}" =~ ^[0-9]+$ ]] || [[ "${POOL_SIZE}" -le 0 ]]; then
  echo "ERROR: --pool-size must be a positive integer (got: '${POOL_SIZE}')" >&2
  exit 2
fi

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd -- "${SCRIPT_DIR}/../.." && pwd)"
TEMPLATE_FILE="${REPO_ROOT}/test/e2e-setup/bicep/msi-pools.bicep"

RESOURCE_GROUP_BASE_NAME="aro-hcp-test-msi-containers-${ENVIRONMENT}"

az stack sub create \
  --name aro-hcp-msi-pool \
  --location "${LOCATION}" \
  --template-file "${TEMPLATE_FILE}" \
  --parameters "poolSize=${POOL_SIZE}" "resourceGroupBaseName=${RESOURCE_GROUP_BASE_NAME}" \
  --deny-settings-mode None \
  --action-on-unmanage deleteResources
