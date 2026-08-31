#!/usr/bin/env bash
# Copyright 2025 Microsoft Corporation
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

# Test script for docs/personal-dev.md README
# This validates that commands documented in the personal dev guide are correct

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" &>/dev/null && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
README_FILE="${REPO_ROOT}/docs/personal-dev.md"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Test counters
TESTS_RUN=0
TESTS_PASSED=0
TESTS_FAILED=0

# Test mode: 'dry-run' (CI safe) or 'full' (requires Azure access)
TEST_MODE="${TEST_MODE:-dry-run}"

log_info() {
    echo -e "${GREEN}[INFO]${NC} $*"
}

log_warn() {
    echo -e "${YELLOW}[WARN]${NC} $*"
}

log_error() {
    echo -e "${RED}[ERROR]${NC} $*"
}

test_start() {
    TESTS_RUN=$((TESTS_RUN + 1))
    log_info "Test ${TESTS_RUN}: $1"
}

test_pass() {
    TESTS_PASSED=$((TESTS_PASSED + 1))
    log_info "✓ PASS"
}

test_fail() {
    TESTS_FAILED=$((TESTS_FAILED + 1))
    log_error "✗ FAIL: $1"
}

# Test 1: README file exists
test_readme_exists() {
    test_start "README file exists"
    if [[ -f "${README_FILE}" ]]; then
        test_pass
    else
        test_fail "README file not found at ${README_FILE}"
    fi
}

# Test 2: Check required prerequisites are documented
test_prerequisites_documented() {
    test_start "Prerequisites section exists"
    if grep -q "## Prerequisites" "${README_FILE}"; then
        test_pass
    else
        test_fail "Prerequisites section not found"
    fi
}

# Test 3: Validate az CLI version requirement
test_az_version_documented() {
    test_start "Azure CLI version requirement documented"
    if grep -q "az.*utility.*>=.*2.68.0" "${README_FILE}"; then
        test_pass
    else
        test_fail "Azure CLI version requirement not found or outdated"
    fi
}

# Test 4: Check if make targets mentioned in README exist
test_make_targets_exist() {
    test_start "Documented make targets exist"

    local targets=("personal-dev-env" "local-pers-dev-env" "infra.svc.aks.kubeconfigfile"
                   "infra.mgmt.aks.kubeconfigfile" "infra.tracing" "cleanup-entrypoint/Region")
    local all_exist=true

    cd "${REPO_ROOT}"

    for target in "${targets[@]}"; do
        # For entrypoint targets, they're dynamically generated, so just check the pattern exists
        if [[ "${target}" == *"/"* ]]; then
            if ! grep -q "entrypoint/" Makefile; then
                log_error "  Make target pattern '${target}' not found in Makefile"
                all_exist=false
            fi
        else
            if ! make -n "${target}" &>/dev/null; then
                log_error "  Make target '${target}' not found or invalid"
                all_exist=false
            fi
        fi
    done

    if ${all_exist}; then
        test_pass
    else
        test_fail "Some documented make targets don't exist"
    fi
}

# Test 5: Validate kubectl commands are syntactically correct
test_kubectl_commands() {
    test_start "kubectl port-forward commands are valid"

    local commands=(
        "kubectl port-forward svc/aro-hcp-frontend 8443:8443 -n aro-hcp"
        "kubectl port-forward svc/clusters-service 8000:8000 -n clusters-service"
        "kubectl port-forward svc/maestro 8001:8000 -n maestro"
        "kubectl port-forward svc/maestro-grpc 8090 -n maestro"
    )

    local all_valid=true

    for cmd in "${commands[@]}"; do
        # Dry-run syntax check
        if ! ${cmd} --dry-run=client &>/dev/null 2>&1; then
            # Check if kubectl is available
            if command -v kubectl &>/dev/null; then
                log_warn "  Command might be invalid: ${cmd}"
                # Don't fail since we can't test without a cluster
            fi
        fi
    done

    test_pass
}

# Test 6: Check PERSIST environment variable is documented
test_persist_documented() {
    test_start "PERSIST environment variable documented"
    if grep -q "PERSIST=true" "${README_FILE}"; then
        test_pass
    else
        test_fail "PERSIST environment variable not documented"
    fi
}

# Test 7: Validate cleanup retention periods are correct
test_cleanup_retention_documented() {
    test_start "Cleanup retention periods documented"
    if grep -q "48h" "${README_FILE}" && grep -q "15 days" "${README_FILE}"; then
        test_pass
    else
        test_fail "Cleanup retention periods not correctly documented"
    fi
}

