#!/usr/bin/env bash
set -euo pipefail

# Exercise the real config and pipeline conditions, even though disabled pipelines
# are not discovered by the repository-wide Helm fixture runner.
TEMPLATIZE=${TEMPLATIZE:-../tooling/templatize/templatize-$(uname -m)}
YQ=${YQ:-yq}
HELM=${HELM:-helm}
tmp=$(mktemp -d)
trap 'rm -rf "${tmp}"' EXIT

for fixture in disabled enabled capture-all; do
  override=()
  if [[ "${fixture}" != disabled ]]; then
    override=(--config-file-override "testdata/${fixture}.yaml")
  fi
  "${TEMPLATIZE}" configuration render --service-config-file ../config/config.yaml \
    --cloud dev --environment dev --region westus3 --stamp 1 --ev2-cloud public \
    "${override[@]}" --output "${tmp}/config.yaml"
  for input in values pipeline; do
    "${TEMPLATIZE}" generate --config-file ../config/config.yaml \
      --cloud dev --deploy-env dev --region westus3 --stamp 1 --ev2-cloud public \
      "${override[@]}" --input "${input}.yaml" --output "${tmp}/${input}.yaml"
  done
  "${HELM}" template swift-recorder deploy --namespace swift-recorder \
    --values "${tmp}/values.yaml" > "${tmp}/manifest.yaml"
  if [[ "${fixture}" == disabled ]]; then
    test -z "$("${YQ}" 'select(.kind != null)' "${tmp}/manifest.yaml")"
    "${YQ}" -e '[.resourceGroups[].steps[] | select(.action == "ImageMirror" or .action == "Helm")] | length == 0' "${tmp}/pipeline.yaml" >/dev/null
    "${YQ}" -e '.enabled == false and .image.digest == ""' "${tmp}/values.yaml" >/dev/null
  else
    "${YQ}" -e '[.resourceGroups[].steps[] | select(.action == "ImageMirror" or .action == "Helm")] | length == 2' "${tmp}/pipeline.yaml" >/dev/null
    "${YQ}" -e '.image.repository == "swift-recorder"' "${tmp}/values.yaml" >/dev/null
    "${YQ}" -e '.resourceGroups[].steps[] | select(.action == "ImageMirror") | .repository.configRef == "swiftRecorder.image.repository"' "${tmp}/pipeline.yaml" >/dev/null
    "${YQ}" -e 'select(.kind == "DaemonSet") | .spec.template.spec.nodeSelector."kubernetes.azure.com/podnetwork-swiftv2-enabled" == "true" and .spec.template.spec.hostNetwork == true and .spec.template.spec.dnsPolicy == "ClusterFirstWithHostNet"' "${tmp}/manifest.yaml" >/dev/null
  fi
  if [[ "${UPDATE:-false}" == true ]]; then
    cp "${tmp}/manifest.yaml" "testdata/zz_fixture_${fixture}.yaml"
  else
    diff -u "testdata/zz_fixture_${fixture}.yaml" "${tmp}/manifest.yaml"
  fi
done

