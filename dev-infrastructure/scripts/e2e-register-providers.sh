#!/bin/bash
set -euo pipefail

# Register the Azure resource providers required by ARO-HCP E2E tests on a set
# of subscriptions, and wait until each reaches the Registered state.
#
# Idempotent: providers already in Registered state are left alone, so a re-run
# against a fully reconciled inventory makes no writes.
#
# Required environment variables:
#   ENV_NAME            - Environment label used in log output only (e.g. "stg")
#   SUBSCRIPTION_IDS    - Space-separated subscription IDs to reconcile
#   PROVIDER_NAMESPACES - Space-separated provider namespaces to register
#
# Optional:
#   POLL_TIMEOUT_SECONDS  - Per-provider wait budget (default 600)
#   POLL_INTERVAL_SECONDS - Delay between registration-state polls (default 10)
#
# Requires the '*/register/action' permission on each target subscription,
# which Contributor already grants — registration does NOT need Owner, unlike
# the RBAC grants in the sibling privileged pipelines.
#
# Subscriptions are addressed by immutable subscription ID. Every az call passes
# --subscription explicitly: templatize points AZURE_CONFIG_DIR at a profile
# whose active subscription is the pipeline's global subscription, not ours.

: "${ENV_NAME:?ENV_NAME is required}"
: "${SUBSCRIPTION_IDS:?SUBSCRIPTION_IDS is required}"
: "${PROVIDER_NAMESPACES:?PROVIDER_NAMESPACES is required}"

POLL_TIMEOUT_SECONDS="${POLL_TIMEOUT_SECONDS:-600}"
POLL_INTERVAL_SECONDS="${POLL_INTERVAL_SECONDS:-10}"

# Failures are collected in a newline-delimited string rather than an array:
# under 'set -u' expanding an empty array is an error on bash < 4.4.
FAILURES=""

# registration_state: echoes the provider's current registrationState, or the
# empty string if the provider or subscription cannot be read.
registration_state() {
    local subscription="$1" namespace="$2"
    az provider show --namespace "${namespace}" --subscription "${subscription}" \
        --query registrationState -o tsv 2>/dev/null || true
}

# ensure_registered: returns 0 once the provider is Registered, 1 if the
# registration call fails or the wait budget is exhausted.
ensure_registered() {
    local subscription="$1" namespace="$2" state deadline

    state="$(registration_state "${subscription}" "${namespace}")"
    if [[ "${state}" == "Registered" ]]; then
        echo "    ${namespace}: already Registered"
        return 0
    fi

    echo "    ${namespace}: ${state:-unreadable} -> registering"
    if ! az provider register --namespace "${namespace}" --subscription "${subscription}" -o none; then
        echo "    ${namespace}: ERROR registration call failed"
        return 1
    fi

    deadline=$((SECONDS + POLL_TIMEOUT_SECONDS))
    while ((SECONDS < deadline)); do
        state="$(registration_state "${subscription}" "${namespace}")"
        if [[ "${state}" == "Registered" ]]; then
            echo "    ${namespace}: Registered"
            return 0
        fi
        sleep "${POLL_INTERVAL_SECONDS}"
    done

    echo "    ${namespace}: ERROR still '${state:-unreadable}' after ${POLL_TIMEOUT_SECONDS}s"
    return 1
}

echo "Registering providers for ${ENV_NAME} E2E subscriptions"

for subscription in ${SUBSCRIPTION_IDS}; do
    echo "  subscription ${subscription}"
    for namespace in ${PROVIDER_NAMESPACES}; do
        # Keep going on failure so one inaccessible subscription or stuck
        # provider does not hide the state of every target behind it.
        if ! ensure_registered "${subscription}" "${namespace}"; then
            FAILURES+="  ${subscription} ${namespace}"$'\n'
        fi
    done
done

if [[ -n "${FAILURES}" ]]; then
    echo "ERROR: provider registration failed for ${ENV_NAME}:"
    printf '%s' "${FAILURES}"
    exit 1
fi

echo "All providers registered for ${ENV_NAME}"
