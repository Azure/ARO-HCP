#!/bin/bash
# Sourced adapter for the offline Go configuration generator.
# SHARED_DIR and DEPLOY_ENV are required. Publish shell outputs only on success;
# stdout is registry data, never shell code.

_aro_hcp_build_config_override() {
    local output_file="${SHARED_DIR:?SHARED_DIR must be set}/config-override.yaml"
    local registries
    local -a args=(
        infra build-config-override
        --source-dir "${PWD}"
        --shared-dir "${SHARED_DIR}"
        --deploy-env "${DEPLOY_ENV:?DEPLOY_ENV must be set}"
        --output-file "${output_file}"
        --msi-mock-pool-catalog "${PWD}/dev-infrastructure/openshift-ci/msi-mock-pool.yaml"
        --arm-helper-pool-catalog "${ARM_HELPER_POOL_CATALOG:-${PWD}/dev-infrastructure/openshift-ci/arm-helper-pool.yaml}"
        --hypershift-override-file "${SHARED_DIR}/hypershift-image-overrides.yaml"
    )
    # Go cannot export into this provisioning shell. Pass through any existing
    # registry list (even if unexported), then publish its result only on success.
    # ARO-Tools uses USE_OC_LOGIN_REGISTRIES to authenticate to CI source
    # registries with oc registry login when mirroring overridden images to ACR.
    # The sentinel preserves any trailing newlines in registry data while
    # command substitution removes the CLI's one framing newline explicitly.
    if registries=$(USE_OC_LOGIN_REGISTRIES="${USE_OC_LOGIN_REGISTRIES:-}" "${ARO_HCP_TESTS_BINARY:-${PWD}/test/aro-hcp-tests}" "${args[@]}" && printf '.'); then
        registries="${registries%.}"
        registries="${registries%$'\n'}"
        # Sourced output consumed by provision-environment.sh.
        # shellcheck disable=SC2034
        OVERRIDE_CONFIG_FILE="${output_file}"
        if [[ "${registries}" != "${USE_OC_LOGIN_REGISTRIES-}" ]]; then
            export USE_OC_LOGIN_REGISTRIES="${registries}"
        fi
    else
        return 1
    fi
}

if _aro_hcp_build_config_override; then
    unset -f _aro_hcp_build_config_override
else
    unset -f _aro_hcp_build_config_override
    return 1
fi
