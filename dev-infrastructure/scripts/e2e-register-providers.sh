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
#   POLL_TIMEOUT_SECONDS  - Shared wait budget for all pending registrations
#                           (default 600)
#   POLL_INTERVAL_SECONDS - Delay between registration-state polls (default 10)
#
# Requires the '*/register/action' permission on each target subscription,
# which Contributor already grants — registration does NOT need Owner, unlike
# the RBAC grants in the sibling privileged pipelines.
#
# Subscriptions are addressed by immutable subscription ID. Every az call passes
# --subscription explicitly: templatize points AZURE_CONFIG_DIR at a profile
# whose active subscription is the pipeline's global subscription, not ours.
#
# Runtime budget: templatize kills a Shell step at 30 minutes and offers no
# per-step override (only IstioUpgradeStep can raise it), so this script must
# finish well inside that. It therefore requests every registration first and
# waits for them together, rather than waiting for each provider in turn:
# 'az provider register' returns immediately and Azure processes registrations
# concurrently, so serial waits would multiply POLL_TIMEOUT_SECONDS by the
# number of pending providers. The wait phase is bounded by
# POLL_TIMEOUT_SECONDS regardless of inventory size; only the request phase
# grows with it, at roughly two az calls per subscription/provider pair.

: "${ENV_NAME:?ENV_NAME is required}"
: "${SUBSCRIPTION_IDS:?SUBSCRIPTION_IDS is required}"
: "${PROVIDER_NAMESPACES:?PROVIDER_NAMESPACES is required}"

POLL_TIMEOUT_SECONDS="${POLL_TIMEOUT_SECONDS:-600}"
POLL_INTERVAL_SECONDS="${POLL_INTERVAL_SECONDS:-10}"

# Failures and the pending worklist are collected in newline-delimited strings
# rather than arrays: under 'set -u' expanding an empty array is an error on
# bash < 4.4.
FAILURES=""
PENDING=""

# registration_state: echoes the provider's current registrationState, or the
# empty string if the provider or subscription cannot be read.
registration_state() {
    local subscription="$1" namespace="$2"
    az provider show --namespace "${namespace}" --subscription "${subscription}" \
        --query registrationState -o tsv 2>/dev/null || true
}

echo "Registering providers for ${ENV_NAME} E2E subscriptions"

# Request phase: ask for every registration that is needed, without waiting for
# any of them. Keep going on failure so one inaccessible subscription or stuck
# provider does not hide the state of every target behind it.
for subscription in ${SUBSCRIPTION_IDS}; do
    echo "  subscription ${subscription}"
    for namespace in ${PROVIDER_NAMESPACES}; do
        state="$(registration_state "${subscription}" "${namespace}")"
        if [[ "${state}" == "Registered" ]]; then
            echo "    ${namespace}: already Registered"
            continue
        fi

        echo "    ${namespace}: ${state:-unreadable} -> registering"
        if ! az provider register --namespace "${namespace}" --subscription "${subscription}" -o none; then
            echo "    ${namespace}: ERROR registration call failed"
            FAILURES+="  ${subscription} ${namespace} (registration call failed)"$'\n'
            continue
        fi
        PENDING+="${subscription} ${namespace}"$'\n'
    done
done

# Wait phase: poll everything requested above against one shared deadline.
if [[ -n "${PENDING}" ]]; then
    echo "Waiting up to ${POLL_TIMEOUT_SECONDS}s for pending registrations to reach Registered"
    deadline=$((SECONDS + POLL_TIMEOUT_SECONDS))
    while [[ -n "${PENDING}" ]] && ((SECONDS < deadline)); do
        sleep "${POLL_INTERVAL_SECONDS}"
        still=""
        while read -r subscription namespace; do
            if [[ -z "${subscription}" ]]; then
                continue
            fi
            state="$(registration_state "${subscription}" "${namespace}")"
            if [[ "${state}" == "Registered" ]]; then
                echo "  ${subscription} ${namespace}: Registered"
            else
                still+="${subscription} ${namespace}"$'\n'
            fi
        done <<< "${PENDING}"
        PENDING="${still}"
    done
fi

# Whatever is still pending exhausted the shared budget.
while read -r subscription namespace; do
    if [[ -z "${subscription}" ]]; then
        continue
    fi
    state="$(registration_state "${subscription}" "${namespace}")"
    echo "  ${subscription} ${namespace}: ERROR still '${state:-unreadable}' after ${POLL_TIMEOUT_SECONDS}s"
    FAILURES+="  ${subscription} ${namespace} (timed out waiting for Registered)"$'\n'
done <<< "${PENDING}"

if [[ -n "${FAILURES}" ]]; then
    echo "ERROR: provider registration failed for ${ENV_NAME}:"
    printf '%s' "${FAILURES}"
    exit 1
fi

echo "All providers registered for ${ENV_NAME}"
