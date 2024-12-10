#!/bin/bash

cd ../uhc-clusters-service/

echo "fetching first-party app configuration"
az keyvault secret show --vault-name "aro-hcp-dev-svc-kv" --name "firstPartyCert" --query "value" -o tsv | base64 -d > ./configs/azure/firstPartyCert.pem
FP_CLIENT_ID=$(az ad app list --display-name aro-dev-first-party --query '[*]'.appId -o tsv)
yq -i '(.azure-first-party-application-certificate-bundle-path) = "./configs/azure/firstPartyCert.pem"' development.yml
yq -i "(.azure-first-party-application-client-id) = \"$FP_CLIENT_ID\"" development.yml

echo "fetching MSI mock configuration"
az keyvault secret show --vault-name "aro-hcp-dev-svc-kv" --name "msiMockCert" --query value  -o tsv | base64 -d > ./configs/azure/msiMockCert.pem
MSI_CLIENT_ID=$(az ad sp list --display-name aro-dev-msi-mock --query "[*].appId" -o tsv)
MSI_PRINCIPAL_ID=$(az ad sp list --display-name aro-dev-msi-mock --query "[*].id" -o tsv)
yq -i '(.azure-mi-mock-service-principal-certificate-bundle-path) = "./configs/azure/msiMockCert.pem"' development.yml
yq -i "(.azure-mi-mock-service-principal-client-id) = \"$MSI_CLIENT_ID\"" development.yml
yq -i "(.azure-mi-mock-service-principal-principal-id) = \"$MSI_PRINCIPAL_ID\"" development.yml

echo "fetching ARM helper configuration"
az keyvault secret show --vault-name "aro-hcp-dev-svc-kv" --name "armHelperCert" --query "value" -o tsv | base64 -d > ./configs/azure/armHelperCert.pem
ARM_CLIENT_ID=$(az ad app list --display-name aro-dev-arm-helper --query '[*]'.appId -o tsv)
ARM_PRINCIPAL_ID=$(az ad sp list --display-name aro-dev-first-party --query "[*].id" -o tsv)
yq -i '(.azure-arm-helper-identity-certificate-bundle-path) = "./configs/azure/armHelperCert.pem"' development.yml
yq -i "(.azure-arm-helper-identity-client-id) = \"$MSI_CLIENT_ID\"" development.yml
yq -i "(.azure-arm-helper-mock-fpa-principal-id) = \"$MSI_PRINCIPAL_ID\"" development.yml

echo "fetching service principal credentials"
az keyvault secret show --vault-name "aro-hcp-dev-svc-kv" --name "aro-hcp-dev-sp-cs" | jq .value -r > ./configs/azure/azure-creds.json
yq -i '(.azure-auth-config-path) = "./configs/azure/azure-creds.json"' development.yml

cd ../ARO-HCP/

echo "preparing Azure runtime configuration"
make -s -C ./cluster-service personal-runtime-config > ../uhc-clusters-service/configs/azure/personal-runtime-config.json
yq -i '(.azure-runtime-config-path) = "./configs/azure/personal-runtime-config.json"' ../uhc-clusters-service/development.yml

echo "extracting managed identity configuration"
cat cluster-service/deploy/openshift-templates/arohcp-service-template.yml | yq eval '.objects[].data["azure-operators-managed-identities-config.yaml"]' | grep -v ^null > ../uhc-clusters-service/configs/azure-operators-managed-identities-config.yaml
yq -i '(.azure-operators-managed-identities-config-path) = "./configs/azure-operators-managed-identities-config.yaml"' ../uhc-clusters-service/development.yml
