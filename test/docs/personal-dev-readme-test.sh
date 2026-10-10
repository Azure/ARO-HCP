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

# Validates that docs/personal-dev.md stays in step with the repo.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" &>/dev/null && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
README_FILE="${REPO_ROOT}/docs/personal-dev.md"

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

TESTS_RUN=0
TESTS_PASSED=0
TESTS_FAILED=0

# 'dry-run' checks the docs only, needs no tooling and no network. 'full' also
# asserts the documented tools are installed locally.
TEST_MODE="${TEST_MODE:-dry-run}"

case "${TEST_MODE}" in
    dry-run | full) ;;
    *)
        echo "Unknown TEST_MODE '${TEST_MODE}' (expected 'dry-run' or 'full')" >&2
        exit 2
        ;;
esac

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

# Print the target of every markdown link, one per line. awk, not grep: a greedy
# grep keeps only the last link on a line, and non-greedy needs grep -P, which
# BSD grep on macOS lacks.
extract_link_targets() {
    awk '{
        rest = $0
        while (match(rest, /\[[^]]*\]\([^)]*\)/)) {
            link = substr(rest, RSTART, RLENGTH)
            sub(/^\[[^]]*\]\(/, "", link)
            sub(/\)$/, "", link)
            print link
            rest = substr(rest, RSTART + RLENGTH)
        }
    }' "$1"
}

test_pass() {
    TESTS_PASSED=$((TESTS_PASSED + 1))
    log_info "✓ PASS"
}

test_fail() {
    TESTS_FAILED=$((TESTS_FAILED + 1))
    log_error "✗ FAIL: $1"
}

test_readme_exists() {
    test_start "README file exists"
    if [[ -f "${README_FILE}" ]]; then
        test_pass
    else
        test_fail "README file not found at ${README_FILE}"
    fi
}

test_prerequisites_documented() {
    test_start "Prerequisites section exists"
    if grep -q "## Prerequisites" "${README_FILE}"; then
        test_pass
    else
        test_fail "Prerequisites section not found"
    fi
}

test_az_version_documented() {
    test_start "Azure CLI version requirement documented"
    if grep -q "az.*utility.*>=.*2.68.0" "${README_FILE}"; then
        test_pass
    else
        test_fail "Azure CLI version requirement not found or outdated"
    fi
}

test_make_targets_exist() {
    test_start "Documented make targets exist"

    local targets=("personal-dev-env" "local-pers-dev-env" "infra.svc.aks.kubeconfigfile"
                   "infra.mgmt.aks.kubeconfigfile" "cleanup-entrypoint/Region")
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

test_persist_documented() {
    test_start "PERSIST environment variable documented"
    if grep -q "PERSIST=true" "${README_FILE}"; then
        test_pass
    else
        test_fail "PERSIST environment variable not documented"
    fi
}

test_cleanup_retention_documented() {
    test_start "Cleanup retention periods documented"
    if grep -q "48h" "${README_FILE}" && grep -q "15 days" "${README_FILE}"; then
        test_pass
    else
        test_fail "Cleanup retention periods not correctly documented"
    fi
}

test_resource_group_patterns() {
    test_start "Resource group naming patterns documented"
    if grep -q "hcp-underlay-.*-svc" "${README_FILE}" &&
       grep -q "hcp-underlay-.*-mgmt" "${README_FILE}"; then
        test_pass
    else
        test_fail "Resource group naming patterns not documented"
    fi
}

test_azure_ids_format() {
    test_start "Azure IDs are valid GUIDs"

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

test_observability_section() {
    test_start "Observability section exists"
    if grep -q "## Observability" "${README_FILE}"; then
        test_pass
    else
        test_fail "Observability section not found"
    fi
}

test_cleanup_command() {
    test_start "Cleanup command is valid"
    if grep -q "make cleanup-entrypoint/Region CLEANUP_DRY_RUN=false CLEANUP_WAIT=true" "${README_FILE}"; then
        test_pass
    else
        test_fail "Cleanup command not found or incorrect"
    fi
}

#
# Offline: targets with a URI scheme are skipped, never fetched.
test_internal_links() {
    test_start "Internal documentation links resolve on disk"

    local all_valid=true
    local checked=0
    local skipped=0

    local base_dir
    base_dir="$(cd "$(dirname "${README_FILE}")" &>/dev/null && pwd)"

    while IFS= read -r target; do
        # Drop any "#anchor" suffix; a bare "#anchor" link leaves an empty path.
        local file_path="${target%%#*}"

        # Same-page anchor, or a link with a URI scheme we deliberately do not follow.
        if [[ -z "${file_path}" ]] || [[ "${file_path}" =~ ^[a-zA-Z][a-zA-Z0-9+.-]*: ]]; then
            skipped=$((skipped + 1))
            continue
        fi

        checked=$((checked + 1))
        if [[ ! -e "${base_dir}/${file_path}" ]]; then
            log_error "  Broken link: ${file_path}"
            all_valid=false
        fi
    done < <(extract_link_targets "${README_FILE}")

    if ${all_valid}; then
        log_info "  ${checked} local link(s) resolved, ${skipped} external/anchor link(s) skipped"
        test_pass
    else
        test_fail "Some internal links are broken"
    fi
}

# TEST_MODE=full only.
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
        version=$(kubectl version --client -o yaml 2>/dev/null | awk '/gitVersion:/ { print $2; exit }')
        version="${version:-unknown}"
        log_info "  Found kubectl version: ${version}"
        test_pass
    else
        test_fail "kubectl not installed"
    fi
}

main() {
    log_info "Starting personal-dev.md README tests (mode: ${TEST_MODE})"
    log_info "================================================"

    test_readme_exists
    test_prerequisites_documented
    test_az_version_documented
    test_make_targets_exist
    test_persist_documented
    test_cleanup_retention_documented
    test_resource_group_patterns
    test_azure_ids_format
    test_observability_section
    test_cleanup_command
    test_internal_links

    if [[ "${TEST_MODE}" == "full" ]]; then
        test_az_cli_available
        test_kubectl_available
    else
        log_info "Skipping tool-availability tests (set TEST_MODE=full to run them)"
    fi

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
