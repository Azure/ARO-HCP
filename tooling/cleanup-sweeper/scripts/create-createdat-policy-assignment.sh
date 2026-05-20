#!/usr/bin/env bash
# Copyright 2026 Microsoft Corporation
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

# Creates or updates a subscription-scoped Azure Policy definition and assignment
# that appends tags.createdAt on new resource groups. Required for cleanup-sweeper
# rg-ordered discovery (see docs/cleanup.md).
#
# Usage:
#   ./create-createdat-policy-assignment.sh [SUBSCRIPTION_ID]
#
# If SUBSCRIPTION_ID is omitted, uses the current default from `az account show`.
#
# Environment overrides:
#   POLICY_DEF_NAME     policy definition resource name (default: aro-rg-createdat-tag)
#   POLICY_ASSIGN_NAME  assignment resource name (default: aro-createdat-rg)
#   POLICY_DISPLAY_NAME display name for definition and assignment (default: ARO-CreatedAt Tag)

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
RULE_FILE="${SCRIPT_DIR}/rg-createdat-policy-rule.json"

usage() {
	echo "Usage: $0 [SUBSCRIPTION_ID]" >&2
	echo "  SUBSCRIPTION_ID  Azure subscription GUID (optional if az default account is set)" >&2
	exit 2
}

if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
	usage
fi

if ! command -v az >/dev/null 2>&1; then
	echo "error: az (Azure CLI) not found in PATH" >&2
	exit 1
fi

SUBSCRIPTION_ID="${1:-${AZURE_SUBSCRIPTION_ID:-}}"
if [[ -z "${SUBSCRIPTION_ID}" ]]; then
	SUBSCRIPTION_ID="$(az account show --query id -o tsv)"
fi

if [[ -z "${SUBSCRIPTION_ID}" ]]; then
	echo "error: no subscription id; pass one as argv[1] or run az account set" >&2
	exit 1
fi

if [[ ! -f "${RULE_FILE}" ]]; then
	echo "error: policy rule file missing: ${RULE_FILE}" >&2
	exit 1
fi

DEF_NAME="${POLICY_DEF_NAME:-aro-rg-createdat-tag}"
ASSIGN_NAME="${POLICY_ASSIGN_NAME:-aro-createdat-rg}"
DISPLAY_NAME="${POLICY_DISPLAY_NAME:-ARO-CreatedAt Tag}"
DESCRIPTION="Append tags.createdAt on resource groups when missing (append + utcNow). mode=All for rg-ordered cleanup-sweeper discovery."

DEF_ID="/subscriptions/${SUBSCRIPTION_ID}/providers/Microsoft.Authorization/policyDefinitions/${DEF_NAME}"
SCOPE="/subscriptions/${SUBSCRIPTION_ID}"

echo "Subscription: ${SUBSCRIPTION_ID}"
echo "Definition:   ${DEF_NAME}"
echo "Assignment: ${ASSIGN_NAME}"

if az policy definition show --subscription "${SUBSCRIPTION_ID}" --name "${DEF_NAME}" &>/dev/null; then
	echo "Updating policy definition ${DEF_NAME}..."
	az policy definition update \
		--subscription "${SUBSCRIPTION_ID}" \
		--name "${DEF_NAME}" \
		--display-name "${DISPLAY_NAME}" \
		--description "${DESCRIPTION}" \
		--mode All \
		--rules @"${RULE_FILE}"
else
	echo "Creating policy definition ${DEF_NAME}..."
	az policy definition create \
		--subscription "${SUBSCRIPTION_ID}" \
		--name "${DEF_NAME}" \
		--display-name "${DISPLAY_NAME}" \
		--description "${DESCRIPTION}" \
		--mode All \
		--rules @"${RULE_FILE}"
fi

if az policy assignment show --subscription "${SUBSCRIPTION_ID}" --name "${ASSIGN_NAME}" &>/dev/null; then
	echo "Policy assignment ${ASSIGN_NAME} already exists; leaving it unchanged."
	echo "To re-point or recreate it, delete the assignment in the portal or:"
	echo "  az policy assignment delete --subscription ${SUBSCRIPTION_ID} --name ${ASSIGN_NAME}"
else
	echo "Creating policy assignment ${ASSIGN_NAME}..."
	az policy assignment create \
		--subscription "${SUBSCRIPTION_ID}" \
		--name "${ASSIGN_NAME}" \
		--display-name "${DISPLAY_NAME}" \
		--policy "${DEF_ID}" \
		--scope "${SCOPE}"
fi
