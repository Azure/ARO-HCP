#!/bin/bash 

# The 'roleName' field in the ../config/config.yaml file is a comma separated list of role names.
# This script separates them and generates `--set` flags for the `helm deploy` command.
# Please note that since here, the role names are being separated by commas, the role name itself
# cannot have a comma in it's name.

genereate_set_flags() {
    HELM_SETS=()
    IFS=',' read -a ROLE_NAMES <<< "${2}" 
    for ((i=0; i<${#ROLE_NAMES[@]}; i++)); do
        ROLE_NAME="${ROLE_NAMES[$i]}"
        HELM_SETS+=(--set "azureOperatorsMI.${1}.roleDefinitions[$i].name=${ROLE_NAME}") 
        ID=$(az role definition list --name "${ROLE_NAME}" --query "[].name" -o tsv)
        HELM_SETS+=(--set "azureOperatorsMI.${1}.roleDefinitions[$i].id=${ID}")
    done
    printf "%q " "${HELM_SETS[@]}"
}

OP_HELM_SET_FLAGS=()
read -a SINGLE_OP_SET_FLAGS <<< "$(genereate_set_flags "clusterApiAzure" "$OP_CLUSTER_API_AZURE_ROLE_NAME")"
OP_HELM_SET_FLAGS+=("${SINGLE_OP_SET_FLAGS[@]}")

read -a SINGLE_OP_SET_FLAGS <<< "$(genereate_set_flags "controlPlane" "$OP_CONTROL_PLANE_ROLE_NAME")"
OP_HELM_SET_FLAGS+=("${SINGLE_OP_SET_FLAGS[@]}")

read -a SINGLE_OP_SET_FLAGS <<< "$(genereate_set_flags "cloudControllerManager" "$OP_CLOUD_CONTROLLER_MANAGER_ROLE_NAME")"
OP_HELM_SET_FLAGS+=("${SINGLE_OP_SET_FLAGS[@]}")

read -a SINGLE_OP_SET_FLAGS <<< "$(genereate_set_flags "ingress" "$OP_INGRESS_ROLE_NAME")"
OP_HELM_SET_FLAGS+=("${SINGLE_OP_SET_FLAGS[@]}")

read -a SINGLE_OP_SET_FLAGS <<< "$(genereate_set_flags "diskCsiDriver" "$OP_DISK_CSI_DRIVER_ROLE_NAME")"
OP_HELM_SET_FLAGS+=("${SINGLE_OP_SET_FLAGS[@]}")

read -a SINGLE_OP_SET_FLAGS <<< "$(genereate_set_flags "fileCsiDriver" "$OP_FILE_CSI_DRIVER_ROLE_NAME")"
OP_HELM_SET_FLAGS+=("${SINGLE_OP_SET_FLAGS[@]}")

read -a SINGLE_OP_SET_FLAGS <<< "$(genereate_set_flags "imageRegistry" "$OP_IMAGE_REGISTRY_DRIVER_ROLE_NAME")"
OP_HELM_SET_FLAGS+=("${SINGLE_OP_SET_FLAGS[@]}")

read -a SINGLE_OP_SET_FLAGS <<< "$(genereate_set_flags "cloudNetworkConfig" "$OP_CLOUD_NETWORK_CONFIG_ROLE_NAME")"
OP_HELM_SET_FLAGS+=("${SINGLE_OP_SET_FLAGS[@]}")

read -a SINGLE_OP_SET_FLAGS <<< "$(genereate_set_flags "kms" "$OP_KMS_ROLE_NAME")"
OP_HELM_SET_FLAGS+=("${SINGLE_OP_SET_FLAGS[@]}")
