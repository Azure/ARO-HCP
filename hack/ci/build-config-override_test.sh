#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/../.."
scratch="$(mktemp -d)"
trap 'rm -rf "${scratch}"' EXIT

run_override() {
    env -i PATH="${PATH}" SHARED_DIR="${scratch}" DEPLOY_ENV=pers \
        LEASED_INFRASTRUCTURE_IDENTITY_BUNDLE="$1" \
        bash -euo pipefail hack/ci/build-config-override.sh > "${scratch}/output.log" 2>&1
}

run_override ""
yq -e '.clouds.dev.environments.pers.defaults.infrastructureIdentities == null' \
    "${scratch}/config-override.yaml" > /dev/null

for field in serviceResourceGroup managementResourceGroups; do
    for name in a 'AZaz09_rg.(bundle)-' "$(printf 'a%.0s' {1..90})"; do
        bundle="$(jq -cn --arg field "${field}" --arg name "${name}" '
            {serviceResourceGroup: "service-rg", managementResourceGroups: {"1": "management-rg"}}
            | if $field == "serviceResourceGroup" then .serviceResourceGroup = $name
              else .managementResourceGroups["2"] = $name end')"
        run_override "${bundle}"
        yq -o=json '.clouds.dev.environments.pers.defaults.infrastructureIdentities' \
            "${scratch}/config-override.yaml" | jq -e --arg field "${field}" --arg name "${name}" '
                .useLeased == true and
                if $field == "serviceResourceGroup" then .serviceResourceGroup == $name
                else .managementResourceGroups == ("1=management-rg,2=" + $name) end' > /dev/null
    done

    for name in "" "$(printf 'a%.0s' {1..91})" 'rg.' '.' "bad'rg" 'bad\rg' \
        'bad/rg' 'bad rg' $'rg\n' 'rg,2=other'; do
        bundle="$(jq -cn --arg field "${field}" --arg name "${name}" '
            {serviceResourceGroup: "service-rg", managementResourceGroups: {"1": "management-rg"}}
            | if $field == "serviceResourceGroup" then .serviceResourceGroup = $name
              else .managementResourceGroups["2"] = $name end')"
        if run_override "${bundle}"; then
            echo "ERROR: accepted invalid ${field} name: ${name}" >&2
            exit 1
        fi
        grep -q 'ERROR: LEASED_INFRASTRUCTURE_IDENTITY_BUNDLE' "${scratch}/output.log"
        yq -e '.clouds.dev.environments.pers.defaults.infrastructureIdentities == null' \
            "${scratch}/config-override.yaml" > /dev/null
    done
done

for bundle in \
    '{"serviceResourceGroup":123,"managementResourceGroups":{"1":"rg"}}' \
    '{"serviceResourceGroup":"rg","managementResourceGroups":{"1":123}}' \
    '{"serviceResourceGroup":"rg","managementResourceGroups":{"1\n":"rg"}}'; do
    if run_override "${bundle}"; then
        echo "ERROR: accepted malformed bundle: ${bundle}" >&2
        exit 1
    fi
    grep -q 'ERROR: LEASED_INFRASTRUCTURE_IDENTITY_BUNDLE' "${scratch}/output.log"
done

echo "PASS: infrastructure bundle validation rejects malformed names before publishing reuse settings"