# The release job supplies only mgmtAgent.image, without sourcing our local CI
# helper. Check the resolved override, not a copied/stale default mgmt-agent pin.
for environment in ci00 ci01; do
  BUILD_ID=1234567890 "${TEMPLATIZE}" configuration render --service-config-file ../config/config.yaml \
    --cloud dev --environment "${environment}" --dev-settings-file ../tooling/templatize/settings.yaml \
    --config-file-override testdata/ci-bootstrap.yaml --output "${tmp}/ci-config.yaml"
  "${YQ}" -e '.swiftRecorder.enabled == true and .swiftRecorder.captureMode == "all" and .swiftRecorder.useMgmtAgentImage == true and .swiftRecorder.image.digest == ""' "${tmp}/ci-config.yaml" >/dev/null
  test "$("${YQ}" '.mgmt.aks.name' "${tmp}/ci-config.yaml")" == "${environment}-j4567890-mgmt-1"
  test "$("${YQ}" '.mgmtAgent.image.digest' "${tmp}/ci-config.yaml")" != "$("${YQ}" '.defaults.mgmtAgent.image.digest' ../config/config.yaml)"
  for input in values pipeline; do
    BUILD_ID=1234567890 "${TEMPLATIZE}" generate --config-file ../config/config.yaml \
      --cloud dev --dev-environment "${environment}" --dev-settings-file ../tooling/templatize/settings.yaml \
      --config-file-override testdata/ci-bootstrap.yaml --input "${input}.yaml" --output "${tmp}/ci-${input}.yaml"
  done
  "${YQ}" -e '[.resourceGroups[].steps[] | select(.action == "ImageMirror" or .action == "Helm")] | length == 2' "${tmp}/ci-pipeline.yaml" >/dev/null
  for field in registry repository digest; do
    case "${field}" in registry) ref=sourceRegistry ;; *) ref=${field} ;; esac
    path=$("${YQ}" ".resourceGroups[].steps[] | select(.action == \"ImageMirror\") | .${ref}.configRef" "${tmp}/ci-pipeline.yaml")
    test "${path}" == "mgmtAgent.image.${field}"
    test "$("${YQ}" ".${path}" "${tmp}/ci-config.yaml")" == "$("${YQ}" ".clouds.dev.environments.${environment}.defaults.mgmtAgent.image.${field}" testdata/ci-bootstrap.yaml)"
  done
  for field in repository digest; do
    test "$("${YQ}" ".image.${field}" "${tmp}/ci-values.yaml")" == "$("${YQ}" ".mgmtAgent.image.${field}" "${tmp}/ci-config.yaml")"
  done
  "${HELM}" template swift-recorder deploy --namespace swift-recorder \
    --values "${tmp}/ci-values.yaml" > "${tmp}/ci-manifest.yaml"
  expected_image="$("${YQ}" '.acr.svc.name + "." + .acrDNSSuffix + "/" + .mgmtAgent.image.repository + "@" + .mgmtAgent.image.digest' "${tmp}/ci-config.yaml")"
  test "$("${YQ}" 'select(.kind == "DaemonSet") | .spec.template.spec.containers[0].image' "${tmp}/ci-manifest.yaml")" == "${expected_image}"
  test "$("${YQ}" 'select(.kind == "DaemonSet") | .spec.template.spec.containers[0].command[0]' "${tmp}/ci-manifest.yaml")" == /swift-recorder
  test "$("${YQ}" 'select(.kind == "DaemonSet") | .spec.template.spec.containers[0].args[0]' "${tmp}/ci-manifest.yaml")" == controller
  "${YQ}" -e 'select(.kind == "DaemonSet") | .spec.template.spec.containers[0].args[] | select(. == "--capture-mode=all")' "${tmp}/ci-manifest.yaml" >/dev/null
  if "${HELM}" template swift-recorder deploy --values "${tmp}/ci-values.yaml" --set image.digest= >/dev/null 2>&1; then
    printf 'CI bootstrap chart accepted an empty selected digest\n' >&2
    exit 1
  fi

  # PR images can originate from a build-cluster registry, not only the hub.
  "${YQ}" ".clouds.dev.environments.${environment}.defaults.mgmtAgent.image.registry = \"registry.build11.ci.openshift.org\"" \
    testdata/ci-bootstrap.yaml > "${tmp}/build-cluster-bootstrap.yaml"
  for input in values pipeline; do
    BUILD_ID=1234567890 "${TEMPLATIZE}" generate --config-file ../config/config.yaml \
      --cloud dev --dev-environment "${environment}" --dev-settings-file ../tooling/templatize/settings.yaml \
      --config-file-override "${tmp}/build-cluster-bootstrap.yaml" --input "${input}.yaml" --output "${tmp}/build-cluster-${input}.yaml"
  done
  "${YQ}" -e '.enabled == true' "${tmp}/build-cluster-values.yaml" >/dev/null
  "${YQ}" -e '[.resourceGroups[].steps[] | select(.action == "ImageMirror" or .action == "Helm")] | length == 2' "${tmp}/build-cluster-pipeline.yaml" >/dev/null
  "${HELM}" template swift-recorder deploy --namespace swift-recorder \
    --values "${tmp}/build-cluster-values.yaml" > "${tmp}/build-cluster-manifest.yaml"
  test "$("${YQ}" 'select(.kind == "DaemonSet") | .spec.template.spec.containers[0].image' "${tmp}/build-cluster-manifest.yaml")" == "${expected_image}"

  # Registry location must not silently override explicit enablement.
  "${YQ}" ".clouds.dev.environments.${environment}.defaults.mgmtAgent.image.registry = \"arohcpsvcdev.azurecr.io\"" \
    testdata/ci-bootstrap.yaml > "${tmp}/acr-bootstrap.yaml"
  BUILD_ID=1234567890 "${TEMPLATIZE}" configuration render --service-config-file ../config/config.yaml \
    --cloud dev --environment "${environment}" --dev-settings-file ../tooling/templatize/settings.yaml \
    --config-file-override "${tmp}/acr-bootstrap.yaml" --output "${tmp}/acr-config.yaml"
  "${YQ}" -e '.swiftRecorder.enabled == true and .swiftRecorder.useMgmtAgentImage == true and .mgmtAgent.image.registry == "arohcpsvcdev.azurecr.io"' "${tmp}/acr-config.yaml" >/dev/null
  for input in values pipeline; do
    BUILD_ID=1234567890 "${TEMPLATIZE}" generate --config-file ../config/config.yaml \
      --cloud dev --dev-environment "${environment}" --dev-settings-file ../tooling/templatize/settings.yaml \
      --config-file-override "${tmp}/acr-bootstrap.yaml" --input "${input}.yaml" --output "${tmp}/acr-${input}.yaml"
  done
  "${YQ}" -e '.enabled == true' "${tmp}/acr-values.yaml" >/dev/null
  "${YQ}" -e '[.resourceGroups[].steps[] | select(.action == "ImageMirror" or .action == "Helm")] | length == 2' "${tmp}/acr-pipeline.yaml" >/dev/null
  "${HELM}" template swift-recorder deploy --namespace swift-recorder \
    --values "${tmp}/acr-values.yaml" > "${tmp}/acr-manifest.yaml"
  test "$("${YQ}" 'select(.kind == "DaemonSet") | .spec.template.spec.containers[0].image' "${tmp}/acr-manifest.yaml")" == "${expected_image}"
