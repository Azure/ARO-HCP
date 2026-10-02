#!/usr/bin/env bash
# shellcheck disable=SC2016 # yq programs use their own variable syntax.
set -euo pipefail

cd "$(dirname "$0")/.."
export TEMPLATIZE="${TEMPLATIZE:-tooling/templatize/templatize-x86_64}"
export YQ="${YQ:-yq}" JQ="${JQ:-jq}"
export CONFIG_FILE=config/config.yaml DEPLOY_ENV=pers
export LEASED_INFRA_IDENTITY_PREFIX=aro-hcp-infra-identities-test
scratch="$(mktemp -d)"
trap 'rm -rf "${scratch}"' EXIT
export LEASED_INFRA_IDENTITY_DIR="${scratch}/prepared"

bash hack/prepare-leased-infra-identities.sh
"${JQ}" -e '
    (.parameters.serviceBundle.value.identityNames | length == 14) and
    (.parameters.managementBundles.value | map(.identityNames | length) == [8, 8]) and
    (.parameters.managementBundles.value | map(.stampIdentifier) == ["1", "2"]) and
    (.parameters.managementBundles.value | map(.resourceGroupName) ==
        ["aro-hcp-infra-identities-test-mgmt-1", "aro-hcp-infra-identities-test-mgmt-2"])
' "${LEASED_INFRA_IDENTITY_DIR}/deployment-parameters.json"

mkdir -p "${scratch}/parameters"
ln -s "${PWD}/dev-infrastructure/templates" "${scratch}/templates"
ln -s "${PWD}/dev-infrastructure/modules" "${scratch}/modules"
cp dev-infrastructure/bicepconfig.json "${scratch}/bicepconfig.json"
for stamp in 1 2; do
    "${YQ}" "(.environments[] | select(.name == \"pers\") | .defaults.cxStamp) = ${stamp}" \
        tooling/templatize/settings.yaml > "${scratch}/settings.yaml"
    for role in svc mgmt; do
        "${TEMPLATIZE}" generate \
            --config-file "${CONFIG_FILE}" \
            --config-file-override "${LEASED_INFRA_IDENTITY_DIR}/config-override.yaml" \
            --dev-settings-file "${scratch}/settings.yaml" \
            --dev-environment pers \
            --input "dev-infrastructure/configurations/${role}-cluster.tmpl.bicepparam" \
            --output "${scratch}/parameters/${role}-${stamp}.bicepparam"
        "${BICEP:-bicep}" build-params "${scratch}/parameters/${role}-${stamp}.bicepparam" \
            --outfile "${scratch}/parameters/${role}-${stamp}.json"
    done
done
for mode in default leased; do
    overrides=()
    if [[ "${mode}" == leased ]]; then
        overrides=(--config-file-override "${LEASED_INFRA_IDENTITY_DIR}/config-override.yaml")
    fi
    "${TEMPLATIZE}" generate \
        --config-file "${CONFIG_FILE}" "${overrides[@]}" \
        --dev-settings-file tooling/templatize/settings.yaml \
        --dev-environment pers \
        --input dev-infrastructure/configurations/fleet-amw-permissions.tmpl.bicepparam \
        --output "${scratch}/parameters/fleet-amw-${mode}.bicepparam"
    "${BICEP:-bicep}" build-params "${scratch}/parameters/fleet-amw-${mode}.bicepparam" \
        --outfile "${scratch}/parameters/fleet-amw-${mode}.json"
    for stamp in 1 2; do
        "${YQ}" "(.environments[] | select(.name == \"pers\") | .defaults.cxStamp) = ${stamp}" \
            tooling/templatize/settings.yaml > "${scratch}/settings.yaml"
        for role in svc mgmt; do
            steps=(arobit-output)
            if [[ "${role}" == mgmt ]]; then
                steps+=(kube-applier-cosmos)
            fi
            for step in "${steps[@]}"; do
                parameters="$("${YQ}" -r ".resourceGroups[].steps[] | select(.name == \"${step}\") | .parameters" \
                    "dev-infrastructure/${role}-pipeline.yaml")"
                "${TEMPLATIZE}" generate \
                    --config-file "${CONFIG_FILE}" "${overrides[@]}" \
                    --dev-settings-file "${scratch}/settings.yaml" \
                    --dev-environment pers \
                    --input "dev-infrastructure/${parameters}" \
                    --output "${scratch}/parameters/${step}-${role}-${stamp}-${mode}.bicepparam"
                "${BICEP:-bicep}" build-params "${scratch}/parameters/${step}-${role}-${stamp}-${mode}.bicepparam" \
                    --outfile "${scratch}/parameters/${step}-${role}-${stamp}-${mode}.json"
            done
        done
    done