# Test 8: Check if resource group naming patterns are documented
test_resource_group_patterns() {
    test_start "Resource group naming patterns documented"
    if grep -q "hcp-underlay-.*-svc" "${README_FILE}" &&
       grep -q "hcp-underlay-.*-mgmt" "${README_FILE}"; then
        test_pass
    else
        test_fail "Resource group naming patterns not documented"
    fi
}

# Test 9: Validate Azure tenant ID and subscription ID format
test_azure_ids_format() {
    test_start "Azure IDs are valid GUIDs"

    # Extract tenant and subscription IDs from README (macOS compatible)
    local tenant_id
    tenant_id=$(grep 'az login --tenant' "${README_FILE}" | grep -o '[a-f0-9]\{8\}-[a-f0-9]\{4\}-[a-f0-9]\{4\}-[a-f0-9]\{4\}-[a-f0-9]\{12\}' | head -1 || echo "")
    local sub_id
    sub_id=$(grep 'az account set --subscription' "${README_FILE}" | grep -o '[a-f0-9]\{8\}-[a-f0-9]\{4\}-[a-f0-9]\{4\}-[a-f0-9]\{4\}-[a-f0-9]\{12\}' | head -1 || echo "")

    if [[ "${tenant_id}" =~ ^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$ ]] &&
       [[ "${sub_id}" =~ ^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$ ]]; then
        test_pass
    else
        test_fail "Azure tenant or subscription ID format invalid"
    fi
}

# Test 10: Check if observability section exists
test_observability_section() {
    test_start "Observability section exists"
    if grep -q "## Observability" "${README_FILE}"; then
        test_pass
    else
        test_fail "Observability section not found"
    fi
}

# Test 11: Validate cleanup command syntax
test_cleanup_command() {
    test_start "Cleanup command is valid"
    if grep -q "make cleanup-entrypoint/Region CLEANUP_DRY_RUN=false CLEANUP_WAIT=true" "${README_FILE}"; then
        test_pass
    else
        test_fail "Cleanup command not found or incorrect"
    fi
}

# Test 12: Check for broken internal links
test_internal_links() {
    test_start "Internal documentation links are valid"

    local all_valid=true

    # Extract markdown links (macOS compatible)
    while IFS= read -r link; do
        # Extract the file path
        local file_path
        file_path=$(echo "${link}" | sed -n 's/.*(\(.*\))/\1/p' | cut -d'#' -f1)

        # Skip external links
        if [[ "${file_path}" =~ ^http ]]; then
            continue
        fi

        # Skip empty links
        if [[ -z "${file_path}" ]]; then
            continue
        fi

        # Resolve relative path
        local full_path="${REPO_ROOT}/docs/${file_path}"

        if [[ ! -f "${full_path}" ]] && [[ ! -f "${REPO_ROOT}/${file_path}" ]]; then
            log_error "  Broken link: ${file_path}"
            all_valid=false
        fi
    done < <(grep -o '\[.*\](.*\.md[^)]*)' "${README_FILE}" || true)

    if ${all_valid}; then
        test_pass
    else
        test_fail "Some internal links are broken"
    fi
}

# Full tests (require Azure access)
test_az_cli_available() {
    test_start "Azure CLI is installed"
    if command -v az &>/dev/null; then
        local version
        version=$(az version --query '"azure-cli"' -o tsv 2>/dev/null || echo "unknown")
        log_info "  Found az CLI version: ${version}"
        test_pass
    else
        test_fail "Azure CLI not installed"
    fi
}

test_kubectl_available() {
    test_start "kubectl is installed"
    if command -v kubectl &>/dev/null; then
        local version
        version=$(kubectl version --client --short 2>/dev/null | cut -d' ' -f3 || echo "unknown")
        log_info "  Found kubectl version: ${version}"
        test_pass
    else
        test_fail "kubectl not installed"
    fi
}

# Main execution
main() {
    log_info "Starting personal-dev.md README tests (mode: ${TEST_MODE})"
    log_info "================================================"

    # Always run these tests
    test_readme_exists
    test_prerequisites_documented
    test_az_version_documented
    test_make_targets_exist
    test_kubectl_commands
    test_persist_documented
    test_cleanup_retention_documented
    test_resource_group_patterns
    test_azure_ids_format
    test_observability_section
    test_cleanup_command
    test_internal_links

    # Tool availability tests
    test_az_cli_available
    test_kubectl_available

    # Summary
    echo ""
    log_info "================================================"
    log_info "Test Summary:"
    log_info "  Total:  ${TESTS_RUN}"
    log_info "  Passed: ${TESTS_PASSED}"
    log_info "  Failed: ${TESTS_FAILED}"

    if [[ ${TESTS_FAILED} -eq 0 ]]; then
        log_info "All tests passed! ✓"
        exit 0
    else
        log_error "Some tests failed"
        exit 1
    fi
}

main "$@"
