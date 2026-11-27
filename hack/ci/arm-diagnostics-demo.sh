#!/bin/bash
# Temporary, draft-only substitute for infrastructure provisioning in e2e-parallel.
set -euo pipefail
set +x
umask 077

: "${SHARED_DIR:?Prow shared directory required}"
: "${BUILD_ID:?Prow build ID required}"
[[ ${JOB_NAME:-} == pull-ci-Azure-ARO-HCP-main-e2e-parallel && $BUILD_ID =~ ^[0-9]+$ ]]
marker="$SHARED_DIR/arm-diagnostics-demo"

scratch=$(mktemp -d)
trap 'rm -rf -- "$scratch"' EXIT

delete_demo_group() {
    local group exists owner
    [[ -s "$marker" ]] || return 0
    read -r group <"$marker"
    [[ $group =~ ^aro-arm-diag-${BUILD_ID}-[a-f0-9]{12}$ ]] || return 1
    exists=$(az group exists --subscription "$INFRA_SUBSCRIPTION_ID" --name "$group" -o tsv) || return 1
    if [[ $exists == true ]]; then
        owner=$(az group show --subscription "$INFRA_SUBSCRIPTION_ID" --name "$group" --query tags.armDiagnosticsRun -o tsv) || return 1
        [[ $owner == "$BUILD_ID" ]] || return 1
        az group delete --subscription "$INFRA_SUBSCRIPTION_ID" --name "$group" --yes --no-wait || return 1
        az group wait --subscription "$INFRA_SUBSCRIPTION_ID" --name "$group" --deleted --interval 5 --timeout 120 || return 1
        exists=$(az group exists --subscription "$INFRA_SUBSCRIPTION_ID" --name "$group" -o tsv) || return 1
    fi
    [[ $exists == false ]] || return 1
    echo 'ARM diagnostics demo: disposable resource group deletion verified.'
}

cleanup() {
    if [[ -s "$marker" ]]; then
        if ! delete_demo_group >"$scratch/cleanup.log" 2>&1; then
            echo 'ARM diagnostics demo: could not verify deletion of the recorded demo group.' >&2
            return 1
        fi
        grep '^ARM diagnostics demo:' "$scratch/cleanup.log"
    fi
}

case "$1" in
cleanup)
    # The standard Prow post-step has already logged in to the same infra subscription.
    : "${INFRA_SUBSCRIPTION_ID:?Prow infra subscription required}"
    cleanup
    ;;
run)
    : "${LOCATION:?Prow location required}"
    # These are written by the unmodified slot-manager acquire step.
    [[ -s "$SHARED_DIR/aro-hcp-slot-state.yaml" && -s "$SHARED_DIR/aro-hcp-slot.env" ]]
    : >"$marker"
    trap 'echo "ARM diagnostics demo: Azure login failed." >&2' ERR
    source "$(dirname "$0")/az-login.sh" >"$scratch/login.log" 2>&1
    trap - ERR
    rg="aro-arm-diag-${BUILD_ID}-$(tr -d '-' </proc/sys/kernel/random/uuid | cut -c1-12)"
    if ! exists=$(az group exists --subscription "$INFRA_SUBSCRIPTION_ID" --name "$rg" -o tsv 2>"$scratch/group.log") || [[ $exists != false ]]; then
        echo 'ARM diagnostics demo: could not establish that the disposable group name is unused.' >&2
        exit 1
    fi
    printf '%s\n' "$rg" >"$marker"
    # Retry cleanup in the normal Prow post-step if this pod is stopped or cleanup fails.
    trap 'status=$?; cleanup || status=1; rm -rf -- "$scratch"; exit "$status"' EXIT
    if ! az group create --subscription "$INFRA_SUBSCRIPTION_ID" --name "$rg" --location "$LOCATION" --tags "armDiagnosticsRun=$BUILD_ID" -o none >"$scratch/create.log" 2>&1; then
        echo 'ARM diagnostics demo: disposable resource group creation failed.' >&2
        exit 1
    fi

    export ARM_DEMO_RESOURCE_GROUP="$rg"
    live_status=0
    mock_status=0
    arm-diagnostics-demo.test -test.v -test.run '^TestARMDiagnosticsDemoLive$' >"$scratch/live.log" 2>&1 || live_status=$?
    arm-diagnostics-demo.test -test.v -test.run '^TestARMDiagnosticsDemoForbidden$' >"$scratch/mock.log" 2>&1 || mock_status=$?

    echo '=== Real nested ARM failure: templatize JUnit output (Azure IDs scrubbed for publication) ==='
    awk '/^[[:space:]]+time=.*(Step errored|ARM deployment operation failed)/ { print }' "$scratch/live.log" |
        sed -E 's/[[:xdigit:]]{8}(-[[:xdigit:]]{4}){3}-[[:xdigit:]]{12}/<redacted>/g'
    echo '=== Mocked ARM 403: templatize JUnit output (synthetic subscription only) ==='
    awk '/^[[:space:]]+time=.*Step errored/ { print }' "$scratch/mock.log"

    if (( live_status != 0 || mock_status != 0 )); then
        echo "ARM diagnostics demo: fixture did not produce both expected errors (live=$live_status, mock=$mock_status)." >&2
        exit 1
    fi
    if ! az deployment operation group list --subscription "$INFRA_SUBSCRIPTION_ID" --resource-group "$rg" --name arm-diagnostics-inner -o json >"$scratch/operations.json" 2>"$scratch/operations.log" ||
        ! jq -e 'any(.[]; .properties.provisioningState == "Failed" and .properties.statusMessage != null)' "$scratch/operations.json" > /dev/null; then
        echo 'ARM diagnostics demo: Azure did not return a failed operation with an error inside the nested deployment.' >&2
        exit 1
    fi
    echo 'ARM diagnostics demo: Azure recorded a failed nested operation. This provisioning step fails intentionally; no underlay was created.'
    exit 1
    ;;
*) exit 2 ;;
esac