done
"${BICEP:-bicep}" build dev-infrastructure/modules/infrastructure-identities.bicep \
    --outfile "${scratch}/identities.json"
for template in arobit-lookup kube-applier-cosmos; do
    "${BICEP:-bicep}" build "dev-infrastructure/templates/${template}.bicep" \
        --outfile "${scratch}/${template}.json"
done

python3 - "${scratch}" <<'PY'
import json
import pathlib
import re
import sys

scratch = pathlib.Path(sys.argv[1])
parameters = json.loads((scratch / "prepared/deployment-parameters.json").read_text())["parameters"]
service_config = json.loads((scratch / "prepared/config-1.json").read_text())
for mode, expected_rg in [
    ("default", service_config["svc"]["rg"]),
    ("leased", parameters["serviceBundle"]["value"]["resourceGroupName"]),
]:
    permissions = json.loads((scratch / f"parameters/fleet-amw-{mode}.json").read_text())["parameters"]
    assert permissions["fleetMIResourceGroup"]["value"] == expected_rg, (mode, permissions)
    assert permissions["fleetMIName"]["value"] == service_config["fleet"]["managedIdentityName"]
    assert permissions["svcMonitorName"]["value"] == service_config["monitoring"]["svcWorkspaceName"]
    assert permissions["hcpMonitorName"]["value"] == service_config["monitoring"]["hcpWorkspaceName"]
print("PASS: Fleet monitor permissions select the correct identity RG in default and leased modes")

for stamp in ("1", "2"):
    config = json.loads((scratch / f"prepared/config-{stamp}.json").read_text())
    for role in ("svc", "mgmt"):
        bundle = parameters["serviceBundle"]["value"] if role == "svc" else next(
            b for b in parameters["managementBundles"]["value"] if b["stampIdentifier"] == stamp
        )
        for mode, expected_rg in [("default", config[role]["rg"]), ("leased", bundle["resourceGroupName"])]:
            steps = ("arobit-output",) if role == "svc" else ("arobit-output", "kube-applier-cosmos")
            for step in steps:
                lookup = json.loads((scratch / f"parameters/{step}-{role}-{stamp}-{mode}.json").read_text())["parameters"]
                assert lookup["infrastructureIdentityResourceGroup"]["value"] == expected_rg, (step, role, stamp, mode, lookup)
                name_param = "msiName" if step == "arobit-output" else "kubeApplierMIName"
                expected_name = config["logs"]["mdsd"]["msiName"] if step == "arobit-output" else config["kubeApplier"]["managedIdentityName"]
                assert lookup[name_param]["value"] == expected_name
print("PASS: pipeline-selected AROBit and kube-applier Cosmos lookups use the correct default/leased RG for service and both management stamps")