done

# Shared/personal dev environments must not inherit the ephemeral CI opt-in.
for environment in dev pers cspr perf; do
  "${TEMPLATIZE}" configuration render --service-config-file ../config/config.yaml \
    --cloud dev --environment "${environment}" --region westus3 --stamp 1 --ev2-cloud public \
    --output "${tmp}/disabled-config.yaml"
  "${YQ}" -e '.swiftRecorder.enabled == false and .swiftRecorder.useMgmtAgentImage == false and .swiftRecorder.captureMode == "slow" and .swiftRecorder.image.repository == "swift-recorder" and .swiftRecorder.image.digest == ""' "${tmp}/disabled-config.yaml" >/dev/null
done
# Public settings inherit the disabled defaults; sensitive public config is external.
"${YQ}" -e '.defaults.swiftRecorder.enabled == false and .defaults.swiftRecorder.useMgmtAgentImage == false and .defaults.swiftRecorder.captureMode == "slow" and ([.clouds | del(.dev) | .. | select(has("swiftRecorder"))] | length == 0)' ../config/config.yaml >/dev/null
"${YQ}" -e '[.. | select(has("swiftRecorder"))] | length == 0' ../config/config.msft.clouds-overlay.yaml >/dev/null

"${YQ}" '.defaults.swiftRecorder.image.digest = ""' testdata/enabled.yaml > "${tmp}/invalid.yaml"
if "${TEMPLATIZE}" configuration render --service-config-file ../config/config.yaml \
  --cloud dev --environment dev --region westus3 --stamp 1 --ev2-cloud public \
  --config-file-override "${tmp}/invalid.yaml" --output "${tmp}/invalid-config.yaml" >/dev/null 2>&1; then
  printf 'enabled config accepted an empty digest\n' >&2
  exit 1
fi

# Fail closed for direct Helm usage too, not just schema-validated deployments.
if "${HELM}" template swift-recorder deploy --values "${tmp}/values.yaml" --set image.digest= >/dev/null 2>&1; then
  printf 'enabled chart accepted an empty digest\n' >&2
  exit 1
fi
if "${HELM}" template swift-recorder deploy --values "${tmp}/values.yaml" --set captureMode=invalid >/dev/null 2>&1; then
  printf 'enabled chart accepted an invalid capture mode\n' >&2
  exit 1
fi
