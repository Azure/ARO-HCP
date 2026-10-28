#!/bin/bash
set -euo pipefail

# The Cost Management settings API persists the setting but omits `kind` from
# its GET response. ARM deployments treat that response as invalid and fail
# after the write succeeds, so use the API directly and verify the persisted
# setting instead.
#
# Required environment variable:
#   SUBSCRIPTIONS - Comma-separated subscriptions, each either "<id>" or
#                   "<id>=<display name>" (the name is ignored)
: "${SUBSCRIPTIONS:?SUBSCRIPTIONS is required}"

# Empty entries (e.g. from concatenated lists where one side is empty) are dropped.
SUBSCRIPTION_IDS="$(tr ',' '\n' <<< "${SUBSCRIPTIONS}" | cut -d= -f1 | grep -v '^$' || true)"
: "${SUBSCRIPTION_IDS:?SUBSCRIPTIONS contains no subscription IDs}"

for subscription in ${SUBSCRIPTION_IDS}; do
    id="/subscriptions/${subscription}/providers/Microsoft.CostManagement/settings/taginheritance"
    url="${id}?api-version=2025-03-01"
    echo "Enabling Cost Management tag inheritance on ${subscription}"
    az rest --method put --url "${url}" --headers 'Content-Type=application/json' \
        --body '{"kind":"taginheritance","properties":{"preferContainerTags":false}}' -o none

    setting="$(az rest --method get --url "${url}" -o json)"
    if ! jq -e --arg id "${id}" \
        '.id == $id and .name == "taginheritance" and .type == "Microsoft.CostManagement/Settings" and .scope == "Subscription" and .properties.preferContainerTags == false' \
        <<< "${setting}" > /dev/null; then
        echo "ERROR: tag inheritance readback did not match on ${subscription}" >&2
        exit 1
    fi
    echo "Verified Cost Management tag inheritance on ${subscription} (resource tags take precedence)"
done