arobit = json.loads((scratch / "arobit-lookup.json").read_text())
assert arobit["parameters"]["infrastructureIdentityResourceGroup"]["defaultValue"] == "[resourceGroup().name]"
assert not arobit["resources"], "AROBit lookup must not create resources"
assert "parameters('infrastructureIdentityResourceGroup')" in arobit["outputs"]["msiClientId"]["value"]
assert "subscription().subscriptionId" in arobit["outputs"]["msiClientId"]["value"]
cosmos = json.loads((scratch / "kube-applier-cosmos.json").read_text())
assert cosmos["parameters"]["infrastructureIdentityResourceGroup"]["defaultValue"] == "[resourceGroup().name]"
identity = cosmos["resources"]["kubeApplierMSI"]
assert identity["existing"] is True
assert identity["resourceGroup"] == "[parameters('infrastructureIdentityResourceGroup')]"
grant = cosmos["resources"]["kubeApplierCosmos"]
assert grant["resourceGroup"] == "[variables('rpCosmosDbAccountRef').resourceGroup.name]"
assert grant["properties"]["parameters"]["kubeApplierManagedIdentityPrincipalId"]["value"] == "[reference('kubeApplierMSI').principalId]"
print("PASS: compiled lookups reference persistent RGs without creating UAMIs or moving the Cosmos permission scope")

for role, stamp, bundle in [
    ("svc", "1", parameters["serviceBundle"]["value"]),
    *[("mgmt", b["stampIdentifier"], b) for b in parameters["managementBundles"]["value"]],
]:
    deployed = json.loads((scratch / f"parameters/{role}-{stamp}.json").read_text())["parameters"]
    template = pathlib.Path(f"dev-infrastructure/templates/{role}-cluster.bicep").read_text()
    workloads = template.split("var workloadIdentities = items({", 1)[1].split("\n})", 1)[0]
    names = []
    for name in re.findall(r"uamiName:\s*('[^']+'|\w+)", workloads):
        names.append(name[1:-1] if name.startswith("'") else deployed[name]["value"])
    names += [deployed["aksClusterName"]["value"] + "-msi", "image-puller"]
    assert sorted(names) == sorted(bundle["identityNames"]), (role, stamp, names, bundle)
    assert deployed["useLeasedInfrastructureIdentities"]["value"] is True

template = json.loads((scratch / "identities.json").read_text())
resources = template["resources"]
resources = list(resources.values()) if isinstance(resources, dict) else resources
assert sorted(r["type"] for r in resources) == [
    "Microsoft.Resources/deployments", "Microsoft.Resources/resourceGroups"
]
deployment = next(r for r in resources if r["type"] == "Microsoft.Resources/deployments")
child = deployment["properties"]["template"]
assert child["parameters"]["useLeasedIdentities"]["defaultValue"] is False
assert "useLeasedIdentities" not in deployment["properties"]["parameters"]
identities = child["resources"]
identities = list(identities.values()) if isinstance(identities, dict) else identities
created = [r for r in identities if not r.get("existing", False)]
assert len(created) == 1 and created[0]["type"] == "Microsoft.ManagedIdentity/userAssignedIdentities"
print("PASS: provisioned names match deployment consumers for all 30 identities; provisioning creates only RGs and UAMIs")
PY

printf 'defaults:\n  frontend:\n    image:\n      digest: test-image\n' > "${scratch}/images.yaml"
"${YQ}" eval-all '. as $item ireduce ({}; . * $item)' \
    "${scratch}/images.yaml" "${LEASED_INFRA_IDENTITY_DIR}/config-override.yaml" \
    > "${scratch}/merged.yaml"
"${YQ}" -e '.defaults.frontend.image.digest == "test-image" and
    .clouds.dev.environments.pers.defaults.infrastructureIdentities.useLeased == true and
    .clouds.dev.environments.pers.defaults.mgmt.stamps.count == 2' "${scratch}/merged.yaml"

if DEPLOY_ENV=prod bash hack/prepare-leased-infra-identities.sh > "${scratch}/invalid.log" 2>&1; then
    echo 'ERROR: non-personal environment accepted' >&2
    exit 1
fi
grep -q 'requires DEPLOY_ENV=pers' "${scratch}/invalid.log"
for prefix in '' 'invalid/prefix' 'hcp-underlay-pers-test'; do
    if LEASED_INFRA_IDENTITY_PREFIX="${prefix}" bash hack/prepare-leased-infra-identities.sh > "${scratch}/invalid.log" 2>&1; then
        echo "ERROR: invalid prefix accepted: ${prefix}" >&2
        exit 1
    fi
done
echo 'PASS: image overrides preserved; invalid environments and prefixes rejected'
