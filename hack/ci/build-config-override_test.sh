#!/bin/bash
# Run from the ARO-HCP root. Optionally pass a release worktree to test its copy.
set -euo pipefail

root=$(pwd)
release_root="${1:-}"
tmp=$(mktemp -d)
trap 'rm -rf "${tmp}"' EXIT

# Only cloud/deployment commands and credential reads are mocked. Use real yq
# and validate its generated output as JSON so quoting bugs cannot pass silently.
cat() {
  case "$1" in
    /mock-profile/*|/var/run/aro-hcp-test/*) printf '%s\n' 'mock-secret-do-not-log' ;;
    *) command cat "$@" ;;
  esac
}
az() {
  if [[ "$*" == acr\ manifest\ show-metadata* ]]; then
    printf '%s\n' 'sha256:baseline'
  fi
}
oc() { :; }
kubelogin() { :; }
git() {
  case "$1" in
    rev-parse) printf '%s\n' 'abcdef0' ;;
    cat-file|fetch) : ;;
    checkout)
      if [[ -n "${SVC_AMW_LEASE:-}${HCP_AMW_LEASE:-}" ]]; then
        [[ "${AMW_POOL_CATALOG}" == "${SHARED_DIR}/baseline-amw-pool.yaml" ]]
        cmp "${AMW_POOL_CATALOG}" "${TEST_AMW_POOL_CATALOG}"
        # Simulate a baseline with a different catalog, without touching the repo.
        printf '{}\n' > "${TEST_AMW_POOL_CATALOG}"
      fi
      ;;
    *) return 1 ;;
  esac
}
make() {
  local arg override='' output=''
  for arg in "$@"; do
    case "${arg}" in
      OVERRIDE_CONFIG_FILE=*) override="${arg#*=}" ;;
      CONFIG_OUTPUT=*) output="${arg#*=}" ;;
    esac
  done
  [[ "${override}" == "${SHARED_DIR}/config-override.yaml" ]]
  [[ -n "${output}" ]]
  yq -o=json ".clouds.dev.environments.${DEPLOY_ENV}.defaults" "${override}" > "${output}"
}
export -f cat az oc kubelogin git make

for mode in builder provision from-main release; do
  if [[ "${mode}" == release && -z "${release_root}" ]]; then
    continue
  fi
  for scenario in unset empty svc hcp both literal lease-svc lease-hcp leases mixed default-catalog \
    missing-lease missing-file malformed-catalog missing-id empty-id missing-location wrong-purpose \
    non-amw-id invalid-subscription child-id svc-conflict hcp-conflict multiple-leases literal-lease \
    alias mixed-alias; do
    (
      export SHARED_DIR="${tmp}/${mode}-${scenario}"
      export ARTIFACT_DIR="${SHARED_DIR}/artifacts"
      mkdir -p "${ARTIFACT_DIR}"
      export DEPLOY_ENV=ci01 ARO_HCP_DEPLOY_ENV=ci01 LOCATION=westus3
      export CLUSTER_PROFILE_DIR=/mock-profile VAULT_SECRET_PROFILE=test
      export PULL_BASE_SHA=abcdef0 JOB_TYPE=presubmit JOB_NAME=test
      export USE_OC_LOGIN_REGISTRIES=''
      unset LEASED_MSI_MOCK_SP LEASED_ARM_HELPER_SP LEASED_MSI_CONTAINERS
      unset SVC_AMW_RESOURCE_ID HCP_AMW_RESOURCE_ID
      unset SVC_AMW_LEASE HCP_AMW_LEASE AMW_POOL_CATALOG
      export AZURE_CLIENT_SECRET=mock-secret-do-not-log
      for prefix in BACKEND FRONTEND ADMIN_API SESSIONGATE HCP_RECOVERY FLEET MGMT_AGENT SWIFT_RECORDER KUBE_APPLIER EXPORTER; do
        export "${prefix}_IMAGE=example.invalid/${prefix}@sha256:pr"
      done
      svc=/subscriptions/11111111-1111-1111-1111-111111111111/resourceGroups/external-svc/providers/Microsoft.Monitor/accounts/services-7
      hcp=/subscriptions/22222222-2222-2222-2222-222222222222/resourceGroups/external-hcp/providers/Microsoft.Monitor/accounts/hcps-2
      export AMW_POOL_CATALOG="${SHARED_DIR}/amw-pool.yaml"
      export TEST_AMW_POOL_CATALOG="${AMW_POOL_CATALOG}"
      SVC_ID="${svc}" HCP_ID="${hcp}" yq -n '
        .amwPool.services = {"workspaceId": strenv(SVC_ID), "location": "westus2", "purpose": "services"} |
        .amwPool.hcps = {"workspaceId": strenv(HCP_ID), "location": "westus2", "purpose": "hcps"}
      ' > "${AMW_POOL_CATALOG}"
      expected_svc='' expected_hcp='' expected_error=''
      case "${scenario}" in
        empty) export SVC_AMW_RESOURCE_ID='' HCP_AMW_RESOURCE_ID='' SVC_AMW_LEASE='' HCP_AMW_LEASE='' ;;
        svc) export SVC_AMW_RESOURCE_ID="${svc}"; expected_svc="${svc}" ;;
        hcp) export HCP_AMW_RESOURCE_ID="${hcp}"; expected_hcp="${hcp}" ;;
        both)
          export SVC_AMW_RESOURCE_ID="${svc}" HCP_AMW_RESOURCE_ID="${hcp}"
          expected_svc="${svc}" expected_hcp="${hcp}"
          ;;
        literal)
          # Deliberately not a valid ARM ID: test literal transport, not ARM validation.
          export SVC_AMW_RESOURCE_ID='" | .injected = true | #'
          export HCP_AMW_RESOURCE_ID=$'literal:\n  yaml: [value]'
          expected_svc="${SVC_AMW_RESOURCE_ID}" expected_hcp="${HCP_AMW_RESOURCE_ID}"
          ;;
        lease-svc) export SVC_AMW_LEASE=services; expected_svc="${svc}" ;;
        lease-hcp) export HCP_AMW_LEASE=hcps; expected_hcp="${hcp}" ;;
        leases)
          export SVC_AMW_LEASE=services HCP_AMW_LEASE=hcps
          expected_svc="${svc}" expected_hcp="${hcp}"
          ;;
        mixed)
          export SVC_AMW_LEASE=services HCP_AMW_RESOURCE_ID="${hcp}"
          expected_svc="${svc}" expected_hcp="${hcp}"
          ;;
        default-catalog)
          unset AMW_POOL_CATALOG
          export SVC_AMW_LEASE=services-ci-pool-0 HCP_AMW_LEASE=hcps-ci-pool-0
          cp dev-infrastructure/openshift-ci/amw-pool.yaml "${TEST_AMW_POOL_CATALOG}"
          expected_svc=$(yq '.amwPool."services-ci-pool-0".workspaceId' "${TEST_AMW_POOL_CATALOG}")
          expected_hcp=$(yq '.amwPool."hcps-ci-pool-0".workspaceId' "${TEST_AMW_POOL_CATALOG}")
          ;;
        svc-conflict|hcp-conflict)
          if [[ "${scenario}" == svc-conflict ]]; then
            export SVC_AMW_LEASE=services SVC_AMW_RESOURCE_ID="${svc}"
          else
            export HCP_AMW_LEASE=hcps HCP_AMW_RESOURCE_ID="${hcp}"
          fi
          expected_error='are mutually exclusive'
          ;;
        multiple-leases)
          export SVC_AMW_LEASE=$'services\nhcps'
          expected_error='must contain exactly one catalog name'
          ;;
        alias|mixed-alias)
          export SVC_AMW_LEASE=services
          if [[ "${scenario}" == alias ]]; then
            export HCP_AMW_LEASE=hcps
            HCP_ID="${svc^^}" yq -i '.amwPool.hcps.workspaceId = strenv(HCP_ID)' "${AMW_POOL_CATALOG}"
          else
            export HCP_AMW_RESOURCE_ID="${svc^^}"
          fi
          expected_error='must resolve to distinct workspaces'
          ;;
        *)
          export SVC_AMW_LEASE=services
          expected_error='not found, incomplete, or wrong purpose'
          case "${scenario}" in
            unset) unset SVC_AMW_LEASE; expected_error='' ;;
            missing-lease) export SVC_AMW_LEASE=missing ;;
            literal-lease) export SVC_AMW_LEASE='"].hcps#' ;;
            missing-file)
              export AMW_POOL_CATALOG="${SHARED_DIR}/missing.yaml"
              if [[ "${mode}" == from-main ]]; then expected_error='cannot stat'; fi
              ;;
            malformed-catalog) printf 'amwPool: [\n' > "${AMW_POOL_CATALOG}" ;;
            missing-id) yq -i 'del(.amwPool.services.workspaceId)' "${AMW_POOL_CATALOG}" ;;
            empty-id)
              yq -i '.amwPool.services.workspaceId = ""' "${AMW_POOL_CATALOG}"
              expected_error='must be a full Microsoft.Monitor/accounts ARM ID'
              ;;
            missing-location) yq -i 'del(.amwPool.services.location)' "${AMW_POOL_CATALOG}" ;;
            wrong-purpose) yq -i '.amwPool.services.purpose = "hcps"' "${AMW_POOL_CATALOG}" ;;
            non-amw-id|invalid-subscription|child-id)
              case "${scenario}" in
                non-amw-id) bad_id="${svc/Microsoft.Monitor\/accounts/Microsoft.OperationalInsights\/workspaces}" ;;
                invalid-subscription) bad_id="${svc/11111111-1111-1111-1111-111111111111/not-a-guid}" ;;
                child-id) bad_id="${svc}/child/name" ;;
              esac
              BAD_ID="${bad_id}" yq -i '.amwPool.services.workspaceId = strenv(BAD_ID)' "${AMW_POOL_CATALOG}"
              expected_error='must be a full Microsoft.Monitor/accounts ARM ID'
              ;;
          esac
          ;;
      esac
      # Direct-ID and default paths must not need a readable catalog.
      if [[ -z "${SVC_AMW_LEASE:-}${HCP_AMW_LEASE:-}" ]]; then
        export AMW_POOL_CATALOG="${SHARED_DIR}/unused-catalog.yaml"
      fi
      case "${mode}" in
        builder) script="${root}/hack/ci/build-config-override.sh" ;;
        provision) script="${root}/hack/ci/provision-environment.sh" ;;
        from-main) script="${root}/hack/ci/provision-from-main.sh" ;;
        release) script="${release_root}/ci-operator/step-registry/aro-hcp/provision/environment/aro-hcp-provision-environment-commands.sh" ;;
      esac
      if bash -eu -o pipefail "${script}" > "${SHARED_DIR}/run.log" 2>&1; then
        if [[ -n "${expected_error}" ]]; then
          printf 'FAIL %s/%s: expected %s\n' "${mode}" "${scenario}" "${expected_error}" >&2
          exit 1
        fi
      else
        if [[ -z "${expected_error}" || "$(command cat "${SHARED_DIR}/run.log")" != *"${expected_error}"* ]]; then
          command cat "${SHARED_DIR}/run.log" >&2
          printf 'FAIL %s/%s: expected %s\n' "${mode}" "${scenario}" "${expected_error}" >&2
          exit 1
        fi
        [[ ! -e "${SHARED_DIR}/config.yaml" ]]
        [[ "$(command cat "${SHARED_DIR}/run.log")" != *mock-secret-do-not-log* ]]
        printf 'PASS %s/%s (rejected)\n' "${mode}" "${scenario}"
        exit 0
      fi
      yq -o=json '.' "${SHARED_DIR}/config-override.yaml" > "${SHARED_DIR}/override.json"
      jq -e --arg svc "${expected_svc}" --arg hcp "${expected_hcp}" '
        .clouds.dev.environments.ci01.defaults as $defaults |
        ($defaults.monitoring // {}) as $monitoring |
        ($monitoring == ({}
          + (if $svc == "" then {} else {svcWorkspaceResourceId: $svc} end)
          + (if $hcp == "" then {} else {hcpWorkspaceResourceId: $hcp} end))) and
        ($defaults.mgmt.aks.userAgentPool.minCount == 1) and
        ($defaults.backend.image.digest | startswith("sha256:")) and
        (has("injected") | not)
      ' "${SHARED_DIR}/override.json" > /dev/null
      if [[ "${mode}" != builder ]]; then
        jq -e --slurpfile override "${SHARED_DIR}/override.json" '
          . == $override[0].clouds.dev.environments.ci01.defaults
        ' "${SHARED_DIR}/config.yaml" > /dev/null
        cmp "${SHARED_DIR}/config.yaml" "${ARTIFACT_DIR}/config.yaml"
      fi
      # Rebuilding for the PR upgrade must retain exactly the same workspace IDs.
      if [[ "${mode}" == from-main && -n "${SVC_AMW_LEASE:-}${HCP_AMW_LEASE:-}" ]]; then
        cp "${SHARED_DIR}/baseline-amw-pool.yaml" "${TEST_AMW_POOL_CATALOG}"
      fi
      bash -eu -o pipefail "${root}/hack/ci/build-config-override.sh" > "${SHARED_DIR}/upgrade.log" 2>&1
      yq -o=json '.' "${SHARED_DIR}/config-override.yaml" | jq -e \
        --slurpfile baseline "${SHARED_DIR}/override.json" '
          .clouds.dev.environments.ci01.defaults.monitoring ==
          $baseline[0].clouds.dev.environments.ci01.defaults.monitoring
        ' > /dev/null
      for file in "${SHARED_DIR}/run.log" "${SHARED_DIR}/upgrade.log" "${SHARED_DIR}/override.json"; do
        if [[ "$(command cat "${file}")" == *mock-secret-do-not-log* ]]; then
          printf 'Credential leaked in %s\n' "${file}" >&2
          exit 1
        fi
      done
      printf 'PASS %s/%s\n' "${mode}" "${scenario}"
    )
  done
done

if [[ -n "${release_root}" ]]; then
  for step in provision/environment/aro-hcp-provision-environment \
    provision/from-main/aro-hcp-provision-from-main \
    test/local-upgrade/aro-hcp-test-local-upgrade; do
    ref="${release_root}/ci-operator/step-registry/aro-hcp/${step}-ref.yaml"
    yq -o=json '.' "${ref}" | jq -e --arg commands "${step##*/}-commands.sh" '
      (.ref.commands == $commands) and
      ([.ref.env[] | select(.name == "SVC_AMW_RESOURCE_ID" or .name == "HCP_AMW_RESOURCE_ID")]
        | sort_by(.name) | map({name, default})) ==
      [{name: "HCP_AMW_RESOURCE_ID", default: ""}, {name: "SVC_AMW_RESOURCE_ID", default: ""}]
    ' > /dev/null
    printf 'PASS release input declarations/%s\n' "${step##*/}"
  done
fi
