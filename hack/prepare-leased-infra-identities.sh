#!/usr/bin/env bash
# shellcheck disable=SC2016 # jq programs use their own variable syntax.
set -euo pipefail

: "${TEMPLATIZE:?TEMPLATIZE must be set}"
: "${YQ:?YQ must be set}"
: "${JQ:?JQ must be set}"
: "${CONFIG_FILE:?CONFIG_FILE must be set}"
: "${LEASED_INFRA_IDENTITY_PREFIX:?LEASED_INFRA_IDENTITY_PREFIX must be set}"
: "${LEASED_INFRA_IDENTITY_DIR:?LEASED_INFRA_IDENTITY_DIR must be set}"

if [[ "${DEPLOY_ENV:-pers}" != "pers" ]]; then
    echo 'ERROR: leased personal-dev identity preparation requires DEPLOY_ENV=pers' >&2
    exit 1
fi
if [[ ! "${LEASED_INFRA_IDENTITY_PREFIX}" =~ ^[a-zA-Z0-9][a-zA-Z0-9_-]{0,69}$ ]] ||
   [[ "${LEASED_INFRA_IDENTITY_PREFIX}" == hcp-underlay-* ]]; then
    echo 'ERROR: identity RG prefix must be 1-70 alphanumeric, hyphen or underscore characters and must not start with hcp-underlay-' >&2
    exit 1
fi

mkdir -p "${LEASED_INFRA_IDENTITY_DIR}"
override="${LEASED_INFRA_IDENTITY_DIR}/config-override.yaml"
parameters="${LEASED_INFRA_IDENTITY_DIR}/deployment-parameters.json"

"${JQ}" -n --arg prefix "${LEASED_INFRA_IDENTITY_PREFIX}" '{
    clouds: {dev: {environments: {pers: {defaults: {
        infrastructureIdentities: {
            useLeased: true,
            serviceResourceGroup: ($prefix + "-svc"),
            managementResourceGroups: ("1=" + $prefix + "-mgmt-1,2=" + $prefix + "-mgmt-2")
        },
        mgmt: {stamps: {count: 2}}
    }}}}}
}' | "${YQ}" -P > "${override}"

for stamp in 1 2; do
    "${TEMPLATIZE}" configuration render \
        --service-config-file "${CONFIG_FILE}" \
        --config-file-override "${override}" \
        --dev-settings-file tooling/templatize/settings.yaml \
        --cloud dev --environment pers --ev2-cloud public \
        --stamp "${stamp}" \
        --output "${LEASED_INFRA_IDENTITY_DIR}/config-${stamp}.yaml"
    "${YQ}" -o=json "${LEASED_INFRA_IDENTITY_DIR}/config-${stamp}.yaml" \
        > "${LEASED_INFRA_IDENTITY_DIR}/config-${stamp}.json"
done

"${JQ}" -n --arg prefix "${LEASED_INFRA_IDENTITY_PREFIX}" \
    --slurpfile first "${LEASED_INFRA_IDENTITY_DIR}/config-1.json" \
    --slurpfile second "${LEASED_INFRA_IDENTITY_DIR}/config-2.json" '
    def serviceNames: [
        .frontend.managedIdentityName,
        .backend.managedIdentityName,
        "aro-billing",
        .maestro.server.managedIdentityName,
        .clustersService.managedIdentityName,
        .logs.mdsd.msiName,
        "prometheus",
        .msiCredentialsRefresher.managedIdentityName,
        .adminApi.managedIdentityName,
        .sessiongate.managedIdentityName,
        .customExporter.managedIdentityName,
        .fleet.managedIdentityName,
        (.svc.aks.name + "-msi"),
        "image-puller"
    ];
    def managementNames: [
        .maestro.agent.managedIdentityName,
        .mgmtAgent.managedIdentityName,
        .logs.mdsd.msiName,
        "prometheus",
        "velero",
        .kubeApplier.managedIdentityName,
        (.mgmt.aks.name + "-msi"),
        "image-puller"
    ];
    def validNames:
        all(.[]; type == "string" and length > 0) and length == (unique | length);
    if $first[0].svc.subscription.key != $first[0].mgmt.subscription.key or
       $first[0].svc.subscription.key != $second[0].mgmt.subscription.key then
        error("personal-dev identity provisioning requires service and management in the same subscription")
    elif $first[0].mgmt.stampIdentifier != "1" or $second[0].mgmt.stampIdentifier != "2" then
        error("personal-dev identity provisioning requires explicit management stamps 1 and 2")
    else
        {
            contentVersion: "1.0.0.0",
            parameters: {
                location: {value: $first[0].region},
                serviceBundle: {value: {
                    resourceGroupName: ($prefix + "-svc"),
                    identityNames: ($first[0] | serviceNames)
                }},
                managementBundles: {value: [
                    $first[0], $second[0]
                    | {
                        stampIdentifier: .mgmt.stampIdentifier,
                        resourceGroupName: ($prefix + "-mgmt-" + .mgmt.stampIdentifier),
                        identityNames: managementNames
                    }
                ]}
            }
        }
        | if ([.parameters.serviceBundle.value] + .parameters.managementBundles.value
              | all(.[]; .identityNames | validNames)) then .
          else error("identity bundles must contain distinct, non-empty configured identity names")
          end
    end
' > "${parameters}"

echo "Prepared 30 identities (14 service + 8 per management stamp) in ${parameters}"
echo "Personal-dev override: ${override}"
echo 'No Azure resources have been created. Provision the bundles before running personal-dev-env-leased-identities.'
